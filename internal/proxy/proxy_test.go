package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/whilstsomebody/securegate/internal/config"
)

func init() {
	config.Cfg = &config.Config{
		UserServiceAddr:     "http://localhost:9001",
		AdminServiceAddr:    "http://localhost:9003",
		PaymentsServiceAddr: "http://localhost:9002",
	}
}

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
	}
	for _, tt := range tests {
		got := rewritePath(tt.in)
		if got != tt.want {
			t.Errorf("rewritePath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGetTargetService(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/users/hello", "http://localhost:9001"},
		{"/users", "http://localhost:9001"},
		{"/admin/dashboard", "http://localhost:9003"},
		{"/payments/checkout", "http://localhost:9002"},
		{"/unknown", ""},
		{"/", ""},
	}
	for _, tt := range tests {
		got := getTargetService(tt.path)
		if got != tt.want {
			t.Errorf("getTargetService(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestProxyHandlerReturns404ForUnknownRoute(t *testing.T) {
	handler := NewProxyhandler()
	req := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown route, got %d", rr.Code)
	}
}
