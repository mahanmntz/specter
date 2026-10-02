package sync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"specter/internal/ats"
	"specter/internal/signals"
	"specter/internal/storage"
)

func TestResolveWebhookURL(t *testing.T) {
	// 1. Explicit override has top priority
	res := ResolveWebhookURL("https://custom.webhook.url/exec")
	if res != "https://custom.webhook.url/exec" {
		t.Errorf("expected override URL, got %s", res)
	}

	// 2. Env variable priority
	os.Setenv("SPECTER_SHEETS_WEBHOOK", "https://env.webhook.url/exec")
	defer os.Unsetenv("SPECTER_SHEETS_WEBHOOK")

	res = ResolveWebhookURL("")
	if res != "https://env.webhook.url/exec" {
		t.Errorf("expected env URL, got %s", res)
	}

	// 3. Fallback when env and override are empty
	os.Unsetenv("SPECTER_SHEETS_WEBHOOK")
	res = ResolveWebhookURL("")
	if res != DefaultSheetsWebhook {
		t.Errorf("expected default webhook, got %s", res)
	}
}

func TestConvertLeadToPayload(t *testing.T) {
	lead := signals.EngineeringLead{
		Domain:         "fly.io",
		CompanyDomain:  "fly.io",
		Name:           "Alice Engineer",
		Role:           "Distributed Systems Lead",
		Email:          "alice@fly.io",
		GitHubHandle:   "aliceeng",
		RepoName:       "superfly/flyctl",
		RelevanceScore: 85,
		LinkedInURL:    "https://linkedin.com/in/alice",
		MatchedSignals: []string{"Distributed Systems", "Go"},
	}

	payload := ConvertLeadToPayload(lead)
	if payload.Company != "fly.io" {
		t.Errorf("expected company fly.io, got %s", payload.Company)
	}
	if payload.Score != 85 {
		t.Errorf("expected score 85, got %d", payload.Score)
	}
	if payload.GitHub != "@aliceeng" {
		t.Errorf("expected @aliceeng with @ prefix, got %s", payload.GitHub)
	}
	if payload.Repo != "superfly/flyctl" {
		t.Errorf("expected repo superfly/flyctl, got %s", payload.Repo)
	}
	if payload.Topic != "Distributed Systems / Go" {
		t.Errorf("expected topic 'Distributed Systems / Go', got %s", payload.Topic)
	}
	if payload.LinkedIn != "https://linkedin.com/in/alice" {
		t.Errorf("expected linkedin URL, got %s", payload.LinkedIn)
	}
	if payload.Icebreaker == "" {
		t.Errorf("expected non-empty icebreaker")
	}

	// Lead with already prefixed @
	lead2 := signals.EngineeringLead{
		Domain:       "test.com",
		Name:         "Bob",
		GitHubHandle: "@bobdev",
		TopLanguages: "Rust",
	}
	payload2 := ConvertLeadToPayload(lead2)
	if payload2.GitHub != "@bobdev" {
		t.Errorf("expected single @bobdev, got %s", payload2.GitHub)
	}
	if payload2.Topic != "Rust" {
		t.Errorf("expected topic Rust, got %s", payload2.Topic)
	}
}

func TestPostBatch_RedirectAndSuccess(t *testing.T) {
	var echoReceived atomic.Bool
	var postReceived atomic.Int32

	// Setup mock echo server (redirect destination)
	echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		echoReceived.Store(true)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer echoServer.Close()

	// Setup mock webhook server that returns 302 to echoServer
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		postReceived.Add(1)
		body, _ := io.ReadAll(r.Body)
		var payloads []SheetLeadPayload
		if err := json.Unmarshal(body, &payloads); err != nil {
			t.Errorf("failed unmarshaling body in webhook: %v", err)
		}
		if len(payloads) != 2 {
			t.Errorf("expected 2 payloads, got %d", len(payloads))
		}

		// Google Apps Script 302 redirect
		http.Redirect(w, r, echoServer.URL, http.StatusFound)
	}))
	defer webhookServer.Close()

	client := NewSheetsClient(webhookServer.URL)
	payloads := []SheetLeadPayload{
		{Company: "TestCo", Name: "User 1", Role: "Backend", Email: "u1@test.com", GitHub: "@u1", Repo: "repo1"},
		{Company: "TestCo", Name: "User 2", Role: "Staff", Email: "u2@test.com", GitHub: "@u2", Repo: "repo2"},
	}

	err := client.PostBatch(context.Background(), payloads)
	if err != nil {
		t.Fatalf("PostBatch failed: %v", err)
	}

	if postReceived.Load() != 1 {
		t.Errorf("expected 1 POST request to webhook, got %d", postReceived.Load())
	}
	if !echoReceived.Load() {
		t.Errorf("expected client to follow 302 redirect to echo endpoint")
	}
}

func TestPostBatch_RetriesOnTransientError(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		if count == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"status":"error","message":"rate limited"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	client := NewSheetsClient(server.URL)
	client.RetryDelay = 10 * time.Millisecond // Fast retries for testing

	err := client.PostBatch(context.Background(), []SheetLeadPayload{
		{Company: "RetryCo", Name: "Retry User", Role: "SRE", Email: "r@test.com"},
	})

	if err != nil {
		t.Fatalf("expected PostBatch to succeed on second attempt, got: %v", err)
	}

	if attempts.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts.Load())
	}
}

func TestSyncLeads_BatchingAndStoreUpdate(t *testing.T) {
	tempDB := filepath.Join(t.TempDir(), "specter_sync_batch_test.db")
	store, err := storage.NewStore(tempDB)
	if err != nil {
		t.Fatalf("failed initializing store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Seed 40 leads to test chunking into 2 batches (35 + 5)
	leads := make([]signals.EngineeringLead, 40)
	for i := 0; i < 40; i++ {
		leads[i] = signals.EngineeringLead{
			Domain:         "posthog.com",
			CompanyDomain:  "posthog.com",
			Name:           "Engineer " + string(rune('A'+i)),
			Role:           "Backend Engineer",
			Email:          "eng" + string(rune('A'+i)) + "@posthog.com",
			Source:         "git_commit",
			GitHubHandle:   "ghuser" + string(rune('A'+i)),
			RelevanceScore: 80,
			RepoName:       "posthog/posthog",
			DiscoveredAt:   time.Now().UTC(),
		}
	}
	_, err = store.SaveEngineeringLeads(ctx, leads)
	if err != nil {
		t.Fatalf("SaveEngineeringLeads failed: %v", err)
	}

	var batchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batchCount.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	client := NewSheetsClient(server.URL)
	client.BatchSize = 35

	res, err := client.SyncLeads(ctx, store, SyncOptions{
		Limit: 50,
	})
	if err != nil {
		t.Fatalf("SyncLeads failed: %v", err)
	}

	if res.TotalProcessed != 40 {
		t.Errorf("expected 40 processed leads, got %d", res.TotalProcessed)
	}
	if res.TotalSynced != 40 {
		t.Errorf("expected 40 synced leads, got %d", res.TotalSynced)
	}
	if res.Batches != 2 {
		t.Errorf("expected 2 batches, got %d", res.Batches)
	}
	if batchCount.Load() != 2 {
		t.Errorf("expected 2 HTTP calls to webhook, got %d", batchCount.Load())
	}

	// Verify all leads are now marked as synced
	unsynced, err := store.GetUnsyncedLeads(50)
	if err != nil {
		t.Fatalf("GetUnsyncedLeads failed: %v", err)
	}
	if len(unsynced) != 0 {
		t.Errorf("expected 0 unsynced leads remaining, got %d", len(unsynced))
	}
}

func TestSyncLeads_DryRun(t *testing.T) {
	tempDB := filepath.Join(t.TempDir(), "specter_dryrun_test.db")
	store, err := storage.NewStore(tempDB)
	if err != nil {
		t.Fatalf("failed initializing store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	_, _ = store.SaveEngineeringLeads(ctx, []signals.EngineeringLead{
		{
			Domain:        "status.im",
			CompanyDomain: "status.im",
			Name:          "Crypto Dev",
			Role:          "P2P Core Engineer",
			Email:         "crypto@status.im",
			GitHubHandle:  "cryptodev",
			DiscoveredAt:  time.Now().UTC(),
		},
	})

	var serverCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewSheetsClient(server.URL)

	res, err := client.SyncLeads(ctx, store, SyncOptions{
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("SyncLeads dry run failed: %v", err)
	}

	if res.TotalProcessed != 1 {
		t.Errorf("expected 1 processed lead, got %d", res.TotalProcessed)
	}
	if res.TotalSynced != 0 {
		t.Errorf("expected 0 synced leads in dry run, got %d", res.TotalSynced)
	}
	if serverCalled.Load() {
		t.Errorf("dry run should not make network calls to webhook")
	}

	// Lead should still be unsynced
	unsynced, err := store.GetUnsyncedLeads(10)
	if err != nil || len(unsynced) != 1 {
		t.Errorf("expected lead to remain unsynced in dry run")
	}
}

func TestConvertJobToPayload(t *testing.T) {
	now := time.Now().UTC()
	role := ats.JobPosting{
		ID:                 "role-123",
		CompanyDomain:      "railway.app",
		CompanyName:        "Railway",
		Title:              "Senior Backend Engineer - Infra",
		Location:           "Remote - Worldwide",
		WorkplaceType:      "remote",
		RemotePolicy:       "Anywhere in the world",
		GlobalRemote:       true,
		ContractorFriendly: true,
		Compensation:       "$160,000 - $210,000",
		ApplyURL:           "https://railway.app/careers/senior-backend-engineer",
		FirstSeenAt:        now,
	}

	payload := ConvertJobToPayload(role)
	if payload.Company != "Railway" {
		t.Errorf("expected company Railway, got %s", payload.Company)
	}
	if payload.Title != "Senior Backend Engineer - Infra" {
		t.Errorf("expected title Senior Backend Engineer - Infra, got %s", payload.Title)
	}
	if !payload.GlobalRemote {
		t.Errorf("expected GlobalRemote=true")
	}
	if !payload.ContractorFriendly {
		t.Errorf("expected ContractorFriendly=true")
	}
	if payload.ApplyURL != "https://railway.app/careers/senior-backend-engineer" {
		t.Errorf("expected ApplyURL, got %s", payload.ApplyURL)
	}
}

func TestSyncJobs_EndToEnd(t *testing.T) {
	tempDB := filepath.Join(t.TempDir(), "specter_jobs_sync_test.db")
	store, err := storage.NewStore(tempDB)
	if err != nil {
		t.Fatalf("failed initializing store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Seed 2 job roles via SaveCompanyAndRoles
	meta := &ats.CompanyMeta{
		Domain: "supabase.com",
		Name:   "Supabase",
		OpenRoles: []ats.JobPosting{
			{
				ID:                 "role-1",
				CompanyDomain:      "supabase.com",
				CompanyName:        "Supabase",
				Title:              "Backend Engineer (Database)",
				Location:           "Remote",
				WorkplaceType:      "remote",
				GlobalRemote:       true,
				ContractorFriendly: true,
				ApplyURL:           "https://boards.greenhouse.io/supabase/jobs/123",
				Keywords:           []string{"backend", "database"},
				FirstSeenAt:        time.Now().UTC(),
			},
			{
				ID:                 "role-2",
				CompanyDomain:      "supabase.com",
				CompanyName:        "Supabase",
				Title:              "Infrastructure Engineer",
				Location:           "Remote",
				WorkplaceType:      "remote",
				GlobalRemote:       true,
				ContractorFriendly: false,
				ApplyURL:           "https://supabase.com/jobs/infra",
				Keywords:           []string{"backend", "infra"},
				FirstSeenAt:        time.Now().UTC(),
			},
		},
	}
	_, _, err = store.SaveCompanyAndRoles(ctx, meta)
	if err != nil {
		t.Fatalf("SaveCompanyAndRoles failed: %v", err)
	}

	var postCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		postCount.Add(1)
		body, _ := io.ReadAll(r.Body)
		var envelope struct {
			Target string            `json:"target"`
			Jobs   []SheetJobPayload `json:"jobs"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Errorf("unmarshal envelope failed: %v", err)
		}
		if envelope.Target != "jobs" {
			t.Errorf("expected target 'jobs', got %s", envelope.Target)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	client := NewSheetsClient(server.URL)

	res, err := client.SyncJobs(ctx, store, SyncOptions{
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SyncJobs failed: %v", err)
	}

	if res.TotalProcessed != 2 {
		t.Errorf("expected 2 processed roles, got %d", res.TotalProcessed)
	}
	if res.TotalSynced != 2 {
		t.Errorf("expected 2 synced roles, got %d", res.TotalSynced)
	}
	if postCount.Load() != 1 {
		t.Errorf("expected 1 webhook request, got %d", postCount.Load())
	}
}
