package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRewritePath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/users/hello", "/hello"},
		{"/admin/dashboard", "/dashboard"},
		{"/payments/checkout", "/checkout"},
		{"/users", "/"},
		{"/admin", "/"},
		{"/unknown/path", "/unknown/path"},
		{"/usersanything", "/usersanything"},
		{"/admin-tools/x", "/admin-tools/x"},
		{"/users/", "/"},
	}
	for _, tt := range tests {
		got := rewritePath(tt.in)
		if got != tt.want {
			t.Errorf("rewritePath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestProxyHandlerReturns404ForUnknownRoute(t *testing.T) {
	handler, err := NewHandler([]Route{{Prefix: "/users", Target: "http://example.test"}}, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown route, got %d", rr.Code)
	}
}

func TestNewHandlerBuildsStandaloneCore(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/hello" {
			t.Errorf("upstream path = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler, err := NewHandler([]Route{{Prefix: "/users", Target: upstream.URL}}, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/users/hello", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestNewHandlerRejectsInvalidRoutes(t *testing.T) {
	for _, routes := range [][]Route{
		nil,
		{{Prefix: "/unknown", Target: "http://example.test"}},
		{{Prefix: "/users", Target: "ftp://example.test"}},
		{{Prefix: "/users", Target: "http://example.test"}, {Prefix: "/users", Target: "http://other.test"}},
	} {
		if _, err := NewHandler(routes, http.DefaultTransport); err == nil {
			t.Fatalf("routes %#v accepted", routes)
		}
	}
}
