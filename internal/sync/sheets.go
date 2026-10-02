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

	"specter/internal/signals"
	"specter/internal/storage"
)

const (
	// DefaultSheetsWebhook is the production Google Apps Script deployment URL.
	DefaultSheetsWebhook = "https://script.google.com/macros/s/AKfycbweFX6XX3ugq_ZESSFQ_YHvDTe2KRGccKYwxh-Q03jKZV5trK5brh8rW7sguKff5mrR/exec"

	// DefaultBatchSize is the maximum number of leads dispatched per HTTP POST.
	DefaultBatchSize = 35

	// DefaultRequestTimeout is the HTTP timeout per request.
	DefaultRequestTimeout = 20 * time.Second

	// MaxRetries specifies maximum retry attempts on transient network or 5xx failures.
	MaxRetries = 2
)

// SheetLeadPayload matches the JSON schema expected by the Google Apps Script doPost handler.
type SheetLeadPayload struct {
	Company string `json:"company"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Email   string `json:"email"`
	GitHub  string `json:"github"`
	Repo    string `json:"repo"`
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
	All        bool
	Limit      int
	DryRun     bool
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

	return SheetLeadPayload{
		Company: company,
		Name:    lead.Name,
		Role:    lead.Role,
		Email:   lead.Email,
		GitHub:  gh,
		Repo:    repo,
	}
}

// PostBatch dispatches a single batch of payloads to the Webhook endpoint with retries.
func (c *SheetsClient) PostBatch(ctx context.Context, payloads []SheetLeadPayload) error {
	if len(payloads) == 0 {
		return nil
	}

	bodyBytes, err := json.Marshal(payloads)
	if err != nil {
		return fmt.Errorf("failed marshaling leads batch: %w", err)
	}

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
		req.Header.Set("User-Agent", "Specter-Sync-Client/1.0")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Apps script redirection endpoint returned 200
			return nil
		}

		// If redirect 302 occurred without automatic following
		if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
			return nil
		}

		lastErr = fmt.Errorf("webhook responded with HTTP %d: %s", resp.StatusCode, string(respBody))
		// Non-retryable client errors (4xx)
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
		log("[SYNC] (Dry-Run) Prepared %d leads across %d batch(es) for %s:\n%s\n",
			len(leads), numBatches, c.WebhookURL, string(data))
		result.Duration = time.Since(start)
		return result, nil
	}

	log("[SYNC] Pushing %d leads to Google Sheets in %d batch(es)...\n", len(leads), numBatches)

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
	log("[SYNC] ✓ Successfully synced %d leads to Google Sheets in %v.\n", result.TotalSynced, result.Duration.Round(time.Millisecond))
	return result, nil
}
