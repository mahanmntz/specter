package signals

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

type mxCacheItem struct {
	valid     bool
	expiresAt time.Time
}

var (
	mxCacheMu   sync.Mutex
	mxCache     sync.Map
	mxCacheTTL  = 1 * time.Hour
	mxLookupNet = func(ctx context.Context, domain string) ([]*net.MX, error) {
		r := &net.Resolver{}
		return r.LookupMX(ctx, domain)
	}
)

// SetMXLookupFunc allows overriding MX resolver for testing or custom resolvers.
func SetMXLookupFunc(fn func(ctx context.Context, domain string) ([]*net.MX, error)) {
	mxCacheMu.Lock()
	defer mxCacheMu.Unlock()
	mxLookupNet = fn
}

// ResetMXLookupFunc resets the MX resolver to standard net.LookupMX with context.
func ResetMXLookupFunc() {
	mxCacheMu.Lock()
	defer mxCacheMu.Unlock()
	mxLookupNet = func(ctx context.Context, domain string) ([]*net.MX, error) {
		r := &net.Resolver{}
		return r.LookupMX(ctx, domain)
	}
}

// SetMXCacheTTL overrides the in-memory cache TTL for MX records.
func SetMXCacheTTL(ttl time.Duration) {
	mxCacheMu.Lock()
	defer mxCacheMu.Unlock()
	mxCacheTTL = ttl
}

// ClearMXCache empties the in-memory DNS MX cache.
func ClearMXCache() {
	mxCache.Range(func(key, value any) bool {
		mxCache.Delete(key)
		return true
	})
}

// ExtractEmailDomain extracts and normalizes the host domain from an email address.
func ExtractEmailDomain(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return ""
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return ""
	}
	domain := strings.ToLower(strings.TrimSpace(parts[1]))
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" || !strings.Contains(domain, ".") {
		return ""
	}
	// Verify no illegal characters in domain name
	if strings.ContainsAny(domain, " \t\r\n/\\:;<>[](),\"") {
		return ""
	}
	return domain
}

// VerifyEmailDomain validates whether the given email address has valid DNS MX records.
// In-memory sync.Map caching avoids redundant DNS queries for frequently encountered domains.
func VerifyEmailDomain(email string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return VerifyEmailDomainContext(ctx, email)
}

// VerifyEmailDomainContext validates email domain MX records with a caller-provided context.
func VerifyEmailDomainContext(ctx context.Context, email string) bool {
	domain := ExtractEmailDomain(email)
	if domain == "" {
		return false
	}

	// 1. Check in-memory sync.Map cache
	if val, ok := mxCache.Load(domain); ok {
		if item, validCast := val.(mxCacheItem); validCast {
			if time.Now().Before(item.expiresAt) {
				return item.valid
			}
		}
	}

	// 2. Perform DNS MX lookup
	mxCacheMu.Lock()
	lookupFn := mxLookupNet
	ttl := mxCacheTTL
	mxCacheMu.Unlock()

	records, err := lookupFn(ctx, domain)
	valid := (err == nil && len(records) > 0)

	// 3. Cache result
	mxCache.Store(domain, mxCacheItem{
		valid:     valid,
		expiresAt: time.Now().Add(ttl),
	})

	return valid
}
