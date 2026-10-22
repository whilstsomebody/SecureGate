package middlewares

import (
	"log/slog"
	"net/http"

	"github.com/whilstsomebody/securegate/internal/httpio"
	"github.com/whilstsomebody/securegate/internal/metrics"
)

func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httpio.NewRecorder(w)
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				slog.Error("panic recovered",
					"error", rec,
					"method", metrics.MethodLabel(r.Method),
					"route", metrics.RouteLabel(r.URL.Path),
					"request_id", r.Header.Get(RequestIDHeader),
				)
				if recorder.Committed {
					panic(http.ErrAbortHandler)
				}
				http.Error(recorder, "Internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(recorder, r)
	})
}
