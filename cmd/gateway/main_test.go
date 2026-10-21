package main

import (
	"errors"
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
