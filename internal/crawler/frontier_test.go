package crawler

import (
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestFrontierDeduplication(t *testing.T) {
	dbFile := "test_frontier.db"
	defer os.Remove(dbFile)

	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("failed opening sqlite: %v", err)
	}
	defer db.Close()

	frontier, err := NewFrontier(db)
	if err != nil {
		t.Fatalf("failed initializing frontier: %v", err)
	}

	target := "https://boards.greenhouse.io/stripe/jobs"

	// 1. Target should initially be crawled
	shouldCrawl, err := frontier.ShouldCrawl(target, 24*time.Hour)
	if err != nil {
		t.Fatalf("ShouldCrawl error: %v", err)
	}
	if !shouldCrawl {
		t.Errorf("expected ShouldCrawl=true for new target")
	}

	// 2. Mark visited successfully
	if err := frontier.MarkVisited(target, "ats_greenhouse", nil); err != nil {
		t.Fatalf("MarkVisited error: %v", err)
	}

	// 3. Target should now be skipped within TTL
	shouldCrawl, err = frontier.ShouldCrawl(target, 24*time.Hour)
	if err != nil {
		t.Fatalf("ShouldCrawl error: %v", err)
	}
	if shouldCrawl {
		t.Errorf("expected ShouldCrawl=false for visited target within TTL")
	}

	// 4. Test TTL expiration
	shouldCrawlExpired, err := frontier.ShouldCrawl(target, 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	shouldCrawlExpired, err = frontier.ShouldCrawl(target, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("ShouldCrawl error: %v", err)
	}
	if !shouldCrawlExpired {
		t.Errorf("expected ShouldCrawl=true when TTL is expired")
	}

	// 5. Test failed crawl marks allows retry
	targetFail := "https://api.lever.co/v0/postings/testfail"
	if err := frontier.MarkVisited(targetFail, "ats_lever", errors.New("500 internal server error")); err != nil {
		t.Fatalf("MarkVisited failed err: %v", err)
	}
	retryable, err := frontier.ShouldCrawl(targetFail, 24*time.Hour)
	if err != nil {
		t.Fatalf("ShouldCrawl error: %v", err)
	}
	if !retryable {
		t.Errorf("expected failed target to be retryable")
	}
}

func TestFrontierCacheWarmup(t *testing.T) {
	dbFile := "test_warmup.db"
	defer os.Remove(dbFile)

	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("failed opening sqlite: %v", err)
	}

	f1, err := NewFrontier(db)
	if err != nil {
		t.Fatalf("failed init f1: %v", err)
	}

	target := "https://jobs.ashbyhq.com/linear"
	if err := f1.MarkVisited(target, "ats_ashby", nil); err != nil {
		t.Fatalf("MarkVisited failed: %v", err)
	}
	db.Close()

	// Reopen DB with new Frontier instance to test cache warmup from DB
	db2, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("reopening sqlite failed: %v", err)
	}
	defer db2.Close()

	f2, err := NewFrontier(db2)
	if err != nil {
		t.Fatalf("failed init f2: %v", err)
	}

	should, err := f2.ShouldCrawl(target, 24*time.Hour)
	if err != nil {
		t.Fatalf("ShouldCrawl on f2 failed: %v", err)
	}
	if should {
		t.Errorf("expected cached warmup to prevent duplicate crawl")
	}
}

func TestFrontier_ConditionalHeadersAnd304(t *testing.T) {
	dbFile := "test_conditional.db"
	defer os.Remove(dbFile)

	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("failed opening sqlite: %v", err)
	}
	defer db.Close()

	f, err := NewFrontier(db)
	if err != nil {
		t.Fatalf("failed init frontier: %v", err)
	}

	target := "https://api.github.com/repos/cockroachdb/cockroach"
	etagVal := `"abc123etag"`
	lastModVal := "Wed, 21 Oct 2026 07:28:00 GMT"

	// 1. Initial lookup should be empty
	etag, lm, err := f.GetConditionalHeaders(target)
	if err != nil {
		t.Fatalf("GetConditionalHeaders failed: %v", err)
	}
	if etag != "" || lm != "" {
		t.Fatalf("expected empty headers, got etag=%q, lm=%q", etag, lm)
	}

	// 2. Mark visited with headers
	if err := f.MarkVisitedWithHeaders(target, "github_repo", nil, etagVal, lastModVal); err != nil {
		t.Fatalf("MarkVisitedWithHeaders failed: %v", err)
	}

	// 3. Lookup should now return cached conditional headers
	etag, lm, err = f.GetConditionalHeaders(target)
	if err != nil {
		t.Fatalf("GetConditionalHeaders failed: %v", err)
	}
	if etag != etagVal || lm != lastModVal {
		t.Fatalf("expected etag=%s, lm=%s; got etag=%s, lm=%s", etagVal, lastModVal, etag, lm)
	}

	// 4. Mark 304 Not Modified
	time.Sleep(10 * time.Millisecond)
	if err := f.MarkNotModified(target); err != nil {
		t.Fatalf("MarkNotModified failed: %v", err)
	}

	// Headers should still be preserved
	etag, lm, err = f.GetConditionalHeaders(target)
	if err != nil {
		t.Fatalf("GetConditionalHeaders failed: %v", err)
	}
	if etag != etagVal || lm != lastModVal {
		t.Fatalf("headers should persist after 304, got etag=%s, lm=%s", etag, lm)
	}
}

