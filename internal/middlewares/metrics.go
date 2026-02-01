package middlewares

import (
	"net/http"
	"time"

	"github.com/whilstsomebody/securegate/internal/metrics"
)

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.statusCode = code
	rr.ResponseWriter.WriteHeader(code)
}

func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		start := time.Now()

		recorder := &responseRecorder{
			ResponseWriter: w,
			statusCode: 200,
		}

		next.ServeHTTP(recorder, r)

		duration := time.Since(start).Seconds()

		metrics.RequestCount.WithLabelValues(
			r.URL.Path,
			r.Method,
			http.StatusText(recorder.statusCode),
		).Inc()

		metrics.RequestDuration.WithLabelValues(r.URL.Path).Observe(duration)
	})
}