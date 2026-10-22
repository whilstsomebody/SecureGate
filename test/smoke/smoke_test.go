package smoke

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/httpio"
	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/middlewares"
	"github.com/whilstsomebody/securegate/internal/proxy"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
)

func skipIfNoRealRedis(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 300 * time.Millisecond})
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("skipping operational smoke test: Redis unreachable at %s: %v", addr, err)
	}
	return addr
}

func makeToken(secret, issuer, audience, role, jti string, expired bool) string {
	exp := time.Now().Add(time.Hour).Unix()
	if expired {
		exp = time.Now().Add(-time.Hour).Unix()
	}
	claims := jwt.MapClaims{
		"user": "smoke-tester",
		"role": role,
		"jti":  jti,
		"exp":  exp,
		"iss":  issuer,
		"aud":  audience,
	}
	token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	return token
}

func randomJTI() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func TestOperationalSmoke(t *testing.T) {
	redisAddr := skipIfNoRealRedis(t)

	// 1. Start mock sample backend services
	userService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hello" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "hello for user service.")
			return
		}
		http.NotFound(w, r)
	}))
	defer userService.Close()

	adminService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dashboard" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "Welcome Admin!")
			return
		}
		http.NotFound(w, r)
	}))
	defer adminService.Close()

	paymentsService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checkout" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
			return
		}
		if r.URL.Path == "/failing" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "error")
			return
		}
		http.NotFound(w, r)
	}))
	defer paymentsService.Close()

	// 2. Configure Gateway
	secret := "smoke-test-secret-key-12345"
	issuer := "smoke-gate"
	audience := "smoke-api"

	oldCfg := config.Cfg
	cfg := &config.Config{
		JWTSecret:           secret,
		JWTIssuer:           issuer,
		JWTAudience:         audience,
		RedisAddr:           redisAddr,
		UserServiceAddr:     userService.URL,
		AdminServiceAddr:    adminService.URL,
		PaymentsServiceAddr: paymentsService.URL,
		RateLimitCount:      5,
		RateLimitWindow:     10 * time.Second,
		Limits:              config.DefaultLimits(),
	}
	config.Cfg = cfg
	defer func() { config.Cfg = oldCfg }()

	// 3. Initialize Redis & Metrics
	if err := ratelimit.InitRedis(context.Background(), redisAddr); err != nil {
		t.Fatalf("InitRedis failed: %v", err)
	}
	defer ratelimit.Rdb.Close()
	metrics.RegisterMetrics()
	proxy.ConfigureTransport(cfg.Limits)

	// Build Gateway handler stack
	secureHandler := middlewares.RequestIDMiddleware(
		middlewares.MetricsMiddleware(
			middlewares.RecoveryMiddleware(
				middlewares.RateLimitMiddleware(
					middlewares.AuthMiddleware(
						proxy.NewProxyhandler(),
					),
				),
			),
		),
	)

	publicMux := http.NewServeMux()
	for _, path := range []string{"/metrics", "/health", "/ready"} {
		publicMux.HandleFunc(path, http.NotFound)
	}
	publicMux.HandleFunc("/auth/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		http.NotFound(w, r)
	})
	publicMux.Handle("/", secureHandler)

	gatewayServer := httptest.NewServer(publicMux)
	defer gatewayServer.Close()

	client := gatewayServer.Client()

	// 4. Public endpoint isolation verification
	for _, path := range []string{"/health", "/ready", "/metrics", "/auth/revoke"} {
		resp, err := client.Get(gatewayServer.URL + path)
		if err != nil {
			t.Fatalf("GET %s error: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("public %s: expected 404, got %d", path, resp.StatusCode)
		}
	}

	resetRateLimit := func() {
		if ratelimit.Rdb != nil {
			ratelimit.Rdb.Del(context.Background(), "rate:127.0.0.1")
		}
	}

	// 5. Authentication checks
	t.Run("Auth_MissingToken", func(t *testing.T) {
		resetRateLimit()
		resp, err := client.Get(gatewayServer.URL + "/users/hello")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
		var errResp httpio.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Error.Code != "MISSING_AUTHORIZATION" {
			t.Fatalf("expected MISSING_AUTHORIZATION, got %s", errResp.Error.Code)
		}
	})

	t.Run("Auth_ExpiredToken", func(t *testing.T) {
		resetRateLimit()
		expiredToken := makeToken(secret, issuer, audience, "USER", randomJTI(), true)
		req, _ := http.NewRequest("GET", gatewayServer.URL+"/users/hello", nil)
		req.Header.Set("Authorization", "Bearer "+expiredToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
		var errResp httpio.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Error.Code != "INVALID_OR_EXPIRED_TOKEN" {
			t.Fatalf("expected INVALID_OR_EXPIRED_TOKEN, got %s", errResp.Error.Code)
		}
	})

	// 6. Authorized USER Token Call
	userToken := makeToken(secret, issuer, audience, "USER", randomJTI(), false)
	t.Run("Auth_ValidUserToken", func(t *testing.T) {
		resetRateLimit()
		req, _ := http.NewRequest("GET", gatewayServer.URL+"/users/hello", nil)
		req.Header.Set("Authorization", "Bearer "+userToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "hello for user service." {
			t.Fatalf("unexpected body: %s", body)
		}
		if resp.Header.Get("X-Request-ID") == "" {
			t.Fatal("expected X-Request-ID header in response")
		}
	})

	// 7. RBAC Role Enforcement
	t.Run("RBAC_ForbiddenRole", func(t *testing.T) {
		resetRateLimit()
		req, _ := http.NewRequest("GET", gatewayServer.URL+"/admin/dashboard", nil)
		req.Header.Set("Authorization", "Bearer "+userToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", resp.StatusCode)
		}
		var errResp httpio.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Error.Code != "FORBIDDEN" {
			t.Fatalf("expected FORBIDDEN, got %s", errResp.Error.Code)
		}
	})

	adminToken := makeToken(secret, issuer, audience, "ADMIN", randomJTI(), false)
	t.Run("RBAC_AdminAccess", func(t *testing.T) {
		resetRateLimit()
		req, _ := http.NewRequest("GET", gatewayServer.URL+"/admin/dashboard", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "Welcome Admin!" {
			t.Fatalf("unexpected body: %s", body)
		}
	})

	// 8. Token Revocation
	t.Run("Auth_Revocation", func(t *testing.T) {
		resetRateLimit()
		revokableJTI := randomJTI()
		tokenToRevoke := makeToken(secret, issuer, audience, "USER", revokableJTI, false)
		revokedKey := fmt.Sprintf("revoked:%s", revokableJTI)
		defer ratelimit.Rdb.Del(context.Background(), revokedKey)

		// First call succeeds
		req, _ := http.NewRequest("GET", gatewayServer.URL+"/users/hello", nil)
		req.Header.Set("Authorization", "Bearer "+tokenToRevoke)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		// Revoke in Redis
		if err := ratelimit.Rdb.Set(context.Background(), revokedKey, "1", time.Hour).Err(); err != nil {
			t.Fatalf("failed to revoke in Redis: %v", err)
		}

		// Second call rejected with 401
		req, _ = http.NewRequest("GET", gatewayServer.URL+"/users/hello", nil)
		req.Header.Set("Authorization", "Bearer "+tokenToRevoke)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 for revoked token, got %d", resp.StatusCode)
		}
		var errResp httpio.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Error.Code != "TOKEN_REVOKED" {
			t.Fatalf("expected TOKEN_REVOKED, got %s", errResp.Error.Code)
		}
	})

	// 9. Rate Limiting Burst
	t.Run("RateLimiting_BurstRejection", func(t *testing.T) {
		resetRateLimit()
		// Perform 5 allowed requests
		for i := 1; i <= 5; i++ {
			req, _ := http.NewRequest("GET", gatewayServer.URL+"/users/hello", nil)
			req.Header.Set("Authorization", "Bearer "+userToken)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("burst %d: expected 200, got %d", i, resp.StatusCode)
			}
		}

		// 6th request is rejected
		req, _ := http.NewRequest("GET", gatewayServer.URL+"/users/hello", nil)
		req.Header.Set("Authorization", "Bearer "+userToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("expected 429, got %d", resp.StatusCode)
		}
		if resp.Header.Get("Retry-After") == "" {
			t.Fatal("expected Retry-After header on 429 response")
		}
		var errResp httpio.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Error.Code != "RATE_LIMIT_EXCEEDED" {
			t.Fatalf("expected RATE_LIMIT_EXCEEDED, got %s", errResp.Error.Code)
		}
	})

	// 10. Circuit Breaker Open on Upstream Failures
	t.Run("CircuitBreaker_OpensOnFailures", func(t *testing.T) {
		resetRateLimit()
		// 5 failures on payments service will trip the breaker
		for i := 1; i <= 5; i++ {
			req, _ := http.NewRequest("GET", gatewayServer.URL+"/payments/failing", nil)
			req.Header.Set("Authorization", "Bearer "+adminToken)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
		}

		// Reset rate limit so 6th request is evaluated by circuit breaker, not rate limiter
		resetRateLimit()

		// 6th request must be short-circuited with 503
		req, _ := http.NewRequest("GET", gatewayServer.URL+"/payments/checkout", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 for tripped breaker, got %d", resp.StatusCode)
		}
		if resp.Header.Get("Retry-After") != "30" {
			t.Fatalf("expected Retry-After: 30, got %s", resp.Header.Get("Retry-After"))
		}
		var errResp httpio.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Error.Code != "SERVICE_UNAVAILABLE" {
			t.Fatalf("expected SERVICE_UNAVAILABLE, got %s", errResp.Error.Code)
		}
	})
}
