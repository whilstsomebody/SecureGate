package middlewares

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/whilstsomebody/securegate/internal/auth"
	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
)

// publicPaths bypass authentication entirely (Prometheus scrape, health probe).
var publicPaths = map[string]bool{
	"/metrics": true,
	"/health":  true,
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(RequestIDHeader)

		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			metrics.AuthFailures.WithLabelValues(r.URL.Path, "missing_authorization_header").Inc()
			http.Error(w, "Missing authorization token!", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			metrics.AuthFailures.WithLabelValues(r.URL.Path, "invalid_authorization_format").Inc()
			http.Error(w, "Invalid authorization format!", http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]
		secret := config.Cfg.JWTSecret

		token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(secret), nil
		})

		if err != nil || !token.Valid {
			metrics.AuthFailures.WithLabelValues(r.URL.Path, "invalid_or_expired_token").Inc()
			http.Error(w, "Invalid or Expired token", http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			metrics.AuthFailures.WithLabelValues(r.URL.Path, "invalid_token_claims").Inc()
			http.Error(w, "Invalid token claims", http.StatusUnauthorized)
			return
		}

		role, ok := claims["role"].(string)
		if !ok || role == "" {
			metrics.AuthFailures.WithLabelValues(r.URL.Path, "missing_role_claim").Inc()
			http.Error(w, "Invalid token claims", http.StatusUnauthorized)
			return
		}

		// Check revocation list if the token carries a JTI.
		if jti, _ := claims["jti"].(string); jti != "" {
			revoked, err := ratelimit.IsRevoked(jti)
			if err != nil {
				slog.Warn("revocation check failed, allowing request", "error", err, "request_id", requestID)
			} else if revoked {
				metrics.AuthFailures.WithLabelValues(r.URL.Path, "token_revoked").Inc()
				http.Error(w, "Token has been revoked", http.StatusUnauthorized)
				return
			}
		}

		if !isAuthorized(r.URL.Path, role) {
			metrics.AuthFailures.WithLabelValues(r.URL.Path, "rbac_forbidden").Inc()
			slog.Warn("access forbidden", "path", r.URL.Path, "role", role, "request_id", requestID)
			http.Error(w, "Access forbidden: Insufficient role permission", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isAuthorized(path string, userRole string) bool {
	for routePrefix, requiredRole := range auth.RouteRoleMap {
		if strings.HasPrefix(path, routePrefix) {
			return userRole == requiredRole
		}
	}
	return false
}
