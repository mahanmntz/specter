package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mahanmntz/specter/internal/ats"
	"github.com/mahanmntz/specter/internal/crawler"
	"github.com/mahanmntz/specter/internal/signals"
)

// Arbeitnow reads the public Arbeitnow job-board API (no key), strong on Germany/Europe.
type Arbeitnow struct {
	fetcher *crawler.Fetcher
	apiURL  string
	Pages   int
}

func NewArbeitnow(f *crawler.Fetcher) *Arbeitnow {
	return &Arbeitnow{fetcher: f, apiURL: "https://www.arbeitnow.com/api/job-board-api", Pages: 2}
}

func (a *Arbeitnow) Name() string { return "arbeitnow" }

type arbeitnowResp struct {
	Data []struct {
		Slug    string   `json:"slug"`
		Company string   `json:"company_name"`
		Title   string   `json:"title"`
		Desc    string   `json:"description"`
		Remote  bool     `json:"remote"`
		URL     string   `json:"url"`
		Tags    []string `json:"tags"`
		Loc     string   `json:"location"`
		Created int64    `json:"created_at"`
	} `json:"data"`
}

func (a *Arbeitnow) Fetch(ctx context.Context) ([]*ats.CompanyMeta, error) {
	var jobs []feedJob
	var firstErr error
	ok := 0
	for page := 1; page <= a.Pages; page++ {
		res, err := a.fetcher.Fetch(ctx, fmt.Sprintf("%s?page=%d", a.apiURL, page))
		if err != nil || res.StatusCode != 200 {
			if firstErr == nil {
				firstErr = fmt.Errorf("arbeitnow page %d: %v (status %d)", page, err, statusOf(res))
			}
			break
		}
		var body arbeitnowResp
		if err := json.Unmarshal(res.Body, &body); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("failed decoding Arbeitnow JSON: %w", err)
			}
			break
		}
		ok++
		for _, p := range body.Data {
			desc := stripHTML(p.Desc)
			raw := ats.RawJob{
				ID: p.Slug, Title: p.Title, Department: strings.Join(p.Tags, " "), Location: p.Loc,
				URL: p.URL, Description: desc, PostedAt: unixTime(p.Created),
			}
			if p.Remote {
				place := strings.TrimSpace(p.Loc)
				if place == "" {
					place = "Germany"
				}
				pol := signals.StructuredPolicy(signals.ScopeLocal, []string{place}, strings.Contains(strings.ToLower(desc), "contractor"), p.URL)
				raw.Policy = &pol
			} // on-site postings fall through to the text classifier, which reports them as on-site
			jobs = append(jobs, feedJob{company: p.Company, job: raw})
		}
		if len(body.Data) == 0 {
			break
		}
	}
	if ok == 0 && firstErr != nil {
		return nil, firstErr
	}
	return groupByCompany("arbeitnow", "https://www.arbeitnow.com", jobs), nil
}
