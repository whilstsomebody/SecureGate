package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	RequestCount = prometheus.NewCounterVec(
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

func RegisterMetrics() {
	prometheus.MustRegister(RequestCount)
	prometheus.MustRegister(RequestDuration)
	prometheus.MustRegister(RateLimitedCount)
	prometheus.MustRegister(AuthFailures)
}
