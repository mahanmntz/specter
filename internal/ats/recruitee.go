package ats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mahanmntz/specter/internal/crawler"
)

// RecruiteeAdapter reads public Recruitee career sites (<company>.recruitee.com).
type RecruiteeAdapter struct {
	fetcher *crawler.Fetcher
	// apiURLFor builds the offers endpoint for a company slug (overridable in tests).
	apiURLFor func(company string) string
}

func NewRecruiteeAdapter(f *crawler.Fetcher) *RecruiteeAdapter {
	return &RecruiteeAdapter{
		fetcher:   f,
		apiURLFor: func(c string) string { return fmt.Sprintf("https://%s.recruitee.com/api/offers/", url.PathEscape(c)) },
	}
}

func (r *RecruiteeAdapter) Name() string { return "Recruitee" }

func (r *RecruiteeAdapter) Detect(target string) bool {
	return strings.Contains(strings.ToLower(target), "recruitee.com")
}

var recruiteeHost = regexp.MustCompile(`(?i)^(?:https?://)?([a-z0-9-]+)\.recruitee\.com`)

type recruiteeResponse struct {
	Offers []struct {
		ID          int64  `json:"id"`
		Title       string `json:"title"`
		Department  string `json:"department"`
		City        string `json:"city"`
		Country     string `json:"country"`
		Remote      bool   `json:"remote"`
		CareersURL  string `json:"careers_url"`
		CreatedAt   string `json:"created_at"`
		Description string `json:"description"`
		Requirement string `json:"requirements"`
	} `json:"offers"`
}

func (r *RecruiteeAdapter) Extract(ctx context.Context, target string) (*CompanyMeta, error) {
	m := recruiteeHost.FindStringSubmatch(target)
	if m == nil {
		return nil, fmt.Errorf("unable to extract Recruitee company from: %s", target)
	}
	return r.ExtractByBoardID(ctx, m[1], m[1]+".com")
}

func (r *RecruiteeAdapter) ExtractByBoardID(ctx context.Context, company, domain string) (*CompanyMeta, error) {
	res, err := r.fetcher.Fetch(ctx, r.apiURLFor(company))
	if err != nil {
		return nil, fmt.Errorf("recruitee API fetch error: %w", err)
	}
	if domain == "" {
		domain = company + ".com"
	}
	careers := fmt.Sprintf("https://%s.recruitee.com", company)
	if res.StatusCode == 304 {
		return &CompanyMeta{Name: formatCompanyName(company), Domain: domain, CareersURL: careers}, nil
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("recruitee API returned status %d for company %s", res.StatusCode, company)
	}

	var body recruiteeResponse
	if err := json.Unmarshal(res.Body, &body); err != nil {
		return nil, fmt.Errorf("failed decoding Recruitee JSON: %w", err)
	}

	jobs := make([]RawJob, 0, len(body.Offers))
	for _, o := range body.Offers {
		posted, _ := time.Parse("2006-01-02 15:04:05 MST", o.CreatedAt)
		workplace := ""
		if o.Remote {
			workplace = "remote"
		}
		jobs = append(jobs, RawJob{
			ID:          fmt.Sprint(o.ID),
			Title:       o.Title,
			Department:  o.Department,
			Location:    joinNonEmpty(", ", o.City, o.Country),
			Workplace:   workplace,
			URL:         o.CareersURL,
			Description: o.Description + " " + o.Requirement,
			PostedAt:    posted,
		})
	}
	return BuildMeta(formatCompanyName(company), domain, careers, "recruitee", jobs), nil
}
