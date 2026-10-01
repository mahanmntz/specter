package crawler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestFetcher_TokenPoolInjectionAndQuota(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("X-RateLimit-Remaining", "42")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(10*time.Minute).Unix()))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	tp := NewTokenPool([]string{"ghp_test123"})
	pacer := NewPacer(5*time.Millisecond, 15*time.Millisecond)

	fetcher := NewFetcher(FetcherConfig{
		AllowPrivateNetworks: true,
		TokenPool:            tp,
		Pacer:                pacer,
	})

	// Inject target as mock github endpoint by passing custom Host or directly
	// Let's test with URL that contains github.com
	githubURL := server.URL + "/repos/test/repo?mock=github.com"
	res, err := fetcher.Fetch(context.Background(), githubURL)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	if receivedAuth != "Bearer ghp_test123" {
		t.Errorf("expected Authorization Bearer ghp_test123, got %q", receivedAuth)
	}

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", res.StatusCode)
	}

	// Verify TokenPool quota was updated
	states := tp.GetTokenStates()
	if len(states) != 1 || states[0].Remaining != 42 {
		t.Errorf("expected token remaining to update to 42, got %v", states)
	}
}

func TestFetcher_ConditionalRequests_304(t *testing.T) {
	dbFile := "test_fetcher_304.db"
	defer os.Remove(dbFile)

	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("failed init sqlite: %v", err)
	}
	defer db.Close()

	frontier, err := NewFrontier(db)
	if err != nil {
		t.Fatalf("failed init frontier: %v", err)
	}

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		ifNoneMatch := r.Header.Get("If-None-Match")
		if ifNoneMatch == `"v1.0.0"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.Header().Set("ETag", `"v1.0.0"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":"payload"}`))
	}))
	defer server.Close()

	pacer := NewPacer(5*time.Millisecond, 10*time.Millisecond)
	fetcher := NewFetcher(FetcherConfig{
		AllowPrivateNetworks: true,
		Frontier:             frontier,
		Pacer:                pacer,
	})

	ctx := context.Background()

	// First fetch: should be 200 OK and cache ETag
	res1, err := fetcher.Fetch(ctx, server.URL)
	if err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	if res1.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", res1.StatusCode)
	}
	if string(res1.Body) != `{"data":"payload"}` {
		t.Fatalf("unexpected body: %s", string(res1.Body))
	}
	if res1.ETag != `"v1.0.0"` {
		t.Errorf("expected ETag v1.0.0, got %s", res1.ETag)
	}

	// Verify Frontier stored the ETag
	storedETag, _, err := frontier.GetConditionalHeaders(server.URL)
	if err != nil || storedETag != `"v1.0.0"` {
		t.Fatalf("frontier should have cached etag, got %q, err: %v", storedETag, err)
	}

	// Second fetch: should inject If-None-Match and receive 304 Not Modified with zero body bytes
	res2, err := fetcher.Fetch(ctx, server.URL)
	if err != nil {
		t.Fatalf("second fetch failed: %v", err)
	}
	if res2.StatusCode != http.StatusNotModified {
		t.Fatalf("expected 304 Not Modified, got %d", res2.StatusCode)
	}
	if !res2.NotModified {
		t.Errorf("expected NotModified=true")
	}
	if len(res2.Body) != 0 {
		t.Errorf("expected empty body on 304, got %d bytes", len(res2.Body))
	}
}

func TestFetcher_CircuitBreaker_429Integration(t *testing.T) {
	requestsReceived := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsReceived++
		if requestsReceived == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`rate limited`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`ok`))
	}))
	defer server.Close()

	pacer := NewPacer(5*time.Millisecond, 10*time.Millisecond)
	fetcher := NewFetcher(FetcherConfig{
		AllowPrivateNetworks: true,
		Pacer:                pacer,
	})

	ctx := context.Background()

	// 1. First fetch triggers 429
	res1, err := fetcher.Fetch(ctx, server.URL)
	if err != nil {
		t.Fatalf("fetch 1 failed: %v", err)
	}
	if res1.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", res1.StatusCode)
	}

	// Verify circuit breaker opened
	if state := pacer.GetCircuitState(server.URL); state != StateOpen {
		t.Errorf("expected circuit breaker StateOpen after 429, got %v", state)
	}
}
