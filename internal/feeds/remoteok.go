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

// RemoteOK reads the public RemoteOK JSON API.
type RemoteOK struct {
	fetcher *crawler.Fetcher
	apiURL  string
}

func NewRemoteOK(f *crawler.Fetcher) *RemoteOK {
	return &RemoteOK{fetcher: f, apiURL: "https://remoteok.com/api"}
}

func (r *RemoteOK) Name() string { return "remoteok" }

type remoteOKJob struct {
	ID          string   `json:"id"`
	Epoch       int64    `json:"epoch"`
	Company     string   `json:"company"`
	Position    string   `json:"position"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	Location    string   `json:"location"`
	URL         string   `json:"url"`
	ApplyURL    string   `json:"apply_url"`
}

func (r *RemoteOK) Fetch(ctx context.Context) ([]*ats.CompanyMeta, error) {
	res, err := r.fetcher.Fetch(ctx, r.apiURL)
	if err != nil {
		return nil, fmt.Errorf("remoteok fetch error: %w", err)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("remoteok returned status %d", res.StatusCode)
	}

	// The first array element is a legal notice object without an "id"/"position".
	var raw []remoteOKJob
	if err := json.Unmarshal(res.Body, &raw); err != nil {
		return nil, fmt.Errorf("failed decoding RemoteOK JSON: %w", err)
	}

	var jobs []feedJob
	for _, j := range raw {
		if j.ID == "" || j.Position == "" || j.Company == "" {
			continue
		}
		posted := time.Time{}
		if j.Epoch > 0 {
			posted = time.Unix(j.Epoch, 0).UTC()
		}
		jobs = append(jobs, feedJob{
			company: j.Company,
			job: ats.RawJob{
				ID:         j.ID,
				Title:      j.Position,
				Department: strings.Join(j.Tags, " "),
				Location:   j.Location,
				// No blanket "remote" hint: the listed location (e.g. "Redwood City") often
				// restricts who can actually apply, so the classifier judges it as-is.
				URL:         firstNonEmpty(j.ApplyURL, j.URL),
				Description: j.Description,
				PostedAt:    posted,
			},
		})
	}
	return groupByCompany("remoteok", "https://remoteok.com", jobs), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
