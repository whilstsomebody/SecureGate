package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"strings"
)

var (
	ResponseAborts = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "securegate_response_aborts_total", Help: "Responses aborted before completion"}, []string{"path"})
	RequestCount   = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "securegate_requests_total",
			Help: "Total number of requests received by SecureGate",
		},
		[]string{"path", "method", "status"},
	)

	RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "securegate_request_duration_seconds",
			Help: "Request latency distribution in SecureGate",
		},
		[]string{"path"},
	)

	RateLimitedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "securegate_rate_limited_total",
			Help: "Total requests blocked due to rate limiting",
		},
		[]string{"path"},
	)

	AuthFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "securegate_auth_failures_total",
			Help: "Total authentication/authorization failures in SecureGate",
		},
		[]string{"path", "reason"},
	)
)

// RegisterMetrics registers all metrics. Safe to call multiple times (e.g. in tests).
func RegisterMetrics() {
	for _, c := range []prometheus.Collector{RequestCount, RequestDuration, RateLimitedCount, AuthFailures, ResponseAborts} {
		if err := prometheus.Register(c); err != nil {
			if _, alreadyRegistered := err.(prometheus.AlreadyRegisteredError); !alreadyRegistered {
				panic(err)
			}
		}
	}
}

// RouteLabel never includes caller-controlled suffixes or unmatched URLs.
// Keep the path label name for compatibility; its values identify routes.
func RouteLabel(path string) string {
	for _, prefix := range []string{"/users", "/payments", "/admin"} {
		if strings.HasPrefix(path, prefix) {
			return prefix
		}
	}
	switch path {
	case "/health", "/metrics":
		return path
	}
	return "unmatched"
}
func MethodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return method
	}
	return "OTHER"
}
