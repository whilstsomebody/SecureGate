package integration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
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
		t.Skipf("skipping real Redis integration test: Redis unreachable at %s: %v", addr, err)
	}
	return addr
}

func randomJTI() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func makeToken(secret, issuer, audience, role, jti string) string {
	claims := jwt.MapClaims{
		"user": "test-user",
		"role": role,
		"jti":  jti,
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iss":  issuer,
		"aud":  audience,
	}
	token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	return token
}

func TestEndToEndRealRedisRateLimiting(t *testing.T) {
	addr := skipIfNoRealRedis(t)
	metrics.RegisterMetrics()

	if err := ratelimit.InitRedis(context.Background(), addr); err != nil {
		t.Fatalf("InitRedis failed: %v", err)
	}
	defer ratelimit.Rdb.Close()

	uniqueIP := fmt.Sprintf("198.51.100.%d:12345", time.Now().UnixNano()%250+1)
	redisKey := fmt.Sprintf("rate:198.51.100.%d", time.Now().UnixNano()%250+1)
	defer ratelimit.Rdb.Del(context.Background(), redisKey)

	oldCfg := config.Cfg
	config.Cfg = &config.Config{
		RateLimitCount:  3,
		RateLimitWindow: 3 * time.Second,
	}
	defer func() { config.Cfg = oldCfg }()

	handler := middlewares.RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest("GET", "/users/hello", nil)
		req.RemoteAddr = uniqueIP
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: got status %d, want 200", i, rr.Code)
		}
	}

	// 4th request must be rate limited with 429, Retry-After header, and JSON error
	req := httptest.NewRequest("GET", "/users/hello", nil)
	req.RemoteAddr = uniqueIP
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("request 4: got status %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header on 429 response")
	}

	var errResp httpio.ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to decode JSON error: %v", err)
	}
	if errResp.Error.Code != "RATE_LIMIT_EXCEEDED" {
		t.Fatalf("unexpected error code: %s", errResp.Error.Code)
	}
}

func TestEndToEndRealRedisRevocation(t *testing.T) {
	addr := skipIfNoRealRedis(t)
	metrics.RegisterMetrics()

	if err := ratelimit.InitRedis(context.Background(), addr); err != nil {
		t.Fatalf("InitRedis failed: %v", err)
	}
	defer ratelimit.Rdb.Close()

	secret := "integration-test-secret"
	oldCfg := config.Cfg
	config.Cfg = &config.Config{
		JWTSecret:   secret,
		JWTIssuer:   "securegate-test",
		JWTAudience: "securegate-test-api",
	}
	defer func() { config.Cfg = oldCfg }()

	jti := randomJTI()
	revokedKey := fmt.Sprintf("revoked:%s", jti)
	defer ratelimit.Rdb.Del(context.Background(), revokedKey)

	token := makeToken(secret, "securegate-test", "securegate-test-api", "USER", jti)

	handler := middlewares.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("authorized"))
	}))

	// Initial request with valid unrevoked token
	req := httptest.NewRequest("GET", "/users/hello", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid token returned status %d, want 200", rr.Code)
	}

	// Revoke in real Redis
	if err := ratelimit.Rdb.Set(context.Background(), revokedKey, "1", time.Hour).Err(); err != nil {
		t.Fatalf("failed to set revocation in Redis: %v", err)
	}

	// Request with revoked token must return 401
	req = httptest.NewRequest("GET", "/users/hello", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token returned status %d, want 401", rr.Code)
	}

	var errResp httpio.ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to decode JSON error: %v", err)
	}
	if errResp.Error.Code != "TOKEN_REVOKED" {
		t.Fatalf("unexpected error code: %s", errResp.Error.Code)
	}
}
