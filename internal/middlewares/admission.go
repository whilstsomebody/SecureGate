package middlewares

import (
	"net/http"

	"github.com/whilstsomebody/securegate/internal/httpio"
	"github.com/whilstsomebody/securegate/internal/metrics"
)

// LimitRequests rejects overload immediately rather than building a wait queue.
func LimitRequests(next http.Handler, limit int) http.Handler {
	if limit < 1 {
		panic("request limit must be positive")
	}
	slots := make(chan struct{}, limit)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			metrics.ActiveRequests.Inc()
			defer func() {
				metrics.ActiveRequests.Dec()
				<-slots
			}()
			next.ServeHTTP(w, r)
		default:
			metrics.AdmissionRejections.WithLabelValues("request_limit").Inc()
			// Avoid draining a slow upload before sending the rejection.
			w.Header().Set("Connection", "close")
			w.Header().Set("Retry-After", "1")
			httpio.WriteError(w, r, http.StatusServiceUnavailable, "PROXY_BUSY", "Proxy busy")
		}
	})
}

// LimitUpgrades reserves a smaller budget for long-lived upgraded connections.
// The outer request limit still bounds all traffic, including SSE streams.
func LimitUpgrades(next http.Handler, limit int) http.Handler {
	if limit < 1 {
		panic("upgrade request limit must be positive")
	}
	slots := make(chan struct{}, limit)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" {
			select {
			case slots <- struct{}{}:
				metrics.ActiveUpgrades.Inc()
				defer func() {
					metrics.ActiveUpgrades.Dec()
					<-slots
				}()
				next.ServeHTTP(w, r)
			default:
				metrics.AdmissionRejections.WithLabelValues("upgrade_limit").Inc()
				w.Header().Set("Connection", "close")
				w.Header().Set("Retry-After", "1")
				httpio.WriteError(w, r, http.StatusServiceUnavailable, "UPGRADE_LIMIT_EXCEEDED", "Upgrade capacity exceeded")
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}
