package main

import (
	"errors"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
	"net/http/httptest"
	"testing"
)

type unreadableBody struct{ t *testing.T }

func (b unreadableBody) Read([]byte) (int, error) {
	b.t.Error("retired endpoint read request body")
	return 0, errors.New("must not read")
}
func (b unreadableBody) Close() error { return nil }
func TestRevocationEndpointRetired(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT"} {
		req := httptest.NewRequest(method, "/auth/revoke", nil)
		req.Body = unreadableBody{t}
		rr := httptest.NewRecorder()
		newHandler().ServeHTTP(rr, req)
		if rr.Code != 404 {
			t.Fatalf("%s: got %d", method, rr.Code)
		}
	}
}

func TestManagementIsolationAndLiveness(t *testing.T) {
	original := ratelimit.Rdb
	ratelimit.Rdb = nil
	t.Cleanup(func() { ratelimit.Rdb = original })
	for _, path := range []string{"/health", "/ready", "/metrics"} {
		rr := httptest.NewRecorder()
		newHandler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != 404 {
			t.Fatalf("public %s: %d", path, rr.Code)
		}
	}
	handler := newManagementHandler()
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/health", 200}, {"GET", "/ready", 503}, {"GET", "/metrics", 200}, {"POST", "/health", 405}, {"GET", "/users", 404},
	} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
		if rr.Code != tc.status {
			t.Fatalf("%s %s: got %d want %d", tc.method, tc.path, rr.Code, tc.status)
		}
	}
}
