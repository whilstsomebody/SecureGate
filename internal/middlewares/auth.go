package middlewares

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/whilstsomebody/securegate/internal/auth"
	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/metrics"
)

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if r.URL.Path == "/metrics" {
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

		secret := config.GetJWTSecret()

		token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {

			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method")
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

		if !isAuthorized(r.URL.Path, role) {
			metrics.AuthFailures.WithLabelValues(r.URL.Path, "rbac_forbidden").Inc()
			http.Error(w, "Access forbidden: Insufficient role permission", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isAuthorized(path string, userRole string) bool {
	for routePrefix, requireRole := range auth.RouteRoleMap {

		if strings.HasPrefix(path, routePrefix) {

			if userRole == "ADMIN" {
				return true
			}

			return userRole == requireRole
		}
	}

	return false
}
