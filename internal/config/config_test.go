package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentDefaults(t *testing.T) {
	cfg, err := fromEnvironment(func(key string) string {
		if key == "JWT_SECRET" {
			return "test-secret"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AuthEnabled || !cfg.RateLimitEnabled || cfg.RateLimitCount != 5 || cfg.RateLimitWindow != 10*time.Second || cfg.JWTIssuer != "securegate" || cfg.JWTAudience != "securegate-api" || cfg.GatewayPort != ":8080" || cfg.RedisAddr != "127.0.0.1:6379" {
		t.Fatalf("defaults not preserved")
	}
}

func TestInvalidEnvironment(t *testing.T) {
	cases := map[string][]string{
		"ACCESS_LOG_ENABLED": {"typo"},
		"AUTH_ENABLED":       {"typo"},
		"RATE_LIMIT_ENABLED": {"typo"},
		"JWT_SECRET":         {"", " \t"},
		"JWT_ISSUER":         {" \t"}, "JWT_AUDIENCE": {" \t"},
		"RATE_LIMIT_COUNT":          {"abc", "0", "-1", "1.5", " 5", "9999999999999999999999999"},
		"RATE_LIMIT_WINDOW_SECONDS": {"abc", "0", "-1", "1.5", "9223372037", "18446744074", "9223372036854775808"},
		"REDIS_ADDR":                {"localhost", ":6379", "redis://localhost:6379", "localhost:0", "localhost:65536", "localhost:http", "localhost:+12", "bad host:6379", "::1:6379", "[localhost]:6379"},
		"GATEWAY_PORT":              {"8080", ":0", ":65536", ":-1", "localhost:", "http://localhost:8080", "bad host:8080"},
	}
	for _, key := range []string{"USER_SERVICE_ADDR", "ADMIN_SERVICE_ADDR", "PAYMENTS_SERVICE_ADDR"} {
		cases[key] = []string{"localhost:9001", "/relative", "ftp://localhost", "http:///path", "http://:80", "http://host:0", "http://host:65536", "http://host:", "http://host:abc", "http://user:password@host", "http://host/#fragment", "http://host/#", "http://bad host", "http://[localhost]", "http://2001:db8::1:80", "http://host/%zz"}
	}
	for key, values := range cases {
		for _, value := range values {
			t.Run(key+"/"+value, func(t *testing.T) {
				cfg, err := fromEnvironment(func(k string) string {
					if k == key {
						return value
					}
					if k == "JWT_SECRET" {
						return "test-secret"
					}
					return ""
				})
				if err == nil || cfg != nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("expected error identifying %s, got config=%v err=%v", key, cfg != nil, err)
				}
				if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "test-secret") {
					t.Fatal("error exposes secret")
				}
			})
		}
	}
}

func TestPolicyFreeModeDoesNotRequireJWTOrRedis(t *testing.T) {
	cfg, err := fromEnvironment(func(key string) string {
		switch key {
		case "AUTH_ENABLED", "RATE_LIMIT_ENABLED":
			return "false"
		case "REDIS_ADDR":
			return "not-a-redis-address"
		case "RATE_LIMIT_COUNT", "RATE_LIMIT_WINDOW_SECONDS":
			return "not-used"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("policy-free configuration rejected: %v", err)
	}
	if cfg.AuthEnabled || cfg.RateLimitEnabled || cfg.RequiresRedis() {
		t.Fatalf("policies unexpectedly enabled: %+v", cfg)
	}
}

func TestRoutesFileOverridesLegacyRoutesAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes.json")
	write := func(contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"routes":[{"prefix":"/catalog/v1","target":"https://catalog.example/v1"}]}`)
	cfg, err := fromEnvironment(func(key string) string {
		switch key {
		case "AUTH_ENABLED", "RATE_LIMIT_ENABLED":
			return "false"
		case "ROUTES_FILE":
			return path
		case "USER_SERVICE_ADDR":
			return "not a URL"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Prefix != "/catalog/v1" {
		t.Fatalf("routes file was not used: %#v", cfg.Routes)
	}

	write(`{"routes":[{"prefix":"/catalog/v2","target":"https://catalog.example/v2"}]}`)
	routes, err := cfg.LoadRoutes()
	if err != nil || len(routes) != 1 || routes[0].Prefix != "/catalog/v2" {
		t.Fatalf("reload routes=%#v err=%v", routes, err)
	}
	write(`{"routes":[{"prefix":"/catalog/v2","target":"ftp://catalog.example"}]}`)
	if _, err := cfg.LoadRoutes(); err == nil || !strings.Contains(err.Error(), "ROUTES_FILE") {
		t.Fatalf("invalid route file accepted: %v", err)
	}
}

func TestRouteBackendPoolValidation(t *testing.T) {
	valid := []RouteConfig{{
		Prefix: "/catalog", Backends: []string{"https://catalog-a.example", "https://catalog-b.example"},
		Balance: "least_connections", HealthCheckPath: "/healthz", HealthCheckIntervalSeconds: 10,
	}}
	if _, err := loadRoutes("", valid, false); err != nil {
		t.Fatalf("valid backend pool rejected: %v", err)
	}
	for _, route := range []RouteConfig{
		{Prefix: "/catalog", Target: "https://catalog.example", Backends: []string{"https://catalog-b.example"}},
		{Prefix: "/catalog", Backends: []string{"https://catalog.example", "https://catalog.example"}},
		{Prefix: "/catalog", Backends: []string{"https://catalog.example"}, Balance: "random"},
		{Prefix: "/catalog", Backends: []string{"https://catalog.example"}, HealthCheckPath: "health"},
		{Prefix: "/catalog", Backends: []string{"https://catalog.example"}, HealthCheckIntervalSeconds: 3601},
	} {
		if _, err := loadRoutes("", []RouteConfig{route}, false); err == nil {
			t.Fatalf("invalid pool accepted: %#v", route)
		}
	}
}

func TestTrustedProxyCIDRValidation(t *testing.T) {
	prefixes, err := parseTrustedProxyCIDRs("10.0.0.0/8, 2001:db8::/32")
	if err != nil || len(prefixes) != 2 {
		t.Fatalf("valid trusted proxies = %#v, %v", prefixes, err)
	}
	for _, raw := range []string{"10.0.0.1/8", "10.0.0.0/8,10.0.0.0/8", "localhost", "10.0.0.0/33", "10.0.0.0/8,"} {
		if _, err := parseTrustedProxyCIDRs(raw); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXY_CIDRS") {
			t.Fatalf("invalid trusted proxy CIDRs accepted: %q (%v)", raw, err)
		}
	}
	cfg, err := fromEnvironment(func(key string) string {
		if key == "JWT_SECRET" {
			return "test-secret"
		}
		if key == "TRUSTED_PROXY_CIDRS" {
			return "10.0.0.0/8"
		}
		return ""
	})
	if err != nil || len(cfg.TrustedProxyCIDRs) != 1 {
		t.Fatalf("configuration did not retain trusted proxies: %#v, %v", cfg, err)
	}
}

func TestTLSConfiguration(t *testing.T) {
	certFile, keyFile := writeTestTLSKeyPair(t)
	cfg := &Config{TLSCertFile: certFile, TLSKeyFile: keyFile}
	tlsConfig, err := cfg.TLSConfig()
	if err != nil || tlsConfig == nil || tlsConfig.MinVersion != 0x0303 || len(tlsConfig.Certificates) != 1 {
		t.Fatalf("TLS configuration = %#v, %v", tlsConfig, err)
	}
	if len(tlsConfig.NextProtos) != 2 || tlsConfig.NextProtos[0] != "h2" || tlsConfig.NextProtos[1] != "http/1.1" {
		t.Fatalf("NextProtos = %v", tlsConfig.NextProtos)
	}
	if !cfg.TLSEnabled() {
		t.Fatal("TLS should be enabled")
	}
	loaded, err := fromEnvironment(func(key string) string {
		switch key {
		case "JWT_SECRET":
			return "test-secret"
		case "TLS_CERT_FILE":
			return certFile
		case "TLS_KEY_FILE":
			return keyFile
		}
		return ""
	})
	if err != nil || !loaded.TLSEnabled() {
		t.Fatalf("environment TLS configuration rejected: %#v, %v", loaded, err)
	}
	for _, invalid := range []*Config{{TLSCertFile: certFile}, {TLSKeyFile: keyFile}, {TLSCertFile: certFile, TLSKeyFile: certFile}} {
		if _, err := invalid.TLSConfig(); err == nil {
			t.Fatalf("invalid TLS configuration accepted: %#v", invalid)
		}
	}
}

func writeTestTLSKeyPair(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "securegate.test"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"securegate.test"}, KeyUsage: x509.KeyUsageDigitalSignature,
	}, &x509.Certificate{SerialNumber: big.NewInt(1)}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestValidEnvironmentOverrides(t *testing.T) {
	env := map[string]string{
		"JWT_SECRET": "test-secret", "JWT_ISSUER": "issuer", "JWT_AUDIENCE": "audience",
		"RATE_LIMIT_COUNT": "123", "RATE_LIMIT_WINDOW_SECONDS": "9223372036",
		"REDIS_ADDR": "[::1]:6379", "GATEWAY_PORT": "[::]:8080",
		"USER_SERVICE_ADDR":     "https://example.com/base%2Fpath?tenant=one",
		"ADMIN_SERVICE_ADDR":    "http://[2001:db8::1]:9003/base",
		"PAYMENTS_SERVICE_ADDR": "http://payments_service:9002",
	}
	cfg, err := fromEnvironment(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimitCount != 123 || cfg.RateLimitWindow != 9223372036*time.Second || cfg.JWTIssuer != "issuer" || cfg.JWTAudience != "audience" || cfg.UserServiceAddr != env["USER_SERVICE_ADDR"] || cfg.GatewayPort != "[::]:8080" {
		t.Fatal("overrides not preserved")
	}
	for _, address := range []string{"http://localhost", "https://example.com.", "http://[::1]", "http://[fe80::1%25en0]:80", "http://127.0.0.1:65535"} {
		if !validUpstream(address) {
			t.Errorf("rejected valid upstream %q", address)
		}
	}
}

func TestLoadPublishesOnlyValidConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, key := range []string{"JWT_ISSUER", "JWT_AUDIENCE", "REDIS_ADDR", "RATE_LIMIT_COUNT", "RATE_LIMIT_WINDOW_SECONDS", "USER_SERVICE_ADDR", "ADMIN_SERVICE_ADDR", "PAYMENTS_SERVICE_ADDR", "GATEWAY_PORT", "MANAGEMENT_ADDR", "ACCESS_LOG_ENABLED", "TRUSTED_PROXY_CIDRS", "TLS_CERT_FILE", "TLS_KEY_FILE"} {
		t.Setenv(key, "")
	}
	t.Setenv("JWT_SECRET", "test-secret")
	original := Cfg
	t.Cleanup(func() { Cfg = original })
	cfg, err := Load()
	if err != nil || cfg == nil || Cfg != cfg {
		t.Fatalf("valid load failed: %v", err)
	}
	t.Setenv("RATE_LIMIT_COUNT", "typo")
	if result, err := Load(); err == nil || result != nil || Cfg != cfg {
		t.Fatal("invalid load published partial config")
	}
}

func TestDotenvErrorsAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := loadENV(filepath.Join(dir, "missing")); err != nil {
		t.Fatal(err)
	}
	if err := loadENV(dir); err == nil {
		t.Fatal("unreadable .env accepted")
	}
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("PRIVATE='secret-not-for-logs\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := loadENV(path)
	if err == nil || strings.Contains(err.Error(), "secret-not-for-logs") {
		t.Fatalf("invalid dotenv error: %v", err)
	}
	t.Setenv("JWT_SECRET", "from-environment")
	if err := os.WriteFile(path, []byte("JWT_SECRET=from-file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadENV(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("JWT_SECRET") != "from-environment" {
		t.Fatal("dotenv overrode environment")
	}
}

func TestCheckAndSummary(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("JWT_SECRET", "test-secret-value")
	cfg, err := Check()
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	summary := cfg.Summary()
	if !strings.Contains(summary, "Configuration syntax and values:") {
		t.Fatalf("unexpected summary: %s", summary)
	}
	if !strings.Contains(summary, "/users ->") || !strings.Contains(summary, "Max Connections:") {
		t.Fatalf("summary missing essential keys: %s", summary)
	}
	if !strings.Contains(summary, "Trusted Proxies:      none") {
		t.Fatalf("summary missing unset trusted proxies: %s", summary)
	}
	if !strings.Contains(summary, "TLS Termination:      false") {
		t.Fatalf("summary missing unset TLS status: %s", summary)
	}

	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 192.168.0.0/16")
	certFile, keyFile := writeTestTLSKeyPair(t)
	t.Setenv("TLS_CERT_FILE", certFile)
	t.Setenv("TLS_KEY_FILE", keyFile)
	cfgWithProxiesAndTLS, err := Check()
	if err != nil {
		t.Fatalf("check with proxies and TLS failed: %v", err)
	}
	summaryFull := cfgWithProxiesAndTLS.Summary()
	if !strings.Contains(summaryFull, "Trusted Proxies:      10.0.0.0/8, 192.168.0.0/16") {
		t.Fatalf("summary missing formatted trusted proxies: %s", summaryFull)
	}
	if !strings.Contains(summaryFull, "TLS Termination:      true") || !strings.Contains(summaryFull, certFile) || !strings.Contains(summaryFull, keyFile) {
		t.Fatalf("summary missing TLS configuration details: %s", summaryFull)
	}
}
