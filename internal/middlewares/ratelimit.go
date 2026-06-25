package middlewares

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
)

// allowFn is the rate-limit check function; can be swapped in tests.
var allowFn = ratelimit.AllowRequest

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
		limit := 5
		window := 10 * time.Second
		if config.Cfg != nil {
			limit = config.Cfg.RateLimitCount
			window = config.Cfg.RateLimitWindow
		}

		ip := clientIP(r)
		allowed, err := allowFn(ip, limit, window)
		if err != nil {
			slog.Error("rate limiter error", "error", err, "ip", ip, "request_id", r.Header.Get(RequestIDHeader))
			http.Error(w, "Rate limiter error", http.StatusInternalServerError)
			return
		}

		if !allowed {
			slog.Warn("rate limit exceeded", "ip", ip, "path", r.URL.Path, "request_id", r.Header.Get(RequestIDHeader))
			metrics.RateLimitedCount.WithLabelValues(r.URL.Path).Inc()
			http.Error(w, "Too many requests to resolve.", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
