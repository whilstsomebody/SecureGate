package config

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/whilstsomebody/securegate/internal/routing"
	"github.com/whilstsomebody/securegate/internal/trustedproxy"
)

type RouteConfig struct {
	Prefix                     string   `json:"prefix"`
	Target                     string   `json:"target,omitempty"`
	Backends                   []string `json:"backends,omitempty"`
	Balance                    string   `json:"balance,omitempty"`
	HealthCheckPath            string   `json:"health_check_path,omitempty"`
	HealthCheckIntervalSeconds int      `json:"health_check_interval_seconds,omitempty"`
	Role                       string   `json:"role,omitempty"`
}

type Config struct {
	Limits              ResourceLimits
	AccessLogEnabled    bool
	AuthEnabled         bool
	RateLimitEnabled    bool
	JWTSecret           string
	JWTIssuer           string
	JWTAudience         string
	RedisAddr           string
	RateLimitCount      int
	RateLimitWindow     time.Duration
	UserServiceAddr     string
	AdminServiceAddr    string
	PaymentsServiceAddr string
	GatewayPort         string
	ManagementAddr      string
	TLSCertFile         string
	TLSKeyFile          string
	TrustedProxyCIDRs   []netip.Prefix
	trustedProxies      trustedproxy.Policy
	RoutesFile          string
	Routes              []RouteConfig
}

// Cfg is the global config instance loaded at startup.
var Cfg *Config

// Check validates environment and file configuration without setting global state.
func Check() (*Config, error) {
	if err := LoadENV(); err != nil {
		return nil, err
	}
	return fromEnvironment(os.Getenv)
}

// Load validates all settings before publishing the global configuration.
func Load() (*Config, error) {
	cfg, err := Check()
	if err != nil {
		return nil, err
	}
	Cfg = cfg
	return cfg, nil
}

// LoadENV permits an absent .env, but rejects unreadable or malformed files.
func LoadENV() error {
	return loadENV(".env")
}

func loadENV(path string) error {
	if err := godotenv.Load(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Parser errors may include file contents, including secrets.
		return errors.New(".env: unable to read or parse configuration file")
	}
	return nil
}

func fromEnvironment(get func(string) string) (*Config, error) {
	value := func(key, fallback string) string {
		if v := get(key); v != "" {
			return v
		}
		return fallback
	}
	cfg := &Config{
		JWTSecret:           get("JWT_SECRET"),
		JWTIssuer:           value("JWT_ISSUER", "securegate"),
		JWTAudience:         value("JWT_AUDIENCE", "securegate-api"),
		RedisAddr:           value("REDIS_ADDR", "127.0.0.1:6379"),
		UserServiceAddr:     value("USER_SERVICE_ADDR", "http://localhost:9001"),
		AdminServiceAddr:    value("ADMIN_SERVICE_ADDR", "http://localhost:9003"),
		PaymentsServiceAddr: value("PAYMENTS_SERVICE_ADDR", "http://localhost:9002"),
		GatewayPort:         value("GATEWAY_PORT", ":8080"),
		ManagementAddr:      value("MANAGEMENT_ADDR", "127.0.0.1:9000"),
		TLSCertFile:         get("TLS_CERT_FILE"),
		TLSKeyFile:          get("TLS_KEY_FILE"),
		RoutesFile:          get("ROUTES_FILE"),
	}
	for _, feature := range []struct {
		key    string
		target *bool
	}{
		{"AUTH_ENABLED", &cfg.AuthEnabled},
		{"RATE_LIMIT_ENABLED", &cfg.RateLimitEnabled},
	} {
		enabled, err := strconv.ParseBool(value(feature.key, "true"))
		if err != nil {
			return nil, fmt.Errorf("%s: must be a boolean", feature.key)
		}
		*feature.target = enabled
	}
	if cfg.AuthEnabled {
		for _, setting := range []struct{ key, value string }{
			{"JWT_SECRET", cfg.JWTSecret}, {"JWT_ISSUER", cfg.JWTIssuer}, {"JWT_AUDIENCE", cfg.JWTAudience},
		} {
			if strings.TrimSpace(setting.value) == "" {
				return nil, fmt.Errorf("%s: must not be blank", setting.key)
			}
		}
	}
	accessLog, err := strconv.ParseBool(value("ACCESS_LOG_ENABLED", "false"))
	if err != nil {
		return nil, errors.New("ACCESS_LOG_ENABLED: must be a boolean")
	}
	cfg.AccessLogEnabled = accessLog
	count := 5
	if cfg.RateLimitEnabled {
		count, err = strconv.Atoi(value("RATE_LIMIT_COUNT", "5"))
		if err != nil || count <= 0 {
			return nil, errors.New("RATE_LIMIT_COUNT: must be a positive integer within the platform int range")
		}
	}
	limits, err := parseLimits(get)
	if err != nil {
		return nil, err
	}
	cfg.Limits = limits
	cfg.RateLimitCount = count
	seconds := int64(10)
	if cfg.RateLimitEnabled {
		seconds, err = strconv.ParseInt(value("RATE_LIMIT_WINDOW_SECONDS", "10"), 10, 64)
		if err != nil || seconds <= 0 || seconds > math.MaxInt64/int64(time.Second) {
			return nil, errors.New("RATE_LIMIT_WINDOW_SECONDS: must be an integer between 1 and 9223372036")
		}
	}
	cfg.RateLimitWindow = time.Duration(seconds) * time.Second
	trustedProxyCIDRs, err := parseTrustedProxyCIDRs(get("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return nil, err
	}
	cfg.TrustedProxyCIDRs = trustedProxyCIDRs
	cfg.trustedProxies = trustedproxy.New(trustedProxyCIDRs)
	if _, err := cfg.TLSConfig(); err != nil {
		return nil, err
	}
	// Preserve precise diagnostics for the legacy environment-based route
	// settings. When ROUTES_FILE is configured it is the complete route source,
	// so these values are intentionally ignored.
	if cfg.RoutesFile == "" {
		for _, setting := range []struct{ key, value string }{
			{"USER_SERVICE_ADDR", cfg.UserServiceAddr},
			{"ADMIN_SERVICE_ADDR", cfg.AdminServiceAddr},
			{"PAYMENTS_SERVICE_ADDR", cfg.PaymentsServiceAddr},
		} {
			if !validUpstream(setting.value) {
				return nil, fmt.Errorf("%s: must be an absolute HTTP(S) URL without credentials or fragment", setting.key)
			}
		}
	}
	routes, err := loadRoutes(cfg.RoutesFile, defaultRoutes(cfg), cfg.AuthEnabled)
	if err != nil {
		return nil, err
	}
	cfg.Routes = routes
	if cfg.RequiresRedis() && !validAddress(cfg.RedisAddr, false) {
		return nil, errors.New("REDIS_ADDR: must be host:port with port between 1 and 65535")
	}
	if !validAddress(cfg.GatewayPort, true) {
		return nil, errors.New("GATEWAY_PORT: must be host:port or :port with port between 1 and 65535")
	}
	if !validAddress(cfg.ManagementAddr, false) {
		return nil, errors.New("MANAGEMENT_ADDR: must be host:port with port between 1 and 65535")
	}
	return cfg, nil
}

// TLSEnabled reports whether the public listener terminates HTTPS.
func (c *Config) TLSEnabled() bool { return c != nil && c.TLSCertFile != "" && c.TLSKeyFile != "" }

// TLSConfig loads the configured key pair and enforces modern TLS settings.
// It returns nil when TLS is not configured.
func (c *Config) TLSConfig() (*tls.Config, error) {
	if c == nil || (c.TLSCertFile == "" && c.TLSKeyFile == "") {
		return nil, nil
	}
	if c.TLSCertFile == "" || c.TLSKeyFile == "" {
		return nil, errors.New("TLS_CERT_FILE and TLS_KEY_FILE must be set together")
	}
	certificate, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
	if err != nil {
		return nil, errors.New("TLS_CERT_FILE/TLS_KEY_FILE: unable to load certificate pair")
	}
	return &tls.Config{
		MinVersion:       tls.VersionTLS12,
		Certificates:     []tls.Certificate{certificate},
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
		NextProtos:       []string{"h2", "http/1.1"},
	}, nil
}

func parseTrustedProxyCIDRs(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	items := strings.Split(raw, ",")
	prefixes := make([]netip.Prefix, 0, len(items))
	seen := make(map[netip.Prefix]struct{}, len(items))
	for _, item := range items {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil || prefix != prefix.Masked() {
			return nil, errors.New("TRUSTED_PROXY_CIDRS: must be a comma-separated list of canonical IP prefixes")
		}
		if _, exists := seen[prefix]; exists {
			return nil, errors.New("TRUSTED_PROXY_CIDRS: must not contain duplicate prefixes")
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

// TrustedProxies returns the immutable policy used for client identity and
// safe forwarding-header propagation.
func (c *Config) TrustedProxies() trustedproxy.Policy {
	if c == nil {
		return trustedproxy.Policy{}
	}
	if len(c.TrustedProxyCIDRs) == 0 {
		return trustedproxy.Policy{}
	}
	if c.trustedProxies.Len() != 0 {
		return c.trustedProxies
	}
	return trustedproxy.New(c.TrustedProxyCIDRs)
}

func defaultRoutes(cfg *Config) []RouteConfig {
	userAddr := "http://localhost:9001"
	adminAddr := "http://localhost:9003"
	paymentsAddr := "http://localhost:9002"
	if cfg != nil {
		if cfg.UserServiceAddr != "" {
			userAddr = cfg.UserServiceAddr
		}
		if cfg.AdminServiceAddr != "" {
			adminAddr = cfg.AdminServiceAddr
		}
		if cfg.PaymentsServiceAddr != "" {
			paymentsAddr = cfg.PaymentsServiceAddr
		}
	}
	return []RouteConfig{
		{Prefix: "/users", Target: userAddr, Role: "USER"},
		{Prefix: "/admin", Target: adminAddr, Role: "ADMIN"},
		{Prefix: "/payments", Target: paymentsAddr, Role: "ADMIN"},
	}
}

// LoadRoutes reads and validates the currently configured route source. It is
// safe to use during reload because it does not mutate Config or global state.
func (c *Config) LoadRoutes() ([]RouteConfig, error) {
	if c == nil {
		return loadRoutes("", defaultRoutes(nil), true)
	}
	return loadRoutes(c.RoutesFile, defaultRoutes(c), c.AuthEnabled)
}

func loadRoutes(path string, fallback []RouteConfig, authEnabled bool) ([]RouteConfig, error) {
	routes := append([]RouteConfig(nil), fallback...)
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("ROUTES_FILE: unable to read route file")
		}
		var document struct {
			Routes []RouteConfig `json:"routes"`
		}
		if err := json.Unmarshal(data, &document); err != nil {
			return nil, errors.New("ROUTES_FILE: must contain valid JSON")
		}
		routes = document.Routes
	}
	routingRoutes := make([]routing.Route, 0, len(routes))
	for _, route := range routes {
		backends, err := routeBackends(route)
		if err != nil {
			return nil, fmt.Errorf("ROUTES_FILE: route %q: %w", route.Prefix, err)
		}
		for _, backend := range backends {
			if !validUpstream(backend) {
				return nil, fmt.Errorf("ROUTES_FILE: route %q must have absolute HTTP(S) backend URLs without credentials or fragments", route.Prefix)
			}
		}
		if route.Balance != "" && route.Balance != "round_robin" && route.Balance != "least_connections" {
			return nil, fmt.Errorf("ROUTES_FILE: route %q balance must be round_robin or least_connections", route.Prefix)
		}
		if route.HealthCheckPath != "" && (!strings.HasPrefix(route.HealthCheckPath, "/") || strings.ContainsAny(route.HealthCheckPath, "?#")) {
			return nil, fmt.Errorf("ROUTES_FILE: route %q health_check_path must be an absolute path without query or fragment", route.Prefix)
		}
		if route.HealthCheckIntervalSeconds < 0 || route.HealthCheckIntervalSeconds > 3600 {
			return nil, fmt.Errorf("ROUTES_FILE: route %q health_check_interval_seconds must be between 0 and 3600", route.Prefix)
		}
		if authEnabled && strings.TrimSpace(route.Role) == "" {
			return nil, fmt.Errorf("ROUTES_FILE: route %q requires a role while AUTH_ENABLED=true", route.Prefix)
		}
		routingRoutes = append(routingRoutes, routing.Route{Prefix: route.Prefix, Role: route.Role})
	}
	if _, err := routing.NewTable(routingRoutes); err != nil {
		return nil, fmt.Errorf("ROUTES_FILE: %w", err)
	}
	return routes, nil
}

func routeBackends(route RouteConfig) ([]string, error) {
	if route.Target != "" && len(route.Backends) != 0 {
		return nil, errors.New("target and backends cannot both be set")
	}
	if route.Target != "" {
		return []string{route.Target}, nil
	}
	if len(route.Backends) == 0 {
		return nil, errors.New("must define target or at least one backend")
	}
	seen := make(map[string]struct{}, len(route.Backends))
	backends := make([]string, 0, len(route.Backends))
	for _, backend := range route.Backends {
		if _, exists := seen[backend]; exists {
			return nil, fmt.Errorf("duplicates backend %q", backend)
		}
		seen[backend] = struct{}{}
		backends = append(backends, backend)
	}
	return backends, nil
}

// RequiresRedis reports whether any enabled gateway policy needs Redis.
func (c *Config) RequiresRedis() bool {
	return c != nil && (c.AuthEnabled || c.RateLimitEnabled)
}

func validPort(port string) bool {
	if port == "" {
		return false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

func validHost(host string) bool {
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	// Validate syntax only: DNS resolution and availability are runtime concerns.
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}

func validAddress(address string, allowEmptyHost bool) bool {
	host, port, err := net.SplitHostPort(address)
	if strings.HasPrefix(address, "[") {
		if _, err := netip.ParseAddr(host); err != nil {
			return false
		}
	}
	return err == nil && validPort(port) && (allowEmptyHost && host == "" || validHost(host))
}

func validUpstream(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || strings.Contains(raw, "#") || !validHost(u.Hostname()) {
		return false
	}
	if strings.Contains(u.Hostname(), ":") && !strings.HasPrefix(u.Host, "[") {
		return false
	}
	if strings.HasPrefix(u.Host, "[") {
		if _, err := netip.ParseAddr(u.Hostname()); err != nil {
			return false
		}
	}
	return !strings.HasSuffix(u.Host, ":") && (u.Port() == "" || validPort(u.Port()))
}

// GetJWTSecret is kept for backward compatibility with the token CLI tool.
func GetJWTSecret() string {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		log.Fatal("JWT_SECRET not set")
	}
	return secret
}

func getenvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// GetJWTIssuer and GetJWTAudience share the gateway policy with the token CLI.
func GetJWTIssuer() string   { return getenvOrDefault("JWT_ISSUER", "securegate") }
func GetJWTAudience() string { return getenvOrDefault("JWT_AUDIENCE", "securegate-api") }

// Summary returns a human-readable description of the loaded configuration.
func (c *Config) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Configuration syntax and values:\n")
	fmt.Fprintf(&b, "  Gateway Listen:       %s\n", c.GatewayPort)
	fmt.Fprintf(&b, "  TLS Termination:      %t\n", c.TLSEnabled())
	if c.TLSEnabled() {
		fmt.Fprintf(&b, "  TLS Certificate:      %s\n", c.TLSCertFile)
		fmt.Fprintf(&b, "  TLS Private Key:      %s\n", c.TLSKeyFile)
	}
	fmt.Fprintf(&b, "  Management Listen:    %s\n", c.ManagementAddr)
	fmt.Fprintf(&b, "  Redis Address:        %s\n", c.RedisAddr)
	fmt.Fprintf(&b, "  Redis Required:       %t\n", c.RequiresRedis())
	fmt.Fprintf(&b, "  Access Logging:       %t\n", c.AccessLogEnabled)
	if len(c.TrustedProxyCIDRs) == 0 {
		fmt.Fprintf(&b, "  Trusted Proxies:      none\n")
	} else {
		prefixes := make([]string, len(c.TrustedProxyCIDRs))
		for i, prefix := range c.TrustedProxyCIDRs {
			prefixes[i] = prefix.String()
		}
		fmt.Fprintf(&b, "  Trusted Proxies:      %s\n", strings.Join(prefixes, ", "))
	}
	if c.RoutesFile == "" {
		fmt.Fprintf(&b, "  Route Source:         legacy environment variables\n")
	} else {
		fmt.Fprintf(&b, "  Route Source:         %s (reload with SIGHUP)\n", c.RoutesFile)
	}
	fmt.Fprintf(&b, "  Routes:\n")
	for _, route := range c.Routes {
		role := route.Role
		if role == "" {
			role = "none"
		}
		backends, _ := routeBackends(route)
		balance := route.Balance
		if balance == "" {
			balance = "round_robin"
		}
		fmt.Fprintf(&b, "    %s -> %s (balance: %s, role: %s)\n", route.Prefix, strings.Join(backends, ", "), balance, role)
	}
	fmt.Fprintf(&b, "  Rate Limiting:        enabled=%t (%d requests / %s per client IP)\n", c.RateLimitEnabled, c.RateLimitCount, c.RateLimitWindow)
	fmt.Fprintf(&b, "  JWT Policy:           enabled=%t issuer=%q audience=%q (secret configured: %t)\n", c.AuthEnabled, c.JWTIssuer, c.JWTAudience, len(c.JWTSecret) > 0)
	fmt.Fprintf(&b, "  Resource Limits:\n")
	fmt.Fprintf(&b, "    Max Connections:    %d\n", c.Limits.MaxConnections)
	fmt.Fprintf(&b, "    Max Requests:       %d (upgrades: %d)\n", c.Limits.MaxRequests, c.Limits.MaxUpgradeRequests)
	fmt.Fprintf(&b, "    Max Upstream Conns: %d\n", c.Limits.MaxUpstreamConnections)
	fmt.Fprintf(&b, "    Max Header Bytes:   %d bytes\n", c.Limits.MaxHeaderBytes)
	fmt.Fprintf(&b, "    Timeouts:\n")
	fmt.Fprintf(&b, "      Header Timeout:   %s\n", c.Limits.HeaderTimeout)
	fmt.Fprintf(&b, "      Request Timeout:  %s\n", c.Limits.RequestTimeout)
	fmt.Fprintf(&b, "      Client Read Idle: %s\n", c.Limits.ClientReadIdle)
	fmt.Fprintf(&b, "      Client Write:     %s\n", c.Limits.ClientWriteTimeout)
	fmt.Fprintf(&b, "      Keep-Alive Idle:  %s\n", c.Limits.KeepAliveTimeout)
	fmt.Fprintf(&b, "      Upstream Dial:    %s\n", c.Limits.UpstreamDialTimeout)
	fmt.Fprintf(&b, "      Upstream Header:  %s\n", c.Limits.UpstreamHeaderTimeout)
	fmt.Fprintf(&b, "      Upstream Read:    %s\n", c.Limits.UpstreamReadIdle)
	fmt.Fprintf(&b, "      Shutdown Budget:  %s\n", c.Limits.ShutdownTimeout)
	return b.String()
}
