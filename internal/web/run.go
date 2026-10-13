package web

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mahanmntz/specter/internal/runner"
	"github.com/mahanmntz/specter/internal/scanner"
	"github.com/mahanmntz/specter/internal/storage"
)

// RunFunc performs one discovery pass; the web UI's "Find jobs" button calls it.
type RunFunc func(ctx context.Context, opts runner.Options, obs *Progress) (runner.Result, error)

// Run states.
const (
	RunIdle      = "idle"
	RunRunning   = "running"
	RunDone      = "done"
	RunCancelled = "cancelled"
	RunFailed    = "error"
)

const maxLogLines = 40

// Progress records a run's progress. It implements scanner.Observer.
type Progress struct {
	mu         sync.Mutex
	state      string
	startedAt  time.Time
	finishedAt time.Time
	total      int
	done       int
	current    string
	newRoles   int
	feedRoles  int
	err        string
	logs       []string
	cancel     context.CancelFunc

	plan   []PlanItem
	seq    int
	events []LiveEvent
	active map[string]scanner.Event
	counts map[string]int
	roles  int
}

// PlanItem is one group of sources a run will look at ("greenhouse": 23 companies).
type PlanItem struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
	Feed   bool   `json:"feed"`
}

// LiveEvent is a scanner event with a sequence number and timestamp.
type LiveEvent struct {
	Seq int       `json:"seq"`
	At  time.Time `json:"at"`
	scanner.Event
}

const maxEvents = 400

// RunStatus is the JSON snapshot of a run.
type RunStatus struct {
	State      string    `json:"state"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Total      int       `json:"total"`
	Done       int       `json:"done"`
	Current    string    `json:"current,omitempty"`
	NewRoles   int       `json:"new_roles"`
	FeedRoles  int       `json:"feed_roles"`
	Error      string    `json:"error,omitempty"`
	Logs       []string  `json:"logs"`

	Plan   []PlanItem      `json:"plan"`
	Events []LiveEvent     `json:"events"` // only events after the requested sequence number
	Active []scanner.Event `json:"active"`
	Counts map[string]int  `json:"counts"`
	Roles  int             `json:"roles_found"`
	Seq    int             `json:"seq"`
}

func newProgress() *Progress {
	return &Progress{state: RunIdle, active: map[string]scanner.Event{}, counts: map[string]int{}}
}

func (p *Progress) Status() RunStatus { return p.StatusSince(0) }

// StatusSince is Status with only the events after since.
func (p *Progress) StatusSince(since int) RunStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	var evs []LiveEvent
	for _, e := range p.events {
		if e.Seq > since {
			evs = append(evs, e)
		}
	}
	active := make([]scanner.Event, 0, len(p.active))
	for _, a := range p.active {
		active = append(active, a)
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Name < active[j].Name })
	counts := map[string]int{}
	for k, v := range p.counts {
		counts[k] = v
	}
	return RunStatus{
		Plan: append([]PlanItem{}, p.plan...), Events: evs, Active: active, Counts: counts, Roles: p.roles, Seq: p.seq,
		State: p.state, StartedAt: p.startedAt, FinishedAt: p.finishedAt, Total: p.total, Done: p.done,
		Current: p.current, NewRoles: p.newRoles, FeedRoles: p.feedRoles, Error: p.err,
		Logs: append([]string{}, p.logs...),
	}
}

func (p *Progress) running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state == RunRunning
}

// scanner.Observer implementation.
func (p *Progress) SetTotal(n int)            { p.mu.Lock(); p.total = n; p.mu.Unlock() }
func (p *Progress) SetCurrentTarget(s string) { p.mu.Lock(); p.current = s; p.mu.Unlock() }
func (p *Progress) IncCrawled()               { p.mu.Lock(); p.done++; p.mu.Unlock() }

// SetPlan records what the run is about to look at, so the UI can show it before work starts.
func (p *Progress) SetPlan(plan []PlanItem) { p.mu.Lock(); p.plan = plan; p.mu.Unlock() }

// Emit records a structured scanner event (scanner.EventSink).
func (p *Progress) Emit(e scanner.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := e.Kind + ":" + e.Name
	if e.Status == scanner.StatusRunning {
		p.active[key] = e
		return
	}
	delete(p.active, key)
	p.seq++
	p.events = append(p.events, LiveEvent{Seq: p.seq, At: time.Now(), Event: e})
	if len(p.events) > maxEvents {
		p.events = p.events[len(p.events)-maxEvents:]
	}
	p.counts[e.Status]++
	p.roles += e.Roles
}

func (p *Progress) IncLeads(int)  {}
func (p *Progress) IncEmails(int) {}
func (p *Progress) Log(format string, args ...any) {
	p.mu.Lock()
	p.logs = append(p.logs, fmt.Sprintf(format, args...))
	if len(p.logs) > maxLogLines {
		p.logs = p.logs[len(p.logs)-maxLogLines:]
	}
	p.mu.Unlock()
}

// start marks a new run; returns false if one is already running.
func (p *Progress) start(cancel context.CancelFunc) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == RunRunning {
		return false
	}
	p.state, p.startedAt, p.finishedAt = RunRunning, time.Now(), time.Time{}
	p.total, p.done, p.current, p.newRoles, p.feedRoles = 0, 0, "", 0, 0
	p.err, p.logs, p.cancel = "", nil, cancel
	p.plan, p.seq, p.events, p.roles = nil, 0, nil, 0
	p.active, p.counts = map[string]scanner.Event{}, map[string]int{}
	return true
}

func (p *Progress) finish(res runner.Result, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finishedAt = time.Now()
	p.newRoles, p.feedRoles = res.NewRoles, res.FeedRoles
	p.current = ""
	switch {
	case err == context.Canceled:
		p.state = RunCancelled
	case err != nil:
		p.state, p.err = RunFailed, err.Error()
	default:
		p.state = RunDone
	}
	p.cancel = nil
}

func (p *Progress) stop() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != RunRunning || p.cancel == nil {
		return false
	}
	p.cancel()
	return true
}

// recordRun stores a crawl-run row so "scanned X ago" works in the UI.
func recordRun(src Source, started time.Time, res runner.Result) {
	_ = src.RecordCrawlRun(context.Background(), &storage.CrawlRun{
		RunID:          "ui-" + started.UTC().Format("2006-01-02_15-04-05"),
		StartedAt:      started,
		CompletedAt:    time.Now(),
		TotalCompanies: res.Companies,
		NewRoles:       res.NewRoles,
	})
}
