package crawler

import (
	"context"
	"testing"
	"time"
)

func TestInScope(t *testing.T) {
	cases := []struct {
		host      string
		scopeHost string
		want      bool
	}{
		{"stripe.com", "stripe.com", true},
		{"www.stripe.com", "stripe.com", true},
		{"jobs.stripe.com", "stripe.com", true},
		{"api.stripe.com", "www.stripe.com", true},
		{"evil-stripe.com", "stripe.com", false},
		{"stripe.com.attacker.com", "stripe.com", false},
		{"google.com", "stripe.com", false},
	}

	for _, c := range cases {
		got := InScope(c.host, c.scopeHost)
		if got != c.want {
			t.Errorf("InScope(%q, %q) = %v, want %v", c.host, c.scopeHost, got, c.want)
		}
	}
}

func TestHostThrottler(t *testing.T) {
	throttler := NewHostThrottler(50 * time.Millisecond)
	ctx := context.Background()

	start := time.Now()
	if err := throttler.Wait(ctx, "https://api.github.com/repos"); err != nil {
		t.Fatalf("first wait failed: %v", err)
	}

	if err := throttler.Wait(ctx, "https://api.github.com/orgs"); err != nil {
		t.Fatalf("second wait failed: %v", err)
	}
	duration := time.Since(start)

	if duration < 45*time.Millisecond {
		t.Errorf("expected throttle delay of at least 45ms, got %v", duration)
	}
}
