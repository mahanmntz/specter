package ghprofile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseLogin(t *testing.T) {
	ok := map[string]string{
		"octocat": "octocat", "@octocat": "octocat", " octocat ": "octocat",
		"https://github.com/octocat": "octocat", "github.com/octocat/": "octocat", "https://github.com/octocat/hello-world": "octocat",
		"a-b-c": "a-b-c",
	}
	for in, want := range ok {
		if got, err := ParseLogin(in); err != nil || got != want {
			t.Errorf("%q → %q, %v want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "  ", "-bad", "bad-", "has space", "evil.com/octocat", "https://evil.com/octocat", "a/b", "x_y", string(make([]byte, 41))} {
		if _, err := ParseLogin(bad); !errors.Is(err, ErrInvalidLogin) {
			t.Errorf("%q should be invalid, got %v", bad, err)
		}
	}
}

var now = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

func months(n int) time.Time { return now.AddDate(0, -n, 0) }

func TestWeighPrefersRecentOriginalWork(t *testing.T) {
	repos := []ghRepo{
		{Name: "kv-store", Language: "Go", Topics: []string{"raft", "kubernetes"}, Stars: 120, PushedAt: months(1), Description: "Distributed key-value store"},
		{Name: "api", Language: "Go", PushedAt: months(2)},
		{Name: "old-site", Language: "JavaScript", PushedAt: months(60)},
		{Name: "forked-react", Language: "TypeScript", Fork: true, Topics: []string{"react"}, PushedAt: months(1)},
		{Name: "infra", Language: "HCL", Topics: []string{"terraform", "aws"}, PushedAt: months(3)},
	}
	w, counts, analyzed := weigh(repos, "Backend dev who loves Postgres", now)
	if analyzed != 4 {
		t.Errorf("forks must be skipped when originals exist: analyzed=%d", analyzed)
	}
	if w["Go"] <= w["JavaScript"] || counts["Go"] != 2 {
		t.Errorf("recent Go should outweigh stale JS: %v %v", w, counts)
	}
	if _, ok := w["React"]; ok {
		t.Error("forked repo topics must not count")
	}
	if w["Kubernetes"] == 0 || w["Terraform"] == 0 || w["AWS"] == 0 {
		t.Errorf("topics missing: %v", w)
	}
	if w["PostgreSQL"] < 2 {
		t.Errorf("bio mention should add weight: %v", w)
	}
	if w["Distributed Systems"] != 0 {
		// "Distributed key-value store" mentions "distributed" which is an alias of Distributed Systems
		t.Logf("description alias hit as expected")
	}
}

func TestWeighForkOnlyAccountStillAnalyzed(t *testing.T) {
	w, _, analyzed := weigh([]ghRepo{{Name: "f", Language: "Rust", Fork: true, PushedAt: months(1)}}, "", now)
	if analyzed != 1 || w["Rust"] == 0 {
		t.Errorf("analyzed=%d w=%v", analyzed, w)
	}
}

func fakeGitHub(t *testing.T, user, repos string, status int) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		switch r.URL.Path {
		case "/users/octocat":
			_, _ = w.Write([]byte(user))
		case "/users/octocat/repos":
			if r.URL.Query().Get("per_page") != "100" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(repos))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), BaseURL: srv.URL}
}

func TestAnalyzeInfersFrontendDeveloper(t *testing.T) {
	c := fakeGitHub(t,
		`{"login":"octocat","name":"The Octocat","bio":"UI engineer","location":"SF","avatar_url":"https://a/b.png","public_repos":9,"followers":50}`,
		`[{"name":"design-system","language":"TypeScript","topics":["react","tailwindcss","accessibility"],"stargazers_count":40,"pushed_at":"2026-09-01T00:00:00Z"},
		  {"name":"site","language":"CSS","topics":["css"],"pushed_at":"2026-08-01T00:00:00Z"},
		  {"name":"scripts","language":"Shell","pushed_at":"2026-01-01T00:00:00Z"}]`, 200)

	res, err := c.Analyze(context.Background(), "https://github.com/octocat")
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "The Octocat" || res.Login != "octocat" || res.ReposAnalyzed != 3 {
		t.Errorf("res = %+v", res)
	}
	if res.Role != "frontend" {
		t.Errorf("role = %s scores=%v", res.Role, res.RoleScores)
	}
	names := map[string]bool{}
	for _, s := range res.Skills {
		names[s.Name] = true
	}
	for _, want := range []string{"TypeScript", "React", "Tailwind", "Accessibility", "CSS"} {
		if !names[want] {
			t.Errorf("missing skill %s in %v", want, res.Skills)
		}
	}
	if res.Skills[0].Score < res.Skills[len(res.Skills)-1].Score {
		t.Error("skills must be sorted by score")
	}
}

func TestAnalyzeErrors(t *testing.T) {
	if _, err := fakeGitHub(t, "", "", 404).Analyze(context.Background(), "octocat"); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 → %v", err)
	}
	if _, err := fakeGitHub(t, "", "", 403).Analyze(context.Background(), "octocat"); !errors.Is(err, ErrRateLimited) {
		t.Errorf("403 → %v", err)
	}
	if _, err := (&Client{}).Analyze(context.Background(), "not a user!"); !errors.Is(err, ErrInvalidLogin) {
		t.Errorf("invalid → %v", err)
	}
}

func TestEmptyAccountDefaultsToBackend(t *testing.T) {
	c := fakeGitHub(t, `{"login":"octocat"}`, `[]`, 200)
	res, err := c.Analyze(context.Background(), "octocat")
	if err != nil || res.Role != "backend" || len(res.Skills) != 0 {
		t.Errorf("res=%+v err=%v", res, err)
	}
}

func TestBestRole(t *testing.T) {
	if got := bestRole(map[string]float64{"ml": 9, "backend": 3, "data": 8}); got != "ml" {
		t.Errorf("got %s", got)
	}
	if got := bestRole(map[string]float64{}); got != "backend" {
		t.Errorf("no signal → %s", got)
	}
}

func TestAvatarDataURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("s") != "96" {
			t.Errorf("size param missing: %s", r.URL.RawQuery)
		}
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG-bytes"))
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<script>"))
		case "/big":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(make([]byte, 400<<10))
		}
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), AllowAnyHost: true}

	got, err := c.AvatarDataURL(context.Background(), srv.URL+"/ok")
	if err != nil || got != "data:image/png;base64,iVBORy1ieXRlcw==" {
		t.Errorf("got %q err=%v", got, err)
	}
	for _, p := range []string{"/html", "/big", "/missing"} {
		if _, err := c.AvatarDataURL(context.Background(), srv.URL+p); err == nil {
			t.Errorf("%s should be rejected", p)
		}
	}
	// Without AllowAnyHost only GitHub's avatar host is permitted.
	strict := &Client{HTTP: srv.Client()}
	for _, bad := range []string{srv.URL + "/ok", "https://evil.example.com/a.png", "http://avatars.githubusercontent.com/u/1", "file:///etc/passwd"} {
		if _, err := strict.AvatarDataURL(context.Background(), bad); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
}

func TestAnalyzeRetriesOnceThenReportsUnreachable(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close() // simulate a connection reset
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}
	if _, err := c.Analyze(context.Background(), "octocat"); !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v", err)
	}
	if calls != 2 {
		t.Errorf("expected exactly one retry, got %d calls", calls)
	}
}
