package sinks

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"specter/internal/ats"
)

// Notion creates one page per job in a database, skipping roles already present.
// Required database properties: Name (title), ID (text), Company (text), Location (text),
// Remote (text), Keywords (text), URL (url).
type Notion struct {
	Token, DatabaseID string
	BaseURL           string
	Client            *http.Client
	// Delay between API calls (Notion allows ~3 requests/second).
	Delay time.Duration
}

// NewNotionFromEnv reads NOTION_TOKEN and NOTION_DATABASE_ID.
func NewNotionFromEnv() (*Notion, error) {
	n := &Notion{
		Token:      os.Getenv("NOTION_TOKEN"),
		DatabaseID: os.Getenv("NOTION_DATABASE_ID"),
		BaseURL:    "https://api.notion.com",
		Client:     defaultClient(),
		Delay:      350 * time.Millisecond,
	}
	if n.Token == "" || n.DatabaseID == "" {
		return nil, fmt.Errorf("NOTION_TOKEN and NOTION_DATABASE_ID must be set")
	}
	return n, nil
}

func (n *Notion) Name() string { return "notion" }

func (n *Notion) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + n.Token, "Notion-Version": "2022-06-28"}
}

func (n *Notion) pause(ctx context.Context) error {
	if n.Delay <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(n.Delay):
		return nil
	}
}

func (n *Notion) Push(ctx context.Context, jobs []ats.JobPosting) (int, error) {
	created := 0
	for _, j := range jobs {
		key := j.ID + "@" + j.CompanyDomain

		exists, err := n.exists(ctx, key)
		if err != nil {
			return created, err
		}
		if err := n.pause(ctx); err != nil {
			return created, err
		}
		if exists {
			continue
		}

		body := map[string]any{
			"parent":     map[string]string{"database_id": n.DatabaseID},
			"properties": n.properties(j, key),
		}
		endpoint := n.BaseURL + "/v1/pages"
		if err := doJSON(ctx, n.Client, http.MethodPost, endpoint, n.headers(), body, nil); err != nil {
			return created, err
		}
		created++
		if err := n.pause(ctx); err != nil {
			return created, err
		}
	}
	return created, nil
}

func (n *Notion) exists(ctx context.Context, key string) (bool, error) {
	var out struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	body := map[string]any{
		"filter":    map[string]any{"property": "ID", "rich_text": map[string]string{"equals": key}},
		"page_size": 1,
	}
	endpoint := fmt.Sprintf("%s/v1/databases/%s/query", n.BaseURL, n.DatabaseID)
	if err := doJSON(ctx, n.Client, http.MethodPost, endpoint, n.headers(), body, &out); err != nil {
		return false, err
	}
	return len(out.Results) > 0, nil
}

func richText(s string) map[string]any {
	if len(s) > 1900 { // Notion caps rich text content at 2000 characters
		s = s[:1900]
	}
	return map[string]any{"rich_text": []map[string]any{{"text": map[string]string{"content": s}}}}
}

func (n *Notion) properties(j ats.JobPosting, key string) map[string]any {
	return map[string]any{
		"Name":     map[string]any{"title": []map[string]any{{"text": map[string]string{"content": j.Title}}}},
		"ID":       richText(key),
		"Company":  richText(j.CompanyName),
		"Location": richText(j.Location),
		"Remote":   richText(remoteLabel(j)),
		"Keywords": richText(strings.Join(j.Keywords, ", ")),
		"URL":      map[string]any{"url": link(j)},
	}
}
