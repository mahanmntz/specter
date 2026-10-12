package sinks

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"specter/internal/ats"
)

// Airtable upserts jobs into a table, merging on the "ID" field.
// Expected fields: ID, Title, Company, Location, Department, Seniority, Remote, Keywords, URL, Posted.
type Airtable struct {
	Token, BaseID, Table string
	BaseURL              string
	Client               *http.Client
}

// NewAirtableFromEnv reads AIRTABLE_TOKEN, AIRTABLE_BASE and AIRTABLE_TABLE (default "Jobs").
func NewAirtableFromEnv() (*Airtable, error) {
	a := &Airtable{
		Token:   os.Getenv("AIRTABLE_TOKEN"),
		BaseID:  os.Getenv("AIRTABLE_BASE"),
		Table:   os.Getenv("AIRTABLE_TABLE"),
		BaseURL: "https://api.airtable.com",
		Client:  defaultClient(),
	}
	if a.Table == "" {
		a.Table = "Jobs"
	}
	if a.Token == "" || a.BaseID == "" {
		return nil, fmt.Errorf("AIRTABLE_TOKEN and AIRTABLE_BASE must be set")
	}
	return a, nil
}

func (a *Airtable) Name() string { return "airtable" }

// Push upserts in batches of 10 (the Airtable API maximum).
func (a *Airtable) Push(ctx context.Context, jobs []ats.JobPosting) (int, error) {
	endpoint := fmt.Sprintf("%s/v0/%s/%s", a.BaseURL, url.PathEscape(a.BaseID), url.PathEscape(a.Table))
	headers := map[string]string{"Authorization": "Bearer " + a.Token}

	written := 0
	for start := 0; start < len(jobs); start += 10 {
		end := start + 10
		if end > len(jobs) {
			end = len(jobs)
		}
		records := make([]map[string]any, 0, end-start)
		for _, j := range jobs[start:end] {
			records = append(records, map[string]any{"fields": a.fields(j)})
		}
		body := map[string]any{
			"performUpsert": map[string]any{"fieldsToMergeOn": []string{"ID"}},
			"typecast":      true,
			"records":       records,
		}
		if err := doJSON(ctx, a.Client, http.MethodPatch, endpoint, headers, body, nil); err != nil {
			return written, err
		}
		written += end - start
	}
	return written, nil
}

func (a *Airtable) fields(j ats.JobPosting) map[string]any {
	f := map[string]any{
		"ID":         j.ID + "@" + j.CompanyDomain,
		"Title":      j.Title,
		"Company":    j.CompanyName,
		"Location":   j.Location,
		"Department": j.Department,
		"Seniority":  j.Seniority,
		"Remote":     remoteLabel(j),
		"Keywords":   strings.Join(j.Keywords, ", "),
		"URL":        link(j),
	}
	if !j.PostedAt.IsZero() {
		f["Posted"] = j.PostedAt.Format("2006-01-02")
	}
	return f
}
