package metrics

import (
	"net/http"
	"testing"
)

func TestMetricsLabels(t *testing.T) {
	RegisterMetrics()

	if got := RouteLabel("/users"); got != "/users" {
		t.Errorf("expected /users, got %s", got)
	}
	if got := RouteLabel("/users/profile"); got != "/users" {
		t.Errorf("expected /users, got %s", got)
	}
	if got := RouteLabel("/payments/checkout"); got != "/payments" {
		t.Errorf("expected /payments, got %s", got)
	}
	if got := RouteLabel("/admin/dashboard"); got != "/admin" {
		t.Errorf("expected /admin, got %s", got)
	}
	if got := RouteLabel("/health"); got != "/health" {
		t.Errorf("expected /health, got %s", got)
	}
	if got := RouteLabel("/metrics"); got != "/metrics" {
		t.Errorf("expected /metrics, got %s", got)
	}
	if got := RouteLabel("/unknown/endpoint"); got != "unmatched" {
		t.Errorf("expected unmatched, got %s", got)
	}

	if got := MethodLabel(http.MethodGet); got != "GET" {
		t.Errorf("expected GET, got %s", got)
	}
	if got := MethodLabel("CUSTOM"); got != "OTHER" {
		t.Errorf("expected OTHER, got %s", got)
	}
}

func TestMetricCollectorsUpdateWithoutPanic(t *testing.T) {
	RegisterMetrics()

	ActiveConnections.Inc()
	ActiveConnections.Dec()
	ActiveRequests.Inc()
	ActiveRequests.Dec()
	ActiveUpgrades.Inc()
	ActiveUpgrades.Dec()

	AdmissionRejections.WithLabelValues("request_limit").Inc()
	AdmissionRejections.WithLabelValues("upgrade_limit").Inc()

	Timeouts.WithLabelValues("upstream_timeout").Inc()
	Timeouts.WithLabelValues("client_read_idle").Inc()

	CircuitBreakerState.WithLabelValues("/users").Set(0)
	CircuitBreakerState.WithLabelValues("/users").Set(1)

	UpstreamDuration.WithLabelValues("/users").Observe(0.025)
	BuildInfo.WithLabelValues("v0.2.0-dev", "test", "go1.26").Set(1)
}
