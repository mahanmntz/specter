package crawler

import (
	"context"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// CircuitState represents the circuit breaker state for a host.
type CircuitState int

const (
	// StateClosed allows normal request traffic.
	StateClosed CircuitState = iota
	// StateOpen halts all requests for the host during exponential backoff.
	StateOpen
	// StateHalfOpen allows a single canary request to test host recovery.
	StateHalfOpen
)

type hostState struct {
	mu                  sync.Mutex
	limiter             *rate.Limiter
	circuitState        CircuitState
	coolingUntil        time.Time
	consecutiveBackoffs int
	canaryInFlight      bool
	notifyChan          chan struct{}
}

func newHostState(limiter *rate.Limiter) *hostState {
	return &hostState{
		limiter:      limiter,
		circuitState: StateClosed,
		notifyChan:   make(chan struct{}),
	}
}

func (h *hostState) broadcast() {
	close(h.notifyChan)
	h.notifyChan = make(chan struct{})
}

// Pacer manages adaptive per-host token bucket rate limiting, random jitter timing,
// and circuit breaking for 429/403 responses with exponential backoff and canary tests.
type Pacer struct {
	mu           sync.RWMutex
	hosts        map[string]*hostState
	minJitter    time.Duration
	maxJitter    time.Duration
	defaultRPS   rate.Limit
	defaultBurst int
}

// NewPacer initializes an adaptive pacer with configurable jitter boundaries.
// Defaults: 300ms to 1200ms jitter, 2.0 req/s ATS/generic, 1.2 req/s api.github.com.
func NewPacer(minJitter, maxJitter time.Duration) *Pacer {
	if minJitter <= 0 {
		minJitter = 300 * time.Millisecond
	}
	if maxJitter <= minJitter {
		maxJitter = 1200 * time.Millisecond
	}

	return &Pacer{
		hosts:        make(map[string]*hostState),
		minJitter:    minJitter,
		maxJitter:    maxJitter,
		defaultRPS:   rate.Limit(2.0),
		defaultBurst: 4,
	}
}

func (p *Pacer) getHostState(rawURLOrHost string) *hostState {
	host := extractHost(rawURLOrHost)

	p.mu.RLock()
	hs, exists := p.hosts[host]
	p.mu.RUnlock()
	if exists {
		return hs
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if hs, exists = p.hosts[host]; exists {
		return hs
	}

	// Host-specific rate rules
	rps := p.defaultRPS
	burst := p.defaultBurst

	if strings.Contains(host, "github.com") {
		// api.github.com capped at 1.2 req/s to avoid abuse triggers
		rps = rate.Limit(1.2)
		burst = 2
	} else if isATS(host) {
		// ATS endpoints capped at 2.0 req/s
		rps = rate.Limit(2.0)
		burst = 4
	}

	limiter := rate.NewLimiter(rps, burst)
	hs = newHostState(limiter)
	p.hosts[host] = hs
	return hs
}

// Wait blocks until the host rate limiter allows a token, circuit breaker permits execution,
// and jitter delay elapses. Fully respects ctx.Done() for immediate graceful cancellation.
func (p *Pacer) Wait(ctx context.Context, rawURL string) error {
	hs := p.getHostState(rawURL)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		hs.mu.Lock()
		now := time.Now()

		// 1. If StateOpen: check if backoff cooling period has elapsed
		if hs.circuitState == StateOpen {
			if now.After(hs.coolingUntil) {
				// Transition to StateHalfOpen for canary probing
				hs.circuitState = StateHalfOpen
				hs.canaryInFlight = false
			} else {
				waitDur := time.Until(hs.coolingUntil)
				ch := hs.notifyChan
				hs.mu.Unlock()

				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(waitDur):
				case <-ch:
				}
				continue
			}
		}

		// 2. If StateHalfOpen: allow exactly ONE canary request through
		if hs.circuitState == StateHalfOpen {
			if hs.canaryInFlight {
				// Another worker is executing the canary request; wait for outcome
				ch := hs.notifyChan
				hs.mu.Unlock()

				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ch:
				}
				continue
			}

			// Claim the canary slot
			hs.canaryInFlight = true
			lim := hs.limiter
			hs.mu.Unlock()

			if err := lim.Wait(ctx); err != nil {
				// Abort canary on cancellation
				hs.mu.Lock()
				hs.canaryInFlight = false
				hs.broadcast()
				hs.mu.Unlock()
				return err
			}
			return p.applyJitter(ctx)
		}

		// 3. StateClosed: normal traffic
		lim := hs.limiter
		hs.mu.Unlock()

		if err := lim.Wait(ctx); err != nil {
			return err
		}
		return p.applyJitter(ctx)
	}
}

// InspectResponse inspects the HTTP status code and response headers.
// Automatically triggers circuit breaker backoff on 429 or 403 rate-limit warnings,
// or recovers to StateClosed on successful responses.
func (p *Pacer) InspectResponse(rawURL string, statusCode int, headers http.Header) {
	hs := p.getHostState(rawURL)

	// Check for rate limiting
	isRateLimit := false
	var retryAfter time.Duration

	if statusCode == http.StatusTooManyRequests {
		isRateLimit = true
	} else if statusCode == http.StatusForbidden {
		// GitHub returns 403 for primary and secondary rate limits
		if headers != nil {
			if headers.Get("X-RateLimit-Remaining") == "0" || headers.Get("Retry-After") != "" {
				isRateLimit = true
			}
		}
	}

	if isRateLimit {
		if headers != nil {
			retryAfter = parseRetryAfterHeader(headers)
		}
		hs.recordRateLimit(retryAfter)
		return
	}

	// 2xx or 304 success
	if (statusCode >= 200 && statusCode < 400) || statusCode == http.StatusNotModified {
		hs.recordSuccess()
	}
}

func (h *hostState) recordSuccess() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.circuitState == StateHalfOpen {
		// Canary succeeded! Restore normal operations
		h.circuitState = StateClosed
		h.consecutiveBackoffs = 0
		h.canaryInFlight = false
		h.broadcast()
	} else if h.circuitState == StateClosed {
		h.consecutiveBackoffs = 0
	}
}

func (h *hostState) recordRateLimit(retryAfter time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.circuitState = StateOpen
	h.canaryInFlight = false

	var backoff time.Duration
	if retryAfter > 0 {
		backoff = retryAfter
		// Cap rate limit backoff to 15 seconds max to keep the crawl moving
		if backoff > 15*time.Second {
			backoff = 15 * time.Second
		}
	} else {
		// Truncated exponential backoff: 2^n * base_delay + jitter, capped at 15s
		n := h.consecutiveBackoffs
		if n > 3 {
			n = 3
		}
		baseDelay := 1500 * time.Millisecond
		mult := time.Duration(1 << n)
		jitter := time.Duration(rand.Int64N(int64(500 * time.Millisecond)))
		backoff = mult*baseDelay + jitter
		if backoff > 15*time.Second {
			backoff = 15 * time.Second
		}
		h.consecutiveBackoffs++
	}

	h.coolingUntil = time.Now().Add(backoff)
	h.broadcast()
}

// GetCircuitState returns the current CircuitState for a given host (for testing and observability).
func (p *Pacer) GetCircuitState(rawURLOrHost string) CircuitState {
	hs := p.getHostState(rawURLOrHost)
	hs.mu.Lock()
	defer hs.mu.Unlock()
	return hs.circuitState
}

// SetCooldown manually sets a cooldown duration for a host (e.g. testing or explicit header).
func (p *Pacer) SetCooldown(rawURLOrHost string, d time.Duration) {
	hs := p.getHostState(rawURLOrHost)
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.circuitState = StateOpen
	hs.coolingUntil = time.Now().Add(d)
	hs.broadcast()
}

func (p *Pacer) applyJitter(ctx context.Context) error {
	jitterRange := p.maxJitter - p.minJitter
	var jitter time.Duration
	if jitterRange > 0 {
		jitter = p.minJitter + time.Duration(rand.Int64N(int64(jitterRange)))
	} else {
		jitter = p.minJitter
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(jitter):
		return nil
	}
}

func extractHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(raw)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return strings.ToLower(raw)
	}
	return host
}

func isATS(host string) bool {
	return strings.Contains(host, "greenhouse.io") ||
		strings.Contains(host, "lever.co") ||
		strings.Contains(host, "ashbyhq.com") ||
		strings.Contains(host, "workable.com") ||
		strings.Contains(host, "smartrecruiters.com")
}

func parseRetryAfterHeader(headers http.Header) time.Duration {
	val := headers.Get("Retry-After")
	if val == "" {
		// Check X-RateLimit-Reset
		if resetStr := headers.Get("X-RateLimit-Reset"); resetStr != "" {
			if resetUnix, err := strconv.ParseInt(resetStr, 10, 64); err == nil && resetUnix > 0 {
				dur := time.Until(time.Unix(resetUnix, 0))
				if dur > 0 {
					return dur
				}
			}
		}
		return 0
	}

	if secs, err := strconv.Atoi(val); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(val); err == nil {
		if dur := time.Until(t); dur > 0 {
			return dur
		}
	}
	return 0
}
