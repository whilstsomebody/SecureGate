package middlewares

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/httpio"
	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
	"github.com/whilstsomebody/securegate/internal/routing"
)

// allowFn is the rate-limit check function; can be swapped in tests.
var allowFn = ratelimit.AllowRequest

func clientIP(r *http.Request) string {
	// Forwarding headers are untrusted until trusted-proxy configuration exists.
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func RateLimitMiddleware(next http.Handler) http.Handler {
	return RateLimitMiddlewareFor(next, config.Cfg, routing.Current())
}

func RateLimitMiddlewareFor(next http.Handler, cfg *config.Config, routes *routing.Table) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := 5
		window := 10 * time.Second
		if cfg != nil {
			limit = cfg.RateLimitCount
			window = cfg.RateLimitWindow
		}

		ip := clientIP(r)
		allowed, err := allowFn(r.Context(), ip, limit, window)
		if err != nil {
			slog.Error("rate limiter error", "error", err, "ip", ip, "request_id", r.Header.Get(RequestIDHeader))
			w.Header().Set("Retry-After", "5")
			httpio.WriteError(w, r, http.StatusServiceUnavailable, "RATE_LIMITER_UNAVAILABLE", "Rate limiter unavailable")
			return
		}

		if !allowed {
			slog.Debug("rate limit exceeded", "ip", ip, "route", metrics.RouteLabelFor(routes, r.URL.Path), "request_id", r.Header.Get(RequestIDHeader))
			metrics.RateLimitedCount.WithLabelValues(metrics.RouteLabelFor(routes, r.URL.Path)).Inc()
			retrySeconds := int(window.Seconds())
			if retrySeconds < 1 {
				retrySeconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
			httpio.WriteError(w, r, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "Too many requests to resolve")
			return
		}

		next.ServeHTTP(w, r)
	})
}
