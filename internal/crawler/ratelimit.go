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

// AdaptiveHostLimiter implements token-bucket rate limiting per host with jittered delays
// and automatic backoff when X-RateLimit-Remaining or Retry-After headers are received.
type AdaptiveHostLimiter struct {
	mu           sync.Mutex
	limiters     map[string]*rate.Limiter
	cooldowns    map[string]time.Time
	minJitter    time.Duration
	maxJitter    time.Duration
	defaultRPS   rate.Limit
	defaultBurst int
}

// NewAdaptiveHostLimiter creates a per-host token bucket limiter with adaptive jittered delay.
func NewAdaptiveHostLimiter(minJitter, maxJitter time.Duration) *AdaptiveHostLimiter {
	if minJitter <= 0 {
		minJitter = 500 * time.Millisecond
	}
	if maxJitter <= minJitter {
		maxJitter = 2000 * time.Millisecond
	}

	return &AdaptiveHostLimiter{
		limiters:     make(map[string]*rate.Limiter),
		cooldowns:    make(map[string]time.Time),
		minJitter:    minJitter,
		maxJitter:    maxJitter,
		defaultRPS:   rate.Limit(2.0), // 2 requests per second per host default
		defaultBurst: 4,
	}
}

// HostThrottler is an alias for backwards compatibility.
type HostThrottler = AdaptiveHostLimiter

// NewHostThrottler creates an AdaptiveHostLimiter.
func NewHostThrottler(delay time.Duration) *HostThrottler {
	maxDelay := delay * 2
	if maxDelay < 500*time.Millisecond {
		maxDelay = 1000 * time.Millisecond
	}
	return NewAdaptiveHostLimiter(delay, maxDelay)
}

// getLimiter returns or initializes the rate.Limiter for a given host.
func (a *AdaptiveHostLimiter) getLimiter(host string) *rate.Limiter {
	a.mu.Lock()
	defer a.mu.Unlock()

	lim, exists := a.limiters[host]
	if !exists {
		// GitHub API gets a more conservative rate limit if unauthenticated
		rps := a.defaultRPS
		burst := a.defaultBurst
		if strings.Contains(host, "github.com") {
			rps = rate.Limit(1.5)
			burst = 2
		}
		lim = rate.NewLimiter(rps, burst)
		a.limiters[host] = lim
	}
	return lim
}

// Wait blocks until the host's token bucket allows execution and any active backoff expires.
func (a *AdaptiveHostLimiter) Wait(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil
	}

	// 1. Check if host is currently cooling down due to 429 or low remaining quota
	a.mu.Lock()
	coolUntil, cooling := a.cooldowns[host]
	a.mu.Unlock()

	if cooling {
		if remaining := time.Until(coolUntil); remaining > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(remaining):
			}
		}
	}

	// 2. Wait on token bucket
	lim := a.getLimiter(host)
	if err := lim.Wait(ctx); err != nil {
		return err
	}

	// 3. Apply randomized jitter to prevent burst patterns
	jitterRange := a.maxJitter - a.minJitter
	var jitter time.Duration
	if jitterRange > 0 {
		jitter = a.minJitter + time.Duration(rand.Int64N(int64(jitterRange)))
	} else {
		jitter = a.minJitter
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(jitter):
		return nil
	}
}

// InspectHeaders checks HTTP response headers for rate-limit warnings and pauses workers if quota is low.
func (a *AdaptiveHostLimiter) InspectHeaders(rawURL string, header http.Header) {
	if header == nil {
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return
	}

	// 1. Check standard Retry-After header
	if retryAfter := header.Get("Retry-After"); retryAfter != "" {
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
			a.setCooldown(host, time.Duration(secs)*time.Second)
			return
		}
		if t, err := http.ParseTime(retryAfter); err == nil {
			if sleepDuration := time.Until(t); sleepDuration > 0 {
				a.setCooldown(host, sleepDuration)
				return
			}
		}
	}

	// 2. Check GitHub X-RateLimit headers
	remainingStr := header.Get("X-RateLimit-Remaining")
	resetStr := header.Get("X-RateLimit-Reset")

	if remainingStr != "" {
		if remaining, err := strconv.Atoi(remainingStr); err == nil {
			// If remaining quota is low (under 5 requests remaining), sleep briefly to pace requests
			if remaining <= 3 {
				sleepDur := 2 * time.Second
				if resetStr != "" {
					if resetUnix, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
						if waitTime := time.Until(time.Unix(resetUnix, 0)); waitTime > 0 {
							if waitTime < 5*time.Second {
								sleepDur = waitTime + time.Second
							} else {
								sleepDur = 5 * time.Second
							}
						}
					}
				}
				a.setCooldown(host, sleepDur)
			}
		}
	}
}

func (a *AdaptiveHostLimiter) setCooldown(host string, d time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cooldowns[host] = time.Now().Add(d)
}
