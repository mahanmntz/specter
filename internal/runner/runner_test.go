package runner

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mahanmntz/specter/internal/ats"
	"github.com/mahanmntz/specter/internal/scanner"
	"github.com/mahanmntz/specter/internal/seeds"
	"github.com/mahanmntz/specter/internal/signals"
)

type ingester struct {
	calls   atomic.Int64
	delay   time.Duration
	current atomic.Int64
	peak    atomic.Int64
}

func (i *ingester) ExtractBySeed(ctx context.Context, s seeds.SeedTarget) (*ats.CompanyMeta, error) {
	i.calls.Add(1)
	cur := i.current.Add(1)
	for {
		p := i.peak.Load()
		if cur <= p || i.peak.CompareAndSwap(p, cur) {
			break
		}
	}
	defer i.current.Add(-1)
	select {
	case <-time.After(i.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &ats.CompanyMeta{Name: s.Name, Domain: s.Domain}, nil
}

type frontier struct{}

func (frontier) ShouldCrawl(string, time.Duration) (bool, error)       { return true, nil }
func (frontier) ShouldCrawlDomain(string, time.Duration) (bool, error) { return true, nil }
func (frontier) MarkVisited(string, string, error) error               { return nil }

type store struct{ roles int }

func (s store) SaveCompanyAndRoles(context.Context, *ats.CompanyMeta) (int64, int, error) {
	return 1, s.roles, nil
}
func (store) SaveEngineeringLeads(context.Context, []signals.EngineeringLead) (int, error) {
	return 0, nil
}

type obs struct {
	mu      sync.Mutex
	crawled int
	total   int
	logs    []string
}

func (o *obs) SetCurrentTarget(string) {}
func (o *obs) IncCrawled()             { o.mu.Lock(); o.crawled++; o.mu.Unlock() }
func (o *obs) IncLeads(int)            {}
func (o *obs) IncEmails(int)           {}
func (o *obs) SetTotal(n int)          { o.total = n }
func (o *obs) Log(f string, a ...any) {
	o.mu.Lock()
	o.logs = append(o.logs, fmt.Sprintf(f, a...))
	o.mu.Unlock()
}

func makeSeeds(n int) []seeds.SeedTarget {
	out := make([]seeds.SeedTarget, n)
	for i := range out {
		out[i] = seeds.SeedTarget{Name: fmt.Sprintf("C%d", i), Domain: fmt.Sprintf("c%d.com", i), Priority: 1 + i%2}
	}
	return out
}

func newScanner(ing *ingester, o *obs, newRoles int) *scanner.Scanner {
	return &scanner.Scanner{Ingester: ing, Frontier: frontier{}, Store: store{roles: newRoles}, Observer: o}
}

func TestRunProcessesAllSeedsWithBoundedConcurrency(t *testing.T) {
	ing := &ingester{delay: 20 * time.Millisecond}
	o := &obs{}
	res, err := runWith(context.Background(), newScanner(ing, o, 2), nil, store{}, Options{Seeds: makeSeeds(12), Concurrency: 3}, o)
	if err != nil {
		t.Fatal(err)
	}
	if ing.calls.Load() != 12 || o.crawled != 12 || o.total != 12 {
		t.Errorf("calls=%d crawled=%d total=%d", ing.calls.Load(), o.crawled, o.total)
	}
	if res.NewRoles != 24 || res.Companies != 12 {
		t.Errorf("res = %+v", res)
	}
	if ing.peak.Load() > 3 {
		t.Errorf("concurrency limit exceeded: peak=%d", ing.peak.Load())
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ing := &ingester{delay: 5 * time.Second}
	o := &obs{}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()

	start := time.Now()
	_, err := runWith(ctx, newScanner(ing, o, 0), nil, store{}, Options{Seeds: makeSeeds(20), Concurrency: 2}, o)
	if err == nil {
		t.Error("cancelled run should report ctx error")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("did not stop promptly: %v", time.Since(start))
	}
}

func TestUnknownFeedIsReportedNotFatal(t *testing.T) {
	o := &obs{}
	res, err := runWith(context.Background(), newScanner(&ingester{}, o, 0), nil, store{}, Options{Feeds: []string{"nope"}}, o)
	if err != nil || res.FeedRoles != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(o.logs) != 1 {
		t.Errorf("logs = %v", o.logs)
	}
}

func TestSelectSeedsOrdersByPriorityAndLimits(t *testing.T) {
	all := makeSeeds(10) // priorities alternate 1,2
	got := SelectSeeds(all, 4)
	if len(got) != 4 {
		t.Fatalf("len = %d", len(got))
	}
	for _, s := range got {
		if s.Priority != 1 {
			t.Errorf("expected only priority-1 seeds first, got %+v", s)
		}
	}
	if len(SelectSeeds(all, 0)) != 10 {
		t.Error("n=0 means all")
	}
	if all[0].Name != "C0" || all[1].Priority != 2 {
		t.Error("input must not be reordered")
	}
}

type fakeSettings struct {
	store
	kv map[string]string
}

func (s *fakeSettings) GetSetting(_ context.Context, k string) (string, bool, error) {
	v, ok := s.kv[k]
	return v, ok, nil
}
func (s *fakeSettings) SetSetting(_ context.Context, k, v string) error { s.kv[k] = v; return nil }

type limitedFeed struct{ name string }

func (f limitedFeed) Name() string                                      { return f.name }
func (f limitedFeed) Fetch(context.Context) ([]*ats.CompanyMeta, error) { return nil, nil }
func (f limitedFeed) MinInterval() time.Duration                        { return 6 * time.Hour }

func TestRateLimitedFeedIsSkippedWhenFetchedRecently(t *testing.T) {
	ss := &fakeSettings{kv: map[string]string{}}
	f := limitedFeed{"remotive"}

	if skip, _ := tooSoon(context.Background(), ss, f); skip {
		t.Fatal("a feed that never ran must not be skipped")
	}
	markFetched(context.Background(), ss, f)
	if skip, why := tooSoon(context.Background(), ss, f); !skip || why == "" {
		t.Errorf("a feed fetched just now must be skipped (why=%q)", why)
	}
	ss.kv["feed_last_remotive"] = time.Now().Add(-7 * time.Hour).UTC().Format(time.RFC3339)
	if skip, _ := tooSoon(context.Background(), ss, f); skip {
		t.Error("after the interval the feed may run again")
	}
	// A plain store without settings never blocks.
	if skip, _ := tooSoon(context.Background(), store{}, f); skip {
		t.Error("no settings store → never skip")
	}
}
