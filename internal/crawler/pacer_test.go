package crawler

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestPacer_JitterRange(t *testing.T) {
	p := NewPacer(50*time.Millisecond, 150*time.Millisecond)
	ctx := context.Background()

	start := time.Now()
	err := p.Wait(ctx, "https://api.github.com/users/octocat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	elapsed := time.Since(start)
	if elapsed < 45*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Fatalf("expected elapsed time between ~50ms and ~200ms, got %v", elapsed)
	}
}

func TestPacer_CircuitBreaker_429Recovery(t *testing.T) {
	p := NewPacer(10*time.Millisecond, 20*time.Millisecond)
	target := "https://api.github.com/orgs/cockroachdb"

	if state := p.GetCircuitState(target); state != StateClosed {
		t.Fatalf("expected StateClosed, got %v", state)
	}

	// 1. Simulate 429 Too Many Requests with short cooldown
	p.SetCooldown(target, 100*time.Millisecond)
	if state := p.GetCircuitState(target); state != StateOpen {
		t.Fatalf("expected StateOpen after cooldown set, got %v", state)
	}

	// 2. Wait until cooldown expires; next Wait() should become canary
	time.Sleep(120 * time.Millisecond)

	ctx := context.Background()
	var wg sync.WaitGroup
	var canarySuccess bool
	var secondWorkerSuccess bool

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := p.Wait(ctx, target)
		if err != nil {
			t.Errorf("canary worker failed: %v", err)
			return
		}
		canarySuccess = true
		// Simulate successful canary request response
		p.InspectResponse(target, http.StatusOK, nil)
	}()

	// Second worker concurrent request
	time.Sleep(5 * time.Millisecond)
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := p.Wait(ctx, target)
		if err != nil {
			t.Errorf("second worker failed: %v", err)
			return
		}
		secondWorkerSuccess = true
	}()

	wg.Wait()

	if !canarySuccess || !secondWorkerSuccess {
		t.Fatalf("expected both workers to succeed after canary recovery, got canary=%v, second=%v",
			canarySuccess, secondWorkerSuccess)
	}

	if state := p.GetCircuitState(target); state != StateClosed {
		t.Fatalf("expected StateClosed after canary success, got %v", state)
	}
}

func TestPacer_CircuitBreaker_403RateLimit(t *testing.T) {
	p := NewPacer(10*time.Millisecond, 20*time.Millisecond)
	target := "https://api.github.com/repos/cockroachdb/cockroach"

	headers := make(http.Header)
	headers.Set("X-RateLimit-Remaining", "0")
	headers.Set("Retry-After", "1") // 1 second

	p.InspectResponse(target, http.StatusForbidden, headers)

	if state := p.GetCircuitState(target); state != StateOpen {
		t.Fatalf("expected StateOpen on 403 with X-RateLimit-Remaining: 0, got %v", state)
	}
}

func TestPacer_ContextCancellation(t *testing.T) {
	p := NewPacer(500*time.Millisecond, 1000*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.Wait(ctx, "https://boards-api.greenhouse.io/v1/boards/stripe/jobs")
	if err == nil {
		t.Fatalf("expected context timeout error, got nil")
	}
}

func TestPacer_ConcurrentLoad(t *testing.T) {
	p := NewPacer(10*time.Millisecond, 30*time.Millisecond)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			url := "https://api.ashbyhq.com/posting-api/job-board/figma"
			if id%2 == 0 {
				url = "https://api.github.com/orgs/figma"
			}
			for j := 0; j < 5; j++ {
				_ = p.Wait(ctx, url)
				p.InspectResponse(url, http.StatusOK, nil)
			}
		}(i)
	}
	wg.Wait()
}
