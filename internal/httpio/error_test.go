package httpio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteErrorFormatAndHeaders(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "req-test-1234")
	w := httptest.NewRecorder()

	WriteError(w, req, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "Rate limit exceeded")

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("unexpected content type: %s", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("X-Request-ID") != "req-test-1234" {
		t.Fatalf("request id header not propagated: %s", w.Header().Get("X-Request-ID"))
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON error: %v", err)
	}
	if resp.Error.Code != "RATE_LIMIT_EXCEEDED" || resp.Error.Message != "Rate limit exceeded" || resp.Error.Status != 429 || resp.Error.RequestID != "req-test-1234" {
		t.Fatalf("unexpected error response content: %+v", resp)
	}
}
