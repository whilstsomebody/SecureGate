package middlewares

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/httpio"
	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
	"github.com/whilstsomebody/securegate/internal/routing"
)

func AuthMiddleware(next http.Handler) http.Handler {
	return AuthMiddlewareFor(next, config.Cfg, routing.Current())
}

func AuthMiddlewareFor(next http.Handler, cfg *config.Config, routes *routing.Table) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(RequestIDHeader)

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabelFor(routes, r.URL.Path), "missing_authorization_header").Inc()
			httpio.WriteError(w, r, http.StatusUnauthorized, "MISSING_AUTHORIZATION", "Missing authorization token")
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabelFor(routes, r.URL.Path), "invalid_authorization_format").Inc()
			httpio.WriteError(w, r, http.StatusUnauthorized, "INVALID_AUTHORIZATION_FORMAT", "Invalid authorization format")
			return
		}

		tokenString := parts[1]
		if cfg == nil || cfg.JWTSecret == "" || strings.TrimSpace(cfg.JWTIssuer) == "" || strings.TrimSpace(cfg.JWTAudience) == "" {
			httpio.WriteError(w, r, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE", "Authentication service unavailable")
			return
		}
		token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
			return []byte(cfg.JWTSecret), nil
		}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(),
			jwt.WithIssuer(cfg.JWTIssuer), jwt.WithAudience(cfg.JWTAudience))

		if err != nil || !token.Valid {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabelFor(routes, r.URL.Path), "invalid_or_expired_token").Inc()
			httpio.WriteError(w, r, http.StatusUnauthorized, "INVALID_OR_EXPIRED_TOKEN", "Invalid or expired token")
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabelFor(routes, r.URL.Path), "invalid_token_claims").Inc()
			httpio.WriteError(w, r, http.StatusUnauthorized, "INVALID_TOKEN_CLAIMS", "Invalid token claims")
			return
		}

		role, ok := claims["role"].(string)
		if !ok || role == "" {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabelFor(routes, r.URL.Path), "missing_role_claim").Inc()
			httpio.WriteError(w, r, http.StatusUnauthorized, "MISSING_ROLE_CLAIM", "Missing role claim")
			return
		}

		jti, ok := claims["jti"].(string)
		if !ok || strings.TrimSpace(jti) == "" {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabelFor(routes, r.URL.Path), "invalid_token_claims").Inc()
			httpio.WriteError(w, r, http.StatusUnauthorized, "INVALID_TOKEN_CLAIMS", "Invalid token claims")
			return
		}

		revoked, err := ratelimit.IsRevoked(r.Context(), jti)
		if err != nil {
			slog.Error("revocation check failed", "error", err, "request_id", requestID)
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabelFor(routes, r.URL.Path), "revocation_unavailable").Inc()
			w.Header().Set("Retry-After", "5")
			httpio.WriteError(w, r, http.StatusServiceUnavailable, "REVOCATION_UNAVAILABLE", "Authentication service unavailable")
			return
		} else if revoked {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabelFor(routes, r.URL.Path), "token_revoked").Inc()
			httpio.WriteError(w, r, http.StatusUnauthorized, "TOKEN_REVOKED", "Token has been revoked")
			return
		}

		if !isAuthorizedFor(routes, r.URL.Path, role) {
			metrics.AuthFailures.WithLabelValues(metrics.RouteLabelFor(routes, r.URL.Path), "rbac_forbidden").Inc()
			slog.Debug("access forbidden", "route", metrics.RouteLabelFor(routes, r.URL.Path), "request_id", requestID)
			httpio.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "Access forbidden: Insufficient role permission")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isAuthorized(path string, userRole string) bool {
	return isAuthorizedFor(routing.Current(), path, userRole)
}

func isAuthorizedFor(routes *routing.Table, path, userRole string) bool {
	route, ok := routes.Match(path)
	return ok && route.Role != "" && userRole == route.Role
}
