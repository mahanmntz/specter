// Package runner performs a full discovery pass (ATS boards + aggregator feeds)
// against a Store. It is the engine behind the web UI's "Find jobs" button.
package runner

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/mahanmntz/specter/internal/ats"
	"github.com/mahanmntz/specter/internal/crawler"
	"github.com/mahanmntz/specter/internal/feeds"
	"github.com/mahanmntz/specter/internal/scanner"
	"github.com/mahanmntz/specter/internal/seeds"
	"github.com/mahanmntz/specter/internal/storage"
)

// Options controls one discovery pass.
type Options struct {
	Seeds       []seeds.SeedTarget
	Concurrency int           // default 4
	Timeout     time.Duration // per-request, default 15s
	Feeds       []string      // names from feeds.Names(); empty = none
}

// Result summarizes a pass.
type Result struct {
	Companies int
	NewRoles  int
	FeedRoles int
}

// SelectSeeds returns the seeds to scan: all of them, or the first n by priority (1 = highest).
func SelectSeeds(all []seeds.SeedTarget, n int) []seeds.SeedTarget {
	out := append([]seeds.SeedTarget(nil), all...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// totalSetter is implemented by observers that can show overall progress.
type totalSetter interface{ SetTotal(n int) }

// Run scans the seeds and feeds, writing results to store. It never returns a
// partial-failure error: per-target problems are reported through obs.Log.
// A cancelled ctx stops promptly and returns what was found so far.
func Run(ctx context.Context, store *storage.Store, opts Options, obs scanner.Observer) (Result, error) {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}

	frontier, err := crawler.NewFrontier(store.DB())
	if err != nil {
		return Result{}, fmt.Errorf("initializing crawl frontier: %w", err)
	}
	fetcher := crawler.NewFetcher(crawler.FetcherConfig{
		Timeout:      opts.Timeout,
		MaxBodyBytes: 5 * 1024 * 1024,
		TokenPool:    crawler.NewTokenPoolFromEnv(),
		Frontier:     frontier,
	})

	sc := &scanner.Scanner{
		Ingester: ats.NewRegistry(fetcher),
		Frontier: frontier,
		Store:    store,
		Observer: obs,
		TTL:      0, // an explicit run always refreshes
	}
	return runWith(ctx, sc, fetcher, store, opts, obs)
}

// runWith is Run's testable core.
func runWith(ctx context.Context, sc *scanner.Scanner, feedFetcher *crawler.Fetcher, store feedStore, opts Options, obs scanner.Observer) (Result, error) {
	if ts, ok := obs.(totalSetter); ok {
		ts.SetTotal(len(opts.Seeds))
	}

	var newRoles atomic.Int64
	var g errgroup.Group
	g.SetLimit(opts.Concurrency)
	for _, s := range opts.Seeds {
		seed := s
		g.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			res, err := sc.ProcessSeed(ctx, seed)
			if err != nil {
				obs.Log("⚠️  %s: %v", seed.Name, err)
				return nil
			}
			newRoles.Add(int64(res.NewRoles))
			return nil
		})
	}
	_ = g.Wait()

	result := Result{Companies: len(opts.Seeds), NewRoles: int(newRoles.Load())}

	if len(opts.Feeds) > 0 && ctx.Err() == nil {
		available := feeds.All(feedFetcher)
		for _, name := range opts.Feeds {
			feed, ok := available[strings.ToLower(strings.TrimSpace(name))]
			if !ok {
				obs.Log("⚠️  Unknown feed %q", name)
				continue
			}
			if skip, why := tooSoon(ctx, store, feed); skip {
				obs.Log("⏭️  %s: %s", feed.Name(), why)
				continue
			}
			obs.SetCurrentTarget(feed.Name())
			metas, err := feed.Fetch(ctx)
			if err != nil {
				obs.Log("⚠️  %s: %v", feed.Name(), err)
				continue
			}
			added := 0
			for _, meta := range metas {
				_, n, saveErr := store.SaveCompanyAndRoles(ctx, meta)
				if saveErr != nil {
					obs.Log("⚠️  %s: saving %s: %v", feed.Name(), meta.Name, saveErr)
					continue
				}
				added += n
			}
			markFetched(ctx, store, feed)
			result.FeedRoles += added
			result.NewRoles += added
			obs.Log("✅ %s: %d new role(s)", feed.Name(), added)
		}
	}
	return result, ctx.Err()
}

// feedStore is the slice of Store that feed ingestion needs.
type feedStore interface {
	SaveCompanyAndRoles(ctx context.Context, meta *ats.CompanyMeta) (int64, int, error)
}

// settingsStore is the optional slice of Store used to remember when a rate-limited feed last ran.
type settingsStore interface {
	GetSetting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// tooSoon reports whether a feed with a provider-imposed minimum interval was fetched too recently.
func tooSoon(ctx context.Context, store feedStore, feed feeds.Feed) (bool, string) {
	mi, ok := feed.(feeds.MinIntervaler)
	ss, hasSettings := store.(settingsStore)
	if !ok || !hasSettings {
		return false, ""
	}
	raw, found, err := ss.GetSetting(ctx, "feed_last_"+feed.Name())
	if err != nil || !found {
		return false, ""
	}
	last, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return false, ""
	}
	if wait := mi.MinInterval() - time.Since(last); wait > 0 {
		return true, fmt.Sprintf("fetched recently; the provider asks for at most one fetch per %s (next in %s)", mi.MinInterval().Round(time.Hour), wait.Round(time.Minute))
	}
	return false, ""
}

func markFetched(ctx context.Context, store feedStore, feed feeds.Feed) {
	if _, ok := feed.(feeds.MinIntervaler); !ok {
		return
	}
	if ss, ok := store.(settingsStore); ok {
		_ = ss.SetSetting(ctx, "feed_last_"+feed.Name(), time.Now().UTC().Format(time.RFC3339))
	}
}
