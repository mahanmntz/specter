// Package runner performs a full discovery pass (ATS boards + aggregator feeds)
// against a Store. It is the engine behind the web UI's "Find jobs" button.
package runner

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
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
		opts.Concurrency = 8
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 25 * time.Second
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
	// Aggregator feeds return large documents from slow servers, so they get a longer timeout.
	feedFetcher := crawler.NewFetcher(crawler.FetcherConfig{Timeout: 60 * time.Second, MaxBodyBytes: 20 * 1024 * 1024})
	return runWith(ctx, sc, feedFetcher, store, opts, obs)
}

// runWith is Run's testable core.
// perCompanyTimeout caps how long one company may hold a worker, so a huge or hung board can never
// stall the whole scan.
var perCompanyTimeout = 75 * time.Second

// runWith is Run's testable core. Companies and job feeds run at the same time; both count toward
// the progress total so 100% means everything has finished.
func runWith(ctx context.Context, sc *scanner.Scanner, feedFetcher *crawler.Fetcher, store feedStore, opts Options, obs scanner.Observer) (Result, error) {
	if ts, ok := obs.(totalSetter); ok {
		ts.SetTotal(len(opts.Seeds) + len(opts.Feeds))
	}

	var newRoles, feedRoles atomic.Int64
	var feedWG sync.WaitGroup
	if len(opts.Feeds) > 0 {
		feedWG.Add(1)
		go func() {
			defer feedWG.Done()
			feedRoles.Store(int64(runFeeds(ctx, feedFetcher, store, opts.Feeds, obs)))
		}()
	}

	var g errgroup.Group
	g.SetLimit(opts.Concurrency)
	for _, s := range opts.Seeds {
		seed := s
		g.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			cctx, cancel := context.WithTimeout(ctx, perCompanyTimeout)
			defer cancel()
			res, err := sc.ProcessSeed(cctx, seed)
			if err != nil {
				obs.Log("⚠️  %s: %v", seed.Name, err)
				return nil
			}
			newRoles.Add(int64(res.NewRoles))
			return nil
		})
	}
	_ = g.Wait()
	feedWG.Wait()

	return Result{
		Companies: len(opts.Seeds),
		NewRoles:  int(newRoles.Load()) + int(feedRoles.Load()),
		FeedRoles: int(feedRoles.Load()),
	}, ctx.Err()
}

// runFeeds fetches the job feeds in parallel (each on its own deadline) and returns the new roles.
func runFeeds(ctx context.Context, fetcher *crawler.Fetcher, store feedStore, names []string, obs scanner.Observer) int {
	available := feeds.All(fetcher)
	var total atomic.Int64
	var g errgroup.Group
	g.SetLimit(6)
	for _, name := range names {
		name := name
		g.Go(func() error {
			defer obs.IncCrawled()
			feed, ok := available[strings.ToLower(strings.TrimSpace(name))]
			if !ok {
				obs.Log("⚠️  Unknown feed %q", name)
				return nil
			}
			fctx, cancel := context.WithTimeout(ctx, 75*time.Second)
			defer cancel()
			total.Add(int64(runFeed(fctx, store, feed, obs)))
			return nil
		})
	}
	_ = g.Wait()
	return int(total.Load())
}

func runFeed(ctx context.Context, store feedStore, feed feeds.Feed, obs scanner.Observer) int {
	emit := func(e scanner.Event) {
		if es, ok := obs.(scanner.EventSink); ok {
			e.Kind, e.Name, e.Source = "feed", feed.Name(), "feed"
			es.Emit(e)
		}
	}
	if skip, why := tooSoon(ctx, store, feed); skip {
		obs.Log("⏭️  %s: %s", feed.Name(), why)
		emit(scanner.Event{Status: scanner.StatusSkipped, Error: "fetched recently (provider rate limit)"})
		return 0
	}
	started := time.Now()
	emit(scanner.Event{Status: scanner.StatusRunning})
	metas, err := feed.Fetch(ctx)
	if err != nil {
		obs.Log("⚠️  %s: %v", feed.Name(), err)
		emit(scanner.Event{Status: scanner.StatusFailed, Error: err.Error(), Millis: time.Since(started).Milliseconds()})
		return 0
	}
	roleCount, added := 0, 0
	for _, meta := range metas {
		_, n, saveErr := store.SaveCompanyAndRoles(ctx, meta)
		if saveErr != nil {
			obs.Log("⚠️  %s: saving %s: %v", feed.Name(), meta.Name, saveErr)
			continue
		}
		added += n
		roleCount += len(meta.OpenRoles)
	}
	status := scanner.StatusOK
	if roleCount == 0 {
		status = scanner.StatusEmpty
	}
	emit(scanner.Event{Status: status, Roles: roleCount, New: added, Millis: time.Since(started).Milliseconds()})
	markFetched(ctx, store, feed)
	obs.Log("✅ %s: %d new role(s)", feed.Name(), added)
	return added
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
