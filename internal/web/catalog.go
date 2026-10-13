package web

import (
	"context"
	"net/http"
	"sync"
)

// ImportFunc adds companies to the catalog (e.g. from the remoteintech directory) and returns how
// many were new. progress may be called from any goroutine.
type ImportFunc func(ctx context.Context, discover bool, progress func(done, total int)) (added int, err error)

type importState struct {
	mu       sync.Mutex
	running  bool
	done     int
	total    int
	added    int
	err      string
	finished bool
}

func (s *Server) handleCatalogStart(w http.ResponseWriter, r *http.Request) {
	if s.opts.Import == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "not available in this build"})
		return
	}
	st := &s.catalog
	st.mu.Lock()
	if st.running {
		st.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already running"})
		return
	}
	*st = importState{running: true}
	st.mu.Unlock()

	go func() {
		added, err := s.opts.Import(s.baseCtx, true, func(done, total int) {
			st.mu.Lock()
			st.done, st.total = done, total
			st.mu.Unlock()
		})
		st.mu.Lock()
		st.running, st.finished, st.added = false, true, added
		if err != nil {
			st.err = err.Error()
		}
		st.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}

func (s *Server) handleCatalogStatus(w http.ResponseWriter, r *http.Request) {
	st := &s.catalog
	st.mu.Lock()
	defer st.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"running": st.running, "done": st.done, "total": st.total, "added": st.added, "error": st.err, "finished": st.finished,
	})
}
