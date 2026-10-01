package reporter

import (
	"os"
	"strings"
	"testing"
	"time"

	"specter/internal/ats"
	"specter/internal/signals"
	"specter/internal/storage"
)

func TestGenerateDeltaReport(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "specter_delta_test_*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	prevRun := &storage.CrawlRun{
		RunID:          "2026-10-01_10-00-00",
		StartedAt:      time.Now().Add(-2 * time.Hour),
		CompletedAt:    time.Now().Add(-1 * time.Hour),
		TotalCompanies: 10,
		TotalRoles:     25,
		NewRoles:       25,
		TotalLeads:     15,
		NewLeads:       15,
	}

	currRun := &storage.CrawlRun{
		RunID:          "2026-10-01_12-00-00",
		StartedAt:      time.Now().Add(-10 * time.Minute),
		CompletedAt:    time.Now(),
		TotalCompanies: 12,
		TotalRoles:     30,
		NewRoles:       5,
		TotalLeads:     18,
		NewLeads:       3,
	}

	freshRoles := []ats.JobPosting{
		{
			ID:            "new-role-1",
			Title:         "Distributed Systems Core Engineer",
			URL:           "https://jobs.lever.co/questdb/1",
			ApplyURL:      "https://jobs.lever.co/questdb/1/apply",
			CompanyDomain: "questdb.io",
			RemotePolicy:  "🟢 Worldwide / Contractor-Friendly",
			Location:      "Remote (Worldwide)",
			Compensation:  "$150k - $180k",
		},
	}

	freshLeads := []signals.EngineeringLead{
		{
			Name:           "Alice Smith",
			Role:           "Core Storage Engineer",
			GitHubHandle:   "alicesmith",
			RelevanceScore: 92,
			RepoName:       "questdb/questdb",
			RepoURL:        "https://github.com/questdb/questdb",
		},
	}

	filePath, content, err := GenerateDeltaReport(currRun, prevRun, freshRoles, freshLeads, tempDir)
	if err != nil {
		t.Fatalf("GenerateDeltaReport failed: %v", err)
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatalf("file not found: %s", filePath)
	}

	if !strings.Contains(content, "Autonomous Recon Delta Report") {
		t.Errorf("missing title in content")
	}
	if !strings.Contains(content, "questdb.io") {
		t.Errorf("missing questdb.io in delta report")
	}
	if !strings.Contains(content, "alicesmith") {
		t.Errorf("missing alicesmith lead in delta report")
	}
	if !strings.Contains(content, "+5 fresh") {
		t.Errorf("missing delta count +5 fresh")
	}
}
