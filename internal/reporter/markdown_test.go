package reporter

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"specter/internal/ats"
	"specter/internal/signals"
	"specter/internal/storage"
)

func TestGenerateMarkdownReport(t *testing.T) {
	dbFile := "test_report.db"
	outDir := "test_reports_out"
	defer os.Remove(dbFile)
	defer os.RemoveAll(outDir)

	store, err := storage.NewStore(dbFile)
	if err != nil {
		t.Fatalf("failed init store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Seed company & roles
	meta := &ats.CompanyMeta{
		Name:       "TestCorp",
		Domain:     "testcorp.com",
		CareersURL: "https://jobs.lever.co/testcorp",
		GitHubOrg:  "testcorp-org",
		Signals:    []string{"Go", "Redis", "Kafka", "Distributed Systems"},
		OpenRoles: []ats.JobPosting{
			{
				ID:        "role-1",
				Title:     "Staff Distributed Systems Architect",
				URL:       "https://jobs.lever.co/testcorp/role-1",
				Location:  "San Francisco, CA",
				Seniority: "Staff/Principal",
				Keywords:  []string{"Go", "Distributed Systems"},
				PostedAt:  time.Now(),
			},
		},
	}
	_, _, err = store.SaveCompanyAndRoles(ctx, meta)
	if err != nil {
		t.Fatalf("failed saving company: %v", err)
	}

	// Seed engineering leads
	leads := []signals.EngineeringLead{
		{
			Domain:         "testcorp.com",
			Name:           "Dev Lead Dave",
			Role:           "Tech Lead",
			Email:          "dave@testcorp.com",
			Source:         "git_commit",
			GitHubHandle:   "davedev",
			TopLanguages:   "Go, Rust",
			RelevanceScore: 95,
			DiscoveredAt:   time.Now(),
		},
	}
	_, err = store.SaveEngineeringLeads(ctx, leads)
	if err != nil {
		t.Fatalf("failed saving leads: %v", err)
	}

	reportPath, err := GenerateMarkdownReport(ctx, store, "testcorp.com", outDir)
	if err != nil {
		t.Fatalf("GenerateMarkdownReport failed: %v", err)
	}

	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed reading generated report: %v", err)
	}

	reportStr := string(content)

	// Check sections
	if !strings.Contains(reportStr, "Executive Technical Recon Dossier: TestCorp") {
		t.Errorf("missing title in report")
	}
	if !strings.Contains(reportStr, "Staff Distributed Systems Architect") {
		t.Errorf("missing open role in report")
	}
	if !strings.Contains(reportStr, "Dev Lead Dave") || !strings.Contains(reportStr, "dave@testcorp.com") {
		t.Errorf("missing lead details in report")
	}
	if !strings.Contains(reportStr, "Contextual Outreach Drafts") {
		t.Errorf("missing outreach templates in report")
	}
	if !strings.Contains(reportStr, "Template A: Architecture & Git Commit Overlap") {
		t.Errorf("missing Template A in report")
	}
}
