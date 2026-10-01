package reporter

import (
	"os"
	"strings"
	"testing"
	"time"

	"specter/internal/ats"
)

func TestGenerateDirectApplyReport(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "specter_direct_apply_test_*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	roles := []ats.JobPosting{
		{
			ID:                 "role-1",
			Title:              "Staff Go Backend Engineer",
			URL:                "https://boards.greenhouse.io/canonical/jobs/1",
			ApplyURL:           "https://boards.greenhouse.io/canonical/jobs/1#app",
			Location:           "Worldwide",
			CompanyDomain:      "canonical.com",
			WorkplaceType:      "Global Remote (Anywhere)",
			RemotePolicy:       "🟢 Worldwide / Contractor-Friendly",
			GlobalRemote:       true,
			ContractorFriendly: true,
			Compensation:       "$140,000 - $180,000 USD",
			IsNew:              true,
			PostedAt:           time.Now(),
		},
		{
			ID:                 "role-2",
			Title:              "Backend Infrastructure Engineer",
			URL:                "https://jobs.lever.co/posthog/2",
			ApplyURL:           "https://jobs.lever.co/posthog/2/apply",
			Location:           "Remote (US / EU)",
			CompanyDomain:      "posthog.com",
			WorkplaceType:      "Timezone Flexible",
			RemotePolicy:       "🟡 Timezone Flexible (EMEA/UTC Overlap)",
			GlobalRemote:       false,
			ContractorFriendly: true,
			Compensation:       "$130,000 USD",
			IsNew:              false,
			PostedAt:           time.Now(),
		},
	}

	filePath, content, err := GenerateDirectApplyReport(roles, tempDir)
	if err != nil {
		t.Fatalf("GenerateDirectApplyReport failed: %v", err)
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatalf("output file does not exist: %s", filePath)
	}

	if !strings.Contains(content, "Direct One-Click Apply Dashboard") {
		t.Errorf("missing title in report")
	}
	if !strings.Contains(content, "canonical.com") {
		t.Errorf("missing company name canonical.com in report")
	}
	if !strings.Contains(content, "https://boards.greenhouse.io/canonical/jobs/1#app") {
		t.Errorf("missing direct apply deep link in report")
	}
	if !strings.Contains(content, "🔥 NEW") {
		t.Errorf("expected 🔥 NEW badge for fresh role")
	}
}
