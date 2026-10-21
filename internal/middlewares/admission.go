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
