package middlewares

import "net/http"

// LimitRequests rejects overload immediately rather than building a wait queue.
func LimitRequests(next http.Handler, limit int) http.Handler {
	if limit < 1 {
		panic("request limit must be positive")
	}
	slots := make(chan struct{}, limit)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			// Avoid draining a slow upload before sending the rejection.
			w.Header().Set("Connection", "close")
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Proxy busy", http.StatusServiceUnavailable)
		}
	})
}

// LimitUpgrades reserves a smaller budget for long-lived upgraded connections.
// The outer request limit still bounds all traffic, including SSE streams.
func LimitUpgrades(next http.Handler, limit int) http.Handler {
	limited := LimitRequests(next, limit)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" {
			limited.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
