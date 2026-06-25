package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDMiddlewareGeneratesIDWhenMissing(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get(RequestIDHeader)
	})

	handler := RequestIDMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if captured == "" {
		t.Error("expected a generated request ID on the request header")
	}
	if rr.Header().Get(RequestIDHeader) == "" {
		t.Error("expected request ID echoed in response header")
	}
}

func TestRequestIDMiddlewarePreservesExistingID(t *testing.T) {
	const existing = "my-trace-id-123"
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get(RequestIDHeader)
	})

	handler := RequestIDMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, existing)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if captured != existing {
		t.Errorf("expected preserved ID %q, got %q", existing, captured)
	}
	if rr.Header().Get(RequestIDHeader) != existing {
		t.Errorf("expected response to echo %q", existing)
	}
}
