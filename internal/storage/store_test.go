package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"specter/internal/ats"
	"specter/internal/signals"
)

func TestStoreIdempotency(t *testing.T) {
	tmpDB := "test_specter.db"
	defer os.Remove(tmpDB)

	store, err := NewStore(tmpDB)
	if err != nil {
		t.Fatalf("failed creating test store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	meta := &ats.CompanyMeta{
		Name:       "Acme Infra",
		Domain:     "acme.com",
		CareersURL: "https://boards.greenhouse.io/acme",
		GitHubOrg:  "acme-infra",
		Signals:    []string{"go", "distributed systems", "redis"},
		OpenRoles: []ats.JobPosting{
			{
				ID:         "gh-101",
				Title:      "Senior Backend Engineer",
				URL:        "https://boards.greenhouse.io/acme/jobs/101",
				Location:   "Remote",
				Department: "Core Platform",
				Seniority:  "Senior",
				Keywords:   []string{"go", "redis"},
				PostedAt:   time.Now().UTC(),
			},
		},
	}

	// 1. Initial insert
	compID, newRoles, err := store.SaveCompanyAndRoles(ctx, meta)
	if err != nil {
		t.Fatalf("first save failed: %v", err)
	}
	if newRoles != 1 {
		t.Errorf("expected 1 new role, got %d", newRoles)
	}

	// 2. Duplicate insert: verify strict idempotency
	compID2, newRoles2, err := store.SaveCompanyAndRoles(ctx, meta)
	if err != nil {
		t.Fatalf("second save failed: %v", err)
	}
	if compID != compID2 {
		t.Errorf("expected same company ID %d, got %d", compID, compID2)
	}
	if newRoles2 != 0 {
		t.Errorf("expected 0 new roles on duplicate insert, got %d", newRoles2)
	}

	// 3. Insert Leads
	leads := []signals.Lead{
		{
			Name:           "Jane Doe",
			Email:          "jane@example.com",
			GitHubUsername: "janedoe",
			RoleTitle:      "Staff Systems Engineer",
			Source:         "https://github.com/acme-infra/core",
		},
	}

	newLeads, err := store.SaveLeads(ctx, compID, leads)
	if err != nil {
		t.Fatalf("failed saving leads: %v", err)
	}
	if newLeads != 1 {
		t.Errorf("expected 1 new lead, got %d", newLeads)
	}

	// 4. Duplicate lead insert
	newLeadsDup, err := store.SaveLeads(ctx, compID, leads)
	if err != nil {
		t.Fatalf("failed saving duplicate leads: %v", err)
	}
	if newLeadsDup != 0 {
		t.Errorf("expected 0 new leads on duplicate, got %d", newLeadsDup)
	}

	// 5. Query leads
	uncontacted, err := store.ListLeads(ctx, true)
	if err != nil {
		t.Fatalf("failed listing uncontacted leads: %v", err)
	}
	if len(uncontacted) != 1 {
		t.Fatalf("expected 1 uncontacted lead, got %d", len(uncontacted))
	}

	// 6. Mark lead contacted
	if err := store.MarkLeadContacted(ctx, uncontacted[0].ID); err != nil {
		t.Fatalf("failed marking lead contacted: %v", err)
	}

	uncontactedAfter, _ := store.ListLeads(ctx, true)
	if len(uncontactedAfter) != 0 {
		t.Errorf("expected 0 uncontacted leads after update, got %d", len(uncontactedAfter))
	}
}

func TestSaveEngineeringLeadsProvenanceAndEnrichment(t *testing.T) {
	tmpDB := "test_provenance.db"
	defer os.Remove(tmpDB)

	store, err := NewStore(tmpDB)
	if err != nil {
		t.Fatalf("failed creating store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	leads := []signals.EngineeringLead{
		{
			Domain:         "cockroachlabs.com",
			CompanyDomain:  "cockroachlabs.com",
			Name:           "Vishal Jaishankar",
			Role:           "Tech Lead",
			Email:          "vishal.jaishankar@cockroachlabs.com",
			Source:         "git_commit",
			GitHubHandle:   "VishalJaishankar",
			TopLanguages:   "Go",
			RelevanceScore: 100,
			RepoName:       "cockroachdb/helm-charts",
			RepoURL:        "https://github.com/cockroachdb/helm-charts",
			CommitSHA:      "709d26d8805bcd08e7957758c2d685994e62aa5f",
			CommitURL:      "https://github.com/cockroachdb/helm-charts/commit/709d26d8805bcd08e7957758c2d685994e62aa5f",
			Bio:            "Software Engineer at Cockroach Labs",
			WebsiteURL:     "https://vishal.dev",
			LinkedInURL:    "https://www.linkedin.com/in/vishaljaishankar",
			Location:       "Bangalore, India",
			MatchedSignals: []string{"k8s", "operator", "storage"},
		},
	}

	saved, err := store.SaveEngineeringLeads(ctx, leads)
	if err != nil {
		t.Fatalf("failed saving engineering leads: %v", err)
	}
	if saved != 1 {
		t.Errorf("expected 1 saved lead, got %d", saved)
	}

	// Read back and verify lineage & enrichment
	retrieved, err := store.ListEngineeringLeads(ctx, "cockroachlabs.com", false)
	if err != nil {
		t.Fatalf("failed listing engineering leads: %v", err)
	}
	if len(retrieved) != 1 {
		t.Fatalf("expected 1 retrieved lead, got %d", len(retrieved))
	}

	l := retrieved[0]
	if l.RepoName != "cockroachdb/helm-charts" {
		t.Errorf("expected repo_name cockroachdb/helm-charts, got %s", l.RepoName)
	}
	if l.CommitSHA != "709d26d8805bcd08e7957758c2d685994e62aa5f" {
		t.Errorf("expected commit_sha preserved, got %s", l.CommitSHA)
	}
	if l.LinkedInURL != "https://www.linkedin.com/in/vishaljaishankar" {
		t.Errorf("expected linkedin_url preserved, got %s", l.LinkedInURL)
	}
	if len(l.MatchedSignals) != 3 {
		t.Errorf("expected 3 matched signals, got %v", l.MatchedSignals)
	}
}

