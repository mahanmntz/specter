package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mahanmntz/specter/internal/ats"
	"github.com/mahanmntz/specter/internal/storage"
)

type fakeSource struct {
	roles    []ats.JobPosting
	tracking map[string]storage.Tracking
	closed   []ats.JobPosting // postings taken down since they were tracked
	settings map[string]string
	runs     []*storage.CrawlRun
}

func (f *fakeSource) ListDirectApplyRoles(_ context.Context, flt storage.DirectApplyFilter) ([]ats.JobPosting, error) {
	if len(flt.ExternalIDs) == 0 {
		return f.roles, nil
	}
	want := map[string]bool{}
	for _, id := range flt.ExternalIDs {
		want[id] = true
	}
	var out []ats.JobPosting
	for _, r := range append(append([]ats.JobPosting{}, f.roles...), f.closed...) {
		if want[r.ID] && (flt.IncludeInactive || !r.Closed) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeSource) ListRoleTracking(context.Context) (map[string]storage.Tracking, error) {
	out := map[string]storage.Tracking{}
	for k, v := range f.tracking {
		out[k] = v
	}
	return out, nil
}

// UpdateTracking mimics the real store closely enough for handler tests.
func (f *fakeSource) UpdateTracking(_ context.Context, id string, u storage.TrackingUpdate) error {
	t := f.tracking[id]
	if u.Status != nil {
		if *u.Status != "" && !storage.ValidRoleStatus(*u.Status) {
			return fmt.Errorf("invalid status %q", *u.Status)
		}
		t.Status = *u.Status
		if t.Status == storage.StatusApplied && t.AppliedAt.IsZero() {
			t.AppliedAt = now
			t.FollowUpAt = now.Add(storage.DefaultFollowUp)
		}
	}
	if u.Note != nil {
		t.Note = *u.Note
	}
	if u.FollowUp != nil {
		t.FollowUpAt = *u.FollowUp
	}
	if t.Status == "" && t.Note == "" {
		delete(f.tracking, id)
	} else {
		if t.Status == "" {
			t.Status = storage.StatusSaved
		}
		f.tracking[id] = t
	}
	return nil
}
func (f *fakeSource) GetLastCrawlRun(context.Context) (*storage.CrawlRun, error) {
	return &storage.CrawlRun{CompletedAt: time.Now()}, nil
}
func (f *fakeSource) RecordCrawlRun(_ context.Context, run *storage.CrawlRun) error {
	f.runs = append(f.runs, run)
	return nil
}
func (f *fakeSource) GetSetting(_ context.Context, k string) (string, bool, error) {
	v, ok := f.settings[k]
	return v, ok, nil
}
func (f *fakeSource) SetSetting(_ context.Context, k, v string) error {
	f.settings[k] = v
	return nil
}

var now = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

func newFake() *fakeSource {
	day := func(n int) time.Time { return now.Add(-time.Duration(n) * 24 * time.Hour) }
	return &fakeSource{tracking: map[string]storage.Tracking{}, settings: map[string]string{}, roles: []ats.JobPosting{
		{ID: "1", Title: "Senior Backend Engineer", CompanyName: "Acme", CompanyDomain: "acme.com", Location: "Remote - Worldwide",
			Seniority: "Senior", Keywords: []string{"go", "redis"}, GlobalRemote: true, ContractorFriendly: true, FirstSeenAt: day(1), ApplyURL: "https://acme.com/apply/1"},
		{ID: "2", Title: "Staff Platform Engineer", CompanyName: "Initech", CompanyDomain: "initech.com", Location: "Berlin",
			Seniority: "Staff/Principal", Keywords: []string{"kubernetes", "go"}, FirstSeenAt: day(20), URL: "https://initech.com/2"},
		{ID: "3", Title: "Python Developer", CompanyName: "Globex", CompanyDomain: "globex.io", Location: "Remote",
			Seniority: "Mid/Standard", Keywords: []string{"python"}, ContractorFriendly: true, FirstSeenAt: day(5), ApplyURL: "https://globex.io/3"},
	}}
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func jobsFor(t *testing.T, s *Server, query string) (int, []string) {
	t.Helper()
	rec := get(t, s.Handler(), "/api/jobs?"+query)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Total int
		Jobs  []Job
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, j := range out.Jobs {
		ids = append(ids, j.ID)
	}
	return out.Total, ids
}

func newServer(src Source) *Server {
	s := New(src, Options{Skills: []string{"Go", "Redis", "Backend"}})
	s.now = func() time.Time { return now }
	return s
}

func TestFilters(t *testing.T) {
	s := newServer(newFake())
	cases := []struct {
		query string
		want  string
	}{
		{"", "1,3,2"}, // best: worldwide first, then contractor, then rest
		{"global=1", "1"},
		{"contractor=1", "1,3"},
		{"q=platform", "2"},
		{"q=remote+go", "1"}, // all terms must match
		{"fresh=7", "1,3"},
		{"fresh=1", "1"},
		{"seniority=Staff/Principal", "2"},
		{"keyword=go", "1,2"},
		{"keyword=go,redis", "1"},
		{"company=glob", "3"},
		{"min_score=45", "1,2"},
		{"sort=newest", "1,3,2"},
		{"sort=company", "1,3,2"},
	}
	for _, c := range cases {
		_, ids := jobsFor(t, s, c.query)
		if got := strings.Join(ids, ","); got != c.want {
			t.Errorf("%q: got %s want %s", c.query, got, c.want)
		}
	}
}

func TestPagination(t *testing.T) {
	s := newServer(newFake())
	total, ids := jobsFor(t, s, "limit=2&offset=1")
	if total != 3 || len(ids) != 2 {
		t.Errorf("total=%d ids=%v", total, ids)
	}
	_, ids = jobsFor(t, s, "limit=2&offset=99")
	if len(ids) != 0 {
		t.Errorf("offset past end should be empty, got %v", ids)
	}
}

func TestStatusFlowAndTabs(t *testing.T) {
	src := newFake()
	s := newServer(src)
	h := s.Handler()

	post := func(body string, header bool) int {
		req := httptest.NewRequest(http.MethodPost, "/api/status", strings.NewReader(body))
		req.Host = "localhost:8080"
		if header {
			req.Header.Set("X-Specter", "1")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if post(`{"id":"1","status":"hidden"}`, false) != http.StatusForbidden {
		t.Error("POST without X-Specter header must be rejected")
	}
	if post(`{"id":"1","status":"bogus"}`, true) != http.StatusBadRequest || post(`{`, true) != http.StatusBadRequest {
		t.Error("invalid input must be 400")
	}
	if post(`{"id":"1","status":"hidden"}`, true) != 200 || post(`{"id":"2","status":"saved"}`, true) != 200 {
		t.Fatal("valid status updates failed")
	}

	tabs := map[string]string{"": "3,2", "all": "1,3,2", "hidden": "1", "saved": "2", "new": "3"}
	for status, want := range tabs {
		_, ids := jobsFor(t, s, "status="+status)
		if got := strings.Join(ids, ","); got != want {
			t.Errorf("status=%q: got %s want %s", status, got, want)
		}
	}

	if post(`{"id":"1","status":""}`, true) != 200 { // clear
		t.Fatal("clear failed")
	}
	if _, ids := jobsFor(t, s, ""); len(ids) != 3 {
		t.Errorf("job should reappear after clearing hidden, got %v", ids)
	}
}

func TestFacets(t *testing.T) {
	s := newServer(newFake())
	rec := get(t, s.Handler(), "/api/facets")
	var f struct {
		Total, Global, Contractor, Companies int
		NewWeek                              int `json:"new_week"`
		Seniority, Keywords                  []facetCount
		LastRun                              string `json:"last_run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if f.Total != 3 || f.Global != 1 || f.Contractor != 2 || f.Companies != 3 || f.NewWeek != 2 || len(f.Seniority) != 3 || f.LastRun == "" {
		t.Errorf("facets = %+v", f)
	}
	if f.Keywords[0].Name != "go" || f.Keywords[0].Count != 2 {
		t.Errorf("keywords = %+v", f.Keywords)
	}
}

func TestHostGuardAndStaticAssets(t *testing.T) {
	h := newServer(newFake()).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign Host must be rejected (DNS rebinding), got %d", rec.Code)
	}

	for _, path := range []string{"/", "/app.js", "/style.css"} {
		rec := get(t, h, path)
		if rec.Code != 200 || rec.Body.Len() == 0 {
			t.Errorf("%s: status %d", path, rec.Code)
		}
	}
	rec = get(t, h, "/")
	if !strings.Contains(rec.Body.String(), "Specter") || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") {
		t.Error("index/CSP missing")
	}
}
