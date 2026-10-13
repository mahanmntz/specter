// Package feeds ingests multi-company job aggregators (RemoteOK, Hacker News
// "Who is hiring?") and maps their postings into the same CompanyMeta model
// used by the per-company ATS adapters.
package feeds

import (
	"context"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mahanmntz/specter/internal/ats"
	"github.com/mahanmntz/specter/internal/crawler"
)

// Feed is a job aggregator that yields postings grouped by company.
type Feed interface {
	Name() string
	Fetch(ctx context.Context) ([]*ats.CompanyMeta, error)
}

// All returns every built-in feed keyed by name.
func All(f *crawler.Fetcher) map[string]Feed {
	return map[string]Feed{
		"remoteok":  NewRemoteOK(f),
		"hn":        NewHackerNews(f),
		"remotive":  NewRemotive(f),
		"jobicy":    NewJobicy(f),
		"himalayas": NewHimalayas(f),
		"arbeitnow": NewArbeitnow(f),
	}
}

// MinIntervaler is implemented by feeds whose provider limits how often they may be fetched.
type MinIntervaler interface{ MinInterval() time.Duration }

// Names returns the sorted names of the built-in feeds.
func Names() []string {
	names := make([]string, 0, 6)
	for n := range All(nil) {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

type feedJob struct {
	company string
	job     ats.RawJob
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// aggregatorHosts are never a company's own domain.
var aggregatorHosts = map[string]bool{
	"remoteok.com": true, "remoteok.io": true, "news.ycombinator.com": true, "remotive.com": true,
	"jobicy.com": true, "himalayas.app": true, "arbeitnow.com": true,
}

// companyDomain derives a stable pseudo-domain for a company. It prefers the host of
// the posting's own URL (when it is not an aggregator) and falls back to "<slug>.unknown".
func companyDomain(company, postingURL string) string {
	if u, err := url.Parse(postingURL); err == nil && u.Host != "" {
		host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
		if !aggregatorHosts[host] && !strings.Contains(host, "greenhouse.io") &&
			!strings.Contains(host, "lever.co") && !strings.Contains(host, "ashbyhq.com") &&
			!strings.Contains(host, "workable.com") && !strings.Contains(host, "smartrecruiters.com") {
			return host
		}
	}
	return slug(company) + ".unknown"
}

// groupByCompany turns flat postings into one CompanyMeta per company.
func groupByCompany(feedName, careersBase string, jobs []feedJob) []*ats.CompanyMeta {
	byCompany := map[string][]ats.RawJob{}
	names := map[string]string{}
	for _, fj := range jobs {
		key := slug(fj.company)
		if key == "" {
			continue
		}
		byCompany[key] = append(byCompany[key], fj.job)
		names[key] = strings.TrimSpace(fj.company)
	}

	keys := make([]string, 0, len(byCompany))
	for k := range byCompany {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []*ats.CompanyMeta
	for _, k := range keys {
		js := byCompany[k]
		meta := ats.BuildMeta(names[k], companyDomain(names[k], js[0].URL), careersBase, feedName, js)
		if len(meta.OpenRoles) > 0 {
			out = append(out, meta)
		}
	}
	return out
}
