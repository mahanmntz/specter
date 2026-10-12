package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/mahanmntz/specter/internal/feeds"
	"github.com/mahanmntz/specter/internal/ghprofile"
	"github.com/mahanmntz/specter/internal/runner"
	"github.com/mahanmntz/specter/internal/signals"
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
		"can_import": s.opts.GitHub != nil,
		"catalog":    signals.SkillCatalog(),
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

type importSkill struct {
	Name     string  `json:"name"`
	Score    float64 `json:"score"`
	Repos    int     `json:"repos"`
	Selected bool    `json:"selected"`
}

// handleGitHubImport reads a public GitHub profile and proposes a name, role and skills.
func (s *Server) handleGitHubImport(w http.ResponseWriter, r *http.Request) {
	if s.opts.GitHub == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "GitHub import is not available"})
		return
	}
	var body struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := s.opts.GitHub.Analyze(ctx, body.Input)
	switch {
	case errors.Is(err, ghprofile.ErrInvalidLogin):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, ghprofile.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, ghprofile.ErrRateLimited):
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	avatar := ""
	if res.AvatarURL != "" {
		avatar, _ = s.opts.GitHub.AvatarDataURL(ctx, res.AvatarURL) // best effort
	}

	// Pre-select the strongest signals: skills with real evidence, capped so the list stays readable.
	skills := make([]importSkill, 0, len(res.Skills))
	selected := 0
	for _, sk := range res.Skills {
		pick := selected < 12 && sk.Score >= 1.5
		if pick {
			selected++
		}
		skills = append(skills, importSkill{sk.Name, sk.Score, sk.Repos, pick})
	}

	// Roles ordered by evidence so the UI can offer the runners-up.
	roles := make([]string, 0, len(res.RoleScores))
	for id, sc := range res.RoleScores {
		if sc > 0 || id == res.Role {
			roles = append(roles, id)
		}
	}
	sort.Slice(roles, func(i, j int) bool {
		if roles[i] == res.Role || roles[j] == res.Role {
			return roles[i] == res.Role
		}
		return res.RoleScores[roles[i]] > res.RoleScores[roles[j]]
	})

	name := res.Name
	if name == "" {
		name = res.Login
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"login": res.Login, "name": name, "bio": res.Bio, "location": res.Location,
		"avatar": avatar, "role": res.Role, "roles": roles, "skills": skills,
		"repos_analyzed": res.ReposAnalyzed, "public_repos": res.PublicRepos,
	})
}
