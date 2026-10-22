package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/whilstsomebody/securegate/internal/routing"
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

	AdmissionRejections = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "securegate_admission_rejections_total",
			Help: "Total requests rejected at admission control due to concurrency limits",
		},
		[]string{"reason"},
	)

	ActiveConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "securegate_active_connections",
			Help: "Current number of active client connections on public listener",
		},
	)

	ActiveRequests = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "securegate_active_requests",
			Help: "Current number of active requests being processed",
		},
	)

	ActiveUpgrades = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "securegate_active_upgrades",
			Help: "Current number of active upgraded connections",
		},
	)

	UpstreamDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "securegate_upstream_duration_seconds",
			Help:    "Upstream roundtrip response duration in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"route"},
	)

	Timeouts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "securegate_timeouts_total",
			Help: "Total timeout events across client and upstream IO",
		},
		[]string{"type"},
	)

	CircuitBreakerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "securegate_circuit_breaker_state",
			Help: "Circuit breaker state by route (0=CLOSED, 1=OPEN, 2=HALF_OPEN)",
		},
		[]string{"route"},
	)

	BuildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "securegate_build_info",
			Help: "Build information about the running SecureGate binary",
		},
		[]string{"version", "commit", "go_version"},
	)
)

// RegisterMetrics registers all metrics. Safe to call multiple times (e.g. in tests).
func RegisterMetrics() {
	collectors := []prometheus.Collector{
		RequestCount, RequestDuration, RateLimitedCount, AuthFailures, ResponseAborts,
		AdmissionRejections, ActiveConnections, ActiveRequests, ActiveUpgrades,
		UpstreamDuration, Timeouts, CircuitBreakerState, BuildInfo,
	}
	for _, c := range collectors {
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
	if prefix := routing.Prefix(path); prefix != "" {
		return prefix
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
