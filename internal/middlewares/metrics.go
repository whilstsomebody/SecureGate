package middlewares

import (
	"log/slog"
	"net/http"
	"strconv"
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

		recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(recorder, r)

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(recorder.statusCode)

		metrics.RequestCount.WithLabelValues(r.URL.Path, r.Method, status).Inc()
		metrics.RequestDuration.WithLabelValues(r.URL.Path).Observe(duration)

		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", r.Header.Get(RequestIDHeader),
		)
	})
}
