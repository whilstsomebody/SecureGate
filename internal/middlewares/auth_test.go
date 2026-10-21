package middlewares

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
)

const testSecret = "test-secret-key"

func TestMain(m *testing.M) {
	os.Setenv("JWT_SECRET", testSecret)
	config.Cfg = &config.Config{
		JWTSecret:       testSecret,
		RateLimitCount:  5,
		RateLimitWindow: 10 * time.Second,
	}
	ratelimit.Rdb = testRevocationClient(nil)
	metrics.RegisterMetrics()
	os.Exit(m.Run())
}

func makeToken(role string, expired bool) string {
	claims := jwt.MapClaims{
		"user": "testuser",
		"role": role,
		"jti":  "test-jti-001",
	}
	if expired {
		claims["exp"] = time.Now().Add(-time.Hour).Unix()
	} else {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
	}
	token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	return token
}

func TestIsAuthorized(t *testing.T) {
	tests := []struct {
		path string
		role string
		want bool
	}{
		{"/users/hello", "USER", true},
		{"/users/hello", "ADMIN", false},
		{"/admin/dashboard", "ADMIN", true},
		{"/admin/dashboard", "USER", false},
		{"/payments/checkout", "ADMIN", true},
		{"/payments/checkout", "USER", false},
		{"/unknown", "ADMIN", false},
	}
	for _, tt := range tests {
		got := isAuthorized(tt.path, tt.role)
		if got != tt.want {
			t.Errorf("isAuthorized(%q, %q) = %v, want %v", tt.path, tt.role, got, tt.want)
		}
	}
}

func TestAuthMiddlewareMissingHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := AuthMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/users/hello", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddlewareInvalidFormat(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := AuthMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/users/hello", nil)
	req.Header.Set("Authorization", "Token abc123")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddlewareExpiredToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := AuthMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/users/hello", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken("USER", true))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired token, got %d", rr.Code)
	}
}

func TestAuthMiddlewareForbiddenRole(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := AuthMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken("USER", false))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestAuthMiddlewareValidToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := AuthMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/users/hello", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken("USER", false))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for valid token, got %d", rr.Code)
	}
}

func TestAuthMiddlewarePublicPathsSkipAuth(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := AuthMiddleware(inner)

	for _, path := range []string{"/metrics", "/health"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected public path %s to skip auth, got %d", path, rr.Code)
		}
	}
}

type revocationHook struct{ err error }

func (h revocationHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h revocationHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h revocationHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.err != nil {
			cmd.SetErr(h.err)
			return h.err
		}
		cmd.(*redis.IntCmd).SetVal(0)
		return nil
	}
}
func testRevocationClient(err error) *redis.Client {
	c := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	c.AddHook(revocationHook{err: err})
	return c
}
func TestRevocationUnavailableFailsClosed(t *testing.T) {
	original := ratelimit.Rdb
	defer func() { ratelimit.Rdb = original }()
	for _, client := range []*redis.Client{nil, testRevocationClient(errors.New("unavailable"))} {
		ratelimit.Rdb = client
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/users/hello", nil)
		req.Header.Set("Authorization", "Bearer "+makeToken("USER", false))
		AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unauthorized forwarding") })).ServeHTTP(rr, req)
		if rr.Code != 503 {
			t.Fatalf("got %d, want 503", rr.Code)
		}
		if client != nil {
			client.Close()
		}
	}
}
