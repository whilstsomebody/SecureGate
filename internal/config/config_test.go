package config

import (
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
	if cfg.RateLimitCount != 5 || cfg.RateLimitWindow != 10*time.Second || cfg.JWTIssuer != "securegate" || cfg.JWTAudience != "securegate-api" || cfg.GatewayPort != ":8080" || cfg.RedisAddr != "127.0.0.1:6379" {
		t.Fatalf("defaults not preserved")
	}
}

func TestInvalidEnvironment(t *testing.T) {
	cases := map[string][]string{
		"ACCESS_LOG_ENABLED": {"typo"},
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
	for _, key := range []string{"JWT_ISSUER", "JWT_AUDIENCE", "REDIS_ADDR", "RATE_LIMIT_COUNT", "RATE_LIMIT_WINDOW_SECONDS", "USER_SERVICE_ADDR", "ADMIN_SERVICE_ADDR", "PAYMENTS_SERVICE_ADDR", "GATEWAY_PORT", "MANAGEMENT_ADDR", "ACCESS_LOG_ENABLED"} {
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
	if !strings.Contains(summary, "/users:") || !strings.Contains(summary, "Max Connections:") {
		t.Fatalf("summary missing essential keys: %s", summary)
	}
}

