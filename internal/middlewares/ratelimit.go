package middlewares

import (
	"net/http"
	"net"
	"strings"
	"time"

	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
)

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		allowed, err := ratelimit.AllowRequest(clientIP(r), 5, 10*time.Second)

		if err != nil {
			http.Error(w, "Rate limiter error", http.StatusInternalServerError)
			return
		}

		if !allowed {
			metrics.RateLimitedCount.WithLabelValues(r.URL.Path).Inc()
			http.Error(w, "Too many requests to resolve.", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
