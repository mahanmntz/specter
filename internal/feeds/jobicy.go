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

// Jobicy reads the public Jobicy remote-jobs API (no key). Each posting carries a structured
// `jobGeo` ("Anywhere", "Europe", "USA"), which gives real eligibility data.
type Jobicy struct {
	fetcher *crawler.Fetcher
	apiURL  string
}

func NewJobicy(f *crawler.Fetcher) *Jobicy {
	return &Jobicy{fetcher: f, apiURL: "https://jobicy.com/api/v2/remote-jobs?count=100&industry=dev"}
}

func (j *Jobicy) Name() string { return "jobicy" }

type jobicyResp struct {
	Jobs []struct {
		ID          any      `json:"id"`
		URL         string   `json:"url"`
		Title       string   `json:"jobTitle"`
		Company     string   `json:"companyName"`
		Industry    []string `json:"jobIndustry"`
		Geo         string   `json:"jobGeo"`
		Level       string   `json:"jobLevel"`
		Excerpt     string   `json:"jobExcerpt"`
		Description string   `json:"jobDescription"`
		PubDate     string   `json:"pubDate"`
		SalaryMin   any      `json:"salaryMin"`
		SalaryMax   any      `json:"salaryMax"`
		Currency    string   `json:"salaryCurrency"`
		Period      string   `json:"salaryPeriod"`
	} `json:"jobs"`
}

func (j *Jobicy) Fetch(ctx context.Context) ([]*ats.CompanyMeta, error) {
	res, err := j.fetcher.Fetch(ctx, j.apiURL)
	if err != nil {
		return nil, fmt.Errorf("jobicy fetch error: %w", err)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("jobicy returned status %d", res.StatusCode)
	}
	var body jobicyResp
	if err := json.Unmarshal(res.Body, &body); err != nil {
		return nil, fmt.Errorf("failed decoding Jobicy JSON: %w", err)
	}

	var jobs []feedJob
	for _, p := range body.Jobs {
		desc := stripHTML(p.Excerpt + " " + p.Description)
		contractor := strings.Contains(strings.ToLower(desc), "contractor")
		policy := geoPolicy(p.Geo, contractor, p.URL)
		posted, _ := time.Parse(time.RFC3339, p.PubDate)
		jobs = append(jobs, feedJob{company: p.Company, job: ats.RawJob{
			ID: fmt.Sprint(p.ID), Title: p.Title, Department: strings.Join(p.Industry, " "), Location: p.Geo,
			URL: p.URL, Description: desc, PostedAt: posted, Policy: &policy,
			Compensation: pay(p.SalaryMin, p.SalaryMax, p.Currency, p.Period),
		}})
	}
	return groupByCompany("jobicy", "https://jobicy.com", jobs), nil
}

// pay formats a salary range, or returns "" when the source gives none.
func pay(min, max any, currency, period string) string {
	lo, hi := num(min), num(max)
	if lo == 0 && hi == 0 {
		return ""
	}
	cur := strings.TrimSpace(currency)
	switch {
	case lo > 0 && hi > 0 && lo != hi:
		return fmt.Sprintf("%s %s–%s/%s", cur, short(lo), short(hi), periodShort(period))
	default:
		v := lo
		if v == 0 {
			v = hi
		}
		return fmt.Sprintf("%s %s/%s", cur, short(v), periodShort(period))
	}
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		var f float64
		fmt.Sscanf(strings.ReplaceAll(x, ",", ""), "%f", &f)
		return f
	}
	return 0
}

func short(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.0fk", v/1000)
	}
	return fmt.Sprintf("%.0f", v)
}

func periodShort(p string) string {
	switch strings.ToLower(p) {
	case "hourly", "hour":
		return "hr"
	case "monthly", "month":
		return "mo"
	default:
		return "yr"
	}
}
