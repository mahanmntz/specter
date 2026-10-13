// Package seedtools validates, discovers and imports the company catalog that Specter scans.
package seedtools

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/mahanmntz/specter/internal/crawler"
	"github.com/mahanmntz/specter/internal/scanner"
	"github.com/mahanmntz/specter/internal/seeds"
)

// Fetcher is the HTTP client seedtools needs; *crawler.Fetcher satisfies it.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) (*crawler.FetchResult, error)
}

// atsServices are the services whose public job-board API we can validate.
var atsServices = map[string]bool{"greenhouse": true, "lever": true, "ashby": true, "workable": true, "smartrecruiters": true, "recruitee": true}

// Status of a checked seed.
type Status string

const (
	StatusOK          Status = "ok"
	StatusStale       Status = "stale"       // the board does not exist (404/410) or is not a job board
	StatusUnreachable Status = "unreachable" // network/server problem: do not conclude anything
	StatusSkipped     Status = "skipped"     // generic site: nothing to validate
)

// Result is the outcome of checking one seed.
type Result struct {
	Seed   seeds.SeedTarget
	Status Status
	HTTP   int
	Detail string
}

// validBoard reports whether body looks like a real job board of the given service.
func validBoard(service string, body []byte) bool {
	switch service {
	case "lever":
		return strings.HasPrefix(strings.TrimSpace(string(body)), "[")
	case "smartrecruiters":
		var v struct {
			TotalFound int `json:"totalFound"`
		}
		return json.Unmarshal(body, &v) == nil && v.TotalFound > 0
	case "recruitee":
		var v struct {
			Offers json.RawMessage `json:"offers"`
		}
		return json.Unmarshal(body, &v) == nil && v.Offers != nil
	default: // greenhouse, ashby, workable all answer {"jobs": [...]}
		var v struct {
			Jobs json.RawMessage `json:"jobs"`
		}
		return json.Unmarshal(body, &v) == nil && v.Jobs != nil
	}
}

// CheckOne validates a single seed against its provider's public API.
func CheckOne(ctx context.Context, f Fetcher, s seeds.SeedTarget) Result {
	svc := strings.ToLower(s.ATSService)
	if !atsServices[svc] {
		return Result{Seed: s, Status: StatusSkipped}
	}
	res, err := f.Fetch(ctx, scanner.SeedAPIURL(s))
	if err != nil || res == nil {
		d := "request failed"
		if err != nil {
			d = err.Error()
		}
		return Result{Seed: s, Status: StatusUnreachable, Detail: d}
	}
	switch {
	case res.StatusCode == 404 || res.StatusCode == 410:
		return Result{Seed: s, Status: StatusStale, HTTP: res.StatusCode, Detail: "board not found"}
	case res.StatusCode != 200:
		return Result{Seed: s, Status: StatusUnreachable, HTTP: res.StatusCode, Detail: "unexpected status"}
	case !validBoard(svc, res.Body):
		return Result{Seed: s, Status: StatusStale, HTTP: 200, Detail: "not a job board"}
	}
	return Result{Seed: s, Status: StatusOK, HTTP: 200}
}

// Check validates seeds concurrently, preserving input order. onDone (optional) reports progress.
func Check(ctx context.Context, f Fetcher, list []seeds.SeedTarget, concurrency int, onDone func(done, total int)) []Result {
	if concurrency <= 0 {
		concurrency = 6
	}
	out := make([]Result, len(list))
	var mu sync.Mutex
	done := 0
	var g errgroup.Group
	g.SetLimit(concurrency)
	for i, s := range list {
		i, s := i, s
		g.Go(func() error {
			if ctx.Err() != nil {
				out[i] = Result{Seed: s, Status: StatusUnreachable, Detail: "cancelled"}
				return nil
			}
			out[i] = CheckOne(ctx, f, s)
			if onDone != nil {
				mu.Lock()
				done++
				onDone(done, len(list))
				mu.Unlock()
			}
			return nil
		})
	}
	_ = g.Wait()
	return out
}

var (
	greenhouseHost = regexp.MustCompile(`(?i)^(?:job-)?boards(?:\.[a-z]+)?\.greenhouse\.io$`)
	slugRe         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,60}$`)
)

// DetectATS recognises a supported job board in a careers URL. It returns ("", "") when the URL is
// a company's own site or an unsupported provider.
func DetectATS(careersURL string) (service, boardID string) {
	raw := strings.TrimSpace(careersURL)
	if raw == "" {
		return "", ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	first := ""
	if len(segs) > 0 {
		first = segs[0]
	}
	ok := func(id string) bool { return slugRe.MatchString(id) }

	switch {
	case greenhouseHost.MatchString(host):
		if id := u.Query().Get("for"); ok(id) { // embed/job_board?for=<id>
			return "greenhouse", id
		}
		if ok(first) && first != "embed" {
			return "greenhouse", first
		}
	case host == "jobs.lever.co" || host == "jobs.eu.lever.co":
		if ok(first) {
			return "lever", first
		}
	case host == "jobs.ashbyhq.com":
		if ok(first) {
			return "ashby", first
		}
	case host == "apply.workable.com":
		if ok(first) {
			return "workable", first
		}
	case strings.HasSuffix(host, ".workable.com"):
		if id := strings.TrimSuffix(host, ".workable.com"); ok(id) && id != "apply" {
			return "workable", id
		}
	case host == "careers.smartrecruiters.com" || host == "jobs.smartrecruiters.com":
		if ok(first) {
			return "smartrecruiters", first
		}
	case strings.HasSuffix(host, ".recruitee.com"):
		if id := strings.TrimSuffix(host, ".recruitee.com"); ok(id) && id != "api" {
			return "recruitee", id
		}
	}
	return "", ""
}

// Discover probes the common ATS providers for a company's board using likely slugs.
// It makes at most 9 requests and returns ok=false when nothing validates.
func Discover(ctx context.Context, f Fetcher, name, domain string) (service, boardID string, ok bool) {
	for _, slug := range slugCandidates(name, domain) {
		for _, svc := range []string{"greenhouse", "lever", "ashby"} {
			if ctx.Err() != nil {
				return "", "", false
			}
			cand := seeds.SeedTarget{Name: name, Domain: domain, ATSService: svc, ATSBoardID: slug}
			if CheckOne(ctx, f, cand).Status == StatusOK {
				return svc, slug, true
			}
		}
	}
	return "", "", false
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slugCandidates(name, domain string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && slugRe.MatchString(s) && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	lower := strings.ToLower(name)
	add(nonAlnum.ReplaceAllString(lower, ""))
	add(strings.Trim(nonAlnum.ReplaceAllString(lower, "-"), "-"))
	host := strings.ToLower(strings.TrimPrefix(domain, "www."))
	if i := strings.Index(host, "."); i > 0 {
		add(host[:i])
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}
