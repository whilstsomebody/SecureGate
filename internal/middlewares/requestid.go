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
		if id == "" {
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
