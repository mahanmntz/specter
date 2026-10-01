package ats

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"specter/internal/crawler"
	"specter/internal/signals"

	"golang.org/x/net/html"
)

type GenericAdapter struct {
	fetcher    *crawler.Fetcher
	greenhouse *GreenhouseAdapter
	lever      *LeverAdapter
	ashby      *AshbyAdapter
}

func NewGenericAdapter(f *crawler.Fetcher) *GenericAdapter {
	return &GenericAdapter{
		fetcher:    f,
		greenhouse: NewGreenhouseAdapter(f),
		lever:      NewLeverAdapter(f),
		ashby:      NewAshbyAdapter(f),
	}
}

func (g *GenericAdapter) Name() string {
	return "Generic"
}

func (g *GenericAdapter) Detect(target string) bool {
	return true // Fallback for any website domain or careers URL
}

func (g *GenericAdapter) Extract(ctx context.Context, target string) (*CompanyMeta, error) {
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}

	res, err := g.fetcher.Fetch(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("failed fetching domain target %s: %w", target, err)
	}

	u, err := url.Parse(res.EffectiveURL)
	if err != nil {
		return nil, err
	}

	doc, err := html.Parse(bytes.NewReader(res.Body))
	if err != nil {
		return nil, fmt.Errorf("failed parsing HTML DOM: %w", err)
	}

	// 1. Scan for embedded ATS links (Greenhouse, Lever, Ashby)
	discoveredLinks := extractLinks(doc, u)
	for _, l := range discoveredLinks {
		if g.greenhouse.Detect(l) {
			return g.greenhouse.Extract(ctx, l)
		}
		if g.lever.Detect(l) {
			return g.lever.Extract(ctx, l)
		}
		if g.ashby.Detect(l) {
			return g.ashby.Extract(ctx, l)
		}
	}

	// 2. If this is a main company site, try discovering /careers or /jobs
	if !strings.Contains(u.Path, "career") && !strings.Contains(u.Path, "job") {
		for _, l := range discoveredLinks {
			lower := strings.ToLower(l)
			if (strings.Contains(lower, "career") || strings.Contains(lower, "job")) && crawler.InScope(mustHost(l), u.Hostname()) {
				// Crawl careers page
				careersRes, err := g.fetcher.Fetch(ctx, l)
				if err == nil {
					cDoc, err := html.Parse(bytes.NewReader(careersRes.Body))
					if err == nil {
						cLinks := extractLinks(cDoc, mustURL(careersRes.EffectiveURL))
						for _, cl := range cLinks {
							if g.greenhouse.Detect(cl) {
								return g.greenhouse.Extract(ctx, cl)
							}
							if g.lever.Detect(cl) {
								return g.lever.Extract(ctx, cl)
							}
							if g.ashby.Detect(cl) {
								return g.ashby.Extract(ctx, cl)
							}
						}
					}
				}
				break
			}
		}
	}

	// 3. Fallback: Parse backend job links found directly in the page
	domain := u.Hostname()
	companyName := formatCompanyName(strings.TrimSuffix(domain, "."+getTLD(domain)))

	meta := &CompanyMeta{
		Name:       companyName,
		Domain:     domain,
		CareersURL: res.EffectiveURL,
		Signals:    []string{},
		OpenRoles:  []JobPosting{},
	}

	signalSet := make(map[string]bool)

	for _, l := range discoveredLinks {
		lower := strings.ToLower(l)
		if signals.IsBackendRole(lower, "") {
			matched := signals.MatchBackendKeywords(lower)
			if len(matched) == 0 {
				matched = []string{"backend"}
			}
			for _, m := range matched {
				signalSet[m] = true
			}

			meta.OpenRoles = append(meta.OpenRoles, JobPosting{
				ID:        fmt.Sprintf("gen-%d", time.Now().UnixNano()),
				Title:     extractTitleFromURL(l),
				URL:       l,
				Seniority: signals.ExtractSeniority(l),
				Keywords:  matched,
				PostedAt:  time.Now().UTC(),
			})
		}
	}

	for kw := range signalSet {
		meta.Signals = append(meta.Signals, kw)
	}

	return meta, nil
}

func extractLinks(n *html.Node, baseURL *url.URL) []string {
	var links []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					resolved, err := baseURL.Parse(attr.Val)
					if err == nil && (resolved.Scheme == "http" || resolved.Scheme == "https") {
						links = append(links, resolved.String())
					}
				}
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return links
}

func mustURL(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}

func mustHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func getTLD(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return ""
}

func extractTitleFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) > 0 {
		slug := parts[len(parts)-1]
		slug = strings.ReplaceAll(slug, "-", " ")
		slug = strings.ReplaceAll(slug, "_", " ")
		return formatCompanyName(slug)
	}
	return "Backend Engineer"
}
