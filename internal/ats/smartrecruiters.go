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

// SmartRecruitersAdapter reads the public SmartRecruiters postings API.
type SmartRecruitersAdapter struct {
	fetcher *crawler.Fetcher
	baseURL string
}

func NewSmartRecruitersAdapter(f *crawler.Fetcher) *SmartRecruitersAdapter {
	return &SmartRecruitersAdapter{fetcher: f, baseURL: "https://api.smartrecruiters.com"}
}

func (s *SmartRecruitersAdapter) Name() string { return "SmartRecruiters" }

func (s *SmartRecruitersAdapter) Detect(target string) bool {
	return strings.Contains(strings.ToLower(target), "smartrecruiters.com")
}

type smartRecruitersResponse struct {
	Content []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		RefURL     string `json:"ref"`
		ReleasedAt string `json:"releasedDate"`
		Department struct {
			Label string `json:"label"`
		} `json:"department"`
		Location struct {
			City    string `json:"city"`
			Region  string `json:"region"`
			Country string `json:"country"`
			Remote  bool   `json:"remote"`
		} `json:"location"`
	} `json:"content"`
}

func (s *SmartRecruitersAdapter) Extract(ctx context.Context, target string) (*CompanyMeta, error) {
	company := lastPathSegment(target, "smartrecruiters.com")
	if company == "" {
		return nil, fmt.Errorf("unable to extract SmartRecruiters company from: %s", target)
	}
	return s.ExtractByBoardID(ctx, company, company+".com")
}

func (s *SmartRecruitersAdapter) ExtractByBoardID(ctx context.Context, company, domain string) (*CompanyMeta, error) {
	apiURL := fmt.Sprintf("%s/v1/companies/%s/postings?limit=100", s.baseURL, url.PathEscape(company))
	res, err := s.fetcher.Fetch(ctx, apiURL)
	if err != nil {
		return nil, fmt.Errorf("smartrecruiters API fetch error: %w", err)
	}
	if domain == "" {
		domain = company + ".com"
	}
	careers := fmt.Sprintf("https://careers.smartrecruiters.com/%s", company)
	if res.StatusCode == 304 {
		return &CompanyMeta{Name: formatCompanyName(company), Domain: domain, CareersURL: careers}, nil
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("smartrecruiters API returned status %d for company %s", res.StatusCode, company)
	}

	var body smartRecruitersResponse
	if err := json.Unmarshal(res.Body, &body); err != nil {
		return nil, fmt.Errorf("failed decoding SmartRecruiters JSON: %w", err)
	}

	jobs := make([]rawJob, 0, len(body.Content))
	for _, p := range body.Content {
		posted, _ := time.Parse(time.RFC3339, p.ReleasedAt)
		workplace := ""
		if p.Location.Remote {
			workplace = "remote"
		}
		jobs = append(jobs, rawJob{
			ID:         p.ID,
			Title:      p.Name,
			Department: p.Department.Label,
			Location:   joinNonEmpty(", ", p.Location.City, p.Location.Region, strings.ToUpper(p.Location.Country)),
			Workplace:  workplace,
			URL:        fmt.Sprintf("https://jobs.smartrecruiters.com/%s/%s", company, p.ID),
			PostedAt:   posted,
		})
	}
	return buildMeta(formatCompanyName(company), domain, careers, "smartrecruiters", jobs), nil
}
