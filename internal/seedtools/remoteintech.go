package seedtools

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/mahanmntz/specter/internal/seeds"
)

// remoteIntechZip is the whole directory repository (https://github.com/remoteintech/remote-jobs,
// ISC licensed). It is downloaded at run time on the user's machine and never bundled into Specter.
const remoteIntechZip = "https://codeload.github.com/remoteintech/remote-jobs/zip/refs/heads/main"

// Company is one entry of the remoteintech directory.
type Company struct {
	Title        string
	Website      string
	CareersURL   string
	Region       string
	RemotePolicy string
}

// ParseFrontmatter reads the `key: value` header of a directory entry (a YAML subset is enough).
func ParseFrontmatter(doc string) Company {
	var c Company
	lines := strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return c
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		k, v, found := strings.Cut(line, ":")
		if !found || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "title":
			c.Title = v
		case "website":
			c.Website = v
		case "careers_url":
			c.CareersURL = v
		case "region":
			c.Region = v
		case "remote_policy":
			c.RemotePolicy = v
		}
	}
	return c
}

// FetchRemoteIntech downloads the directory as one archive and parses every company entry.
func FetchRemoteIntech(ctx context.Context, f Fetcher) ([]Company, error) {
	res, err := f.Fetch(ctx, remoteIntechZip)
	if err != nil {
		return nil, fmt.Errorf("downloading the remoteintech directory: %w", err)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("downloading the remoteintech directory: status %d", res.StatusCode)
	}
	return ParseArchive(res.Body)
}

// ParseArchive reads company entries (src/companies/*.md) out of a repository zip.
func ParseArchive(zipData []byte) ([]Company, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("reading the remoteintech archive: %w", err)
	}
	var out []Company
	for _, file := range zr.File {
		if !strings.Contains(file.Name, "/src/companies/") || !strings.HasSuffix(file.Name, ".md") {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(rc, 256<<10))
		rc.Close()
		if err != nil {
			continue
		}
		if c := ParseFrontmatter(string(data)); c.Title != "" {
			out = append(out, c)
		}
	}
	return out, nil
}

// SeedFor converts a directory entry to a seed when its careers page is a supported ATS board.
func SeedFor(c Company) (seeds.SeedTarget, bool) {
	svc, board := DetectATS(c.CareersURL)
	if svc == "" {
		return seeds.SeedTarget{}, false
	}
	return seeds.SeedTarget{Name: c.Title, Domain: domainOf(c.Website), ATSService: svc, ATSBoardID: board, Priority: 2}, true
}

func domainOf(website string) string {
	raw := strings.TrimSpace(website)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// ImportSummary reports what an import did.
type ImportSummary struct {
	Companies  int // entries read
	Detected   int // careers page was a supported ATS board
	Discovered int // found by probing (only with discover=true)
	Valid      []seeds.SeedTarget
}

// BuildSeeds turns directory entries into validated seeds. With discover, companies whose careers
// page is not a recognised board are probed on Greenhouse/Lever/Ashby using likely slugs.
func BuildSeeds(ctx context.Context, f Fetcher, companies []Company, discover bool, concurrency int, onDone func(done, total int)) ImportSummary {
	sum := ImportSummary{Companies: len(companies)}
	type cand struct {
		seed       seeds.SeedTarget
		discovered bool
	}
	var cands []cand
	var unknown []Company
	for _, c := range companies {
		if s, ok := SeedFor(c); ok {
			sum.Detected++
			cands = append(cands, cand{seed: s})
		} else {
			unknown = append(unknown, c)
		}
	}

	if discover {
		var mu sync.Mutex
		var g errgroup.Group
		if concurrency <= 0 {
			concurrency = 6
		}
		g.SetLimit(concurrency)
		for _, c := range unknown {
			c := c
			g.Go(func() error {
				dom := domainOf(c.Website)
				if svc, id, ok := Discover(ctx, f, c.Title, dom); ok {
					mu.Lock()
					cands = append(cands, cand{seed: seeds.SeedTarget{Name: c.Title, Domain: dom, ATSService: svc, ATSBoardID: id, Priority: 2}, discovered: true})
					mu.Unlock()
				}
				return nil
			})
		}
		_ = g.Wait()
	}

	list := make([]seeds.SeedTarget, len(cands))
	for i, c := range cands {
		list[i] = c.seed
	}
	results := Check(ctx, f, list, concurrency, onDone)
	for i, r := range results {
		if r.Status == StatusOK {
			sum.Valid = append(sum.Valid, r.Seed)
			if cands[i].discovered {
				sum.Discovered++
			}
		}
	}
	return sum
}
