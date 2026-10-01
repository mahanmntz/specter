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
