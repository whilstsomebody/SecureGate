package middlewares

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/whilstsomebody/securegate/internal/httpio"
	"github.com/whilstsomebody/securegate/internal/metrics"
)

func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		path := r.URL.Path
		route := metrics.RouteLabel(path)
		recorder := httpio.NewRecorder(w)
		defer func() {
			panicValue := recover()
			status := recorder.Status
			if panicValue != nil && !recorder.Committed {
				status = http.StatusInternalServerError
			}
			metrics.RequestCount.WithLabelValues(route, metrics.MethodLabel(r.Method), strconv.Itoa(status)).Inc()
			metrics.RequestDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
			if panicValue != nil {
				metrics.ResponseAborts.WithLabelValues(route).Inc()
			}
			slog.Info("request", "method", r.Method, "path", path, "status", status, "aborted", panicValue != nil, "duration_ms", time.Since(start).Milliseconds(), "request_id", r.Header.Get(RequestIDHeader))
			if panicValue != nil {
				panic(panicValue)
			}
		}()
		next.ServeHTTP(recorder, r)
	})
}
