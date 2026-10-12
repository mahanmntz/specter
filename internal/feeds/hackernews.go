package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"

	"specter/internal/ats"
	"specter/internal/crawler"
)

// HackerNews reads the latest monthly "Ask HN: Who is hiring?" thread via the Algolia API.
type HackerNews struct {
	fetcher *crawler.Fetcher
	apiBase string
}

func NewHackerNews(f *crawler.Fetcher) *HackerNews {
	return &HackerNews{fetcher: f, apiBase: "https://hn.algolia.com/api/v1"}
}

func (h *HackerNews) Name() string { return "hn" }

type hnSearch struct {
	Hits []struct {
		ObjectID string `json:"objectID"`
		Title    string `json:"title"`
	} `json:"hits"`
}

type hnItem struct {
	Children []struct {
		ID        int64  `json:"id"`
		Text      string `json:"text"`
		CreatedAt string `json:"created_at"`
	} `json:"children"`
}

func (h *HackerNews) Fetch(ctx context.Context) ([]*ats.CompanyMeta, error) {
	q := url.Values{"query": {"Ask HN: Who is hiring?"}, "tags": {"story,author_whoishiring"}}
	res, err := h.fetcher.Fetch(ctx, h.apiBase+"/search_by_date?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("hn search error: %w", err)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("hn search returned status %d", res.StatusCode)
	}
	var search hnSearch
	if err := json.Unmarshal(res.Body, &search); err != nil {
		return nil, fmt.Errorf("failed decoding HN search: %w", err)
	}

	threadID := ""
	for _, hit := range search.Hits {
		if strings.Contains(strings.ToLower(hit.Title), "who is hiring") {
			threadID = hit.ObjectID
			break
		}
	}
	if threadID == "" {
		return nil, fmt.Errorf("no 'Who is hiring?' thread found")
	}

	res, err = h.fetcher.Fetch(ctx, h.apiBase+"/items/"+url.PathEscape(threadID))
	if err != nil {
		return nil, fmt.Errorf("hn thread error: %w", err)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("hn thread returned status %d", res.StatusCode)
	}
	var item hnItem
	if err := json.Unmarshal(res.Body, &item); err != nil {
		return nil, fmt.Errorf("failed decoding HN thread: %w", err)
	}

	var jobs []feedJob
	for _, c := range item.Children {
		company, job, ok := parseHNPost(c.Text)
		if !ok {
			continue
		}
		job.ID = fmt.Sprint(c.ID)
		job.PostedAt, _ = time.Parse(time.RFC3339, c.CreatedAt)
		if job.URL == "" {
			job.URL = fmt.Sprintf("https://news.ycombinator.com/item?id=%d", c.ID)
		}
		jobs = append(jobs, feedJob{company: company, job: job})
	}
	return groupByCompany("hn", "https://news.ycombinator.com/item?id="+threadID, jobs), nil
}

var (
	hnTags     = regexp.MustCompile(`<[^>]+>`)
	hnBreaks   = regexp.MustCompile(`(?i)<p>|<br\s*/?>`)
	hnLinkHref = regexp.MustCompile(`href="(https?://[^"]+)"`)
)

// parseHNPost parses the conventional "Company | Role | Location | ..." first line.
func parseHNPost(raw string) (company string, job ats.RawJob, ok bool) {
	linkMatch := hnLinkHref.FindStringSubmatch(raw)
	text := html.UnescapeString(hnTags.ReplaceAllString(hnBreaks.ReplaceAllString(raw, "\n"), ""))
	text = strings.TrimSpace(text)
	if text == "" {
		return "", job, false
	}

	header := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	parts := strings.Split(header, "|")
	if len(parts) < 2 {
		return "", job, false
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	company = parts[0]
	if company == "" || len(company) > 80 {
		return "", job, false
	}

	var title, location, workplace string
	for _, p := range parts[1:] {
		lower := strings.ToLower(p)
		switch {
		case title == "" && roleHint(lower):
			title = p
		case strings.Contains(lower, "remote"):
			workplace = "remote"
			location = firstNonEmpty(location, p)
		case strings.Contains(lower, "onsite") || strings.Contains(lower, "hybrid"):
			location = firstNonEmpty(location, p)
		}
	}
	if title == "" {
		return "", job, false
	}

	job = ats.RawJob{Title: title, Location: location, Workplace: workplace, Description: text}
	if linkMatch != nil {
		job.URL = linkMatch[1]
	}
	return company, job, true
}

func roleHint(lower string) bool {
	for _, h := range []string{"engineer", "developer", "backend", "back-end", "full stack", "full-stack", "fullstack", "sre", "devops", "architect", "swe"} {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}
