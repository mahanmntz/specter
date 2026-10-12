package signals

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mahanmntz/specter/internal/crawler"
)

func TestIsValidOutreachEmail(t *testing.T) {
	tests := []struct {
		email string
		valid bool
	}{
		{"jane.doe@stripe.com", true},
		{"alex_chen@gmail.com", true},
		{"dev-ops@netflix.co.uk", true},
		{"123456+user@users.noreply.github.com", false},
		{"user@users.noreply.github.com", false},
		{"dependabot[bot]@users.noreply.github.com", false},
		{"actions@github.com", false},
		{"info@company.com", false},
		{"support@company.com", false},
		{"sales@company.com", false},
		{"help@company.com", false},
		{"not-an-email", false},
		{"test@localhost", false},
		{"user@example.com", false},
		{"", false},
	}

	for _, tt := range tests {
		got := IsValidOutreachEmail(tt.email)
		if got != tt.valid {
			t.Errorf("IsValidOutreachEmail(%q) = %v; want %v", tt.email, got, tt.valid)
		}
	}
}

func TestClassifyRole(t *testing.T) {
	tests := []struct {
		msgs        []string
		commitCount int
		expected    string
	}{
		{[]string{"arch: define rfc for distributed consensus", "cut release v1.0.0"}, 4, "Tech Lead"},
		{[]string{"refactor memory alloc in raft runtime", "optimize gc pause"}, 2, "Staff/Principal"},
		{[]string{"add redis caching to grpc payment service", "postgres migration"}, 3, "Senior Backend"},
		{[]string{"fix typo in readme", "update unit test"}, 1, "Engineer"},
	}

	for _, tt := range tests {
		got := ClassifyRole(tt.msgs, tt.commitCount)
		if got != tt.expected {
			t.Errorf("ClassifyRole(%v, %d) = %q; want %q", tt.msgs, tt.commitCount, got, tt.expected)
		}
	}
}

func TestCalculateRelevanceScore(t *testing.T) {
	langsGo := map[string]int{"Go": 10}
	scoreLead := CalculateRelevanceScore("Tech Lead", langsGo, true, 5)
	if scoreLead < 90 {
		t.Errorf("expected high score (>90) for Tech Lead with Go and domain match, got %d", scoreLead)
	}

	langsOther := map[string]int{"HTML": 1}
	scoreEng := CalculateRelevanceScore("Engineer", langsOther, false, 1)
	if scoreEng > 60 {
		t.Errorf("expected lower score (<60) for Engineer without Go, got %d", scoreEng)
	}
}

func TestIsBotOrCI(t *testing.T) {
	tests := []struct {
		name   string
		handle string
		email  string
		isBot  bool
	}{
		{"github-actions[bot]", "github-actions[bot]", "teamcity@cockroachlabs.com", true},
		{"Vishal Jaishankar", "VishalJaishankar", "vishal.jaishankar@cockroachlabs.com", false},
		{"Srinath Shrestha", "srinathshrestha", "srinathshrestha9890@gmail.com", false},
		{"Dependabot", "dependabot[bot]", "dependabot@github.com", true},
		{"CI Builder", "ci-runner", "ci@stoic.io", true},
		{"Teamcity Agent", "teamcity-ci", "teamcity@example.com", true},
		{"CircleCI", "circleci", "build@circleci.com", true},
		{"Abbott Miller", "abbottm", "abbott@cockroachlabs.com", false},
		{"Bors", "bors-bot", "bors@users.noreply.github.com", true},
	}

	for _, tt := range tests {
		got := IsBotOrCI(tt.name, tt.handle, tt.email)
		if got != tt.isBot {
			t.Errorf("IsBotOrCI(%q, %q, %q) = %v; want %v", tt.name, tt.handle, tt.email, got, tt.isBot)
		}
	}
}

func TestClassifyBackendArchetype(t *testing.T) {
	role1, score1 := ClassifyBackendArchetype(
		[]string{"define architecture rfc for distributed consensus", "cut release v2.0"},
		map[string]int{"Go": 5}, 10, "cockroachdb/cockroach", []string{"consensus", "raft"},
	)
	if role1 != "Tech Lead" {
		t.Errorf("expected Tech Lead, got %s", role1)
	}
	if score1 < 90 {
		t.Errorf("expected high score >=90, got %d", score1)
	}

	role2, _ := ClassifyBackendArchetype(
		[]string{"refactor pebble lsm tree compaction and btree indexing"},
		map[string]int{"Go": 2}, 4, "cockroachdb/pebble", []string{"storage", "indexing"},
	)
	if role2 != "Core Storage Engineer" {
		t.Errorf("expected Core Storage Engineer, got %s", role2)
	}

	role3, _ := ClassifyBackendArchetype(
		[]string{"update helm release crd and operator deployment"},
		map[string]int{"Go": 2}, 3, "cockroachdb/helm-charts", []string{"k8s"},
	)
	if role3 != "Infrastructure Engineer" {
		t.Errorf("expected Infrastructure Engineer, got %s", role3)
	}
}

func newTestMiner(t *testing.T, srv *httptest.Server) *GitMiner {
	t.Helper()
	t.Setenv("GITHUB_TOKENS", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("SPECTER_MAX_REPOS", "")
	t.Setenv("SPECTER_MAX_COMMITS", "")
	f := crawler.NewFetcher(crawler.FetcherConfig{Timeout: 5 * time.Second, AllowPrivateNetworks: true})
	m := NewGitMiner(f, GitMinerOptions{ExtractPatches: true, PolitenessDelay: time.Millisecond})
	m.apiBase = srv.URL
	m.webBase = srv.URL
	return m
}

func TestExtractFromPatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/core/commit/good.patch":
			_, _ = w.Write([]byte("From 3f8a Mon Sep 17 00:00:00 2001\nFrom: Marcus Aurelius <marcus@stoic.io>\nSubject: [PATCH] x\n"))
		case "/acme/core/commit/bot.patch":
			_, _ = w.Write([]byte("From: dependabot[bot] <bot@dependabot.com>\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	m := newTestMiner(t, srv)

	if email, name := m.extractFromPatch(context.Background(), "acme", "core", "good", nil); email != "marcus@stoic.io" || name != "Marcus Aurelius" {
		t.Errorf("got %q, %q", email, name)
	}
	if email, _ := m.extractFromPatch(context.Background(), "acme", "core", "bot", nil); email != "" {
		t.Errorf("bot patch should be rejected, got %q", email)
	}
	if email, _ := m.extractFromPatch(context.Background(), "acme", "core", "missing", nil); email != "" {
		t.Errorf("404 should yield nothing, got %q", email)
	}
}

func TestMineOrganizationEndToEnd(t *testing.T) {
	SetMXLookupFunc(func(ctx context.Context, domain string) ([]*net.MX, error) {
		return []*net.MX{{Host: "mx." + domain}}, nil
	})
	defer ResetMXLookupFunc()
	ClearMXCache()

	commits := `[
	  {"sha":"c1","author":{"login":"alice"},"commit":{"author":{"name":"Alice Dev","email":"alice@acme.com"},"message":"raft: add wal and consensus"}},
	  {"sha":"c2","author":{"login":"alice"},"commit":{"author":{"name":"Alice Dev","email":"alice@acme.com"},"message":"grpc replication fix"}},
	  {"sha":"c3","author":{"login":"dependabot[bot]"},"commit":{"author":{"name":"dependabot[bot]","email":"49699333+dependabot[bot]@users.noreply.github.com"},"message":"bump"}},
	  {"sha":"c4","author":{"login":"bobby"},"commit":{"author":{"name":"Bob","email":"1234+bobby@users.noreply.github.com"},"message":"fix storage bug"}}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orgs/acme/repos":
			_, _ = w.Write([]byte(`[
			  {"name":"core-engine","language":"Go"},
			  {"name":"docs","language":"Go"},
			  {"name":"forked-lib","language":"Go","fork":true}]`))
		case "/repos/acme/core-engine/commits":
			_, _ = w.Write([]byte(commits))
		case "/acme/core-engine/commit/c4.patch":
			_, _ = w.Write([]byte("From: Bob Builder <bob@builder.dev>\n"))
		default:
			t.Errorf("unexpected request %s (docs/forks must be skipped)", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var progress []string
	leads, err := newTestMiner(t, srv).MineOrganization(context.Background(), "acme", "acme.com", func(msg string) {
		progress = append(progress, msg)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(leads) != 2 {
		t.Fatalf("want 2 leads (alice, bob), got %d: %+v", len(leads), leads)
	}

	byEmail := map[string]EngineeringLead{}
	for _, l := range leads {
		byEmail[l.Email] = l
	}
	alice, ok := byEmail["alice@acme.com"]
	if !ok {
		t.Fatalf("alice missing: %+v", leads)
	}
	if !alice.EmailVerified || alice.GitHubHandle != "alice" || alice.TopLanguages == "" || alice.LinkedInURL == "" {
		t.Errorf("alice = %+v", alice)
	}
	if alice.CommitURL != srv.URL+"/acme/core-engine/commit/c1" {
		t.Errorf("provenance URL = %q", alice.CommitURL)
	}
	if _, ok := byEmail["bob@builder.dev"]; !ok {
		t.Errorf("bob's email should come from the patch header: %+v", leads)
	}
	if leads[0].RelevanceScore < leads[1].RelevanceScore {
		t.Error("leads must be sorted by relevance")
	}
	if len(progress) == 0 || !strings.Contains(progress[0], "acme") {
		t.Errorf("progress = %v", progress)
	}
}

func TestMineOrganizationErrors(t *testing.T) {
	statuses := map[string]int{"/orgs/missing/repos": 404, "/orgs/limited/repos": 403, "/orgs/broken/repos": 500}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code, ok := statuses[r.URL.Path]; ok {
			w.WriteHeader(code)
			return
		}
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	m := newTestMiner(t, srv)

	cases := map[string]string{"missing": "not found", "limited": "rate limit", "broken": "HTTP 500", "garbage": "unmarshaling"}
	for org, want := range cases {
		_, err := m.MineOrganization(context.Background(), org, "", nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want containing %q", org, err, want)
		}
	}
	if leads, err := m.MineOrganization(context.Background(), "", "", nil); leads != nil || err != nil {
		t.Errorf("empty org should be a no-op, got %v %v", leads, err)
	}
}

func TestMineOrganizationYieldStats(t *testing.T) {
	SetMXLookupFunc(func(ctx context.Context, domain string) ([]*net.MX, error) { return []*net.MX{{Host: "mx"}}, nil })
	defer ResetMXLookupFunc()
	ClearMXCache()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orgs/acme/repos":
			_, _ = w.Write([]byte(`[{"name":"api-server","language":"Go"}]`))
		case "/repos/acme/api-server/commits":
			_, _ = w.Write([]byte(`[{"sha":"s","author":{"login":"z"},"commit":{"author":{"name":"Zed","email":"zed@acme.com"},"message":"kafka"}}]`))
		}
	}))
	defer srv.Close()
	m := newTestMiner(t, srv)
	if _, err := m.MineOrganization(context.Background(), "acme", "acme.com", nil); err != nil {
		t.Fatal(err)
	}
	stats := m.GetYieldStats()
	if len(stats) != 1 || stats[0].RepoName != "acme/api-server" || stats[0].CommitsScanned != 1 || stats[0].VerifiedLeads != 1 {
		t.Errorf("stats = %+v", stats[0])
	}
}

func TestEnrichLeadProfileWithoutTokens(t *testing.T) {
	m := &GitMiner{rotator: NewTokenRotator("")}
	lead := &EngineeringLead{GitHubHandle: "alice", Name: "Alice Dev", Domain: "acme.com"}
	m.enrichLeadProfile(context.Background(), lead, "Acme")
	if lead.LinkedInURL != "https://www.linkedin.com/in/alice" {
		t.Errorf("short clean handle → direct profile guess, got %q", lead.LinkedInURL)
	}

	lead = &EngineeringLead{GitHubHandle: "alice-dev", Name: "Alice Dev", Domain: "acme.com"}
	m.enrichLeadProfile(context.Background(), lead, "Acme")
	if !strings.Contains(lead.LinkedInURL, "linkedin.com/search") && !strings.Contains(lead.LinkedInURL, "google.com") {
		t.Logf("fallback OSINT link: %s", lead.LinkedInURL)
	}
	if lead.LinkedInURL == "" {
		t.Error("expected a fallback LinkedIn search link")
	}

	lead = &EngineeringLead{Name: "No Handle"}
	m.enrichLeadProfile(context.Background(), lead, "Acme")
	if lead.LinkedInURL == "" {
		t.Error("no handle should still produce a search link")
	}
}

func TestEnrichLeadProfileWithToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/alice":
			_, _ = w.Write([]byte(`{"bio":"Distributed systems nerd","blog":"https://alice.dev","location":"Berlin"}`))
		case "/users/alice/social_accounts":
			_, _ = w.Write([]byte(`[{"provider":"linkedin","url":"https://www.linkedin.com/in/alice-real"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	m := newTestMiner(t, srv)
	m.rotator = NewTokenRotator("tok123")

	lead := &EngineeringLead{GitHubHandle: "alice", Name: "Alice"}
	m.enrichLeadProfile(context.Background(), lead, "Acme")
	if lead.Bio != "Distributed systems nerd" || lead.Location != "Berlin" || lead.WebsiteURL != "https://alice.dev" {
		t.Errorf("profile not applied: %+v", lead)
	}
	if !strings.Contains(lead.LinkedInURL, "alice-real") {
		t.Errorf("linkedin = %q", lead.LinkedInURL)
	}
}

func TestTokenRotatorAndHelpers(t *testing.T) {
	r := NewTokenRotator(" a, b ,,c")
	if r.Count() != 3 {
		t.Fatalf("count = %d", r.Count())
	}
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		seen[r.Next()] = true
	}
	if len(seen) != 3 {
		t.Errorf("rotation should cycle all tokens, saw %v", seen)
	}
	if NewTokenRotator("").Next() != "" {
		t.Error("empty rotator returns empty token")
	}
	if got := formatTopLanguages(nil); got != "Go" {
		t.Errorf("default language = %q", got)
	}
	if got := formatTopLanguages(map[string]int{"Rust": 5, "Go": 9, "C": 1}); !strings.HasPrefix(got, "Go") {
		t.Errorf("top languages = %q", got)
	}
}
