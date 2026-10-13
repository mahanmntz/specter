package signals

import (
	"strings"
	"testing"
)

func TestClassifyRemotePolicy_Worldwide(t *testing.T) {
	location := "Remote - Worldwide"
	desc := "We are a fully distributed team. You can work from anywhere. We support contractors and B2B invoices via Deel."
	url := "https://boards.greenhouse.io/sourcegraph/jobs/12345"

	res := ClassifyRemotePolicy(location, desc, url)
	if res.Tier != TierGlobalRemote {
		t.Fatalf("expected TierGlobalRemote, got %v", res.Tier)
	}
	if !res.IsGlobalRemote || !res.SanctionSafe {
		t.Errorf("expected IsGlobalRemote and SanctionSafe to be true")
	}
	if !strings.HasSuffix(res.DirectApplyURL, "#app") {
		t.Errorf("expected greenhouse apply url to end with #app, got %s", res.DirectApplyURL)
	}
}

func TestClassifyRemotePolicy_GeoRestricted(t *testing.T) {
	location := "Remote (US Only)"
	desc := "Candidates must be located in the United States and require US citizenship or W2 authorization. Security clearance required."
	url := "https://jobs.lever.co/defensecorp/67890"

	res := ClassifyRemotePolicy(location, desc, url)
	if res.Tier != TierGeoRestricted {
		t.Fatalf("expected TierGeoRestricted, got %v", res.Tier)
	}
	if res.SanctionSafe {
		t.Errorf("expected SanctionSafe to be false for US-restricted job")
	}
	if !strings.HasSuffix(res.DirectApplyURL, "/apply") {
		t.Errorf("expected lever apply url to end with /apply, got %s", res.DirectApplyURL)
	}
}

func TestClassifyRemotePolicy_TimezoneFlexible(t *testing.T) {
	location := "Remote (EMEA)"
	desc := "Looking for engineers with 4 hours overlap with UTC. Competitive contractor package."
	url := "https://jobs.ashbyhq.com/linear/abcde"

	res := ClassifyRemotePolicy(location, desc, url)
	if res.Tier != TierTimezoneFlexible {
		t.Fatalf("expected TierTimezoneFlexible, got %v", res.Tier)
	}
	if !res.SanctionSafe {
		t.Errorf("expected SanctionSafe to be true for EMEA flexible")
	}
	if !strings.HasSuffix(res.DirectApplyURL, "/application") {
		t.Errorf("expected ashby apply url to end with /application, got %s", res.DirectApplyURL)
	}
}

func TestGenerateDirectApplyURL(t *testing.T) {
	cases := []struct {
		in  string
		out string
	}{
		{"https://boards.greenhouse.io/stripe/jobs/100", "https://boards.greenhouse.io/stripe/jobs/100#app"},
		{"https://jobs.lever.co/netflix/200", "https://jobs.lever.co/netflix/200/apply"},
		{"https://jobs.ashbyhq.com/linear/300", "https://jobs.ashbyhq.com/linear/300/application"},
		{"https://example.com/careers/job-1", "https://example.com/careers/job-1"},
	}

	for _, c := range cases {
		got := GenerateDirectApplyURL(c.in)
		if got != c.out {
			t.Errorf("in=%s expected=%s got=%s", c.in, c.out, got)
		}
	}
}

func TestGenerateQuickPitch(t *testing.T) {
	pitch := GenerateQuickPitch("Senior Go Engineer", "Cockroach Labs", "Go, Pebble, Distributed SQL")
	if !strings.Contains(pitch, "Cockroach Labs") {
		t.Errorf("missing company name in pitch")
	}
	if !strings.Contains(pitch, "contractor/B2B") {
		t.Errorf("missing contractor/B2B mention in pitch")
	}
	if !strings.Contains(pitch, "Senior Go Engineer") {
		t.Errorf("missing role title in pitch")
	}
}

func TestClassifyRemotePolicyScopes(t *testing.T) {
	cases := []struct {
		name, location, desc string
		scope                Scope
		global               bool
	}{
		{"explicit worldwide", "Remote - Worldwide", "", ScopeWorldwide, true},
		{"global in parens", "Remote (Global)", "", ScopeWorldwide, true},
		{"anywhere", "Anywhere", "", ScopeWorldwide, true},
		{"named country is local, not worldwide", "Remote - Canada", "Join our team.", ScopeLocal, false},
		{"India", "Remote India", "Work with Kubernetes and Go. Async friendly.", ScopeLocal, false},
		{"two countries", "Remote (US or Canada)", "", ScopeLocal, false},
		{"list of places", "Remote, Poland; Remote, United Kingdom", "", ScopeLocal, false},
		{"emea band", "Remote (EMEA)", "", ScopeRegional, false},
		{"europe", "Remote - Europe", "", ScopeRegional, false},
		{"bare remote, silent description", "Remote", "We build great software. Async culture, invoices welcome.", ScopeUnknown, false},
		{"bare remote, description says anywhere", "Remote", "You can work from anywhere in the world.", ScopeWorldwide, true},
		{"bare remote, description says EMEA overlap", "Remote", "Must overlap with UTC+1 to UTC+3.", ScopeRegional, false},
		{"office", "San Francisco, CA", "", ScopeOnsite, false},
		{"hybrid city", "Hybrid - London", "", ScopeOnsite, false},
		{"empty location says nothing", "", "A great job.", ScopeOnsite, false},
		{"description restriction beats worldwide location", "Remote - Worldwide", "Must be authorized to work in the United States without sponsorship.", ScopeRestrict, false},
	}
	for _, c := range cases {
		got := ClassifyRemotePolicy(c.location, c.desc, "https://example.com/j/1")
		if got.Scope != c.scope || got.IsGlobalRemote != c.global {
			t.Errorf("%s: %q → scope=%s global=%v badge=%q; want scope=%s global=%v", c.name, c.location, got.Scope, got.IsGlobalRemote, got.Badge, c.scope, c.global)
		}
	}
}

func TestOnlyWorldwideIsGlobalAndContractorIsNotAssumed(t *testing.T) {
	// A worldwide posting that says nothing about contractors must not be flagged contractor-friendly.
	if r := ClassifyRemotePolicy("Remote - Worldwide", "Join us. Great benefits.", ""); !r.IsGlobalRemote || r.IsContractorFriendly {
		t.Errorf("worldwide=%v contractor=%v", r.IsGlobalRemote, r.IsContractorFriendly)
	}
	if r := ClassifyRemotePolicy("Remote - Worldwide", "We hire independent contractors via Deel.", ""); !r.IsContractorFriendly {
		t.Error("contractor wording should be detected")
	}
	if r := ClassifyRemotePolicy("Remote - Worldwide", "Employees only. No contractors.", ""); r.IsContractorFriendly {
		t.Error("an explicit 'no contractors' must not count")
	}
	// Honest default: remote with no stated region is not claimed to be worldwide or sanction-safe.
	if r := ClassifyRemotePolicy("Remote", "Build things.", ""); r.IsGlobalRemote || r.SanctionSafe || r.Tier != TierUnknown {
		t.Errorf("unknown scope must not be presented as open: %+v", r)
	}
}

func TestStructuredPolicy(t *testing.T) {
	w := StructuredPolicy(ScopeWorldwide, nil, true, "https://boards.greenhouse.io/x/jobs/1")
	if !w.IsGlobalRemote || !w.IsContractorFriendly || !strings.HasSuffix(w.DirectApplyURL, "#app") {
		t.Errorf("worldwide = %+v", w)
	}
	r := StructuredPolicy(ScopeRegional, []string{"Europe", "UTC+0 to UTC+3"}, false, "")
	if r.IsGlobalRemote || !strings.Contains(r.Badge, "Europe") || r.Tier != TierTimezoneFlexible {
		t.Errorf("regional = %+v", r)
	}
	l := StructuredPolicy(ScopeLocal, []string{"Germany"}, false, "")
	if l.IsGlobalRemote || l.SanctionSafe || l.Tier != TierGeoRestricted {
		t.Errorf("local = %+v", l)
	}
	if StructuredPolicy(Scope(""), nil, false, "").Tier != TierUnknown {
		t.Error("unknown scope")
	}
}
