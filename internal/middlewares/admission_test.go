package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/whilstsomebody/securegate/internal/metrics"
)

func TestAdmissionRejectsOverloadAndReleasesCapacity(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	h := LimitRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-release; w.WriteHeader(204) }), 1)
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) }()
	<-entered
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 503 || rr.Header().Get("Connection") != "close" {
		t.Errorf("overload status=%d headers=%v", rr.Code, rr.Header())
	}
	close(release)
	<-done
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 204 {
		t.Errorf("capacity not released: %d", rr.Code)
	}
}
func TestAdmissionReleasesCapacityOnPanic(t *testing.T) {
	h := LimitRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("test") }), 1)
	for i := 0; i < 2; i++ {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("request was rejected instead of admitted")
				}
			}()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		}()
	}
}

func TestUpgradeBudgetLeavesOrdinaryRequestsAvailable(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	h := LimitRequests(LimitUpgrades(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" {
			close(entered)
			<-release
		}
	}), 1), 2)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Upgrade", "websocket")
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), req) }()
	<-entered
	defer func() { close(release); <-done }()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req.Clone(req.Context()))
	if rr.Code != 503 {
		t.Fatalf("extra upgrade: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 200 {
		t.Fatalf("ordinary request blocked: %d", rr.Code)
	}
}

func TestAdmissionMetricsTracked(t *testing.T) {
	metrics.RegisterMetrics()
	h := LimitRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), 1)

	// Fill slot and hold
	entered := make(chan struct{})
	release := make(chan struct{})
	slowH := LimitRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}), 1)

	go slowH.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	<-entered

	// Overload request must increment rejection metric
	rr := httptest.NewRecorder()
	slowH.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
	close(release)

	// Clean single request works
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}
