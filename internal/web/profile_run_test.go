package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mahanmntz/specter/internal/runner"
	"github.com/mahanmntz/specter/internal/seeds"
	"github.com/mahanmntz/specter/internal/signals"
)

// restoreProfile puts the process-wide matcher back to the backend defaults after a test.
func restoreProfile(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		p, _ := signals.ProfileFor("backend", nil)
		signals.ApplyProfile(p)
	})
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	if method != http.MethodGet {
		req.Header.Set("X-Specter", "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestProfileSaveAppliesAndPersists(t *testing.T) {
	restoreProfile(t)
	src := newFake()
	s := newServer(src)
	h := s.Handler()

	var before struct {
		Configured bool
		Presets    []presetView
	}
	_ = json.Unmarshal(do(t, h, "GET", "/api/profile", "").Body.Bytes(), &before)
	if before.Configured || len(before.Presets) < 6 {
		t.Fatalf("fresh profile = %+v", before)
	}

	rec := do(t, h, "PUT", "/api/profile", `{"role":"Frontend","skills":["React"," Elm ","react"],"worldwide_only":true}`)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if _, ok := src.settings[prefsKey]; !ok {
		t.Error("prefs not persisted")
	}
	if !signals.IsBackendRole("Senior Frontend Engineer", "") || signals.IsBackendRole("Backend Engineer", "Platform") {
		t.Error("matcher profile not switched to frontend")
	}

	// Saved roles outside the chosen profile are hidden from the list.
	_, ids := jobsFor(t, s, "status=all")
	for _, id := range ids {
		if id == "1" { // "Senior Backend Engineer"
			t.Error("backend role should be hidden for a frontend profile")
		}
	}

	// A new server restores the saved profile from settings.
	restoreProfile(t)
	p, _ := signals.ProfileFor("backend", nil)
	signals.ApplyProfile(p)
	s2 := New(src, Options{})
	s2.mu.RLock()
	got := s2.prefs
	s2.mu.RUnlock()
	if got == nil || got.Role != "frontend" || len(got.Skills) != 2 || !got.WorldwideOnly {
		t.Errorf("restored prefs = %+v", got)
	}
	if !signals.IsBackendRole("Frontend Engineer", "") {
		t.Error("restored profile not applied to matcher")
	}
}

func TestProfileValidation(t *testing.T) {
	restoreProfile(t)
	h := newServer(newFake()).Handler()
	for _, body := range []string{`{"role":"astronaut"}`, `{`, `{"role":""}`} {
		if rec := do(t, h, "PUT", "/api/profile", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s → %d", body, rec.Code)
		}
	}
	// PUT without the X-Specter header is rejected by the guard.
	req := httptest.NewRequest("PUT", "/api/profile", strings.NewReader(`{"role":"backend"}`))
	req.Host = "localhost"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("PUT without header → %d", rec.Code)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func runServer(t *testing.T, run RunFunc) (*Server, *fakeSource) {
	t.Helper()
	restoreProfile(t)
	src := newFake()
	s := New(src, Options{Run: run, Seeds: func() ([]seeds.SeedTarget, error) {
		out := make([]seeds.SeedTarget, 40)
		for i := range out {
			out[i] = seeds.SeedTarget{Name: "c", Domain: "c.com", Priority: 1}
		}
		return out, nil
	}})
	t.Cleanup(s.Close)
	if rec := do(t, s.Handler(), "PUT", "/api/profile", `{"role":"backend","feeds":true}`); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	return s, src
}

func status(t *testing.T, h http.Handler) RunStatus {
	t.Helper()
	var st RunStatus
	_ = json.Unmarshal(do(t, h, "GET", "/api/run", "").Body.Bytes(), &st)
	return st
}

func TestRunLifecycle(t *testing.T) {
	var gotOpts runner.Options
	release := make(chan struct{})
	s, src := runServer(t, func(ctx context.Context, opts runner.Options, obs *Progress) (runner.Result, error) {
		gotOpts = opts
		obs.SetTotal(len(opts.Seeds))
		obs.IncCrawled()
		obs.Log("working")
		<-release
		return runner.Result{Companies: len(opts.Seeds), NewRoles: 7, FeedRoles: 3}, nil
	})
	h := s.Handler()

	if rec := do(t, h, "POST", "/api/run", `{"mode":"quick"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	waitFor(t, func() bool { return status(t, h).Done == 1 })
	st := status(t, h)
	if st.State != RunRunning || st.Total != quickSeedCount || len(st.Logs) != 1 {
		t.Errorf("running status = %+v", st)
	}
	if len(gotOpts.Feeds) == 0 {
		t.Error("feeds pref should enable feeds")
	}

	if rec := do(t, h, "POST", "/api/run", ""); rec.Code != http.StatusConflict {
		t.Errorf("second start → %d, want 409", rec.Code)
	}
	if rec := do(t, h, "PUT", "/api/profile", `{"role":"mobile"}`); rec.Code != http.StatusConflict {
		t.Errorf("profile change during run → %d, want 409", rec.Code)
	}

	close(release)
	waitFor(t, func() bool { return status(t, h).State == RunDone })
	st = status(t, h)
	if st.NewRoles != 7 || st.FeedRoles != 3 || st.FinishedAt.IsZero() {
		t.Errorf("done status = %+v", st)
	}
	if len(src.runs) != 1 || src.runs[0].NewRoles != 7 {
		t.Errorf("crawl run not recorded: %+v", src.runs)
	}
}

func TestRunFullModeCancelAndErrors(t *testing.T) {
	var mu sync.Mutex
	var seen []int
	s, _ := runServer(t, func(ctx context.Context, opts runner.Options, obs *Progress) (runner.Result, error) {
		mu.Lock()
		seen = append(seen, len(opts.Seeds))
		mu.Unlock()
		<-ctx.Done()
		return runner.Result{}, ctx.Err()
	})
	h := s.Handler()

	do(t, h, "POST", "/api/run", `{"mode":"full"}`)
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) == 1 })
	if seen[0] != 40 {
		t.Errorf("full mode should scan every seed, got %d", seen[0])
	}
	var c struct{ Cancelled bool }
	_ = json.Unmarshal(do(t, h, "POST", "/api/run/cancel", "").Body.Bytes(), &c)
	if !c.Cancelled {
		t.Error("cancel should report true while running")
	}
	waitFor(t, func() bool { return status(t, h).State == RunCancelled })

	// A failing run reports the error and allows a new start.
	s2, _ := runServer(t, func(context.Context, runner.Options, *Progress) (runner.Result, error) {
		return runner.Result{}, context.DeadlineExceeded
	})
	h2 := s2.Handler()
	do(t, h2, "POST", "/api/run", "")
	waitFor(t, func() bool { return status(t, h2).State == RunFailed })
	if st := status(t, h2); st.Error == "" {
		t.Error("error message missing")
	}
	if rec := do(t, h2, "POST", "/api/run", ""); rec.Code != http.StatusAccepted {
		t.Errorf("restart after failure → %d", rec.Code)
	}
}

func TestRunRequiresProfileAndRunner(t *testing.T) {
	s := New(newFake(), Options{Run: func(context.Context, runner.Options, *Progress) (runner.Result, error) {
		return runner.Result{}, nil
	}})
	t.Cleanup(s.Close)
	if rec := do(t, s.Handler(), "POST", "/api/run", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("no profile → %d, want 400", rec.Code)
	}
	s2 := New(newFake(), Options{})
	t.Cleanup(s2.Close)
	if rec := do(t, s2.Handler(), "POST", "/api/run", ""); rec.Code != http.StatusNotImplemented {
		t.Errorf("no runner → %d, want 501", rec.Code)
	}
}
