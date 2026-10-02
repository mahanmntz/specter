package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"specter/internal/ats"
	"specter/internal/signals"
	"specter/internal/storage"
)

const (
	// DefaultSheetsWebhook is the production Google Apps Script deployment URL.
	DefaultSheetsWebhook = "https://script.google.com/macros/s/AKfycbweFX6XX3ugq_ZESSFQ_YHvDTe2KRGccKYwxh-Q03jKZV5trK5brh8rW7sguKff5mrR/exec"

	// DefaultBatchSize is the maximum number of leads or jobs dispatched per HTTP POST.
	DefaultBatchSize = 35

	// DefaultRequestTimeout is the HTTP timeout per request.
	DefaultRequestTimeout = 25 * time.Second

	// MaxRetries specifies maximum retry attempts on transient network or 5xx failures.
	MaxRetries = 2
)

// SheetLeadPayload matches the JSON schema expected by the Google Apps Script doPost handler for Contacts tab.
type SheetLeadPayload struct {
	Score      int    `json:"score"`
	Company    string `json:"company"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Topic      string `json:"topic"`
	Email      string `json:"email"`
	LinkedIn   string `json:"linkedin"`
	GitHub     string `json:"github"`
	Repo       string `json:"repo"`
	Icebreaker string `json:"icebreaker"`
}

// SheetJobPayload matches the JSON schema expected by the Google Apps Script doPost handler for Jobs tab.
type SheetJobPayload struct {
	Company            string `json:"company"`
	Title              string `json:"title"`
	Location           string `json:"location"`
	WorkplaceType      string `json:"workplace_type"`
	RemotePolicy       string `json:"remote_policy"`
	GlobalRemote       bool   `json:"global_remote"`
	ContractorFriendly bool   `json:"contractor_friendly"`
	Compensation       string `json:"compensation"`
	ApplyURL           string `json:"apply_url"`
	DiscoveredAt       string `json:"discovered_at"`
}

// SheetsClient manages batch synchronization to Google Sheets via Webhook.
type SheetsClient struct {
	WebhookURL string
	HTTPClient *http.Client
	BatchSize  int
	RetryDelay time.Duration
}

// SyncOptions configures a synchronization run.
type SyncOptions struct {
	WebhookURL string
	Target     string // "leads", "jobs", "all" (default: "all")
	All        bool   // Push all records regardless of previously synced status
	Limit      int
	DryRun     bool
	GlobalOnly bool   // For jobs: only sync global/contractor-friendly roles
	LogFunc    func(format string, args ...any)
}

// SyncResult summarizes the outcome of a sync operation.
type SyncResult struct {
	TotalProcessed int
	TotalSynced    int
	Batches        int
	Duration       time.Duration
}

// ResolveWebhookURL determines the target endpoint using resolution hierarchy:
// 1. CLI flag override (if non-empty)
// 2. SPECTER_SHEETS_WEBHOOK environment variable (if non-empty)
// 3. Default fallback webhook URL
func ResolveWebhookURL(override string) string {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return trimmed
	}
	if env := strings.TrimSpace(os.Getenv("SPECTER_SHEETS_WEBHOOK")); env != "" {
		return env
	}
	return DefaultSheetsWebhook
}

// NewSheetsClient initializes a new SheetsClient with configured redirect policy and timeout.
func NewSheetsClient(webhookURL string) *SheetsClient {
	resolved := ResolveWebhookURL(webhookURL)
	return &SheetsClient{
		WebhookURL: resolved,
		HTTPClient: &http.Client{
			Timeout: DefaultRequestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("stopped after 5 redirects")
				}
				return nil
			},
		},
		BatchSize:  DefaultBatchSize,
		RetryDelay: 1 * time.Second,
	}
}

// ConvertLeadToPayload maps an EngineeringLead domain model to the SheetLeadPayload contract.
func ConvertLeadToPayload(lead signals.EngineeringLead) SheetLeadPayload {
	company := lead.CompanyDomain
	if company == "" {
		company = lead.Domain
	}
	if company == "" {
		company = "Unknown"
	}

	gh := lead.GitHubHandle
	if gh != "" && !strings.HasPrefix(gh, "@") {
		gh = "@" + gh
	}

	repo := lead.RepoName
	if repo == "" && lead.RepoURL != "" {
		repo = lead.RepoURL
	}
	if repo == "" {
		repo = lead.Source
	}

	// Topic synthesis
	topic := ""
	if len(lead.MatchedSignals) > 0 {
		topic = strings.Join(lead.MatchedSignals, " / ")
	} else if lead.TopLanguages != "" {
		topic = lead.TopLanguages
	} else {
		topic = "Distributed Systems & Go"
	}

	// Contextual icebreaker synthesis
	icebreaker := ""
	if lead.RepoName != "" {
		if len(lead.MatchedSignals) > 0 {
			icebreaker = fmt.Sprintf("Followed your work on %s regarding %s; impressed by your backend contributions.", lead.RepoName, lead.MatchedSignals[0])
		} else {
			icebreaker = fmt.Sprintf("Followed your recent contributions to %s; wanted to connect regarding backend architecture and systems design.", lead.RepoName)
		}
	} else if lead.Role != "" {
		icebreaker = fmt.Sprintf("Came across your profile as %s; wanted to connect regarding distributed systems and modern backend tooling.", lead.Role)
	} else {
		icebreaker = "Impressed by your engineering contributions; wanted to connect regarding backend systems and distributed architecture."
	}

	return SheetLeadPayload{
		Score:      lead.RelevanceScore,
		Company:    company,
		Name:       lead.Name,
		Role:       lead.Role,
		Topic:      topic,
		Email:      lead.Email,
		LinkedIn:   lead.LinkedInURL,
		GitHub:     gh,
		Repo:       repo,
		Icebreaker: icebreaker,
	}
}

// ConvertJobToPayload maps an ats.JobPosting domain model to the SheetJobPayload contract.
func ConvertJobToPayload(job ats.JobPosting) SheetJobPayload {
	comp := job.CompanyName
	if comp == "" {
		comp = job.CompanyDomain
	}
	applyURL := job.ApplyURL
	if applyURL == "" {
		applyURL = job.URL
	}
	disc := job.FirstSeenAt.Format("2006-01-02")
	if job.FirstSeenAt.IsZero() {
		disc = time.Now().Format("2006-01-02")
	}

	loc := job.Location
	if loc == "" {
		loc = "Remote"
	}

	workplace := job.WorkplaceType
	if workplace == "" {
		if job.GlobalRemote {
			workplace = "Remote (Global)"
		} else {
			workplace = "Remote"
		}
	}

	return SheetJobPayload{
		Company:            comp,
		Title:              job.Title,
		Location:           loc,
		WorkplaceType:      workplace,
		RemotePolicy:       job.RemotePolicy,
		GlobalRemote:       job.GlobalRemote,
		ContractorFriendly: job.ContractorFriendly,
		Compensation:       job.Compensation,
		ApplyURL:           applyURL,
		DiscoveredAt:       disc,
	}
}

// PostBatch dispatches a single batch of lead payloads to the Webhook endpoint with retries.
func (c *SheetsClient) PostBatch(ctx context.Context, payloads []SheetLeadPayload) error {
	if len(payloads) == 0 {
		return nil
	}
	bodyBytes, err := json.Marshal(payloads)
	if err != nil {
		return fmt.Errorf("failed marshaling leads batch: %w", err)
	}
	return c.postRaw(ctx, bodyBytes)
}

// PostJobsBatch dispatches a single batch of job payloads to the Webhook endpoint with retries.
func (c *SheetsClient) PostJobsBatch(ctx context.Context, payloads []SheetJobPayload) error {
	if len(payloads) == 0 {
		return nil
	}
	envelope := struct {
		Type   string            `json:"type"`
		Target string            `json:"target"`
		Jobs   []SheetJobPayload `json:"jobs"`
		Data   []SheetJobPayload `json:"data"`
	}{
		Type:   "jobs",
		Target: "jobs",
		Jobs:   payloads,
		Data:   payloads,
	}
	bodyBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed marshaling jobs batch: %w", err)
	}
	return c.postRaw(ctx, bodyBytes)
}

func (c *SheetsClient) postRaw(ctx context.Context, bodyBytes []byte) error {
	var lastErr error
	delay := c.RetryDelay
	if delay <= 0 {
		delay = 1 * time.Second
	}

	for attempt := 0; attempt <= MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.WebhookURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("failed building request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Specter-Sync-Client/2.0")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}

		if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
			return nil
		}

		lastErr = fmt.Errorf("webhook responded with HTTP %d: %s", resp.StatusCode, string(respBody))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return lastErr
		}
	}

	return fmt.Errorf("webhook post failed after %d retries: %w", MaxRetries, lastErr)
}

// SyncLeads extracts unsynced (or all) leads from SQLite and synchronizes them to Google Sheets in batches.
func (c *SheetsClient) SyncLeads(ctx context.Context, store *storage.Store, opts SyncOptions) (*SyncResult, error) {
	start := time.Now()
	log := opts.LogFunc
	if log == nil {
		log = func(string, ...any) {}
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	var leads []signals.EngineeringLead
	var err error

	if opts.All {
		leads, err = store.GetAllEngineeringLeadsForSync(ctx, limit)
	} else {
		leads, err = store.GetUnsyncedEngineeringLeads(ctx, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("failed retrieving leads for sync: %w", err)
	}

	result := &SyncResult{
		TotalProcessed: len(leads),
	}

	if len(leads) == 0 {
		return result, nil
	}

	batchSize := c.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	numBatches := (len(leads) + batchSize - 1) / batchSize
	result.Batches = numBatches

	if opts.DryRun {
		var allPayloads []SheetLeadPayload
		for _, l := range leads {
			allPayloads = append(allPayloads, ConvertLeadToPayload(l))
		}
		data, _ := json.MarshalIndent(allPayloads, "", "  ")
		log("[SYNC] (Dry-Run: Contacts) Prepared %d leads across %d batch(es) for %s:\n%s\n",
			len(leads), numBatches, c.WebhookURL, string(data))
		result.Duration = time.Since(start)
		return result, nil
	}

	log("[SYNC] Pushing %d leads to Google Sheets ('Contacts' tab) in %d batch(es)...\n", len(leads), numBatches)

	for i := 0; i < len(leads); i += batchSize {
		end := i + batchSize
		if end > len(leads) {
			end = len(leads)
		}

		batchLeads := leads[i:end]
		payloads := make([]SheetLeadPayload, len(batchLeads))
		leadIDs := make([]int64, len(batchLeads))

		for j, l := range batchLeads {
			payloads[j] = ConvertLeadToPayload(l)
			leadIDs[j] = l.ID
		}

		batchIndex := (i / batchSize) + 1
		if err := c.PostBatch(ctx, payloads); err != nil {
			return result, fmt.Errorf("batch %d/%d failed: %w", batchIndex, numBatches, err)
		}

		if markErr := store.MarkLeadsSyncedCtx(ctx, leadIDs); markErr != nil {
			log("[SYNC] ⚠️  Warning: failed marking batch %d leads as synced in database: %v\n", batchIndex, markErr)
		}

		result.TotalSynced += len(batchLeads)
	}

	result.Duration = time.Since(start)
	log("[SYNC] ✓ Successfully synced %d leads to 'Contacts' in %v.\n", result.TotalSynced, result.Duration.Round(time.Millisecond))
	return result, nil
}

// SyncJobs extracts open job postings from SQLite and synchronizes them to the "Jobs" tab in Google Sheets.
func (c *SheetsClient) SyncJobs(ctx context.Context, store *storage.Store, opts SyncOptions) (*SyncResult, error) {
	start := time.Now()
	log := opts.LogFunc
	if log == nil {
		log = func(string, ...any) {}
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	filter := storage.DirectApplyFilter{
		GlobalOnly:     opts.GlobalOnly,
		ContractorOnly: opts.GlobalOnly,
	}

	roles, err := store.ListDirectApplyRoles(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving jobs for sync: %w", err)
	}
	if limit > 0 && len(roles) > limit {
		roles = roles[:limit]
	}

	result := &SyncResult{
		TotalProcessed: len(roles),
	}

	if len(roles) == 0 {
		return result, nil
	}

	batchSize := c.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	numBatches := (len(roles) + batchSize - 1) / batchSize
	result.Batches = numBatches

	if opts.DryRun {
		var allPayloads []SheetJobPayload
		for _, r := range roles {
			allPayloads = append(allPayloads, ConvertJobToPayload(r))
		}
		data, _ := json.MarshalIndent(allPayloads, "", "  ")
		log("[SYNC] (Dry-Run: Jobs) Prepared %d open roles across %d batch(es) for %s:\n%s\n",
			len(roles), numBatches, c.WebhookURL, string(data))
		result.Duration = time.Since(start)
		return result, nil
	}

	log("[SYNC] Pushing %d jobs to Google Sheets ('Jobs' tab) in %d batch(es)...\n", len(roles), numBatches)

	for i := 0; i < len(roles); i += batchSize {
		end := i + batchSize
		if end > len(roles) {
			end = len(roles)
		}

		batchRoles := roles[i:end]
		payloads := make([]SheetJobPayload, len(batchRoles))

		for j, r := range batchRoles {
			payloads[j] = ConvertJobToPayload(r)
		}

		batchIndex := (i / batchSize) + 1
		if err := c.PostJobsBatch(ctx, payloads); err != nil {
			return result, fmt.Errorf("jobs batch %d/%d failed: %w", batchIndex, numBatches, err)
		}

		result.TotalSynced += len(batchRoles)
	}

	result.Duration = time.Since(start)
	log("[SYNC] ✓ Successfully synced %d jobs to 'Jobs' in %v.\n", result.TotalSynced, result.Duration.Round(time.Millisecond))
	return result, nil
}

// SyncAll synchronizes both Contacts (leads) and Direct Apply Jobs to Google Sheets.
func (c *SheetsClient) SyncAll(ctx context.Context, store *storage.Store, opts SyncOptions) (*SyncResult, error) {
	leadRes, leadErr := c.SyncLeads(ctx, store, opts)
	if leadErr != nil {
		return nil, leadErr
	}

	jobRes, jobErr := c.SyncJobs(ctx, store, opts)
	if jobErr != nil {
		return nil, jobErr
	}

	combined := &SyncResult{
		TotalProcessed: leadRes.TotalProcessed + jobRes.TotalProcessed,
		TotalSynced:    leadRes.TotalSynced + jobRes.TotalSynced,
		Batches:        leadRes.Batches + jobRes.Batches,
		Duration:       leadRes.Duration + jobRes.Duration,
	}
	return combined, nil
}
