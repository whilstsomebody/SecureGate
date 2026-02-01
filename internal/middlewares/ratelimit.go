package middlewares

import (
	"net/http"
	"strings"
	"time"

	"github.com/whilstsomebody/securegate/internal/ratelimit"
)

func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		clientIP := strings.Split(r.RemoteAddr, ":")[0]

		allowed, err := ratelimit.AllowRequest(clientIP, 5, 10*time.Second)

		if err != nil {
			http.Error(w, "Rate limiter error", http.StatusInternalServerError)
			return
		}

		if !allowed {
			http.Error(w, "Too many requests to resolve.", http.StatusTooManyRequests)
		}

		next.ServeHTTP(w, r)
	})
}