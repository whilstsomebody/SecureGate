package middlewares

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/whilstsomebody/securegate/internal/auth"
	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
	"github.com/whilstsomebody/securegate/internal/routing"
)

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(RequestIDHeader)

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabel(r.URL.Path), "missing_authorization_header").Inc()
			http.Error(w, "Missing authorization token!", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabel(r.URL.Path), "invalid_authorization_format").Inc()
			http.Error(w, "Invalid authorization format!", http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]
		cfg := config.Cfg
		if cfg == nil || cfg.JWTSecret == "" || strings.TrimSpace(cfg.JWTIssuer) == "" || strings.TrimSpace(cfg.JWTAudience) == "" {
			http.Error(w, "Authentication service unavailable", http.StatusServiceUnavailable)
			return
		}
		token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
			return []byte(cfg.JWTSecret), nil
		}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(),
			jwt.WithIssuer(cfg.JWTIssuer), jwt.WithAudience(cfg.JWTAudience))

		if err != nil || !token.Valid {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabel(r.URL.Path), "invalid_or_expired_token").Inc()
			http.Error(w, "Invalid or Expired token", http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabel(r.URL.Path), "invalid_token_claims").Inc()
			http.Error(w, "Invalid token claims", http.StatusUnauthorized)
			return
		}

		role, ok := claims["role"].(string)
		if !ok || role == "" {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabel(r.URL.Path), "missing_role_claim").Inc()
			http.Error(w, "Invalid token claims", http.StatusUnauthorized)
			return
		}

		jti, ok := claims["jti"].(string)
		if !ok || strings.TrimSpace(jti) == "" {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabel(r.URL.Path), "invalid_token_claims").Inc()
			http.Error(w, "Invalid token claims", http.StatusUnauthorized)
			return
		}

		revoked, err := ratelimit.IsRevoked(r.Context(), jti)
		if err != nil {
			slog.Error("revocation check failed", "error", err, "request_id", requestID)
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabel(r.URL.Path), "revocation_unavailable").Inc()
			http.Error(w, "Authentication service unavailable", http.StatusServiceUnavailable)
			return
		} else if revoked {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabel(r.URL.Path), "token_revoked").Inc()
			http.Error(w, "Token has been revoked", http.StatusUnauthorized)
			return
		}

		if !isAuthorized(r.URL.Path, role) {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabel(r.URL.Path), "rbac_forbidden").Inc()
			slog.Warn("access forbidden", "path", r.URL.Path, "role", role, "request_id", requestID)
			http.Error(w, "Access forbidden: Insufficient role permission", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isAuthorized(path string, userRole string) bool {
	requiredRole, ok := auth.RouteRoleMap[routing.Prefix(path)]
	return ok && userRole == requiredRole
}
