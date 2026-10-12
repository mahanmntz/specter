package web

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mahanmntz/specter/internal/runner"
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
}

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
}

func newProgress() *Progress { return &Progress{state: RunIdle} }

func (p *Progress) Status() RunStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return RunStatus{
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
func (p *Progress) IncLeads(int)              {}
func (p *Progress) IncEmails(int)             {}
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
