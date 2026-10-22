package proxy

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/whilstsomebody/securegate/internal/circuitbreaker"
	"github.com/whilstsomebody/securegate/internal/httpio"
	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/routing"
)

func init() {
	circuitbreaker.OnStateChange = func(name string, state circuitbreaker.State) {
		metrics.CircuitBreakerState.WithLabelValues(name).Set(float64(state))
	}
}

// Route is an explicit route definition for the proxy core. Prefix must be a
// supported decoded mount such as /users, and Target must be an absolute HTTP
// or HTTPS URL.
type Route struct {
	Prefix string
	Target string
	Role   string
}

// NewHandler constructs the standalone reverse-proxy core. It does not depend
// on JWT, RBAC, rate limiting, revocation, or Redis. Those remain optional
// gateway middleware concerns.
func NewHandler(routes []Route, transport http.RoundTripper) (http.Handler, error) {
	targets := make(map[string]*url.URL, len(routes))
	routingRoutes := make([]routing.Route, 0, len(routes))
	for _, route := range routes {
		target, err := url.Parse(route.Target)
		if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.Fragment != "" {
			return nil, fmt.Errorf("invalid upstream target for %s", route.Prefix)
		}
		if _, exists := targets[route.Prefix]; exists {
			return nil, fmt.Errorf("duplicate route prefix %s", route.Prefix)
		}
		targets[route.Prefix] = target
		routingRoutes = append(routingRoutes, routing.Route{Prefix: route.Prefix, Role: route.Role})
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("at least one route is required")
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	table, err := routing.NewTable(routingRoutes)
	if err != nil {
		return nil, err
	}
	return newCoreHandler(table, targets, transport), nil
}

func newCoreHandler(table *routing.Table, targets map[string]*url.URL, transport http.RoundTripper) http.Handler {
	breakers := map[string]*circuitbreaker.Breaker{}
	for prefix := range targets {
		breakers[prefix] = circuitbreaker.New(prefix, 5, 30*time.Second)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, matched := table.Match(r.URL.Path)
		if !matched {
			httpio.WriteError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND", "Not found")
			return
		}
		targetURL := targets[route.Prefix]
		breaker := breakers[route.Prefix]
		finish, allowed := breaker.Acquire()
		if !allowed {
			w.Header().Set("Retry-After", "30")
			httpio.WriteError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Service temporarily unavailable")
			return
		}
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
				pr.SetURL(targetURL)
				pr.Out.Host = pr.In.Host
				pr.SetXForwarded()
				pr.Out.Header.Del("X-Real-IP")
				if ip, _, err := net.SplitHostPort(pr.In.RemoteAddr); err == nil {
					pr.Out.Header.Set("X-Real-IP", ip)
				}
			},
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				start := time.Now()
				response, err := transport.RoundTrip(req)
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
				slog.Error("upstream error", "route", route.Prefix, "error", err, "request_id", r.Header.Get("X-Request-ID"))
				httpio.WriteError(w, req, http.StatusBadGateway, "BAD_GATEWAY", "Bad gateway")
			},
		}
		rp.ServeHTTP(w, req)
		completed = true
	})
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
