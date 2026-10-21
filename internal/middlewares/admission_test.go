package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
