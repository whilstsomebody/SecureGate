package middlewares

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestInvalidRequestIDsAreReplaced(t *testing.T) {
	for _, id := range []string{strings.Repeat("a", 129), "a b", "x\ny", "🚀", "a,b"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set(RequestIDHeader, id)
		rr := httptest.NewRecorder()
		RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get(RequestIDHeader); got == id || !validRequestID(got) {
				t.Fatalf("invalid ID forwarded: %q", got)
			}
		})).ServeHTTP(rr, req)
		if !validRequestID(rr.Header().Get(RequestIDHeader)) {
			t.Fatal("invalid echoed ID")
		}
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header[RequestIDHeader] = []string{"first", "second"}
	rr := httptest.NewRecorder()
	RequestIDMiddleware(http.NotFoundHandler()).ServeHTTP(rr, req)
	if rr.Header().Get(RequestIDHeader) == "first" {
		t.Fatal("ambiguous ID preserved")
	}
}
