package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/whilstsomebody/securegate/internal/circuitbreaker"
	"github.com/whilstsomebody/securegate/internal/metrics"
)

const (
	roundRobin       = "round_robin"
	leastConnections = "least_connections"
)

type backend struct {
	target   *url.URL
	route    string
	label    string
	healthy  atomic.Bool
	draining atomic.Bool
	active   atomic.Int64
	breaker  *circuitbreaker.Breaker
}

type backendPool struct {
	backends       []*backend
	balance        string
	healthPath     string
	healthInterval time.Duration
	healthChecks   bool
	next           atomic.Uint64
	draining       atomic.Bool
}

func newBackendPool(route Route) (*backendPool, error) {
	if route.Target != "" && len(route.Backends) != 0 {
		return nil, fmt.Errorf("target and backends cannot both be set")
	}
	targets := route.Backends
	if route.Target != "" {
		targets = []string{route.Target}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("at least one backend is required")
	}
	balance := route.Balance
	if balance == "" {
		balance = roundRobin
	}
	if balance != roundRobin && balance != leastConnections {
		return nil, fmt.Errorf("unsupported balance algorithm %q", balance)
	}
	healthPath := route.HealthCheckPath
	if healthPath == "" {
		healthPath = "/health"
	}
	if !strings.HasPrefix(healthPath, "/") || strings.ContainsAny(healthPath, "?#") {
		return nil, fmt.Errorf("invalid health check path")
	}
	interval := route.HealthCheckIntervalSeconds
	if interval == 0 {
		interval = 5
	}
	if interval < 1 || interval > 3600 {
		return nil, fmt.Errorf("health check interval must be between 1 and 3600 seconds")
	}
	pool := &backendPool{
		balance: balance, healthPath: healthPath, healthInterval: time.Duration(interval) * time.Second,
		// Legacy single-target routes retain their previous request-only
		// behavior. Every explicit backend pool gets active checks by default.
		healthChecks: len(route.Backends) > 0 || route.HealthCheckPath != "" || route.HealthCheckIntervalSeconds != 0,
	}
	seen := make(map[string]struct{}, len(targets))
	for _, raw := range targets {
		target, err := url.Parse(raw)
		if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.Fragment != "" {
			return nil, fmt.Errorf("invalid backend target")
		}
		if _, exists := seen[target.String()]; exists {
			return nil, fmt.Errorf("duplicate backend target")
		}
		seen[target.String()] = struct{}{}
		b := &backend{target: target, route: route.Prefix, label: target.String(), breaker: circuitbreaker.New(route.Prefix+"\x00"+target.String(), 5, 30*time.Second)}
		b.healthy.Store(true)
		metrics.BackendHealth.WithLabelValues(b.route, b.label).Set(1)
		metrics.BackendActive.WithLabelValues(b.route, b.label).Set(0)
		pool.backends = append(pool.backends, b)
	}
	return pool, nil
}

func (p *backendPool) startHealthChecks(ctx context.Context, wg *sync.WaitGroup, transport http.RoundTripper) {
	if !p.healthChecks {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.checkAll(ctx, transport)
		ticker := time.NewTicker(p.healthInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.checkAll(ctx, transport)
			}
		}
	}()
}

func (p *backendPool) checkAll(ctx context.Context, transport http.RoundTripper) {
	for _, b := range p.backends {
		if p.draining.Load() || b.draining.Load() || ctx.Err() != nil {
			return
		}
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		u := *b.target
		u.Path, u.RawPath, u.RawQuery = p.healthPath, "", ""
		req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, u.String(), nil)
		if err != nil {
			p.setHealthy(b, false)
			cancel()
			continue
		}
		response, err := transport.RoundTrip(req)
		if err == nil && response != nil {
			if response.Body != nil {
				response.Body.Close()
			}
			p.setHealthy(b, response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusBadRequest)
		} else {
			p.setHealthy(b, false)
		}
		cancel()
	}
}

// Acquire selects a healthy, non-draining backend and reserves one active
// request slot. The returned completion function advances that backend's
// circuit breaker exactly once.
func (p *backendPool) Acquire() (*backend, func(circuitbreaker.Outcome), bool) {
	if p.draining.Load() {
		return nil, nil, false
	}
	candidates := make([]*backend, 0, len(p.backends))
	for _, backend := range p.backends {
		if backend.healthy.Load() && !backend.draining.Load() {
			candidates = append(candidates, backend)
		}
	}
	if len(candidates) == 0 {
		return nil, nil, false
	}
	if p.balance == leastConnections {
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].active.Load() < candidates[j].active.Load() })
	} else {
		start := int((p.next.Add(1) - 1) % uint64(len(candidates)))
		candidates = append(candidates[start:], candidates[:start]...)
	}
	for _, backend := range candidates {
		backend.active.Add(1)
		metrics.BackendActive.WithLabelValues(backend.route, backend.label).Set(float64(backend.active.Load()))
		if p.draining.Load() || backend.draining.Load() || !backend.healthy.Load() {
			backend.active.Add(-1)
			metrics.BackendActive.WithLabelValues(backend.route, backend.label).Set(float64(backend.active.Load()))
			continue
		}
		finish, allowed := backend.breaker.Acquire()
		if !allowed {
			backend.active.Add(-1)
			metrics.BackendActive.WithLabelValues(backend.route, backend.label).Set(float64(backend.active.Load()))
			continue
		}
		return backend, finish, true
	}
	return nil, nil, false
}

func (p *backendPool) Release(backend *backend) {
	backend.active.Add(-1)
	metrics.BackendActive.WithLabelValues(backend.route, backend.label).Set(float64(backend.active.Load()))
}

func (p *backendPool) setHealthy(backend *backend, healthy bool) {
	backend.healthy.Store(healthy)
	health := 0.0
	if healthy {
		health = 1
	}
	metrics.BackendHealth.WithLabelValues(backend.route, backend.label).Set(health)
}

// Drain immediately removes all backends from new selection while preserving
// already acquired requests until their normal completion.
func (p *backendPool) Drain() {
	p.draining.Store(true)
	for _, backend := range p.backends {
		backend.draining.Store(true)
	}
}
