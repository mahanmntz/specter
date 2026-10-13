package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mahanmntz/specter/internal/ats"
	"github.com/mahanmntz/specter/internal/crawler"
)

// Remotive reads the public Remotive API. Their terms ask for at most four requests a day and
// attribution, so this feed declares a minimum interval and links every job back to Remotive.
type Remotive struct {
	fetcher *crawler.Fetcher
	apiURL  string
}

func NewRemotive(f *crawler.Fetcher) *Remotive {
	return &Remotive{fetcher: f, apiURL: "https://remotive.com/api/remote-jobs?category=software-dev"}
}

func (r *Remotive) Name() string { return "remotive" }

// MinInterval keeps us within Remotive's "no more than 4 requests per day" guidance.
func (r *Remotive) MinInterval() time.Duration { return 6 * time.Hour }

type remotiveResp struct {
	Jobs []struct {
		ID       any      `json:"id"`
		URL      string   `json:"url"`
		Title    string   `json:"title"`
		Company  string   `json:"company_name"`
		Category string   `json:"category"`
		Tags     []string `json:"tags"`
		Posted   string   `json:"publication_date"`
		Location string   `json:"candidate_required_location"`
		Salary   string   `json:"salary"`
		Desc     string   `json:"description"`
	} `json:"jobs"`
}

func (r *Remotive) Fetch(ctx context.Context) ([]*ats.CompanyMeta, error) {
	res, err := r.fetcher.Fetch(ctx, r.apiURL)
	if err != nil {
		return nil, fmt.Errorf("remotive fetch error: %w", err)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("remotive returned status %d", res.StatusCode)
	}
	var body remotiveResp
	if err := json.Unmarshal(res.Body, &body); err != nil {
		return nil, fmt.Errorf("failed decoding Remotive JSON: %w", err)
	}

	var jobs []feedJob
	for _, p := range body.Jobs {
		desc := stripHTML(p.Desc)
		policy := geoPolicy(p.Location, strings.Contains(strings.ToLower(desc), "contractor"), p.URL)
		posted, _ := time.Parse("2006-01-02T15:04:05", p.Posted)
		jobs = append(jobs, feedJob{company: p.Company, job: ats.RawJob{
			ID: fmt.Sprint(p.ID), Title: p.Title, Department: p.Category + " " + strings.Join(p.Tags, " "), Location: p.Location,
			URL: p.URL, Description: desc, PostedAt: posted, Policy: &policy, Compensation: strings.TrimSpace(p.Salary),
		}})
	}
	return groupByCompany("remotive", "https://remotive.com", jobs), nil
}
