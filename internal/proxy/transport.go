package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

var upstreamTransport = boundedTransport()

func boundedTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 15 * time.Second
	transport.MaxConnsPerHost = 128
	transport.MaxIdleConnsPerHost = 32
	transport.MaxResponseHeaderBytes = 64 << 10
	return transport
}

func CloseIdleConnections() { upstreamTransport.CloseIdleConnections() }

// readIdleTransport bounds each upstream body read, not total stream duration.
// The timer is stopped during downstream writes so slow clients do not look like
// unhealthy upstreams. Closing an upgrade must still close both directions.
func readIdleTransport(transport http.RoundTripper, timeout time.Duration) http.RoundTripper {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		ctx, cancel := context.WithCancel(req.Context())
		response, err := transport.RoundTrip(req.Clone(ctx))
		if err != nil {
			cancel()
			return response, err
		}
		body := &idleBody{ReadCloser: response.Body, cancel: cancel, timeout: timeout}
		if response.StatusCode == http.StatusSwitchingProtocols {
			if writer, ok := response.Body.(io.Writer); ok {
				response.Body = &idleUpgrade{idleBody: body, writer: writer}
			} else {
				response.Body = body
			}
		} else {
			response.Body = body
		}
		return response, nil
	})
}

type idleBody struct {
	expired atomic.Bool
	io.ReadCloser
	cancel  context.CancelFunc
	timeout time.Duration
}

func (b *idleBody) Read(p []byte) (int, error) {
	if b.expired.Load() {
		return 0, context.DeadlineExceeded
	}
	timer := time.AfterFunc(b.timeout, func() { b.expired.Store(true); b.cancel(); b.ReadCloser.Close() })
	n, err := b.ReadCloser.Read(p)
	timer.Stop()
	if b.expired.Load() {
		return n, context.DeadlineExceeded
	}
	return n, err
}
func (b *idleBody) Close() error { b.cancel(); return b.ReadCloser.Close() }

type idleUpgrade struct {
	*idleBody
	writer io.Writer
}

func (b *idleUpgrade) Write(p []byte) (int, error) { return b.writer.Write(p) }
