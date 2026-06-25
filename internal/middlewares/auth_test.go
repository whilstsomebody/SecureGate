package middlewares

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/metrics"
)

const testSecret = "test-secret-key"

func TestMain(m *testing.M) {
	os.Setenv("JWT_SECRET", testSecret)
	config.Cfg = &config.Config{
		JWTSecret:       testSecret,
		RateLimitCount:  5,
		RateLimitWindow: 10 * time.Second,
	}
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
