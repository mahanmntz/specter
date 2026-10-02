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

type GreenhouseAdapter struct {
	fetcher *crawler.Fetcher
}

func NewGreenhouseAdapter(f *crawler.Fetcher) *GreenhouseAdapter {
	return &GreenhouseAdapter{fetcher: f}
}

func (g *GreenhouseAdapter) Name() string {
	return "Greenhouse"
}

func (g *GreenhouseAdapter) Detect(target string) bool {
	lower := strings.ToLower(target)
	return strings.Contains(lower, "greenhouse.io") ||
		strings.Contains(lower, "boards.greenhouse.io") ||
		strings.Contains(lower, "job-boards.greenhouse.io")
}

type ghJobListing struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	AbsoluteURL string `json:"absolute_url"`
	UpdatedAt   string `json:"updated_at"`
	Location    struct {
		Name string `json:"name"`
	} `json:"location"`
	Departments []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"departments"`
	Content string `json:"content"`
}

type ghResponse struct {
	Jobs []ghJobListing `json:"jobs"`
}

func (g *GreenhouseAdapter) Extract(ctx context.Context, target string) (*CompanyMeta, error) {
	boardToken := extractGreenhouseToken(target)
	if boardToken == "" {
		return nil, fmt.Errorf("unable to extract Greenhouse board token from target: %s", target)
	}
	return g.ExtractByBoardID(ctx, boardToken, boardToken+".com")
}

func (g *GreenhouseAdapter) ExtractByBoardID(ctx context.Context, boardToken, domain string) (*CompanyMeta, error) {
	apiEndpoint := fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs?content=true", boardToken)
	res, err := g.fetcher.Fetch(ctx, apiEndpoint)
	if err != nil {
		return nil, fmt.Errorf("greenhouse API request failed: %w", err)
	}

	compName := formatCompanyName(boardToken)
	if domain == "" {
		domain = boardToken + ".com"
	}

	if res.StatusCode == 304 {
		return &CompanyMeta{
			Name:       compName,
			Domain:     domain,
			CareersURL: fmt.Sprintf("https://boards.greenhouse.io/%s", boardToken),
		}, nil
	}

	if res.StatusCode != 200 {
		return nil, fmt.Errorf("greenhouse API returned status %d for token %s", res.StatusCode, boardToken)
	}

	var payload ghResponse
	if err := json.Unmarshal(res.Body, &payload); err != nil {
		return nil, fmt.Errorf("failed parsing greenhouse JSON response: %w", err)
	}
	careersURL := fmt.Sprintf("https://boards.greenhouse.io/%s", boardToken)
	meta := &CompanyMeta{
		Name:       compName,
		Domain:     domain,
		CareersURL: careersURL,
		Signals:    []string{},
		OpenRoles:  []JobPosting{},
	}

	signalSet := make(map[string]bool)

	for _, j := range payload.Jobs {
		deptName := ""
		if len(j.Departments) > 0 {
			deptName = j.Departments[0].Name
		}

		if !signals.IsBackendRole(j.Title, deptName) {
			continue
		}

		combinedContent := j.Title + " " + deptName + " " + j.Content
		matchedKws := signals.MatchBackendKeywords(combinedContent)
		if len(matchedKws) == 0 {
			continue
		}

		for _, kw := range matchedKws {
			signalSet[kw] = true
		}

		postedTime := time.Now().UTC()
		if j.UpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, j.UpdatedAt); err == nil {
				postedTime = t
			}
		}

		policy := signals.ClassifyRemotePolicy(j.Location.Name, j.Content, j.AbsoluteURL)

		meta.OpenRoles = append(meta.OpenRoles, JobPosting{
			ID:                 fmt.Sprintf("gh-%d", j.ID),
			Title:              j.Title,
			URL:                j.AbsoluteURL,
			ApplyURL:           policy.DirectApplyURL,
			Location:           j.Location.Name,
			Department:         deptName,
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

func extractGreenhouseToken(target string) string {
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	if parts[0] == "embed" && len(parts) > 1 {
		return parts[1]
	}
	return parts[0]
}

func formatCompanyName(token string) string {
	if len(token) == 0 {
		return ""
	}
	cleaned := strings.ReplaceAll(token, "-", " ")
	cleaned = strings.ReplaceAll(cleaned, "_", " ")
	words := strings.Fields(cleaned)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}
