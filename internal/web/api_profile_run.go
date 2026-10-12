package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"specter/internal/feeds"
	"specter/internal/runner"
	"specter/internal/signals"
)

type presetView struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Emoji  string   `json:"emoji"`
	Blurb  string   `json:"blurb"`
	Skills []string `json:"skills"`
}

func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	var presets []presetView
	for _, p := range signals.Presets() {
		presets = append(presets, presetView{p.ID, p.Label, p.Emoji, p.Blurb, p.Profile.Skills})
	}
	s.mu.RLock()
	prefs := s.prefs
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": prefs != nil,
		"prefs":      prefs,
		"presets":    presets,
		"can_run":    s.opts.Run != nil,
	})
}

func (s *Server) handlePutProfile(w http.ResponseWriter, r *http.Request) {
	if s.progress.running() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a search is running; wait for it to finish or cancel it"})
		return
	}
	var p Prefs
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := p.normalize(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.setPrefs(p, true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "prefs": p})
}

func (s *Server) handleRunStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.progress.Status())
}

const quickSeedCount = 25

func (s *Server) handleRunStart(w http.ResponseWriter, r *http.Request) {
	if s.opts.Run == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "searching is not available in this build"})
		return
	}
	s.mu.RLock()
	prefs := s.prefs
	s.mu.RUnlock()
	if prefs == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "choose your role first"})
		return
	}

	var body struct {
		Mode string `json:"mode"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body)

	all, err := s.opts.Seeds()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	n := quickSeedCount
	if body.Mode == "full" {
		n = 0
	}
	opts := runner.Options{Seeds: runner.SelectSeeds(all, n)}
	if prefs.Feeds {
		opts.Feeds = feeds.Names()
	}

	ctx, cancel := context.WithCancel(s.baseCtx)
	if !s.progress.start(cancel) {
		cancel()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a search is already running"})
		return
	}
	go s.execute(ctx, opts)
	writeJSON(w, http.StatusAccepted, s.progress.Status())
}

func (s *Server) execute(ctx context.Context, opts runner.Options) {
	started := time.Now()
	res, err := s.opts.Run(ctx, opts, s.progress)
	if err == nil {
		recordRun(s.src, started, res)
	}
	s.progress.finish(res, err)
}

func (s *Server) handleRunCancel(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": s.progress.stop()})
}
