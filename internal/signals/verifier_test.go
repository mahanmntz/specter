package signals

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExtractEmailDomain(t *testing.T) {
	tests := []struct {
		email    string
		expected string
	}{
		{"alice@cockroachlabs.com", "cockroachlabs.com"},
		{"bob@sub.domain.org.", "sub.domain.org"},
		{"  charlie@Google.COM  ", "google.com"},
		{"invalid-email", ""},
		{"@nodomain.com", ""},
		{"noat.com", ""},
		{"user@bad domain.com", ""},
		{"user@nodot", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := ExtractEmailDomain(tt.email)
		if got != tt.expected {
			t.Errorf("ExtractEmailDomain(%q) = %q; expected %q", tt.email, got, tt.expected)
		}
	}
}

func TestVerifyEmailDomain_MockLookupAndCache(t *testing.T) {
	ClearMXCache()
	defer ClearMXCache()

	var lookupCount atomic.Int32

	// Setup mock resolver
	SetMXLookupFunc(func(ctx context.Context, domain string) ([]*net.MX, error) {
		lookupCount.Add(1)
		switch domain {
		case "validcorp.com":
			return []*net.MX{{Host: "mail.validcorp.com", Pref: 10}}, nil
		case "emptycorp.com":
			return []*net.MX{}, nil
		case "errorcorp.com":
			return nil, errors.New("dns failure")
		default:
			return nil, errors.New("unknown host")
		}
	})
	defer ResetMXLookupFunc()

	// 1. Valid domain
	if !VerifyEmailDomain("engineer@validcorp.com") {
		t.Errorf("expected validcorp.com to be verified")
	}
	if lookupCount.Load() != 1 {
		t.Errorf("expected exactly 1 lookup, got %d", lookupCount.Load())
	}

	// 2. Cache hit for validcorp.com - lookupCount should remain 1
	if !VerifyEmailDomain("other.engineer@validcorp.com") {
		t.Errorf("expected cached validcorp.com to be verified")
	}
	if lookupCount.Load() != 1 {
		t.Errorf("expected cache hit with 1 lookup, got %d", lookupCount.Load())
	}

	// 3. Domain with empty MX records
	if VerifyEmailDomain("user@emptycorp.com") {
		t.Errorf("expected emptycorp.com to fail verification")
	}
	if lookupCount.Load() != 2 {
		t.Errorf("expected 2 lookups, got %d", lookupCount.Load())
	}

	// 4. Cached negative result for emptycorp.com
	if VerifyEmailDomain("another@emptycorp.com") {
		t.Errorf("expected cached emptycorp.com to fail verification")
	}
	if lookupCount.Load() != 2 {
		t.Errorf("expected cached negative lookup with 2 lookups, got %d", lookupCount.Load())
	}

	// 5. Malformed email - should return false immediately without DNS lookup
	if VerifyEmailDomain("not-an-email") {
		t.Errorf("expected invalid email to fail")
	}
	if lookupCount.Load() != 2 {
		t.Errorf("expected no additional DNS lookup for malformed email, got %d", lookupCount.Load())
	}
}

func TestVerifyEmailDomain_TTL_Expiration(t *testing.T) {
	ClearMXCache()
	defer ClearMXCache()

	var lookupCount atomic.Int32
	SetMXLookupFunc(func(ctx context.Context, domain string) ([]*net.MX, error) {
		lookupCount.Add(1)
		return []*net.MX{{Host: "mail.shortttl.com", Pref: 10}}, nil
	})
	defer ResetMXLookupFunc()

	// Set very short TTL
	SetMXCacheTTL(50 * time.Millisecond)
	defer SetMXCacheTTL(1 * time.Hour)

	if !VerifyEmailDomain("user@shortttl.com") {
		t.Errorf("expected verified email")
	}
	if lookupCount.Load() != 1 {
		t.Errorf("expected 1 lookup, got %d", lookupCount.Load())
	}

	// Wait for TTL expiration
	time.Sleep(70 * time.Millisecond)

	if !VerifyEmailDomain("user@shortttl.com") {
		t.Errorf("expected verified email after TTL expiry")
	}
	if lookupCount.Load() != 2 {
		t.Errorf("expected 2 lookups after TTL expiry, got %d", lookupCount.Load())
	}
}

func TestVerifyEmailDomain_Concurrency(t *testing.T) {
	ClearMXCache()
	defer ClearMXCache()

	SetMXLookupFunc(func(ctx context.Context, domain string) ([]*net.MX, error) {
		time.Sleep(5 * time.Millisecond)
		return []*net.MX{{Host: "mail." + domain, Pref: 10}}, nil
	})
	defer ResetMXLookupFunc()

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			email := "user@concurrent.org"
			if !VerifyEmailDomain(email) {
				t.Errorf("concurrent lookup failed for %s", email)
			}
		}(i)
	}
	wg.Wait()
}
