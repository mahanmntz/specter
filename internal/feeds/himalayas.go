package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mahanmntz/specter/internal/ats"
	"github.com/mahanmntz/specter/internal/crawler"
	"github.com/mahanmntz/specter/internal/signals"
)

// Himalayas reads the public Himalayas jobs search API (no key). Postings list explicit
// `locationRestrictions` and `timezoneRestrictions`, so eligibility is stated, not guessed.
type Himalayas struct {
	fetcher *crawler.Fetcher
	apiBase string
	// Queries are the search phrases to run; defaults to the active profile's role terms.
	Queries func() []string
	Pages   int
}

func NewHimalayas(f *crawler.Fetcher) *Himalayas {
	return &Himalayas{fetcher: f, apiBase: "https://himalayas.app/jobs/api/search", Queries: func() []string { return signals.SearchQueries(4) }, Pages: 2}
}

func (h *Himalayas) Name() string { return "himalayas" }

type himalayasResp struct {
	Jobs []struct {
		Title        string    `json:"title"`
		Excerpt      string    `json:"excerpt"`
		Description  string    `json:"description"`
		Company      string    `json:"companyName"`
		Seniority    []string  `json:"seniority"`
		Locations    []string  `json:"locationRestrictions"`
		Timezones    []float64 `json:"timezoneRestrictions"`
		Categories   []string  `json:"categories"`
		PubDate      int64     `json:"pubDate"`
		ApplyLink    string    `json:"applicationLink"`
		GUID         string    `json:"guid"`
		MinSalary    any       `json:"minSalary"`
		MaxSalary    any       `json:"maxSalary"`
		Currency     string    `json:"currency"`
		SalaryPeriod string    `json:"salaryPeriod"`
	} `json:"jobs"`
}

func (h *Himalayas) Fetch(ctx context.Context) ([]*ats.CompanyMeta, error) {
	queries := h.Queries()
	if len(queries) == 0 {
		queries = []string{"software engineer"}
	}
	seen := map[string]bool{}
	var jobs []feedJob
	var firstErr error
	ok := 0
	for _, q := range queries {
		for page := 1; page <= h.Pages; page++ {
			endpoint := fmt.Sprintf("%s?q=%s&page=%d", h.apiBase, url.QueryEscape(q), page)
			res, err := h.fetcher.Fetch(ctx, endpoint)
			if err != nil || res.StatusCode != 200 {
				if firstErr == nil {
					firstErr = fmt.Errorf("himalayas search %q: %v (status %d)", q, err, statusOf(res))
				}
				break
			}
			var body himalayasResp
			if err := json.Unmarshal(res.Body, &body); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("failed decoding Himalayas JSON: %w", err)
				}
				break
			}
			ok++
			for _, p := range body.Jobs {
				id := p.GUID
				if i := strings.LastIndex(id, "/"); i >= 0 {
					id = id[i+1:]
				}
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				desc := stripHTML(p.Excerpt + " " + p.Description)
				policy := himalayasPolicy(p.Locations, p.Timezones, strings.Contains(strings.ToLower(desc), "contractor"), p.ApplyLink)
				jobs = append(jobs, feedJob{company: p.Company, job: ats.RawJob{
					ID: id, Title: p.Title, Department: strings.Join(p.Categories, " "), Location: strings.Join(p.Locations, ", "),
					URL: firstNonEmpty(p.ApplyLink, p.GUID), Description: desc, PostedAt: unixTime(p.PubDate), Policy: &policy,
					Compensation: pay(p.MinSalary, p.MaxSalary, p.Currency, p.SalaryPeriod),
				}})
			}
			if len(body.Jobs) < 20 {
				break
			}
		}
	}
	if ok == 0 && firstErr != nil {
		return nil, firstErr
	}
	return groupByCompany("himalayas", "https://himalayas.app", jobs), nil
}

func statusOf(r *crawler.FetchResult) int {
	if r == nil {
		return 0
	}
	return r.StatusCode
}

// himalayasPolicy: no country list and a full timezone band means worldwide; a country list
// means those countries only; a narrower timezone band is regional.
func himalayasPolicy(countries []string, tz []float64, contractor bool, link string) signals.RemotePolicyResult {
	if len(countries) > 0 {
		return signals.StructuredPolicy(signals.ScopeLocal, summarizePlaces(countries), contractor, link)
	}
	if len(tz) == 0 || len(tz) >= 24 {
		return signals.StructuredPolicy(signals.ScopeWorldwide, nil, contractor, link)
	}
	lo, hi := tz[0], tz[0]
	for _, t := range tz {
		if t < lo {
			lo = t
		}
		if t > hi {
			hi = t
		}
	}
	return signals.StructuredPolicy(signals.ScopeRegional, []string{fmt.Sprintf("UTC%+.0f to UTC%+.0f", lo, hi)}, contractor, link)
}
