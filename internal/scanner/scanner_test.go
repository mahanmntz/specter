package scanner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mahanmntz/specter/internal/ats"
	"github.com/mahanmntz/specter/internal/seeds"
	"github.com/mahanmntz/specter/internal/signals"
)

type fakeIngester struct {
	meta *ats.CompanyMeta
	err  error
}

func (f fakeIngester) ExtractBySeed(context.Context, seeds.SeedTarget) (*ats.CompanyMeta, error) {
	return f.meta, f.err
}

type fakeMiner struct {
	leads  []signals.EngineeringLead
	err    error
	called int
}

func (f *fakeMiner) MineOrganization(context.Context, string, string, func(string)) ([]signals.EngineeringLead, error) {
	f.called++
	return f.leads, f.err
}

type fakeFrontier struct {
	crawl   bool
	err     error
	visited []string
}

func (f *fakeFrontier) ShouldCrawl(string, time.Duration) (bool, error)       { return f.crawl, f.err }
func (f *fakeFrontier) ShouldCrawlDomain(string, time.Duration) (bool, error) { return f.crawl, f.err }
func (f *fakeFrontier) MarkVisited(u, _ string, _ error) error {
	f.visited = append(f.visited, u)
	return nil
}

type fakeStore struct {
	newRoles, newLeads int
	saveErr            error
}

func (f fakeStore) SaveCompanyAndRoles(context.Context, *ats.CompanyMeta) (int64, int, error) {
	return 1, f.newRoles, f.saveErr
}
func (f fakeStore) SaveEngineeringLeads(context.Context, []signals.EngineeringLead) (int, error) {
	return f.newLeads, nil
}

type recorder struct {
	mu                    sync.Mutex
	logs                  []string
	crawled, leads, mails int
}

func (r *recorder) SetCurrentTarget(string) {}
func (r *recorder) IncCrawled()             { r.mu.Lock(); r.crawled++; r.mu.Unlock() }
func (r *recorder) IncLeads(n int)          { r.mu.Lock(); r.leads += n; r.mu.Unlock() }
func (r *recorder) IncEmails(n int)         { r.mu.Lock(); r.mails += n; r.mu.Unlock() }
func (r *recorder) Log(f string, a ...any) {
	r.mu.Lock()
	r.logs = append(r.logs, fmt.Sprintf(f, a...))
	r.mu.Unlock()
}

var seed = seeds.SeedTarget{Name: "Acme", Domain: "acme.com", ATSService: "greenhouse", ATSBoardID: "acme", GitHubOrg: "acme"}

func newScanner(fr *fakeFrontier, m *fakeMiner, ing Ingester, st fakeStore, obs *recorder, mine bool) *Scanner {
	return &Scanner{Ingester: ing, Miner: m, Frontier: fr, Store: st, Observer: obs, MineLeads: mine}
}

func TestProcessSeedSkipsRecentlyCrawled(t *testing.T) {
	obs := &recorder{}
	m := &fakeMiner{}
	s := newScanner(&fakeFrontier{crawl: false}, m, fakeIngester{}, fakeStore{}, obs, true)
	res, err := s.ProcessSeed(context.Background(), seed)
	if err != nil || !res.Skipped {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if obs.crawled != 1 || m.called != 0 {
		t.Errorf("crawled=%d mined=%d", obs.crawled, m.called)
	}
}

func TestProcessSeedIngestsAndMinesWhenEnabled(t *testing.T) {
	obs := &recorder{}
	m := &fakeMiner{leads: []signals.EngineeringLead{{Email: "a@acme.com"}, {}}}
	meta := &ats.CompanyMeta{Name: "Acme", Domain: "acme.com"}
	s := newScanner(&fakeFrontier{crawl: true}, m, fakeIngester{meta: meta}, fakeStore{newRoles: 3, newLeads: 2}, obs, true)
	res, err := s.ProcessSeed(context.Background(), seed)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewRoles != 3 || res.NewLeads != 2 {
		t.Errorf("res=%+v", res)
	}
	if obs.leads != 2 || obs.mails != 1 {
		t.Errorf("leads=%d emails=%d", obs.leads, obs.mails)
	}
	if meta.GitHubOrg != "acme" {
		t.Errorf("seed org not propagated: %q", meta.GitHubOrg)
	}
}

func TestProcessSeedMiningIsOptIn(t *testing.T) {
	m := &fakeMiner{}
	s := newScanner(&fakeFrontier{crawl: true}, m, fakeIngester{meta: &ats.CompanyMeta{}}, fakeStore{}, &recorder{}, false)
	if _, err := s.ProcessSeed(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	if m.called != 0 {
		t.Error("miner must not run when MineLeads is false")
	}
}

func TestProcessSeedSurfacesErrorsInsteadOfSwallowing(t *testing.T) {
	obs := &recorder{}
	fr := &fakeFrontier{crawl: false, err: errors.New("db locked")}
	s := newScanner(fr, &fakeMiner{}, fakeIngester{err: errors.New("boom")}, fakeStore{}, obs, false)
	if _, err := s.ProcessSeed(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	joined := fmt.Sprint(obs.logs)
	for _, want := range []string{"Frontier check failed", "ATS extraction error"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing log %q in %v", want, obs.logs)
		}
	}
}

func TestSeedAPIURL(t *testing.T) {
	cases := map[string]string{
		"greenhouse": "https://boards-api.greenhouse.io/v1/boards/acme/jobs",
		"lever":      "https://api.lever.co/v0/postings/acme",
		"ashby":      "https://api.ashbyhq.com/posting-api/job-board/acme",
		"":           "https://acme.com",
	}
	for svc, want := range cases {
		s := seed
		s.ATSService = svc
		if got := SeedAPIURL(s); got != want {
			t.Errorf("%q: got %s want %s", svc, got, want)
		}
	}
}
