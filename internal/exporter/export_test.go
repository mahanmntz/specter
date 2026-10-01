package exporter

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"specter/internal/ats"
)

func TestExportRolesCSV(t *testing.T) {
	roles := []ats.JobPosting{
		{
			ID:                 "r-101",
			CompanyDomain:      "canonical.com",
			CompanyName:        "Canonical",
			Title:              "Senior Linux Kernel Engineer",
			ApplyURL:           "https://boards.greenhouse.io/canonical/jobs/101#app",
			URL:                "https://boards.greenhouse.io/canonical/jobs/101",
			Location:           "Worldwide",
			RemotePolicy:       "🟢 Worldwide / Contractor-Friendly",
			GlobalRemote:       true,
			ContractorFriendly: true,
			Compensation:       "$130,000 - $160,000",
			Seniority:          "Senior",
			Department:         "Engineering",
			FirstSeenAt:        time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		},
	}

	var buf bytes.Buffer
	if err := ExportRolesCSV(&buf, roles); err != nil {
		t.Fatalf("ExportRolesCSV failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Apply URL") {
		t.Errorf("expected header with Apply URL")
	}
	if !strings.Contains(out, "canonical.com") {
		t.Errorf("expected canonical.com in csv output")
	}
	if !strings.Contains(out, "https://boards.greenhouse.io/canonical/jobs/101#app") {
		t.Errorf("expected direct apply url in csv output")
	}
	if !strings.Contains(out, "true,true") {
		t.Errorf("expected global_remote and contractor_friendly flags true")
	}
}
