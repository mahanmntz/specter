package ats

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mahanmntz/specter/internal/crawler"
)

func testFetcher() *crawler.Fetcher {
	return crawler.NewFetcher(crawler.FetcherConfig{Timeout: 5 * time.Second, AllowPrivateNetworks: true})
}

func jsonServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func assertSingleBackendRole(t *testing.T, meta *CompanyMeta, wantIDPrefix string) {
	t.Helper()
	if meta == nil || len(meta.OpenRoles) != 1 {
		t.Fatalf("expected exactly 1 relevant role, got %+v", meta)
	}
	role := meta.OpenRoles[0]
	if !strings.HasPrefix(role.ID, wantIDPrefix) {
		t.Errorf("role ID %q lacks prefix %q", role.ID, wantIDPrefix)
	}
	if !strings.Contains(strings.ToLower(role.Title), "backend") {
		t.Errorf("unexpected role %q", role.Title)
	}
	if len(role.Keywords) == 0 || len(meta.Signals) == 0 {
		t.Errorf("expected keywords/signals, got %v / %v", role.Keywords, meta.Signals)
	}
}

func TestWorkableExtraction(t *testing.T) {
	srv := jsonServer(t, `{"jobs":[
	  {"shortcode":"AB12","title":"Senior Backend Engineer","department":"Engineering","url":"https://apply.workable.com/acme/j/AB12/","city":"","country":"","telecommuting":true,"published_on":"2026-09-01","description":"Build Go and Kafka services with PostgreSQL."},
	  {"shortcode":"CD34","title":"Marketing Manager","department":"Marketing","url":"https://apply.workable.com/acme/j/CD34/","description":"Brand."}]}`)
	a := NewWorkableAdapter(testFetcher())
	a.baseURL = srv.URL

	if !a.Detect("https://apply.workable.com/acme/") || a.Detect("https://boards.greenhouse.io/x") {
		t.Error("Detect mismatch")
	}
	meta, err := a.ExtractByBoardID(context.Background(), "acme", "acme.com")
	if err != nil {
		t.Fatal(err)
	}
	assertSingleBackendRole(t, meta, "workable-")
	if meta.OpenRoles[0].PostedAt.Year() != 2026 {
		t.Errorf("posted date not parsed: %v", meta.OpenRoles[0].PostedAt)
	}
}

func TestSmartRecruitersExtraction(t *testing.T) {
	srv := jsonServer(t, `{"content":[
	  {"id":"744","name":"Backend Engineer (Go)","releasedDate":"2026-09-02T10:00:00.000Z","department":{"label":"Engineering"},"location":{"city":"Berlin","country":"de","remote":false},"ref":"x"},
	  {"id":"745","name":"Account Executive","department":{"label":"Sales"},"location":{"city":"Berlin","country":"de"}}]}`)
	a := NewSmartRecruitersAdapter(testFetcher())
	a.baseURL = srv.URL

	meta, err := a.ExtractByBoardID(context.Background(), "acme", "acme.com")
	if err != nil {
		t.Fatal(err)
	}
	assertSingleBackendRole(t, meta, "smartrecruiters-")
	if got := meta.OpenRoles[0].Location; got != "Berlin, DE" {
		t.Errorf("location = %q", got)
	}
	if got := meta.OpenRoles[0].URL; got != "https://jobs.smartrecruiters.com/acme/744" {
		t.Errorf("url = %q", got)
	}
}

func TestRecruiteeExtraction(t *testing.T) {
	srv := jsonServer(t, `{"offers":[
	  {"id":9,"title":"Backend Developer","department":"Tech","city":"Amsterdam","country":"Netherlands","remote":true,"careers_url":"https://acme.recruitee.com/o/backend","created_at":"2026-09-03 08:00:00 UTC","description":"Node.js and PostgreSQL","requirements":"Redis"},
	  {"id":10,"title":"Recruiter","department":"People","careers_url":"https://acme.recruitee.com/o/recruiter","description":"hiring"}]}`)
	a := NewRecruiteeAdapter(testFetcher())
	a.apiURLFor = func(string) string { return srv.URL }

	if !a.Detect("https://acme.recruitee.com") {
		t.Error("Detect failed")
	}
	meta, err := a.ExtractByBoardID(context.Background(), "acme", "acme.com")
	if err != nil {
		t.Fatal(err)
	}
	assertSingleBackendRole(t, meta, "recruitee-")
}

func TestExtractErrorsOnBadStatusAndJSON(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer bad.Close()
	garbage := jsonServer(t, `{not json`)

	w := NewWorkableAdapter(testFetcher())
	w.baseURL = bad.URL
	if _, err := w.ExtractByBoardID(context.Background(), "x", ""); err == nil {
		t.Error("expected status error")
	}
	w.baseURL = garbage.URL
	if _, err := w.ExtractByBoardID(context.Background(), "x", ""); err == nil {
		t.Error("expected decode error")
	}
}

func TestRegistryResolvesNewAdapters(t *testing.T) {
	r := NewRegistry(testFetcher())
	for _, name := range []string{"greenhouse", "lever", "ashby", "workable", "smartrecruiters", "recruitee"} {
		if _, ok := r.boards[name]; !ok {
			t.Errorf("board %q not registered", name)
		}
	}
}
