package ghprofile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// repo builds a typical original repository last pushed `ago` months back after `span` months of work.
func repo(name, lang string, ago, span int, topics ...string) ghRepo {
	return ghRepo{Name: name, Language: lang, Topics: topics, PushedAt: months(ago), CreatedAt: months(ago + span), Size: 800}
}

func repeat(n int, f func(i int) ghRepo) []ghRepo {
	out := make([]ghRepo, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

func skillMap(skills []DetectedSkill) map[string]DetectedSkill {
	m := map[string]DetectedSkill{}
	for _, s := range skills {
		m[s.Name] = s
	}
	return m
}

func TestRepoWeightFavoursRecentSustainedOriginalWork(t *testing.T) {
	recent := repoWeight(repo("api", "Go", 1, 18), now)
	stale := repoWeight(repo("api", "Go", 60, 18), now)
	oneDay := repoWeight(repo("api", "Go", 1, 0), now)
	tutorial := repoWeight(repo("go-tutorial", "Go", 1, 18), now)
	if !(recent > stale*1.5) {
		t.Errorf("recent %.2f should clearly beat 5-year-old %.2f", recent, stale)
	}
	if !(recent > oneDay) {
		t.Errorf("sustained work %.2f should beat a one-day repo %.2f", recent, oneDay)
	}
	if !(tutorial < recent*0.5) {
		t.Errorf("tutorial repos must be discounted: %.2f vs %.2f", tutorial, recent)
	}
	starred := repo("lib", "Go", 1, 18)
	starred.Stars = 500
	if repoWeight(starred, now) <= recent {
		t.Error("stars should add weight")
	}
}

func TestSelectionKeepsDepthAndRecencyNotEverythingEverTouched(t *testing.T) {
	var repos []ghRepo
	// Deep, recent Go + Kubernetes work.
	repos = append(repos, repeat(6, func(i int) ghRepo { return repo("svc", "Go", 1+i, 20, "kubernetes") })...)
	// Solid Python a year or two back.
	repos = append(repos, repeat(3, func(i int) ghRepo { return repo("tool", "Python", 14+i, 10) })...)
	// One-off experiments and old coursework.
	repos = append(repos, repo("try-swift", "Swift", 2, 0), repo("flutter-demo", "Dart", 40, 1), repo("site", "HTML", 50, 1), repo("sh", "Shell", 3, 0))
	acc, analyzed := weighSkills(repos, nil, profileText{hints: map[string]float64{}}, now)
	if analyzed != len(repos) {
		t.Fatalf("analyzed=%d", analyzed)
	}
	got := skillMap(selectSkills(acc))

	for _, want := range []string{"Go", "Kubernetes", "Python"} {
		if !got[want].Selected {
			t.Errorf("%s should be pre-selected: %+v", want, got[want])
		}
	}
	for _, noise := range []string{"Swift", "Dart", "HTML", "Shell"} {
		if s, ok := got[noise]; ok && s.Selected {
			t.Errorf("%s was a one-off and must not be pre-selected: %+v", noise, s)
		}
	}
	if got["Go"].Score <= got["Python"].Score {
		t.Errorf("recent deep Go should outrank older Python: %v vs %v", got["Go"].Score, got["Python"].Score)
	}
	if got["Go"].LastUsed.IsZero() || got["Go"].Repos != 6 {
		t.Errorf("Go usage details wrong: %+v", got["Go"])
	}
}

func TestPreselectionIsCappedAndGenericSkillsNeedEmphasis(t *testing.T) {
	langs := []string{"Go", "Python", "Java", "Rust", "Kotlin", "Ruby", "PHP", "Scala", "Elixir", "C#", "Swift", "TypeScript"}
	var repos []ghRepo
	for _, l := range langs {
		repos = append(repos, repeat(3, func(i int) ghRepo { return repo("p", l, 1+i, 12) })...)
	}
	repos = append(repos, repeat(2, func(i int) ghRepo { return repo("page", "HTML", 1, 12) })...)
	acc, _ := weighSkills(repos, nil, profileText{hints: map[string]float64{}}, now)
	sel := 0
	for _, s := range selectSkills(acc) {
		if s.Selected {
			sel++
		}
		if s.Name == "HTML" && s.Selected {
			t.Error("a minor generic skill must not be pre-selected")
		}
	}
	if sel != maxPreselected {
		t.Errorf("pre-selected %d, want cap %d", sel, maxPreselected)
	}
}

func TestSparseProfileStillGetsStartingPoint(t *testing.T) {
	acc, _ := weighSkills([]ghRepo{repo("a", "Go", 1, 1)}, nil, profileText{hints: map[string]float64{}}, now)
	var picked int
	for _, s := range selectSkills(acc) {
		if s.Selected {
			picked++
		}
	}
	if picked == 0 {
		t.Error("expected at least one suggestion for a sparse profile")
	}
}

func TestProfileTextReadsREADMEAndIgnoresLearning(t *testing.T) {
	readme := `# Hi, I'm Ada 👋
Backend engineer building APIs.

![Go](https://img.shields.io/badge/Go-00ADD8?logo=go&logoColor=white)
![Postgres](https://img.shields.io/badge/PostgreSQL-4169E1)
Tech: Kubernetes, Redis and gRPC.

Currently learning Rust and Swift.
Let's go build something. Fun fact: I rust my bike.`
	pt := readProfileText("Open source lover", readme)
	have := map[string]bool{}
	for _, s := range pt.skills {
		have[s] = true
	}
	for _, want := range []string{"Go", "PostgreSQL", "Kubernetes", "Redis", "gRPC"} {
		if !have[want] {
			t.Errorf("missing %s in %v", want, pt.skills)
		}
	}
	for _, bad := range []string{"Rust", "Swift"} {
		if have[bad] {
			t.Errorf("%s is something Ada is still learning", bad)
		}
	}
	if pt.hints["backend"] == 0 {
		t.Errorf("'Backend engineer' should hint the backend role: %v", pt.hints)
	}
}

func TestEnglishWordsAreNotSkills(t *testing.T) {
	pt := readProfileText("Let's go swiftly. I like to rest and go for walks, rust never sleeps.", "")
	if len(pt.skills) != 0 {
		t.Errorf("lowercase English words must not become skills: %v", pt.skills)
	}
}

func TestRoleRanking(t *testing.T) {
	mk := func(w map[string]float64) map[string]*skillAcc {
		m := map[string]*skillAcc{}
		for k, v := range w {
			m[k] = &skillAcc{weight: v}
		}
		return m
	}
	// Clear specialist.
	best, order, _ := rankRoles(mk(map[string]float64{"React": 12, "TypeScript": 14, "CSS": 6, "Next.js": 5}), profileText{hints: map[string]float64{}})
	if best != "frontend" || order[0] != "frontend" || order[1] != "software-engineer" {
		t.Errorf("specialist: best=%s order=%v", best, order)
	}
	// Broad: backend, frontend and devops work all in similar amounts.
	best, order, _ = rankRoles(mk(map[string]float64{
		"Go": 9, "PostgreSQL": 8, "Redis": 6, // backend
		"React": 9, "TypeScript": 9, "CSS": 6, // frontend
		"Kubernetes": 8, "Docker": 8, "Terraform": 6, // devops
	}), profileText{hints: map[string]float64{}})
	if best != "software-engineer" {
		t.Errorf("a broad profile is a generalist, got %s (%v)", best, order)
	}
	// Broad code, but the person says what they are.
	best, _, _ = rankRoles(mk(map[string]float64{"Go": 9, "PostgreSQL": 8, "React": 9, "TypeScript": 9, "Kubernetes": 8, "Docker": 8}),
		profileText{hints: map[string]float64{"devops": 3}})
	if best != "devops" {
		t.Errorf("self-declared role should win: %s", best)
	}
	// Nothing to go on.
	if best, _, _ = rankRoles(map[string]*skillAcc{}, profileText{hints: map[string]float64{}}); best != "software-engineer" {
		t.Errorf("empty → %s", best)
	}
}

func fakeGitHub(t *testing.T, user, repos, readme string, status int, extra ...map[string]string) *Client {
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
		case "/repos/octocat/octocat/readme":
			if !strings.Contains(r.Header.Get("Accept"), "raw") {
				t.Errorf("README must be requested raw, Accept=%q", r.Header.Get("Accept"))
			}
			if readme == "" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(readme))
		default:
			for _, m := range extra {
				if body, ok := m[r.URL.Path]; ok {
					_, _ = w.Write([]byte(body))
					return
				}
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), BaseURL: srv.URL}
}

func TestAnalyzeUsesReposAndProfileREADME(t *testing.T) {
	recent := now.AddDate(0, -1, 0).Format(time.RFC3339)
	created := now.AddDate(-2, 0, 0).Format(time.RFC3339)
	repos := `[` + strings.Join([]string{
		`{"name":"ds","language":"TypeScript","topics":["react","accessibility"],"stargazers_count":40,"size":900,"pushed_at":"` + recent + `","created_at":"` + created + `"}`,
		`{"name":"site","language":"CSS","topics":["css"],"size":500,"pushed_at":"` + recent + `","created_at":"` + created + `"}`,
		`{"name":"ui-kit","language":"TypeScript","topics":["react","tailwindcss"],"size":700,"pushed_at":"` + recent + `","created_at":"` + created + `"}`,
	}, ",") + `]`
	c := fakeGitHub(t, `{"login":"octocat","name":"The Octocat","bio":"UI engineer","public_repos":9}`, repos,
		"# Octo\nFrontend developer. Stack: React, TypeScript, Redux.", 200)

	res, err := c.Analyze(context.Background(), "https://github.com/octocat")
	if err != nil {
		t.Fatal(err)
	}
	if !res.ReadmeUsed || res.ReposAnalyzed != 3 || res.Name != "The Octocat" {
		t.Errorf("res = %+v", res)
	}
	if res.Role != "frontend" || res.Roles[0] != "frontend" {
		t.Errorf("role = %s %v scores=%v", res.Role, res.Roles, res.RoleScores)
	}
	got := skillMap(res.Skills)
	for _, want := range []string{"React", "TypeScript"} {
		if !got[want].Selected {
			t.Errorf("%s should be pre-selected: %+v", want, got[want])
		}
	}
	if !got["Redux"].FromProfile {
		t.Errorf("Redux comes from the README only: %+v", got["Redux"])
	}
}

func TestAnalyzeWorksWithoutProfileREADME(t *testing.T) {
	c := fakeGitHub(t, `{"login":"octocat"}`, `[]`, "", 200)
	res, err := c.Analyze(context.Background(), "octocat")
	if err != nil || res.ReadmeUsed || res.Role != "software-engineer" || len(res.Skills) != 0 {
		t.Errorf("res=%+v err=%v", res, err)
	}
}

func TestAnalyzeErrors(t *testing.T) {
	if _, err := fakeGitHub(t, "", "", "", 404).Analyze(context.Background(), "octocat"); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 → %v", err)
	}
	if _, err := fakeGitHub(t, "", "", "", 403).Analyze(context.Background(), "octocat"); !errors.Is(err, ErrRateLimited) {
		t.Errorf("403 → %v", err)
	}
	if _, err := (&Client{}).Analyze(context.Background(), "not a user!"); !errors.Is(err, ErrInvalidLogin) {
		t.Errorf("invalid → %v", err)
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
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close() // simulate a connection reset
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}
	if _, err := c.Analyze(context.Background(), "octocat"); !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected exactly one retry, got %d calls", calls.Load())
	}
}

func TestLanguageBytesBeatPrimaryLanguageLabel(t *testing.T) {
	// GitHub labels this repo "TypeScript", but most of the code is Go (and a little shell).
	r := repo("platform", "TypeScript", 1, 24)
	bytes := map[string]map[string]int{"platform": {"Go": 80000, "TypeScript": 15000, "Shell": 1000}}
	acc, _ := weighSkills([]ghRepo{r}, bytes, profileText{hints: map[string]float64{}}, now)
	if acc["Go"] == nil || acc["TypeScript"] == nil {
		t.Fatalf("both real languages expected: %v", acc)
	}
	if acc["Go"].weight <= acc["TypeScript"].weight*2 {
		t.Errorf("Go (84%% of bytes) must clearly outweigh TypeScript: %.2f vs %.2f", acc["Go"].weight, acc["TypeScript"].weight)
	}
	if acc["Shell"] != nil {
		t.Error("a 1% language is noise and must be ignored")
	}

	// Without byte counts the primary language is all we know.
	acc, _ = weighSkills([]ghRepo{r}, nil, profileText{hints: map[string]float64{}}, now)
	if acc["Go"] != nil || acc["TypeScript"] == nil {
		t.Errorf("fallback should use the primary language: %v", acc)
	}
}

func TestAnalyzeFetchesLanguagesForStrongestReposAndSurvivesRateLimit(t *testing.T) {
	recent := now.AddDate(0, -1, 0).Format(time.RFC3339)
	created := now.AddDate(-2, 0, 0).Format(time.RFC3339)
	repos := `[{"name":"platform","language":"TypeScript","size":900,"pushed_at":"` + recent + `","created_at":"` + created + `"}]`
	c := fakeGitHub(t, `{"login":"octocat"}`, repos, "", 200,
		map[string]string{"/repos/octocat/platform/languages": `{"Go":90000,"TypeScript":9000}`})
	res, err := c.Analyze(context.Background(), "octocat")
	if err != nil {
		t.Fatal(err)
	}
	got := skillMap(res.Skills)
	if got["Go"].Score <= got["TypeScript"].Score {
		t.Errorf("real language bytes should be used: Go %.1f TS %.1f", got["Go"].Score, got["TypeScript"].Score)
	}

	// When the languages endpoint is rate limited the analysis still succeeds from primary languages.
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/octocat":
			_, _ = w.Write([]byte(`{"login":"octocat"}`))
		case "/users/octocat/repos":
			_, _ = w.Write([]byte(repos))
		case "/repos/octocat/octocat/readme":
			http.NotFound(w, r)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer limited.Close()
	res, err = (&Client{HTTP: limited.Client(), BaseURL: limited.URL}).Analyze(context.Background(), "octocat")
	if err != nil || skillMap(res.Skills)["TypeScript"].Name == "" {
		t.Errorf("rate-limited languages must degrade gracefully: err=%v skills=%v", err, res.Skills)
	}
}
