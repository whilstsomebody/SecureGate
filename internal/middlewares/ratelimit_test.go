package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/metrics"
)

func init() {
	metrics.RegisterMetrics()
	config.Cfg = &config.Config{RateLimitCount: 3, RateLimitWindow: 10 * time.Second}
}

func TestClientIPFromXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := clientIP(req); got != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", got)
	}
}

func TestClientIPFromXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", "9.10.11.12")
	if got := clientIP(req); got != "9.10.11.12" {
		t.Errorf("expected 9.10.11.12, got %s", got)
	}
}

func TestClientIPFallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:54321"
	if got := clientIP(req); got != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", got)
	}
}

func TestRateLimitMiddlewareAllows(t *testing.T) {
	// Replace allowFn with a stub that always permits.
	original := allowFn
	defer func() { allowFn = original }()
	allowFn = func(key string, limit int, window time.Duration) (bool, error) { return true, nil }

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
	allowFn = func(key string, limit int, window time.Duration) (bool, error) { return false, nil }

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := RateLimitMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/users/hello", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
}
