package ats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"specter/internal/crawler"
)

// WorkableAdapter reads public Workable job boards (apply.workable.com/<account>).
type WorkableAdapter struct {
	fetcher *crawler.Fetcher
	baseURL string
}

func NewWorkableAdapter(f *crawler.Fetcher) *WorkableAdapter {
	return &WorkableAdapter{fetcher: f, baseURL: "https://apply.workable.com"}
}

func (w *WorkableAdapter) Name() string { return "Workable" }

func (w *WorkableAdapter) Detect(target string) bool {
	return strings.Contains(strings.ToLower(target), "workable.com")
}

type workableResponse struct {
	Jobs []struct {
		Shortcode      string `json:"shortcode"`
		Title          string `json:"title"`
		Department     string `json:"department"`
		URL            string `json:"url"`
		ApplicationURL string `json:"application_url"`
		City           string `json:"city"`
		State          string `json:"state"`
		Country        string `json:"country"`
		Telecommuting  bool   `json:"telecommuting"`
		PublishedOn    string `json:"published_on"`
		Description    string `json:"description"`
	} `json:"jobs"`
}

func (w *WorkableAdapter) Extract(ctx context.Context, target string) (*CompanyMeta, error) {
	account := lastPathSegment(target, "workable.com")
	if account == "" {
		return nil, fmt.Errorf("unable to extract Workable account from: %s", target)
	}
	return w.ExtractByBoardID(ctx, account, account+".com")
}

func (w *WorkableAdapter) ExtractByBoardID(ctx context.Context, account, domain string) (*CompanyMeta, error) {
	apiURL := fmt.Sprintf("%s/api/v1/widget/accounts/%s?details=true", w.baseURL, url.PathEscape(account))
	res, err := w.fetcher.Fetch(ctx, apiURL)
	if err != nil {
		return nil, fmt.Errorf("workable API fetch error: %w", err)
	}
	if domain == "" {
		domain = account + ".com"
	}
	careers := fmt.Sprintf("https://apply.workable.com/%s", account)
	if res.StatusCode == 304 {
		return &CompanyMeta{Name: formatCompanyName(account), Domain: domain, CareersURL: careers}, nil
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("workable API returned status %d for account %s", res.StatusCode, account)
	}

	var body workableResponse
	if err := json.Unmarshal(res.Body, &body); err != nil {
		return nil, fmt.Errorf("failed decoding Workable JSON: %w", err)
	}

	jobs := make([]rawJob, 0, len(body.Jobs))
	for _, j := range body.Jobs {
		posted, _ := time.Parse("2006-01-02", j.PublishedOn)
		workplace := ""
		if j.Telecommuting {
			workplace = "remote"
		}
		jobs = append(jobs, rawJob{
			ID:          j.Shortcode,
			Title:       j.Title,
			Department:  j.Department,
			Location:    joinNonEmpty(", ", j.City, j.State, j.Country),
			Workplace:   workplace,
			URL:         firstNonEmpty(j.URL, j.ApplicationURL),
			Description: j.Description,
			PostedAt:    posted,
		})
	}
	return buildMeta(formatCompanyName(account), domain, careers, "workable", jobs), nil
}
