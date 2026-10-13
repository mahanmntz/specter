package feeds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mahanmntz/specter/internal/ats"
	"github.com/mahanmntz/specter/internal/signals"
)

func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func roleByTitle(t *testing.T, metas []*ats.CompanyMeta, title string) ats.JobPosting {
	t.Helper()
	for _, m := range metas {
		for _, r := range m.OpenRoles {
			if r.Title == title {
				return r
			}
		}
	}
	t.Fatalf("role %q not found", title)
	return ats.JobPosting{}
}

func TestGeoPolicy(t *testing.T) {
	cases := []struct {
		geo    string
		scope  signals.Scope
		global bool
	}{
		{"Anywhere", signals.ScopeWorldwide, true},
		{"Worldwide", signals.ScopeWorldwide, true},
		{"USA, Anywhere", signals.ScopeWorldwide, true},
		{"Europe", signals.ScopeRegional, false},
		{"EMEA, APAC", signals.ScopeRegional, false},
		{"USA", signals.ScopeLocal, false},
		{"Canada, United Kingdom", signals.ScopeLocal, false},
		{"", signals.ScopeUnknown, false},
	}
	for _, c := range cases {
		got := geoPolicy(c.geo, false, "")
		if got.Scope != c.scope || got.IsGlobalRemote != c.global {
			t.Errorf("%q → %s global=%v, want %s %v", c.geo, got.Scope, got.IsGlobalRemote, c.scope, c.global)
		}
	}
	if got := geoPolicy("A, B, C, D, E", false, ""); !strings.Contains(got.Badge, "+2") {
		t.Errorf("long lists should be summarized: %q", got.Badge)
	}
}

func TestStripHTML(t *testing.T) {
	got := stripHTML(`<p>Build <b>Go</b> &amp; Postgres services.</p><ul><li>gRPC</li><li>Kafka</li></ul>`)
	for _, want := range []string{"Build Go & Postgres services.", "gRPC", "Kafka"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.ContainsAny(got, "<>") {
		t.Errorf("tags left: %q", got)
	}
}

func TestJobicyUsesStructuredGeo(t *testing.T) {
	srv := serve(t, `{"jobs":[
	 {"id":1,"url":"https://jobicy.com/jobs/1","jobTitle":"Senior Backend Engineer","companyName":"Acme","jobIndustry":["Software Engineering"],"jobGeo":"Anywhere","jobDescription":"<p>Go, PostgreSQL and Kafka. We pay contractors.</p>","pubDate":"2026-10-10T10:00:00+00:00","salaryMin":120000,"salaryMax":"160000","salaryCurrency":"USD","salaryPeriod":"yearly"},
	 {"id":2,"url":"https://jobicy.com/jobs/2","jobTitle":"Backend Engineer","companyName":"Beta","jobIndustry":["Software Engineering"],"jobGeo":"USA","jobDescription":"Python and Django."},
	 {"id":3,"url":"https://jobicy.com/jobs/3","jobTitle":"Backend Developer","companyName":"Gamma","jobIndustry":["Dev"],"jobGeo":"Europe","jobDescription":"Node.js and PostgreSQL."},
	 {"id":4,"url":"https://jobicy.com/jobs/4","jobTitle":"Marketing Lead","companyName":"Delta","jobIndustry":["Marketing"],"jobGeo":"Anywhere","jobDescription":"Brand."}]}`)
	f := NewJobicy(fetcher())
	f.apiURL = srv.URL
	metas, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 3 {
		t.Fatalf("want 3 companies with backend roles, got %d", len(metas))
	}
	w := roleByTitle(t, metas, "Senior Backend Engineer")
	if !w.GlobalRemote || !w.ContractorFriendly || w.Compensation != "USD 120k–160k/yr" {
		t.Errorf("worldwide role = %+v", w)
	}
	us := roleByTitle(t, metas, "Backend Engineer")
	if us.GlobalRemote || !strings.Contains(us.RemotePolicy, "USA") {
		t.Errorf("a USA role must not be worldwide: %+v", us)
	}
	eu := roleByTitle(t, metas, "Backend Developer")
	if eu.GlobalRemote || !strings.Contains(eu.RemotePolicy, "Europe") {
		t.Errorf("regional role = %+v", eu)
	}
	if !strings.HasPrefix(w.ID, "jobicy-") {
		t.Errorf("id = %s", w.ID)
	}
}

func TestHimalayasEligibilityFromRestrictions(t *testing.T) {
	tz := strings.TrimSuffix(strings.Repeat("0,", 24), ",") // a full 24-slot band
	srv := serve(t, `{"jobs":[
	 {"title":"Staff Backend Engineer","companyName":"Acme","guid":"https://himalayas.app/companies/acme/jobs/staff-be-1","applicationLink":"https://himalayas.app/companies/acme/jobs/staff-be-1","locationRestrictions":[],"timezoneRestrictions":[`+tz+`],"categories":["Backend-Engineer"],"excerpt":"Go and Kafka","pubDate":1791883995},
	 {"title":"Backend Engineer","companyName":"Beta","guid":"https://himalayas.app/companies/beta/jobs/be-2","locationRestrictions":["Germany","France","Spain","Italy"],"timezoneRestrictions":[1],"categories":["Backend-Engineer"],"excerpt":"Python and PostgreSQL"},
	 {"title":"Backend Developer","companyName":"Gamma","guid":"https://himalayas.app/companies/gamma/jobs/bd-3","locationRestrictions":[],"timezoneRestrictions":[0,1,2,3],"categories":["Backend"],"excerpt":"Node.js and Redis"}]}`)
	h := NewHimalayas(fetcher())
	h.apiBase = srv.URL
	h.Queries = func() []string { return []string{"backend"} }
	h.Pages = 1
	metas, err := h.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r := roleByTitle(t, metas, "Staff Backend Engineer"); !r.GlobalRemote {
		t.Errorf("no restrictions + full timezone band = worldwide: %+v", r)
	}
	if r := roleByTitle(t, metas, "Backend Engineer"); r.GlobalRemote || !strings.Contains(r.RemotePolicy, "Germany") || !strings.Contains(r.RemotePolicy, "+1") {
		t.Errorf("country-limited role = %+v", r)
	}
	if r := roleByTitle(t, metas, "Backend Developer"); r.GlobalRemote || !strings.Contains(r.RemotePolicy, "UTC") {
		t.Errorf("timezone-limited role = %+v", r)
	}
}

func TestRemotiveAndArbeitnow(t *testing.T) {
	rem := serve(t, `{"jobs":[
	 {"id":10,"url":"https://remotive.com/remote-jobs/software-dev/be-10","title":"Backend Engineer","company_name":"Acme","category":"Software Development","tags":["go","postgres"],"publication_date":"2026-10-11T20:17:27","candidate_required_location":"Worldwide","salary":"$100k","description":"<p>Go services</p>"},
	 {"id":11,"url":"https://remotive.com/remote-jobs/software-dev/be-11","title":"Platform Engineer","company_name":"Beta","category":"DevOps","tags":["kubernetes","go"],"publication_date":"2026-10-11T20:17:27","candidate_required_location":"USA Only","description":"Kubernetes and Go"}]}`)
	r := NewRemotive(fetcher())
	r.apiURL = rem.URL
	if r.MinInterval() < 6*3600*1e9 {
		t.Error("Remotive must declare its rate limit")
	}
	metas, err := r.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if w := roleByTitle(t, metas, "Backend Engineer"); !w.GlobalRemote || w.Compensation != "$100k" || !strings.HasPrefix(w.ID, "remotive-") {
		t.Errorf("worldwide = %+v", w)
	}
	if u := roleByTitle(t, metas, "Platform Engineer"); u.GlobalRemote {
		t.Errorf("USA-only must not be worldwide: %+v", u)
	}

	arb := serve(t, `{"data":[
	 {"slug":"be-berlin-1","company_name":"Acme GmbH","title":"Backend Engineer (m/w/d)","description":"<p>Go and PostgreSQL</p>","remote":true,"url":"https://www.arbeitnow.com/jobs/be-berlin-1","tags":["Backend"],"location":"Berlin","created_at":1791892877},
	 {"slug":"be-munich-2","company_name":"Beta AG","title":"Backend Developer","description":"Python and Django","remote":false,"url":"https://www.arbeitnow.com/jobs/be-munich-2","tags":[],"location":"Munich","created_at":1791892877}]}`)
	a := NewArbeitnow(fetcher())
	a.apiURL = arb.URL
	a.Pages = 1
	metas, err = a.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rr := roleByTitle(t, metas, "Backend Engineer (m/w/d)"); rr.GlobalRemote || !strings.Contains(rr.RemotePolicy, "Berlin") {
		t.Errorf("remote-in-Germany role = %+v", rr)
	}
	if rr := roleByTitle(t, metas, "Backend Developer"); rr.GlobalRemote || strings.Contains(rr.RemotePolicy, "Worldwide") {
		t.Errorf("on-site role = %+v", rr)
	}
}

func TestNamesIncludeAllFeeds(t *testing.T) {
	if got := strings.Join(Names(), ","); got != "arbeitnow,himalayas,hn,jobicy,remoteok,remotive" {
		t.Errorf("names = %s", got)
	}
}
