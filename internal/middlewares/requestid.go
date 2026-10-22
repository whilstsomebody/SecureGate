package middlewares

import (
	"crypto/rand"
	"fmt"
	"net/http"
)

const RequestIDHeader = "X-Request-ID"

// RequestIDMiddleware ensures every request carries an X-Request-ID header.
// It generates one if the client did not supply it, and echoes it back in the response.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if len(r.Header.Values(RequestIDHeader)) != 1 || !validRequestID(id) {
			id = newRequestID()
		}
		r.Header.Set(RequestIDHeader, id)
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r)
	})
}

func newRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// IDs are opaque correlation labels, not proof of client identity.
func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}
