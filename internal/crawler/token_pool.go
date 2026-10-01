package crawler

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TokenState tracks the quota, health, and cooling status of a GitHub personal access token.
type TokenState struct {
	Token     string    `json:"token"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"reset_at"`
	IsCooling bool      `json:"is_cooling"`
}

// TokenPool manages a thread-safe round-robin pool of GitHub tokens with quota tracking,
// cooling triggers for low remaining calls (< 25), and dynamic waiting until quota reset.
type TokenPool struct {
	mu      sync.Mutex
	tokens  []*TokenState
	nextIdx int
}

// NewTokenPool creates a TokenPool from a slice of token strings.
func NewTokenPool(rawTokens []string) *TokenPool {
	var states []*TokenState
	seen := make(map[string]bool)

	for _, rt := range rawTokens {
		token := strings.TrimSpace(rt)
		token = strings.TrimPrefix(token, "Bearer ")
		token = strings.TrimPrefix(token, "token ")
		token = strings.TrimSpace(token)
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		states = append(states, &TokenState{
			Token:     token,
			Remaining: 5000, // Standard default for authenticated GitHub API
			ResetAt:   time.Time{},
			IsCooling: false,
		})
	}

	return &TokenPool{
		tokens:  states,
		nextIdx: 0,
	}
}

// NewTokenPoolFromEnv initializes a TokenPool loading comma-separated tokens from
// GITHUB_TOKENS, falling back to GITHUB_TOKEN if empty.
func NewTokenPoolFromEnv() *TokenPool {
	raw := os.Getenv("GITHUB_TOKENS")
	if strings.TrimSpace(raw) == "" {
		raw = os.Getenv("GITHUB_TOKEN")
	}
	if strings.TrimSpace(raw) == "" {
		return NewTokenPool(nil)
	}

	parts := strings.Split(raw, ",")
	return NewTokenPool(parts)
}

// Count returns the number of tokens managed by the pool.
func (p *TokenPool) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.tokens)
}

// GetTokenStates returns a copy of current token health states.
func (p *TokenPool) GetTokenStates() []TokenState {
	p.mu.Lock()
	defer p.mu.Unlock()

	res := make([]TokenState, len(p.tokens))
	for i, s := range p.tokens {
		res[i] = *s
	}
	return res
}

// AcquireToken selects the next available non-cooling token using round-robin.
// If a token's remaining quota is < 25, it is marked cooling until ResetAt.
// If all tokens are cooling, it dynamically blocks until the earliest ResetAt or context cancellation.
// Returns an empty string and nil error if the pool has zero tokens configured.
func (p *TokenPool) AcquireToken(ctx context.Context) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		p.mu.Lock()
		n := len(p.tokens)
		if n == 0 {
			p.mu.Unlock()
			return "", nil
		}

		now := time.Now()
		var earliestReset time.Time
		foundIdx := -1

		// 1. Check tokens in round-robin sequence
		for i := 0; i < n; i++ {
			idx := (p.nextIdx + i) % n
			t := p.tokens[idx]

			// If token was cooling but reset time has passed, unfreeze it
			if t.IsCooling && !t.ResetAt.IsZero() && now.After(t.ResetAt) {
				t.IsCooling = false
				t.Remaining = 5000
				t.ResetAt = time.Time{}
			}

			// Cooling check: remaining < 25 triggers cooling
			if t.Remaining < 25 {
				t.IsCooling = true
				if t.ResetAt.IsZero() || now.After(t.ResetAt) {
					t.ResetAt = now.Add(1 * time.Minute)
				}
			}

			if !t.IsCooling {
				foundIdx = idx
				break
			}

			// Track earliest reset time among cooling tokens
			if earliestReset.IsZero() || (!t.ResetAt.IsZero() && t.ResetAt.Before(earliestReset)) {
				earliestReset = t.ResetAt
			}
		}

		if foundIdx != -1 {
			p.nextIdx = (foundIdx + 1) % n
			token := p.tokens[foundIdx].Token
			p.mu.Unlock()
			return token, nil
		}

		// 2. All tokens are cooling: calculate dynamic wait duration
		waitDuration := time.Until(earliestReset)
		if waitDuration <= 0 {
			waitDuration = 200 * time.Millisecond
		}
		p.mu.Unlock()

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(waitDuration):
			// Woke up after earliest reset; retry acquisition
		}
	}
}

// InspectHeaders parses X-RateLimit-Remaining and X-RateLimit-Reset headers from a GitHub API response
// and updates the corresponding token state in the pool.
func (p *TokenPool) InspectHeaders(token string, headers http.Header) {
	if headers == nil || token == "" {
		return
	}

	remStr := headers.Get("X-RateLimit-Remaining")
	resetStr := headers.Get("X-RateLimit-Reset")
	if remStr == "" && resetStr == "" {
		return
	}

	var remaining = -1
	if remStr != "" {
		if r, err := strconv.Atoi(remStr); err == nil {
			remaining = r
		}
	}

	var resetAt time.Time
	if resetStr != "" {
		if rSec, err := strconv.ParseInt(resetStr, 10, 64); err == nil && rSec > 0 {
			resetAt = time.Unix(rSec, 0)
		}
	}

	p.UpdateQuota(token, remaining, resetAt)
}

// UpdateQuota explicitly synchronizes the remaining quota and reset timestamp for a specific token.
func (p *TokenPool) UpdateQuota(token string, remaining int, resetAt time.Time) {
	token = strings.TrimSpace(token)
	token = strings.TrimPrefix(token, "Bearer ")
	token = strings.TrimPrefix(token, "token ")
	token = strings.TrimSpace(token)

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, s := range p.tokens {
		if s.Token == token {
			if remaining >= 0 {
				s.Remaining = remaining
			}
			if !resetAt.IsZero() {
				s.ResetAt = resetAt
			}
			// Automatic cooling trigger: Remaining < 25
			if s.Remaining < 25 {
				s.IsCooling = true
				if s.ResetAt.IsZero() || time.Now().After(s.ResetAt) {
					s.ResetAt = time.Now().Add(1 * time.Minute)
				}
			} else {
				s.IsCooling = false
			}
			break
		}
	}
}
