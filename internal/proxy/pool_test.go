package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/whilstsomebody/securegate/internal/circuitbreaker"
)

func TestBackendPoolRoundRobin(t *testing.T) {
	backend := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.WriteHeader(http.StatusOK)
				return
			}
			_, _ = w.Write([]byte(name))
		}))
	}
	a, b := backend("a"), backend("b")
	defer a.Close()
	defer b.Close()
	h, err := NewManagedHandler([]Route{{Prefix: "/api", Backends: []string{a.URL, b.URL}, HealthCheckIntervalSeconds: 3600}}, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	for i, want := range []string{"a", "b", "a", "b"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/items", nil))
		if rr.Code != http.StatusOK || rr.Body.String() != want {
			t.Fatalf("request %d: status=%d body=%q, want %q", i+1, rr.Code, rr.Body.String(), want)
		}
	}
}

func TestBackendPoolLeastConnections(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		close(started)
		<-release
		_, _ = w.Write([]byte("a"))
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte("b"))
	}))
	defer b.Close()
	h, err := NewManagedHandler([]Route{{Prefix: "/api", Backends: []string{a.URL, b.URL}, Balance: leastConnections, HealthCheckIntervalSeconds: 3600}}, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/items", nil))
		first <- rr
	}()
	<-started
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/items", nil))
	if second.Code != http.StatusOK || second.Body.String() != "b" {
		t.Fatalf("least-connections selected status=%d body=%q", second.Code, second.Body.String())
	}
	close(release)
	if result := <-first; result.Code != http.StatusOK || result.Body.String() != "a" {
		t.Fatalf("first result status=%d body=%q", result.Code, result.Body.String())
	}
}

func TestBackendPoolHealthAndDrain(t *testing.T) {
	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("unhealthy"))
	}))
	defer unhealthy.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("healthy"))
	}))
	defer healthy.Close()
	h, err := NewManagedHandler([]Route{{Prefix: "/api", Backends: []string{unhealthy.URL, healthy.URL}, HealthCheckIntervalSeconds: 1}}, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	pool := h.pools["/api"]
	deadline := time.Now().Add(2 * time.Second)
	for pool.backends[0].healthy.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if pool.backends[0].healthy.Load() {
		t.Fatal("failed health check did not mark backend unhealthy")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/items", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "healthy" {
		t.Fatalf("unhealthy backend selected: status=%d body=%q", rr.Code, rr.Body.String())
	}

	backend, finish, ok := pool.Acquire()
	if !ok {
		t.Fatal("healthy backend was not selectable")
	}
	pool.Drain()
	if _, _, ok := pool.Acquire(); ok {
		t.Fatal("drained pool accepted a new request")
	}
	pool.Release(backend)
	finish(circuitbreaker.Success)
}
