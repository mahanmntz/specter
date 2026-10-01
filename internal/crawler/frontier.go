package crawler

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// FrontierItem represents a single recorded crawl entity in the persistent state.
type FrontierItem struct {
	ID            int64     `json:"id"`
	TargetURL     string    `json:"target_url"`
	Domain        string    `json:"domain"`
	EntityType    string    `json:"entity_type"`
	Status        string    `json:"status"`
	LastCrawledAt time.Time `json:"last_crawled_at"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	ETag          string    `json:"etag,omitempty"`
	LastModified  string    `json:"last_modified,omitempty"`
}

type frontierCacheEntry struct {
	lastCrawledAt time.Time
	etag          string
	lastModified  string
}

// Frontier provides persistent state tracking and deduplication backed by SQLite
// and an in-memory thread-safe TTL cache.
type Frontier struct {
	db    *sql.DB
	cache sync.Map // map[string]any (normalized key -> *frontierCacheEntry or time.Time)
	mu    sync.RWMutex
}

// NewFrontier initializes the persistent frontier and runs schema migration for crawl_frontier.
func NewFrontier(db *sql.DB) (*Frontier, error) {
	schema := `
	CREATE TABLE IF NOT EXISTS crawl_frontier (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		target_url TEXT UNIQUE NOT NULL,
		domain TEXT NOT NULL,
		entity_type TEXT NOT NULL,
		status TEXT NOT NULL,
		last_crawled_at DATETIME NOT NULL,
		error_message TEXT,
		etag TEXT DEFAULT '',
		last_modified TEXT DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS idx_frontier_domain ON crawl_frontier(domain);
	CREATE INDEX IF NOT EXISTS idx_frontier_url ON crawl_frontier(target_url);
	CREATE INDEX IF NOT EXISTS idx_frontier_status ON crawl_frontier(status);
	`
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("failed creating crawl_frontier table: %w", err)
	}

	// Runtime migrations for existing databases
	_, _ = db.Exec("ALTER TABLE crawl_frontier ADD COLUMN etag TEXT DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE crawl_frontier ADD COLUMN last_modified TEXT DEFAULT ''")

	f := &Frontier{
		db: db,
	}

	// Warm cache with recently visited targets (last 30 days)
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	rows, err := db.Query(`
		SELECT target_url, domain, last_crawled_at, COALESCE(etag, ''), COALESCE(last_modified, '')
		FROM crawl_frontier 
		WHERE status = 'success' AND last_crawled_at >= ?
	`, cutoff)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tURL, dom, etag, lastMod string
			var lastCrawled time.Time
			if err := rows.Scan(&tURL, &dom, &lastCrawled, &etag, &lastMod); err == nil {
				f.cache.Store(normalizeFrontierKey(tURL), &frontierCacheEntry{
					lastCrawledAt: lastCrawled,
					etag:          etag,
					lastModified:  lastMod,
				})
				if dom != "" {
					f.cache.Store("domain:"+strings.ToLower(dom), lastCrawled)
				}
			}
		}
	}

	return f, nil
}

// ShouldCrawl checks whether a URL has been visited within the last ttl duration.
// Returns true if the target should be crawled (not visited or TTL expired).
func (f *Frontier) ShouldCrawl(rawURL string, ttl time.Duration) (bool, error) {
	if rawURL == "" {
		return false, nil
	}

	normKey := normalizeFrontierKey(rawURL)

	// 1. Check in-memory sync.Map cache
	if val, ok := f.cache.Load(normKey); ok {
		var lastCrawled time.Time
		switch v := val.(type) {
		case time.Time:
			lastCrawled = v
		case *frontierCacheEntry:
			lastCrawled = v.lastCrawledAt
		}
		if !lastCrawled.IsZero() && ttl > 0 && time.Since(lastCrawled) < ttl {
			return false, nil // Recently visited, skip
		}
	}

	// 2. Query SQLite store for source of truth
	var lastCrawled time.Time
	var status string
	err := f.db.QueryRow(`
		SELECT last_crawled_at, status 
		FROM crawl_frontier 
		WHERE target_url = ?
	`, rawURL).Scan(&lastCrawled, &status)

	if err == sql.ErrNoRows {
		return true, nil // Never visited
	}
	if err != nil {
		return true, fmt.Errorf("error querying crawl_frontier: %w", err)
	}

	// If previous crawl failed, allow retry
	if status == "failed" {
		return true, nil
	}

	// Check TTL
	if ttl > 0 && time.Since(lastCrawled) < ttl {
		f.cache.Store(normKey, lastCrawled)
		return false, nil
	}

	return true, nil
}

// ShouldCrawlDomain checks if a company domain as a whole has been visited within ttl.
func (f *Frontier) ShouldCrawlDomain(domain string, ttl time.Duration) (bool, error) {
	if domain == "" {
		return true, nil
	}
	domKey := "domain:" + strings.ToLower(domain)
	if val, ok := f.cache.Load(domKey); ok {
		if lastCrawled, ok := val.(time.Time); ok {
			if ttl > 0 && time.Since(lastCrawled) < ttl {
				return false, nil
			}
		}
	}

	var lastCrawled time.Time
	err := f.db.QueryRow(`
		SELECT MAX(last_crawled_at) 
		FROM crawl_frontier 
		WHERE domain = ? AND status = 'success'
	`, domain).Scan(&lastCrawled)

	if err == sql.ErrNoRows || lastCrawled.IsZero() {
		return true, nil
	}
	if err != nil {
		return true, fmt.Errorf("error checking domain frontier: %w", err)
	}

	if ttl > 0 && time.Since(lastCrawled) < ttl {
		f.cache.Store(domKey, lastCrawled)
		return false, nil
	}

	return true, nil
}

// GetConditionalHeaders retrieves any stored ETag and Last-Modified headers for a URL.
func (f *Frontier) GetConditionalHeaders(rawURL string) (string, string, error) {
	if rawURL == "" {
		return "", "", nil
	}

	normKey := normalizeFrontierKey(rawURL)
	if val, ok := f.cache.Load(normKey); ok {
		if entry, ok := val.(*frontierCacheEntry); ok {
			return entry.etag, entry.lastModified, nil
		}
	}

	var etag, lastModified string
	err := f.db.QueryRow(`
		SELECT COALESCE(etag, ''), COALESCE(last_modified, '')
		FROM crawl_frontier
		WHERE target_url = ?
	`, rawURL).Scan(&etag, &lastModified)

	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return etag, lastModified, err
}

// MarkVisited records the crawl result in SQLite and updates the in-memory cache.
func (f *Frontier) MarkVisited(rawURL, entityType string, crawlErr error) error {
	return f.MarkVisitedWithHeaders(rawURL, entityType, crawlErr, "", "")
}

// MarkVisitedWithHeaders records the crawl result along with HTTP ETag and Last-Modified headers.
func (f *Frontier) MarkVisitedWithHeaders(rawURL, entityType string, crawlErr error, etag, lastModified string) error {
	if rawURL == "" {
		return nil
	}

	dom := extractDomain(rawURL)
	status := "success"
	errMsg := ""
	if crawlErr != nil {
		status = "failed"
		errMsg = crawlErr.Error()
	}

	now := time.Now().UTC()

	query := `
	INSERT INTO crawl_frontier (target_url, domain, entity_type, status, last_crawled_at, error_message, etag, last_modified)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(target_url) DO UPDATE SET
		domain = excluded.domain,
		entity_type = excluded.entity_type,
		status = excluded.status,
		last_crawled_at = excluded.last_crawled_at,
		error_message = excluded.error_message,
		etag = CASE WHEN excluded.etag != '' THEN excluded.etag ELSE crawl_frontier.etag END,
		last_modified = CASE WHEN excluded.last_modified != '' THEN excluded.last_modified ELSE crawl_frontier.last_modified END;
	`
	_, err := f.db.Exec(query, rawURL, dom, entityType, status, now, errMsg, etag, lastModified)
	if err != nil {
		return fmt.Errorf("failed updating crawl_frontier: %w", err)
	}

	if status == "success" {
		normKey := normalizeFrontierKey(rawURL)
		f.cache.Store(normKey, &frontierCacheEntry{
			lastCrawledAt: now,
			etag:          etag,
			lastModified:  lastModified,
		})
		if dom != "" {
			f.cache.Store("domain:"+strings.ToLower(dom), now)
		}
	}

	return nil
}

// MarkNotModified updates last_crawled_at on an HTTP 304 Not Modified response
// without invalidating or altering stored ETag and Last-Modified headers.
func (f *Frontier) MarkNotModified(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	now := time.Now().UTC()
	_, err := f.db.Exec(`
		UPDATE crawl_frontier 
		SET last_crawled_at = ?, status = 'success', error_message = ''
		WHERE target_url = ?
	`, now, rawURL)
	if err != nil {
		return fmt.Errorf("failed updating crawl_frontier on 304: %w", err)
	}

	normKey := normalizeFrontierKey(rawURL)
	if val, ok := f.cache.Load(normKey); ok {
		if entry, ok := val.(*frontierCacheEntry); ok {
			entry.lastCrawledAt = now
			f.cache.Store(normKey, entry)
		} else {
			f.cache.Store(normKey, now)
		}
	} else {
		f.cache.Store(normKey, now)
	}

	dom := extractDomain(rawURL)
	if dom != "" {
		f.cache.Store("domain:"+strings.ToLower(dom), now)
	}
	return nil
}

// GetStats returns count of visited URLs and domains in frontier.
func (f *Frontier) GetStats(ctx context.Context) (total int, successful int, failed int, err error) {
	row := f.db.QueryRowContext(ctx, `
		SELECT 
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0)
		FROM crawl_frontier
	`)
	err = row.Scan(&total, &successful, &failed)
	return
}

func normalizeFrontierKey(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil {
		return strings.ToLower(rawURL)
	}
	host := strings.ToLower(u.Hostname())
	path := strings.TrimRight(u.Path, "/")
	return host + path
}

func extractDomain(rawURL string) string {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
