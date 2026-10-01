package ats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"specter/internal/crawler"
	"specter/internal/signals"
)

type AshbyAdapter struct {
	fetcher *crawler.Fetcher
}

func NewAshbyAdapter(f *crawler.Fetcher) *AshbyAdapter {
	return &AshbyAdapter{fetcher: f}
}

func (a *AshbyAdapter) Name() string {
	return "Ashby"
}

func (a *AshbyAdapter) Detect(target string) bool {
	lower := strings.ToLower(target)
	return strings.Contains(lower, "ashbyhq.com") || strings.Contains(lower, "jobs.ashbyhq.com")
}

type ashbyJob struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Department  string `json:"department"`
	Location    string `json:"location"`
	JobURL      string `json:"jobUrl"`
	PublishedAt string `json:"publishedAt"`
}

type ashbyResponse struct {
	Jobs []ashbyJob `json:"jobs"`
}

func (a *AshbyAdapter) Extract(ctx context.Context, target string) (*CompanyMeta, error) {
	orgToken := extractAshbyToken(target)
	if orgToken == "" {
		return nil, fmt.Errorf("unable to extract Ashby organization name from: %s", target)
	}
	return a.ExtractByBoardID(ctx, orgToken, orgToken+".com")
}

func (a *AshbyAdapter) ExtractByBoardID(ctx context.Context, orgToken, domain string) (*CompanyMeta, error) {
	apiURL := fmt.Sprintf("https://api.ashbyhq.com/posting-api/job-board/%s", orgToken)
	res, err := a.fetcher.Fetch(ctx, apiURL)
	if err != nil {
		return nil, fmt.Errorf("ashby API fetch error: %w", err)
	}

	if res.StatusCode != 200 {
		return nil, fmt.Errorf("ashby API returned status %d for organization %s", res.StatusCode, orgToken)
	}

	var payload ashbyResponse
	if err := json.Unmarshal(res.Body, &payload); err != nil {
		return nil, fmt.Errorf("failed parsing Ashby JSON: %w", err)
	}

	compName := formatCompanyName(orgToken)
	if domain == "" {
		domain = orgToken + ".com"
	}
	careersURL := fmt.Sprintf("https://jobs.ashbyhq.com/%s", orgToken)
	meta := &CompanyMeta{
		Name:       compName,
		Domain:     domain,
		CareersURL: careersURL,
		Signals:    []string{},
		OpenRoles:  []JobPosting{},
	}

	signalSet := make(map[string]bool)

	for _, j := range payload.Jobs {
		if !signals.IsBackendRole(j.Title, j.Department) {
			continue
		}

		matchedKws := signals.MatchBackendKeywords(j.Title + " " + j.Department)
		// For Ashby list endpoints, detailed description may require an individual job fetch
		// If title has keywords or it's a backend role, include it
		if len(matchedKws) == 0 {
			matchedKws = []string{"backend"}
		}

		for _, kw := range matchedKws {
			signalSet[kw] = true
		}

		postedTime := time.Now().UTC()
		if j.PublishedAt != "" {
			if t, err := time.Parse(time.RFC3339, j.PublishedAt); err == nil {
				postedTime = t
			}
		}

		jobURL := j.JobURL
		if jobURL == "" {
			jobURL = fmt.Sprintf("https://jobs.ashbyhq.com/%s/%s", orgToken, j.ID)
		}

		policy := signals.ClassifyRemotePolicy(j.Location, j.Title+" "+j.Department, jobURL)

		meta.OpenRoles = append(meta.OpenRoles, JobPosting{
			ID:                 fmt.Sprintf("ashby-%s", j.ID),
			Title:              j.Title,
			URL:                jobURL,
			ApplyURL:           policy.DirectApplyURL,
			Location:           j.Location,
			Department:         j.Department,
			Seniority:          signals.ExtractSeniority(j.Title),
			Keywords:           matchedKws,
			PostedAt:           postedTime,
			WorkplaceType:      policy.PolicyName,
			RemotePolicy:       policy.Badge,
			GlobalRemote:       policy.IsGlobalRemote,
			ContractorFriendly: policy.IsContractorFriendly,
		})
	}

	for kw := range signalSet {
		meta.Signals = append(meta.Signals, kw)
	}

	return meta, nil
}

func extractAshbyToken(target string) string {
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return ""
}
