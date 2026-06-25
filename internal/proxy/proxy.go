package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/whilstsomebody/securegate/internal/circuitbreaker"
	"github.com/whilstsomebody/securegate/internal/config"
)

// breakers holds one circuit breaker per upstream route prefix.
var breakers = map[string]*circuitbreaker.Breaker{
	"/users":    circuitbreaker.New("user-service", 5, 30*time.Second),
	"/payments": circuitbreaker.New("payments-service", 5, 30*time.Second),
	"/admin":    circuitbreaker.New("admin-service", 5, 30*time.Second),
}

func NewProxyhandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		slog.Info("proxying request", "method", r.Method, "path", r.URL.Path, "request_id", requestID)

		target := getTargetService(r.URL.Path)
		if target == "" {
			http.Error(w, "Service not Found", http.StatusNotFound)
			return
		}

		breaker := getBreakerForPath(r.URL.Path)
		if breaker != nil && !breaker.Allow() {
			slog.Warn("circuit breaker open, rejecting request", "path", r.URL.Path, "request_id", requestID)
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}

		targetURL, err := url.Parse(target)
		if err != nil {
			http.Error(w, "Invalid Target URL", http.StatusInternalServerError)
			return
		}

		rp := httputil.NewSingleHostReverseProxy(targetURL)
		rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("upstream error", "path", r.URL.Path, "error", err, "request_id", requestID)
			if breaker != nil {
				breaker.RecordFailure()
			}
			http.Error(w, "Bad gateway", http.StatusBadGateway)
		}

		r.URL.Path = rewritePath(r.URL.Path)

		rec := &proxyRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		rp.ServeHTTP(rec, r)

		if breaker != nil {
			if rec.statusCode >= 500 {
				breaker.RecordFailure()
			} else {
				breaker.RecordSuccess()
			}
		}
	})
}

// proxyRecorder captures the status code so circuit breaker can react to 5xx responses.
type proxyRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (p *proxyRecorder) WriteHeader(code int) {
	p.statusCode = code
	p.ResponseWriter.WriteHeader(code)
}

func getBreakerForPath(path string) *circuitbreaker.Breaker {
	for prefix, b := range breakers {
		if strings.HasPrefix(path, prefix) {
			return b
		}
	}
	return nil
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
