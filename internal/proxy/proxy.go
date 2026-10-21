package proxy

import (
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
	"github.com/whilstsomebody/securegate/internal/config"
)

func NewProxyhandler() http.Handler {
	return newProxyHandler(readIdleTransport(upstreamTransport, 60*time.Second))
}

func newProxyHandler(transport http.RoundTripper) http.Handler {
	breakers := map[string]*circuitbreaker.Breaker{}
	for _, prefix := range []string{"/users", "/payments", "/admin"} {
		breakers[prefix] = circuitbreaker.New(prefix, 5, 30*time.Second)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := getTargetService(r.URL.Path)
		if target == "" {
			http.NotFound(w, r)
			return
		}
		targetURL, err := url.Parse(target)
		if err != nil || targetURL.Host == "" || (targetURL.Scheme != "http" && targetURL.Scheme != "https") {
			http.Error(w, "Invalid target URL", http.StatusInternalServerError)
			return
		}
		var breaker *circuitbreaker.Breaker
		for prefix, b := range breakers {
			if strings.HasPrefix(r.URL.Path, prefix) {
				breaker = b
				break
			}
		}
		finish, allowed := breaker.Acquire()
		if !allowed {
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
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
				pr.Out.URL.Path = rewritePath(pr.In.URL.Path)
				pr.SetURL(targetURL)
				pr.Out.Host = pr.In.Host
				pr.SetXForwarded()
				pr.Out.Header.Del("X-Real-IP")
				if ip, _, err := net.SplitHostPort(pr.In.RemoteAddr); err == nil {
					pr.Out.Header.Set("X-Real-IP", ip)
				}
			},
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				response, err := transport.RoundTrip(req)
				if err != nil {
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
				slog.Error("upstream error", "path", r.URL.Path, "error", err, "request_id", r.Header.Get("X-Request-ID"))
				http.Error(w, "Bad gateway", http.StatusBadGateway)
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

func getTargetService(path string) string {
	cfg := config.Cfg
	if strings.HasPrefix(path, "/users") {
		if cfg != nil {
			return cfg.UserServiceAddr
		}
		return "http://localhost:9001"
	}
	if strings.HasPrefix(path, "/payments") {
		if cfg != nil {
			return cfg.PaymentsServiceAddr
		}
		return "http://localhost:9002"
	}
	if strings.HasPrefix(path, "/admin") {
		if cfg != nil {
			return cfg.AdminServiceAddr
		}
		return "http://localhost:9003"
	}
	return ""
}

func rewritePath(path string) string {
	for _, prefix := range []string{"/users", "/payments", "/admin"} {
		if after, ok := strings.CutPrefix(path, prefix); ok {
			if after == "" {
				return "/"
			}
			return after
		}
	}
	return path
}
