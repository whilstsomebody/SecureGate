package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
	"github.com/whilstsomebody/securegate/internal/version"
)

type unreadableBody struct{ t *testing.T }

func (b unreadableBody) Read([]byte) (int, error) {
	b.t.Error("retired endpoint read request body")
	return 0, errors.New("must not read")
}
func (b unreadableBody) Close() error { return nil }
func TestRevocationEndpointRetired(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT"} {
		req := httptest.NewRequest(method, "/auth/revoke", nil)
		req.Body = unreadableBody{t}
		rr := httptest.NewRecorder()
		newHandler().ServeHTTP(rr, req)
		if rr.Code != 404 {
			t.Fatalf("%s: got %d", method, rr.Code)
		}
	}
}

func TestManagementIsolationAndLiveness(t *testing.T) {
	original := ratelimit.Rdb
	ratelimit.Rdb = nil
	t.Cleanup(func() { ratelimit.Rdb = original })
	for _, path := range []string{"/health", "/ready", "/metrics"} {
		rr := httptest.NewRecorder()
		newHandler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != 404 {
			t.Fatalf("public %s: %d", path, rr.Code)
		}
	}
	handler := newManagementHandler()
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/health", 200}, {"GET", "/ready", 503}, {"GET", "/metrics", 200}, {"POST", "/health", 405}, {"GET", "/users", 404},
	} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
		if rr.Code != tc.status {
			t.Fatalf("%s %s: got %d want %d", tc.method, tc.path, rr.Code, tc.status)
		}
	}
}

func TestVersionAndCheckConfig(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	cfg, err := config.Check()
	if err != nil {
		t.Fatalf("expected valid check: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	info := version.Info()
	if !strings.Contains(info, "SecureGate") {
		t.Fatalf("version info invalid: %s", info)
	}
}

func TestPolicyFreeGatewayForwardsWithoutRedisOrAuthorization(t *testing.T) {
	cfg := &config.Config{AuthEnabled: false, RateLimitEnabled: false, Limits: config.DefaultLimits()}
	called := false
	handler := newGatewayHandler(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/users/hello", nil))
	if rr.Code != http.StatusNoContent || !called {
		t.Fatalf("status=%d forwarded=%t", rr.Code, called)
	}

	rr = httptest.NewRecorder()
	newManagementHandlerFor(cfg).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"redis":"disabled"`) {
		t.Fatalf("policy-free readiness = %d %q", rr.Code, rr.Body.String())
	}
}

func TestPolicyFreeGatewayEndToEndWithRealUpstream(t *testing.T) {
	receivedHeader := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"upstream":"handled"}`))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		AuthEnabled:         false,
		RateLimitEnabled:    false,
		UserServiceAddr:     upstream.URL,
		AdminServiceAddr:    upstream.URL,
		PaymentsServiceAddr: upstream.URL,
		Limits:              config.DefaultLimits(),
	}

	proxyHandler, err := newProxyHandler(cfg)
	if err != nil {
		t.Fatalf("newProxyHandler failed: %v", err)
	}

	gateway := newGatewayHandler(cfg, proxyHandler)

	// 1. Unauthenticated request forwards directly and succeeds without requiring tokens or Redis
	req := httptest.NewRequest(http.MethodGet, "/users/profile", nil)
	rr := httptest.NewRecorder()
	gateway.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"upstream":"handled"`) {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}

	// 2. Request with arbitrary/invalid auth header is not blocked or rejected by gateway
	req = httptest.NewRequest(http.MethodGet, "/users/profile", nil)
	req.Header.Set("Authorization", "Bearer invalid-or-arbitrary-token")
	rr = httptest.NewRecorder()
	gateway.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if receivedHeader != "Bearer invalid-or-arbitrary-token" {
		t.Fatalf("expected authorization header forwarded, got: %q", receivedHeader)
	}

	// 3. Burst requests are not rate limited
	for i := 0; i < 10; i++ {
		req = httptest.NewRequest(http.MethodGet, "/users/profile", nil)
		rr = httptest.NewRecorder()
		gateway.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("burst request %d rejected with %d", i+1, rr.Code)
		}
	}

	// 4. Management readiness reports Redis disabled
	rr = httptest.NewRecorder()
	newManagementHandlerFor(cfg).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"redis":"disabled"`) {
		t.Fatalf("expected redis disabled in readiness, got %d %s", rr.Code, rr.Body.String())
	}
}
