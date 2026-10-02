package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrBlockedAddress indicates SSRF protection prevented connection to a private/internal IP.
	ErrBlockedAddress = errors.New("connection blocked: address is not a routable public IP")
)

// guardDial prevents SSRF attacks by blocking requests to loopback, private RFC1918, link-local, and multicast IPs.
func guardDial(network, address string, c syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}

	ip := net.ParseIP(host)
	if ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("%w: %s", ErrBlockedAddress, ip.String())
		}
		return nil
	}

	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, address)
	}
	if !isPublicAddr(addrPort.Addr()) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, addrPort.Addr())
	}
	return nil
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	return true
}

func isPublicAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	return true
}

// FetcherConfig controls HTTP behavior, timeouts, body ceilings, and rate limits.
type FetcherConfig struct {
	UserAgent            string
	Timeout              time.Duration
	MaxBodyBytes         int64
	AllowPrivateNetworks bool
	TokenPool            *TokenPool
	Pacer                *Pacer
	Frontier             *Frontier
}

// Fetcher provides high-performance, SSRF-guarded HTTP requests with connection pooling,
// adaptive pacing with jitter, token pool rotation, and ETag conditional caching.
type Fetcher struct {
	client        *http.Client
	userAgent     string
	maxBodyBytes  int64
	limiter       *AdaptiveHostLimiter
	pacer         *Pacer
	tokenPool     *TokenPool
	frontier      *Frontier
	OnRequestDone func(statusCode int)
}

// NewFetcher creates a production-ready Fetcher.
func NewFetcher(cfg FetcherConfig) *Fetcher {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if !cfg.AllowPrivateNetworks {
		dialer.Control = guardDial
	}

	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   30,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    false,
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			return nil
		},
	}

	maxBody := cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 5 * 1024 * 1024 // 5 MB default
	}

	ua := cfg.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (compatible; SpecterRecon/1.0; +https://github.com/specter-recon)"
	}

	pacer := cfg.Pacer
	if pacer == nil {
		pacer = NewPacer(300*time.Millisecond, 1200*time.Millisecond)
	}

	tp := cfg.TokenPool
	if tp == nil {
		tp = NewTokenPoolFromEnv()
	}

	return &Fetcher{
		client:       client,
		userAgent:    ua,
		maxBodyBytes: maxBody,
		limiter:      NewAdaptiveHostLimiter(500*time.Millisecond, 2000*time.Millisecond),
		pacer:        pacer,
		tokenPool:    tp,
		frontier:     cfg.Frontier,
	}
}

// Client returns the underlying http.Client.
func (f *Fetcher) Client() *http.Client {
	return f.client
}

// SetLimiter assigns a custom AdaptiveHostLimiter.
func (f *Fetcher) SetLimiter(l *AdaptiveHostLimiter) {
	f.limiter = l
}

// Limiter returns the assigned AdaptiveHostLimiter.
func (f *Fetcher) Limiter() *AdaptiveHostLimiter {
	return f.limiter
}

// SetPacer assigns a custom Pacer.
func (f *Fetcher) SetPacer(p *Pacer) {
	f.pacer = p
}

// Pacer returns the assigned Pacer.
func (f *Fetcher) Pacer() *Pacer {
	return f.pacer
}

// SetTokenPool assigns a GitHub TokenPool.
func (f *Fetcher) SetTokenPool(tp *TokenPool) {
	f.tokenPool = tp
}

// TokenPool returns the assigned TokenPool.
func (f *Fetcher) TokenPool() *TokenPool {
	return f.tokenPool
}

// SetFrontier assigns a persistent Frontier for conditional ETag caching.
func (f *Fetcher) SetFrontier(frontier *Frontier) {
	f.frontier = frontier
}

// Frontier returns the assigned Frontier.
func (f *Fetcher) Frontier() *Frontier {
	return f.frontier
}

// FetchResult represents an executed HTTP request result.
type FetchResult struct {
	EffectiveURL string
	StatusCode   int
	ContentType  string
	Body         []byte
	Headers      http.Header
	DurationMs   int64
	RetryAfter   time.Duration
	NotModified  bool
	ETag         string
	LastModified string
}

// Fetch performs an HTTP GET request with timeouts and body limits.
func (f *Fetcher) Fetch(ctx context.Context, targetURL string) (*FetchResult, error) {
	return f.FetchWithHeaders(ctx, targetURL, nil)
}

// FetchWithHeaders performs an HTTP GET with optional custom headers, automatic token rotation,
// adaptive pacing with jitter, and conditional request injection (If-None-Match / If-Modified-Since).
func (f *Fetcher) FetchWithHeaders(ctx context.Context, targetURL string, headers map[string]string) (*FetchResult, error) {
	// 1. Adaptive Pacing & Jitter
	if f.pacer != nil {
		if err := f.pacer.Wait(ctx, targetURL); err != nil {
			return nil, err
		}
	} else if f.limiter != nil {
		if err := f.limiter.Wait(ctx, targetURL); err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/json,text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	// 2. GitHub Token Pool Acquisition
	var usedToken string
	isGitHub := strings.Contains(targetURL, "github.com")

	if isGitHub {
		authHeader := ""
		if headers != nil {
			authHeader = headers["Authorization"]
		}
		if authHeader == "" && f.tokenPool != nil && f.tokenPool.Count() > 0 {
			token, tErr := f.tokenPool.AcquireToken(ctx)
			if tErr != nil {
				return nil, fmt.Errorf("failed to acquire github token: %w", tErr)
			}
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
				usedToken = token
			}
		} else if authHeader != "" {
			usedToken = strings.TrimPrefix(authHeader, "Bearer ")
			usedToken = strings.TrimPrefix(usedToken, "token ")
			usedToken = strings.TrimSpace(usedToken)
		}
	}

	// 3. Conditional Request Injection (ETag / If-Modified-Since)
	if f.frontier != nil {
		hasConditional := false
		if headers != nil {
			if _, ok := headers["If-None-Match"]; ok {
				hasConditional = true
			}
			if _, ok := headers["If-Modified-Since"]; ok {
				hasConditional = true
			}
		}
		if !hasConditional {
			if etag, lastMod, _ := f.frontier.GetConditionalHeaders(targetURL); etag != "" || lastMod != "" {
				if etag != "" {
					req.Header.Set("If-None-Match", etag)
				}
				if lastMod != "" {
					req.Header.Set("If-Modified-Since", lastMod)
				}
			}
		}
	}

	// 4. Apply custom headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if f.OnRequestDone != nil {
		f.OnRequestDone(resp.StatusCode)
	}

	// 5. Inspect response in Pacer, Limiter, and TokenPool
	if f.pacer != nil {
		f.pacer.InspectResponse(targetURL, resp.StatusCode, resp.Header)
	}
	if f.limiter != nil {
		f.limiter.InspectHeaders(targetURL, resp.Header)
	}
	if usedToken != "" && f.tokenPool != nil {
		f.tokenPool.InspectHeaders(usedToken, resp.Header)
	}

	duration := time.Since(start).Milliseconds()
	etag := resp.Header.Get("ETag")
	lastMod := resp.Header.Get("Last-Modified")

	// 6. Handle HTTP 304 Not Modified
	if resp.StatusCode == http.StatusNotModified {
		if f.frontier != nil {
			_ = f.frontier.MarkNotModified(targetURL)
		}
		return &FetchResult{
			EffectiveURL: resp.Request.URL.String(),
			StatusCode:   http.StatusNotModified,
			ContentType:  resp.Header.Get("Content-Type"),
			Body:         nil, // Skip reading body on 304
			Headers:      resp.Header,
			DurationMs:   duration,
			RetryAfter:   parseRetryAfter(resp.Header.Get("Retry-After")),
			NotModified:  true,
			ETag:         etag,
			LastModified: lastMod,
		}, nil
	}

	// 7. Read response body
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed reading body: %w", err)
	}

	// Update cached conditional headers in Frontier on 2xx
	if f.frontier != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if etag != "" || lastMod != "" {
			_ = f.frontier.MarkVisitedWithHeaders(targetURL, "page", nil, etag, lastMod)
		}
	}

	return &FetchResult{
		EffectiveURL: resp.Request.URL.String(),
		StatusCode:   resp.StatusCode,
		ContentType:  resp.Header.Get("Content-Type"),
		Body:         bodyBytes,
		Headers:      resp.Header,
		DurationMs:   duration,
		RetryAfter:   parseRetryAfter(resp.Header.Get("Retry-After")),
		NotModified:  false,
		ETag:         etag,
		LastModified: lastMod,
	}, nil
}

// IsHTML checks whether the response is HTML.
func IsHTML(contentType string) bool {
	if contentType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return time.Until(at)
	}
	return 0
}
