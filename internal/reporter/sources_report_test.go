package reporter

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"specter/internal/signals"
	"specter/internal/storage"
)

func TestGenerateSourcesReport(t *testing.T) {
	dbFile := "test_sources_report.db"
	outDir := "test_sources_out"
	defer os.Remove(dbFile)
	defer os.RemoveAll(outDir)

	store, err := storage.NewStore(dbFile)
	if err != nil {
		t.Fatalf("failed init store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Seed leads with provenance and archetypes
	leads := []signals.EngineeringLead{
		{
			Domain:         "cockroachlabs.com",
			CompanyDomain:  "cockroachlabs.com",
			Name:           "Lead Alice",
			Role:           "Engineering Leadership",
			Email:          "alice@cockroachlabs.com",
			Source:         "git_commit",
			GitHubHandle:   "alice-eng",
			TopLanguages:   "Go, C++",
			RelevanceScore: 98,
			RepoName:       "cockroachdb/cockroach",
			RepoURL:        "https://github.com/cockroachdb/cockroach",
			CommitSHA:      "abcdef1234567890",
			CommitURL:      "https://github.com/cockroachdb/cockroach/commit/abcdef1234567890",
			LinkedInURL:    "https://linkedin.com/in/alice-eng",
			DiscoveredAt:   time.Now(),
		},
		{
			Domain:         "cockroachlabs.com",
			CompanyDomain:  "cockroachlabs.com",
			Name:           "Principal Bob",
			Role:           "Staff / Principal",
			Email:          "bob@cockroachlabs.com",
			Source:         "git_commit",
			GitHubHandle:   "bob-staff",
			TopLanguages:   "Go, Raft",
			RelevanceScore: 92,
			RepoName:       "cockroachdb/cockroach",
			RepoURL:        "https://github.com/cockroachdb/cockroach",
			CommitSHA:      "1234567890abcdef",
			CommitURL:      "https://github.com/cockroachdb/cockroach/commit/1234567890abcdef",
			DiscoveredAt:   time.Now(),
		},
		{
			Domain:         "hashicorp.com",
			CompanyDomain:  "hashicorp.com",
			Name:           "Senior Carol",
			Role:           "Senior Backend",
			Email:          "carol@hashicorp.com",
			Source:         "git_commit",
			GitHubHandle:   "carol-dev",
			TopLanguages:   "Go, Consul",
			RelevanceScore: 88,
			RepoName:       "hashicorp/consul",
			RepoURL:        "https://github.com/hashicorp/consul",
			CommitSHA:      "fedcba0987654321",
			CommitURL:      "https://github.com/hashicorp/consul/commit/fedcba0987654321",
			LinkedInURL:    "https://linkedin.com/in/carol-dev",
			DiscoveredAt:   time.Now(),
		},
	}

	_, err = store.SaveEngineeringLeads(ctx, leads)
	if err != nil {
		t.Fatalf("failed saving leads: %v", err)
	}

	yieldStats := []*signals.RepoYieldStats{
		{
			RepoName:        "cockroachdb/cockroach",
			RepoURL:         "https://github.com/cockroachdb/cockroach",
			CommitsScanned:  25,
			VerifiedLeads:   2,
			ExtractedEmails: 2,
			PrimaryStack:    "Go, Distributed SQL",
		},
		{
			RepoName:        "hashicorp/consul",
			RepoURL:         "https://github.com/hashicorp/consul",
			CommitsScanned:  20,
			VerifiedLeads:   1,
			ExtractedEmails: 1,
			PrimaryStack:    "Go, Service Mesh",
		},
	}

	reportPath, err := GenerateSourcesReport(ctx, store, yieldStats, outDir)
	if err != nil {
		t.Fatalf("GenerateSourcesReport failed: %v", err)
	}

	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed reading sources report: %v", err)
	}

	reportStr := string(content)

	if !strings.Contains(reportStr, "# Source Analytics & Engineering Lead Yield Report") {
		t.Errorf("missing main title")
	}
	if !strings.Contains(reportStr, "Total Repositories Scanned:** 2") {
		t.Errorf("missing or incorrect total repositories scanned")
	}
	if !strings.Contains(reportStr, "Total Commits Inspected:** 45") {
		t.Errorf("missing or incorrect total commits inspected")
	}
	if !strings.Contains(reportStr, "Total Verified Leads:** 3") {
		t.Errorf("missing or incorrect total verified leads")
	}
	if !strings.Contains(reportStr, "Top Producing Repositories") {
		t.Errorf("missing Top Producing Repositories section")
	}
	if !strings.Contains(reportStr, "cockroachdb/cockroach") || !strings.Contains(reportStr, "hashicorp/consul") {
		t.Errorf("missing repository names in table")
	}
	if !strings.Contains(reportStr, "Engineering Leadership") || !strings.Contains(reportStr, "Staff / Principal") {
		t.Errorf("missing archetype distribution rows")
	}
	if !strings.Contains(reportStr, "Target Domain Coverage") {
		t.Errorf("missing domain coverage section")
	}
}
