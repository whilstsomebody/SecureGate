package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/whilstsomebody/securegate/internal/circuitbreaker"
	"github.com/whilstsomebody/securegate/internal/httpio"
	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/routing"
	"github.com/whilstsomebody/securegate/internal/trustedproxy"
)

func init() {
	circuitbreaker.OnStateChange = func(name string, state circuitbreaker.State) {
		route, backend, ok := strings.Cut(name, "\x00")
		if !ok {
			backend = "unknown"
		}
		metrics.CircuitBreakerState.WithLabelValues(route, backend).Set(float64(state))
	}
}

// Route is an explicit route definition for the proxy core. Target is kept for
// single-backend compatibility; Backends defines a load-balanced backend pool.
type Route struct {
	Prefix                     string
	Target                     string
	Backends                   []string
	Balance                    string
	HealthCheckPath            string
	HealthCheckIntervalSeconds int
	Role                       string
}

// Handler owns proxy pools and their active health checks. Call Close when a
// replaced handler has drained so its health-check workers stop cleanly.
type Handler struct {
	table     *routing.Table
	pools     map[string]*backendPool
	transport http.RoundTripper
	trusted   trustedproxy.Policy
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// NewHandler constructs the standalone reverse-proxy core. It does not depend
// on JWT, RBAC, rate limiting, revocation, or Redis. Those remain optional
// gateway middleware concerns.
func NewHandler(routes []Route, transport http.RoundTripper, policies ...trustedproxy.Policy) (http.Handler, error) {
	return NewManagedHandler(routes, transport, policies...)
}

// NewManagedHandler returns a closeable core handler. Call Close when the
// handler is no longer used to stop active health checks and drain its pools.
func NewManagedHandler(routes []Route, transport http.RoundTripper, policies ...trustedproxy.Policy) (*Handler, error) {
	routingRoutes := make([]routing.Route, 0, len(routes))
	for _, route := range routes {
		routingRoutes = append(routingRoutes, routing.Route{Prefix: route.Prefix, Role: route.Role})
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("at least one route is required")
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	table, err := routing.NewTable(routingRoutes)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	trusted := trustedproxy.Policy{}
	if len(policies) > 0 {
		trusted = policies[0]
	}
	h := &Handler{table: table, pools: make(map[string]*backendPool, len(routes)), transport: transport, trusted: trusted, cancel: cancel}
	for _, route := range routes {
		pool, err := newBackendPool(route)
		if err != nil {
			return nil, fmt.Errorf("route %s: %w", route.Prefix, err)
		}
		h.pools[route.Prefix] = pool
	}
	for _, pool := range h.pools {
		pool.startHealthChecks(ctx, &h.wg, transport)
	}
	return h, nil
}

// Close drains every pool from new selections and stops background health
// checks. Requests that already acquired a backend retain it until completion.
func (h *Handler) Close() {
	h.closeOnce.Do(func() {
		for _, pool := range h.pools {
			pool.Drain()
		}
		h.cancel()
		h.wg.Wait()
	})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, matched := h.table.Match(r.URL.Path)
	if !matched {
		httpio.WriteError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND", "Not found")
		return
	}
	pool := h.pools[route.Prefix]
	backend, finish, allowed := pool.Acquire()
	if !allowed {
		w.Header().Set("Retry-After", "30")
		httpio.WriteError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "No healthy backend available")
		return
	}
	defer pool.Release(backend)
	var upstreamFailed, clientBodyFailed atomic.Bool
	completed, gotResponse := false, false
	defer func() {
		outcome := circuitbreaker.Neutral
		if r.Context().Err() == nil && !clientBodyFailed.Load() {
			if upstreamFailed.Load() {
				outcome = circuitbreaker.Failure
			} else if completed && gotResponse {
				outcome = circuitbreaker.Success
			}
		}
		finish(outcome)
	}()
	req := r.Clone(r.Context())
	if req.Body != nil {
		req.Body = &errorBody{ReadCloser: req.Body, onError: func() { clientBodyFailed.Store(true) }}
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			rewriteURLPath(pr.Out.URL, route.Prefix)
			pr.SetURL(backend.target)
			pr.Out.Host = pr.In.Host
			h.setForwardedHeaders(pr)
		},
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			start := time.Now()
			response, err := h.transport.RoundTrip(req)
			metrics.UpstreamDuration.WithLabelValues(route.Prefix).Observe(time.Since(start).Seconds())
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					metrics.Timeouts.WithLabelValues("upstream_timeout").Inc()
				}
				upstreamFailed.Store(true)
				return response, err
			}
			gotResponse = true
			if response.StatusCode >= 500 {
				upstreamFailed.Store(true)
			}
			// Upgrade bodies must retain their io.ReadWriteCloser capability.
			if response.StatusCode != http.StatusSwitchingProtocols {
				response.Body = &errorBody{ReadCloser: response.Body, onError: func() { upstreamFailed.Store(true) }}
			}
			return response, nil
		}),
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			if r.Context().Err() != nil {
				return
			}
			slog.Error("upstream error", "route", route.Prefix, "backend", backend.target.Host, "error", err, "request_id", r.Header.Get("X-Request-ID"))
			httpio.WriteError(w, req, http.StatusBadGateway, "BAD_GATEWAY", "Bad gateway")
		},
	}
	rp.ServeHTTP(w, req)
	completed = true
}

func (h *Handler) setForwardedHeaders(pr *httputil.ProxyRequest) {
	headers := pr.Out.Header
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-IP"} {
		headers.Del(name)
	}
	if forwardedFor := h.trusted.ForwardedFor(pr.In); forwardedFor != "" {
		headers.Set("X-Forwarded-For", forwardedFor)
	}
	if clientIP := h.trusted.ClientIP(pr.In); clientIP != "" {
		headers.Set("X-Real-IP", clientIP)
	}
	if pr.In.Host != "" {
		headers.Set("X-Forwarded-Host", pr.In.Host)
	}
	if pr.In.TLS != nil {
		headers.Set("X-Forwarded-Proto", "https")
	} else {
		headers.Set("X-Forwarded-Proto", "http")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type errorBody struct {
	io.ReadCloser
	onError func()
}

func (b *errorBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		b.onError()
	}
	return n, err
}

func rewritePath(path, prefix string) string {
	after := strings.TrimPrefix(path, prefix)
	if after == "" {
		return "/"
	}
	return after
}

// rewriteURLPath strips the decoded mount from both URL representations.
// The mount's separating slash is structural (even when written as %2F).
// Escapes in the remaining suffix are kept byte-for-byte, including their case.
func rewriteURLPath(u *url.URL, prefix string) {
	if prefix == "" {
		return
	}
	escaped := u.EscapedPath()
	// EscapedPath is a valid encoding of Path. Count decoded bytes so encoded
	// characters within the mount do not cause us to cut the wrong raw prefix.
	end := 0
	for range len(prefix) {
		if escaped[end] == '%' {
			end += 3
		} else {
			end++
		}
	}
	suffix := escaped[end:]
	if suffix == "" {
		suffix = "/"
	} else if suffix[0] == '%' {
		// Prefix() guarantees this next decoded byte is the mount separator '/'.
		suffix = "/" + suffix[3:]
	}
	u.Path = rewritePath(u.Path, prefix)
	u.RawPath = suffix
}
