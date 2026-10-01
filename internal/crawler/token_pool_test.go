package crawler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

func TestTokenPool_RoundRobin(t *testing.T) {
	tokens := []string{"ghp_token1", "ghp_token2", "ghp_token3"}
	pool := NewTokenPool(tokens)

	if pool.Count() != 3 {
		t.Fatalf("expected 3 tokens, got %d", pool.Count())
	}

	ctx := context.Background()
	seen := make(map[string]int)
	for i := 0; i < 6; i++ {
		tok, err := pool.AcquireToken(ctx)
		if err != nil {
			t.Fatalf("unexpected error acquiring token: %v", err)
		}
		seen[tok]++
	}

	for _, tok := range tokens {
		if seen[tok] != 2 {
			t.Errorf("token %s expected 2 acquires, got %d", tok, seen[tok])
		}
	}
}

func TestTokenPool_CoolingThreshold(t *testing.T) {
	pool := NewTokenPool([]string{"ghp_healthy", "ghp_exhausted"})
	ctx := context.Background()

	// Exhaust second token
	pool.UpdateQuota("ghp_exhausted", 10, time.Now().Add(500*time.Millisecond))

	// Acquire multiple times: should only yield ghp_healthy
	for i := 0; i < 4; i++ {
		tok, err := pool.AcquireToken(ctx)
		if err != nil {
			t.Fatalf("failed acquiring token: %v", err)
		}
		if tok != "ghp_healthy" {
			t.Fatalf("expected ghp_healthy, got %s (cooling token should be bypassed)", tok)
		}
	}

	// Verify states
	states := pool.GetTokenStates()
	for _, s := range states {
		if s.Token == "ghp_exhausted" && !s.IsCooling {
			t.Errorf("expected ghp_exhausted to be cooling")
		}
	}
}

func TestTokenPool_DynamicWaitUntilReset(t *testing.T) {
	pool := NewTokenPool([]string{"ghp_single"})
	ctx := context.Background()

	resetDuration := 150 * time.Millisecond
	resetAt := time.Now().Add(resetDuration)
	pool.UpdateQuota("ghp_single", 5, resetAt)

	start := time.Now()
	tok, err := pool.AcquireToken(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	elapsed := time.Since(start)
	if tok != "ghp_single" {
		t.Fatalf("expected ghp_single, got %s", tok)
	}
	if elapsed < 120*time.Millisecond {
		t.Fatalf("expected to wait at least ~120ms for cooling to expire, waited only %v", elapsed)
	}
}

func TestTokenPool_ContextCancellation(t *testing.T) {
	pool := NewTokenPool([]string{"ghp_cooling"})
	pool.UpdateQuota("ghp_cooling", 0, time.Now().Add(5*time.Second))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := pool.AcquireToken(ctx)
	if err == nil {
		t.Fatalf("expected error due to context cancellation, got nil")
	}
}

func TestTokenPool_InspectHeaders(t *testing.T) {
	pool := NewTokenPool([]string{"ghp_test"})

	header := make(http.Header)
	header.Set("X-RateLimit-Remaining", "18")
	nowSec := time.Now().Add(2 * time.Minute).Unix()
	header.Set("X-RateLimit-Reset", fmt.Sprintf("%d", nowSec))

	pool.InspectHeaders("ghp_test", header)

	states := pool.GetTokenStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 token state, got %d", len(states))
	}

	s := states[0]
	if s.Remaining != 18 {
		t.Errorf("expected remaining 18, got %d", s.Remaining)
	}
	if !s.IsCooling {
		t.Errorf("remaining 18 < 25 should set IsCooling to true")
	}
}

func TestTokenPool_EnvLoading(t *testing.T) {
	os.Setenv("GITHUB_TOKENS", "ghp_env1, ghp_env2 , ghp_env3")
	defer os.Unsetenv("GITHUB_TOKENS")

	pool := NewTokenPoolFromEnv()
	if pool.Count() != 3 {
		t.Fatalf("expected 3 tokens from GITHUB_TOKENS, got %d", pool.Count())
	}
}

func TestTokenPool_EmptyPool(t *testing.T) {
	pool := NewTokenPool(nil)
	tok, err := pool.AcquireToken(context.Background())
	if err != nil {
		t.Fatalf("empty pool should return nil error, got %v", err)
	}
	if tok != "" {
		t.Fatalf("empty pool should return empty token, got %q", tok)
	}
}

func TestTokenPool_ConcurrentAccess(t *testing.T) {
	tokens := []string{"ghp_1", "ghp_2", "ghp_3", "ghp_4"}
	pool := NewTokenPool(tokens)

	var wg sync.WaitGroup
	ctx := context.Background()

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				tok, err := pool.AcquireToken(ctx)
				if err != nil {
					t.Errorf("worker %d failed acquire: %v", workerID, err)
					return
				}
				if tok == "" {
					t.Errorf("worker %d got empty token", workerID)
					return
				}
				// Simulate periodic header update
				if j%10 == 0 {
					pool.UpdateQuota(tok, 1000-j, time.Time{})
				}
			}
		}(i)
	}

	wg.Wait()
}
