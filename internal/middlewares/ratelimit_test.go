package middlewares

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/metrics"
)

func init() {
	metrics.RegisterMetrics()
	config.Cfg = &config.Config{RateLimitCount: 3, RateLimitWindow: 10 * time.Second}
}

func TestClientIPIgnoresUntrustedXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := clientIP(req); got != "192.0.2.10" {
		t.Errorf("expected socket peer, got %s", got)
	}
}

func TestClientIPIgnoresUntrustedXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("X-Real-IP", "9.10.11.12")
	if got := clientIP(req); got != "192.0.2.10" {
		t.Errorf("expected socket peer, got %s", got)
	}
}

func TestClientIPFallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:54321"
	if got := clientIP(req); got != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", got)
	}
}

func TestClientIPUsesTrustedProxyChain(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	req.RemoteAddr = "10.1.2.3:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.8, 192.0.2.2")
	cfg := &config.Config{TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.0/24")}}
	if got := clientIPFor(req, cfg); got != "198.51.100.8" {
		t.Fatalf("client IP = %q", got)
	}
}

func TestRateLimitMiddlewareAllows(t *testing.T) {
	// Replace allowFn with a stub that always permits.
	original := allowFn
	defer func() { allowFn = original }()
	allowFn = func(ctx context.Context, key string, limit int, window time.Duration) (bool, error) { return true, nil }

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := RateLimitMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/users/hello", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestRateLimitMiddlewareBlocks(t *testing.T) {
	original := allowFn
	defer func() { allowFn = original }()
	allowFn = func(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
		return false, nil
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := RateLimitMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/users/hello", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
}

func TestRateLimitMiddlewareWithTrustedProxyChain(t *testing.T) {
	original := allowFn
	defer func() { allowFn = original }()

	var lastKey string
	allowFn = func(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
		lastKey = key
		return true, nil
	}

	cfg := &config.Config{
		TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		RateLimitCount:    10,
		RateLimitWindow:   time.Minute,
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := RateLimitMiddlewareFor(inner, cfg, nil)

	// Trusted proxy with forwarded chain
	req := httptest.NewRequest(http.MethodGet, "/users/test", nil)
	req.RemoteAddr = "10.1.2.3:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.55")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if lastKey != "198.51.100.55" {
		t.Errorf("rate limit key = %q, want %q", lastKey, "198.51.100.55")
	}

	// Untrusted peer with spoofed header
	req = httptest.NewRequest(http.MethodGet, "/users/test", nil)
	req.RemoteAddr = "203.0.113.99:54321"
	req.Header.Set("X-Forwarded-For", "198.51.100.55")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if lastKey != "203.0.113.99" {
		t.Errorf("rate limit key = %q, want %q", lastKey, "203.0.113.99")
	}
}
