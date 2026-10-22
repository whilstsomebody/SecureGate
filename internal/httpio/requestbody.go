package httpio

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/whilstsomebody/securegate/internal/metrics"
)

type connectionKey struct{}

// ConnectionContext records the owned socket so a stalled body read can be
// interrupted without placing deadlines on background reads during responses.
func ConnectionContext(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, connectionKey{}, conn)
}

func LimitRequestBodyReads(next http.Handler, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if conn, ok := r.Context().Value(connectionKey{}).(net.Conn); ok && r.Body != nil && r.Body != http.NoBody {
			r.Body = &idleRequestBody{ReadCloser: r.Body, conn: conn, timeout: timeout}
		}
		next.ServeHTTP(w, r)
	})
}

type idleRequestBody struct {
	io.ReadCloser
	conn    net.Conn
	timeout time.Duration
	expired atomic.Bool
}

func (b *idleRequestBody) Read(p []byte) (int, error) {
	if b.expired.Load() {
		return 0, context.DeadlineExceeded
	}
	timer := time.AfterFunc(b.timeout, func() {
		b.expired.Store(true)
		metrics.Timeouts.WithLabelValues("client_read_idle").Inc()
		b.conn.Close()
	})
	n, err := b.ReadCloser.Read(p)
	timer.Stop()
	if b.expired.Load() {
		return n, context.DeadlineExceeded
	}
	return n, err
}
