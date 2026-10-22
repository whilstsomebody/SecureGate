package config

import (
	"fmt"
	"strconv"
	"time"
)

// ResourceLimits are fixed for a process lifetime; changes require a restart.
type ResourceLimits struct {
	MaxConnections, MaxRequests, MaxUpgradeRequests, MaxUpstreamConnections, MaxHeaderBytes int
	HeaderTimeout, RequestTimeout, ClientReadIdle, ClientWriteTimeout, KeepAliveTimeout     time.Duration
	UpstreamDialTimeout, UpstreamHeaderTimeout, UpstreamReadIdle, ShutdownTimeout           time.Duration
}

func DefaultLimits() ResourceLimits {
	return ResourceLimits{
		MaxConnections: 1024, MaxRequests: 256, MaxUpgradeRequests: 32, MaxUpstreamConnections: 128, MaxHeaderBytes: 64 << 10,
		HeaderTimeout: 5 * time.Second, RequestTimeout: 30 * time.Second, ClientReadIdle: 30 * time.Second, ClientWriteTimeout: 30 * time.Second, KeepAliveTimeout: 60 * time.Second,
		UpstreamDialTimeout: 5 * time.Second, UpstreamHeaderTimeout: 15 * time.Second, UpstreamReadIdle: 60 * time.Second, ShutdownTimeout: 10 * time.Second,
	}
}

func EffectiveLimits() ResourceLimits {
	if Cfg == nil || Cfg.Limits.MaxConnections == 0 {
		return DefaultLimits()
	}
	return Cfg.Limits
}

func parseLimits(get func(string) string) (ResourceLimits, error) {
	limits := DefaultLimits()
	for _, s := range []struct {
		key    string
		target *int
		max    int
	}{
		{"MAX_CONNECTIONS", &limits.MaxConnections, 1000000}, {"MAX_REQUESTS", &limits.MaxRequests, 1000000},
		{"MAX_UPGRADE_REQUESTS", &limits.MaxUpgradeRequests, 1000000}, {"MAX_UPSTREAM_CONNECTIONS", &limits.MaxUpstreamConnections, 1000000},
		{"MAX_HEADER_BYTES", &limits.MaxHeaderBytes, 1 << 20},
	} {
		if raw := get(s.key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > s.max {
				return limits, fmt.Errorf("%s: must be an integer between 1 and %d", s.key, s.max)
			}
			*s.target = n
		}
	}
	if limits.MaxUpgradeRequests > limits.MaxRequests {
		return limits, fmt.Errorf("MAX_UPGRADE_REQUESTS: must not exceed MAX_REQUESTS")
	}
	for _, s := range []struct {
		key    string
		target *time.Duration
		min    int
	}{
		{"HEADER_TIMEOUT_SECONDS", &limits.HeaderTimeout, 1}, {"REQUEST_TIMEOUT_SECONDS", &limits.RequestTimeout, 0},
		{"CLIENT_READ_IDLE_SECONDS", &limits.ClientReadIdle, 1}, {"CLIENT_WRITE_TIMEOUT_SECONDS", &limits.ClientWriteTimeout, 1},
		{"KEEPALIVE_TIMEOUT_SECONDS", &limits.KeepAliveTimeout, 1}, {"UPSTREAM_DIAL_TIMEOUT_SECONDS", &limits.UpstreamDialTimeout, 1},
		{"UPSTREAM_HEADER_TIMEOUT_SECONDS", &limits.UpstreamHeaderTimeout, 1}, {"UPSTREAM_READ_IDLE_SECONDS", &limits.UpstreamReadIdle, 1},
		{"SHUTDOWN_TIMEOUT_SECONDS", &limits.ShutdownTimeout, 1},
	} {
		if raw := get(s.key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < s.min || n > 86400 {
				return limits, fmt.Errorf("%s: must be an integer between %d and 86400", s.key, s.min)
			}
			*s.target = time.Duration(n) * time.Second
		}
	}
	return limits, nil
}
