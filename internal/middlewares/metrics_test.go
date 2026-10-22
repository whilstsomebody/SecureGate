package middlewares

import (
	"bytes"
	"context"
	"fmt"
	"github.com/whilstsomebody/securegate/internal/config"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/whilstsomebody/securegate/internal/metrics"
)

func TestRejectedTrafficHasBoundedMetricLabels(t *testing.T) {
	metrics.RequestCount.Reset()
	metrics.RequestDuration.Reset()
	metrics.AuthFailures.Reset()
	metrics.RateLimitedCount.Reset()
	original := allowFn
	defer func() { allowFn = original }()
	allow := true
	allowFn = func(context.Context, string, int, time.Duration) (bool, error) { return allow, nil }
	h := MetricsMiddleware(RecoveryMiddleware(RateLimitMiddleware(AuthMiddleware(http.NotFoundHandler()))))
	for i := 0; i < 200; i++ {
		allow = i%2 == 0
		path := fmt.Sprintf("/users/%d", i)
		if i%3 == 0 {
			path = fmt.Sprintf("/unknown/%d", i)
		}
		req := httptest.NewRequest(fmt.Sprintf("METHOD%d", i), path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 401 && rr.Code != 429 {
			t.Fatalf("status=%d", rr.Code)
		}
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(metrics.RequestCount, metrics.RequestDuration, metrics.AuthFailures, metrics.RateLimitedCount)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if len(family.Metric) > 4 {
			t.Errorf("unbounded %s: %d", family.GetName(), len(family.Metric))
		}
		for _, m := range family.Metric {
			for _, l := range m.Label {
				if l.GetName() == "path" && l.GetValue() != "/users" && l.GetValue() != "unmatched" {
					t.Errorf("raw path: %s", l.GetValue())
				}
				if l.GetName() == "method" && l.GetValue() != "OTHER" {
					t.Errorf("raw method: %s", l.GetValue())
				}
			}
		}
	}
}
func TestMetricsRecordAbortAndPropagateIt(t *testing.T) {
	metrics.ResponseAborts.Reset()
	metrics.RequestCount.Reset()
	defer func() {
		if p := recover(); p != http.ErrAbortHandler {
			t.Errorf("panic=%v", p)
		}
		reg := prometheus.NewRegistry()
		reg.MustRegister(metrics.ResponseAborts, metrics.RequestCount)
		families, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		if len(families) != 2 {
			t.Fatalf("missing metrics: %d", len(families))
		}
		for _, f := range families {
			if len(f.Metric) != 1 || f.Metric[0].Counter.GetValue() != 1 {
				t.Errorf("wrong count: %v", f)
			}
		}
	}()
	MetricsMiddleware(RecoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("partial"))
		panic(http.ErrAbortHandler)
	}))).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/users/x", nil))
}

func TestAccessLogCanBeDisabledWithoutDisablingMetrics(t *testing.T) {
	originalCfg, originalLogger := config.Cfg, slog.Default()
	t.Cleanup(func() { config.Cfg = originalCfg; slog.SetDefault(originalLogger) })
	for _, enabled := range []bool{false, true} {
		cfg := *originalCfg
		cfg.AccessLogEnabled = enabled
		config.Cfg = &cfg
		var out bytes.Buffer
		slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
		metrics.RequestCount.Reset()
		MetricsMiddleware(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/users/private-path", nil))
		if (out.Len() > 0) != enabled {
			t.Fatalf("access logging enabled=%v output=%s", enabled, out.String())
		}
		if strings.Contains(out.String(), "private-path") {
			t.Fatal("raw path logged")
		}
		reg := prometheus.NewRegistry()
		reg.MustRegister(metrics.RequestCount)
		families, err := reg.Gather()
		if err != nil || len(families) != 1 {
			t.Fatal("metrics missing")
		}
	}
}
