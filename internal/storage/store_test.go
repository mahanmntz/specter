package storage

import (
	"context"
	"os"
	"path/filepath"
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

func TestDirectApplyRolesAndCrawlRuns(t *testing.T) {
	tmpDB := "test_apply_runs.db"
	defer os.Remove(tmpDB)

	store, err := NewStore(tmpDB)
	if err != nil {
		t.Fatalf("failed creating store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. Save company and roles with remote policy
	meta := &ats.CompanyMeta{
		Name:       "Distributed Corp",
		Domain:     "distcorp.io",
		CareersURL: "https://jobs.lever.co/distcorp",
		Signals:    []string{"go", "distributed"},
		OpenRoles: []ats.JobPosting{
			{
				ID:                 "dist-1",
				Title:              "Staff Go Systems Architect",
				URL:                "https://jobs.lever.co/distcorp/1",
				ApplyURL:           "https://jobs.lever.co/distcorp/1/apply",
				Location:           "Worldwide",
				Department:         "Infrastructure",
				Seniority:          "Staff/Principal",
				Keywords:           []string{"go", "distributed"},
				WorkplaceType:      "Global Remote (Anywhere)",
				RemotePolicy:       "🟢 Worldwide / Contractor-Friendly",
				GlobalRemote:       true,
				ContractorFriendly: true,
				Compensation:       "$160,000 - $210,000",
				PostedAt:           time.Now().UTC(),
			},
			{
				ID:                 "dist-2",
				Title:              "Senior Backend Engineer (US)",
				URL:                "https://jobs.lever.co/distcorp/2",
				ApplyURL:           "https://jobs.lever.co/distcorp/2/apply",
				Location:           "US Only",
				Department:         "Product",
				Seniority:          "Senior",
				Keywords:           []string{"go"},
				WorkplaceType:      "Geo-Restricted",
				RemotePolicy:       "🔴 Geo-Restricted (Domestic / W-2)",
				GlobalRemote:       false,
				ContractorFriendly: false,
				PostedAt:           time.Now().UTC(),
			},
		},
	}

	_, newRoles, err := store.SaveCompanyAndRoles(ctx, meta)
	if err != nil || newRoles != 2 {
		t.Fatalf("expected 2 new roles, got %d, err: %v", newRoles, err)
	}

	// 2. Query direct apply roles with GlobalOnly filter
	globalRoles, err := store.ListDirectApplyRoles(ctx, DirectApplyFilter{GlobalOnly: true})
	if err != nil {
		t.Fatalf("ListDirectApplyRoles failed: %v", err)
	}
	if len(globalRoles) != 1 {
		t.Fatalf("expected 1 global role, got %d", len(globalRoles))
	}
	gr := globalRoles[0]
	if gr.ApplyURL != "https://jobs.lever.co/distcorp/1/apply" {
		t.Errorf("expected direct apply url preserved, got %s", gr.ApplyURL)
	}
	if !gr.GlobalRemote || !gr.ContractorFriendly {
		t.Errorf("expected GlobalRemote and ContractorFriendly true")
	}

	// 3. Test CrawlRun recording and retrieval
	run := &CrawlRun{
		RunID:          "2026-10-01_18-00-00",
		StartedAt:      time.Now().Add(-10 * time.Minute),
		CompletedAt:    time.Now(),
		TotalCompanies: 1,
		TotalRoles:     2,
		NewRoles:       2,
		TotalLeads:     5,
		NewLeads:       5,
		ReportDir:      "reports/runs/2026-10-01_18-00-00",
	}
	if err := store.RecordCrawlRun(ctx, run); err != nil {
		t.Fatalf("RecordCrawlRun failed: %v", err)
	}

	lastRun, err := store.GetLastCrawlRun(ctx)
	if err != nil || lastRun == nil {
		t.Fatalf("GetLastCrawlRun failed: %v", err)
	}
	if lastRun.RunID != "2026-10-01_18-00-00" {
		t.Errorf("expected run_id 2026-10-01_18-00-00, got %s", lastRun.RunID)
	}
	if lastRun.NewRoles != 2 {
		t.Errorf("expected 2 new roles in last run, got %d", lastRun.NewRoles)
	}
}

func TestSheetsSyncTracking(t *testing.T) {
	tempDB := filepath.Join(t.TempDir(), "specter_sync_test.db")
	store, err := NewStore(tempDB)
	if err != nil {
		t.Fatalf("failed initializing store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	leads := []signals.EngineeringLead{
		{
			Domain:         "cockroachlabs.com",
			CompanyDomain:  "cockroachlabs.com",
			Name:           "Dev Alpha",
			Role:           "Distributed Systems Engineer",
			Email:          "alpha@cockroachlabs.com",
			Source:         "git_commit",
			GitHubHandle:   "devalpha",
			RelevanceScore: 90,
			RepoName:       "cockroachdb/cockroach",
			DiscoveredAt:   time.Now().UTC(),
		},
		{
			Domain:         "canonical.com",
			CompanyDomain:  "canonical.com",
			Name:           "Dev Beta",
			Role:           "Core Linux Engineer",
			Email:          "beta@canonical.com",
			Source:         "git_commit",
			GitHubHandle:   "devbeta",
			RelevanceScore: 85,
			RepoName:       "canonical/multipass",
			DiscoveredAt:   time.Now().UTC(),
		},
	}

	n, err := store.SaveEngineeringLeads(ctx, leads)
	if err != nil || n != 2 {
		t.Fatalf("SaveEngineeringLeads failed: n=%d, err=%v", n, err)
	}

	// 1. Initially both should be unsynced
	unsynced, err := store.GetUnsyncedLeads(10)
	if err != nil {
		t.Fatalf("GetUnsyncedLeads failed: %v", err)
	}
	if len(unsynced) != 2 {
		t.Fatalf("expected 2 unsynced leads, got %d", len(unsynced))
	}

	// 2. Mark the first lead as synced
	firstID := unsynced[0].ID
	if err := store.MarkLeadsSynced([]int64{firstID}); err != nil {
		t.Fatalf("MarkLeadsSynced failed: %v", err)
	}

	// 3. Now only 1 lead should be unsynced
	unsyncedAfter, err := store.GetUnsyncedLeads(10)
	if err != nil {
		t.Fatalf("GetUnsyncedLeads after sync failed: %v", err)
	}
	if len(unsyncedAfter) != 1 {
		t.Fatalf("expected 1 unsynced lead, got %d", len(unsyncedAfter))
	}
	if unsyncedAfter[0].ID == firstID {
		t.Errorf("expected unsynced lead to not be %d", firstID)
	}

	// 4. GetAllEngineeringLeadsForSync should still return all 2
	allLeads, err := store.GetAllEngineeringLeadsForSync(ctx, 10)
	if err != nil {
		t.Fatalf("GetAllEngineeringLeadsForSync failed: %v", err)
	}
	if len(allLeads) != 2 {
		t.Fatalf("expected 2 leads for all sync, got %d", len(allLeads))
	}

	// 5. CountUnsyncedEngineeringLeads should return 1
	count, err := store.CountUnsyncedEngineeringLeads(ctx)
	if err != nil {
		t.Fatalf("CountUnsyncedEngineeringLeads failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}
}


