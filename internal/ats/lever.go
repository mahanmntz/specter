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

type LeverAdapter struct {
	fetcher *crawler.Fetcher
}

func NewLeverAdapter(f *crawler.Fetcher) *LeverAdapter {
	return &LeverAdapter{fetcher: f}
}

func (l *LeverAdapter) Name() string {
	return "Lever"
}

func (l *LeverAdapter) Detect(target string) bool {
	lower := strings.ToLower(target)
	return strings.Contains(lower, "lever.co") || strings.Contains(lower, "jobs.lever.co")
}

type leverPosting struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	HostedURL  string `json:"hostedUrl"`
	CreatedAt  int64  `json:"createdAt"`
	Categories struct {
		Commitment    string `json:"commitment"`
		Department    string `json:"department"`
		Location      string `json:"location"`
		Team          string `json:"team"`
		WorkplaceType string `json:"workplaceType"`
	} `json:"categories"`
	WorkplaceType    string `json:"workplaceType"`
	DescriptionPlain string `json:"descriptionPlain"`
	AdditionalPlain  string `json:"additionalPlain"`
}

func (l *LeverAdapter) Extract(ctx context.Context, target string) (*CompanyMeta, error) {
	companyToken := extractLeverToken(target)
	if companyToken == "" {
		return nil, fmt.Errorf("unable to extract Lever company name from: %s", target)
	}
	return l.ExtractByBoardID(ctx, companyToken, companyToken+".com")
}

func (l *LeverAdapter) ExtractByBoardID(ctx context.Context, companyToken, domain string) (*CompanyMeta, error) {
	apiURL := fmt.Sprintf("https://api.lever.co/v0/postings/%s?mode=json", companyToken)
	res, err := l.fetcher.Fetch(ctx, apiURL)
	if err != nil {
		return nil, fmt.Errorf("lever API fetch error: %w", err)
	}

	compName := formatCompanyName(companyToken)
	if domain == "" {
		domain = companyToken + ".com"
	}

	if res.StatusCode == 304 {
		return &CompanyMeta{
			Name:       compName,
			Domain:     domain,
			CareersURL: fmt.Sprintf("https://jobs.lever.co/%s", companyToken),
		}, nil
	}

	if res.StatusCode != 200 {
		return nil, fmt.Errorf("lever API returned status %d for company %s", res.StatusCode, companyToken)
	}

	var postings []leverPosting
	if err := json.Unmarshal(res.Body, &postings); err != nil {
		return nil, fmt.Errorf("failed decoding Lever JSON: %w", err)
	}
	careersURL := fmt.Sprintf("https://jobs.lever.co/%s", companyToken)
	meta := &CompanyMeta{
		Name:       compName,
		Domain:     domain,
		CareersURL: careersURL,
		Signals:    []string{},
		OpenRoles:  []JobPosting{},
	}

	signalSet := make(map[string]bool)

	for _, p := range postings {
		dept := p.Categories.Department
		if dept == "" {
			dept = p.Categories.Team
		}

		if !signals.IsBackendRole(p.Text, dept) {
			continue
		}

		combined := p.Text + " " + dept + " " + p.DescriptionPlain + " " + p.AdditionalPlain
		matchedKws := signals.MatchBackendKeywords(combined)
		if len(matchedKws) == 0 {
			continue
		}

		for _, kw := range matchedKws {
			signalSet[kw] = true
		}

		postedAt := time.Now().UTC()
		if p.CreatedAt > 0 {
			postedAt = time.UnixMilli(p.CreatedAt).UTC()
		}

		policy := signals.ClassifyRemotePolicy(p.Categories.Location+" "+p.Categories.WorkplaceType, combined, p.HostedURL)

		meta.OpenRoles = append(meta.OpenRoles, JobPosting{
			ID:                 fmt.Sprintf("lever-%s", p.ID),
			Title:              p.Text,
			URL:                p.HostedURL,
			ApplyURL:           policy.DirectApplyURL,
			Location:           p.Categories.Location,
			Department:         dept,
			Seniority:          signals.ExtractSeniority(p.Text),
			Keywords:           matchedKws,
			PostedAt:           postedAt,
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

func extractLeverToken(target string) string {
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) > 0 && parts[0] != "" {
		if parts[0] == "v0" && len(parts) > 2 && parts[1] == "postings" {
			return parts[2]
		}
		return parts[0]
	}
	return ""
}
