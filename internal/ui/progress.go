package ui

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// ProgressTracker maintains live recon metrics and renders a dynamic terminal line.
type ProgressTracker struct {
	out             io.Writer
	ticker          *time.Ticker
	stopCh          chan struct{}
	wg              sync.WaitGroup
	mu              sync.Mutex
	active          bool

	startTime       time.Time
	activeWorkers   atomic.Int32
	targetsCrawled  atomic.Int64
	targetsTotal    atomic.Int64
	leadsFound      atomic.Int64
	emailsCaptured  atomic.Int64
	requestCount    atomic.Int64

	currentTarget   string
	rateLimitStatus string
	lastLineLen     int
}

// NewProgressTracker creates a new terminal progress tracker.
func NewProgressTracker(out io.Writer) *ProgressTracker {
	if out == nil {
		out = os.Stderr
	}
	return &ProgressTracker{
		out:             out,
		stopCh:          make(chan struct{}),
		rateLimitStatus: "OK",
		startTime:       time.Now(),
	}
}

// Start begins background rendering of the progress line.
func (p *ProgressTracker) Start(interval time.Duration) {
	p.mu.Lock()
	if p.active {
		p.mu.Unlock()
		return
	}
	if interval <= 0 {
		interval = 150 * time.Millisecond
	}
	p.ticker = time.NewTicker(interval)
	p.active = true
	p.stopCh = make(chan struct{})
	p.startTime = time.Now()
	p.wg.Add(1)
	p.mu.Unlock()

	go func() {
		defer p.wg.Done()
		for {
			select {
			case <-p.stopCh:
				return
			case <-p.ticker.C:
				p.render()
			}
		}
	}()
}

// Stop halts background rendering and clears the line.
func (p *ProgressTracker) Stop() {
	p.mu.Lock()
	if !p.active {
		p.mu.Unlock()
		return
	}
	p.active = false
	p.ticker.Stop()
	close(p.stopCh)
	p.mu.Unlock()

	p.wg.Wait()
	// Clear the status line
	fmt.Fprintf(p.out, "\r\033[K")
}

// IncCrawled increments targets crawled count and request count.
func (p *ProgressTracker) IncCrawled() {
	p.targetsCrawled.Add(1)
	p.requestCount.Add(1)
}

// IncRequests tracks raw network requests made.
func (p *ProgressTracker) IncRequests(n int) {
	p.requestCount.Add(int64(n))
}

// SetTotalTargets sets total count of targets to process.
func (p *ProgressTracker) SetTotalTargets(total int64) {
	p.targetsTotal.Store(total)
}

// SetTotalURLs is an alias for backwards compatibility.
func (p *ProgressTracker) SetTotalURLs(total int64) {
	p.targetsTotal.Store(total)
}

// SetActiveWorkers updates the number of in-flight worker goroutines.
func (p *ProgressTracker) SetActiveWorkers(n int) {
	p.activeWorkers.Store(int32(n))
}

// IncLeads increments discovered engineering leads.
func (p *ProgressTracker) IncLeads(n int) {
	p.leadsFound.Add(int64(n))
}

// IncEmails increments verified outreach emails captured.
func (p *ProgressTracker) IncEmails(n int) {
	p.emailsCaptured.Add(int64(n))
}

// SetCurrentTarget updates the name/board of the target currently being scanned.
func (p *ProgressTracker) SetCurrentTarget(target string) {
	p.mu.Lock()
	p.currentTarget = target
	p.mu.Unlock()
}

// SetRateLimitStatus sets current throttle/rate-limit notice.
func (p *ProgressTracker) SetRateLimitStatus(status string) {
	p.mu.Lock()
	p.rateLimitStatus = status
	p.mu.Unlock()
}

// Log prints an informational log without tearing the live progress line.
func (p *ProgressTracker) Log(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Clear current progress line
	fmt.Fprintf(p.out, "\r\033[K")
	fmt.Fprintf(p.out, format+"\n", args...)
}

func (p *ProgressTracker) render() {
	p.mu.Lock()
	defer p.mu.Unlock()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memAllocMB := m.Alloc / 1024 / 1024

	crawled := p.targetsCrawled.Load()
	total := p.targetsTotal.Load()
	leads := p.leadsFound.Load()
	emails := p.emailsCaptured.Load()
	current := p.currentTarget
	if current == "" {
		current = "idle"
	}

	// Calculate RPS
	elapsedSec := time.Since(p.startTime).Seconds()
	rps := 0.0
	if elapsedSec > 0.2 {
		rps = float64(p.requestCount.Load()) / elapsedSec
	}

	ratio := fmt.Sprintf("%d", crawled)
	if total > 0 {
		ratio = fmt.Sprintf("%d/%d", crawled, total)
	}

	line := fmt.Sprintf(
		"\r\033[K[Specter] Targets: %s | Leads: %d | Emails: %d | RPS: %.1f | Mem: %dMB | Current: %s",
		ratio, leads, emails, rps, memAllocMB, current,
	)

	fmt.Fprint(p.out, line)
	p.lastLineLen = len(line)
}
