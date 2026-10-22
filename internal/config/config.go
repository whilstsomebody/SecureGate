package config

import (
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
)

type Config struct {
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
}

// Cfg is the global config instance loaded at startup.
var Cfg *Config

// Load validates all settings before publishing the global configuration.
func Load() (*Config, error) {
	if err := LoadENV(); err != nil {
		return nil, err
	}
	cfg, err := fromEnvironment(os.Getenv)
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
	}
	for _, setting := range []struct{ key, value string }{
		{"JWT_SECRET", cfg.JWTSecret}, {"JWT_ISSUER", cfg.JWTIssuer}, {"JWT_AUDIENCE", cfg.JWTAudience},
	} {
		if strings.TrimSpace(setting.value) == "" {
			return nil, fmt.Errorf("%s: must not be blank", setting.key)
		}
	}
	count, err := strconv.Atoi(value("RATE_LIMIT_COUNT", "5"))
	if err != nil || count <= 0 {
		return nil, errors.New("RATE_LIMIT_COUNT: must be a positive integer within the platform int range")
	}
	cfg.RateLimitCount = count
	seconds, err := strconv.ParseInt(value("RATE_LIMIT_WINDOW_SECONDS", "10"), 10, 64)
	if err != nil || seconds <= 0 || seconds > math.MaxInt64/int64(time.Second) {
		return nil, errors.New("RATE_LIMIT_WINDOW_SECONDS: must be an integer between 1 and 9223372036")
	}
	cfg.RateLimitWindow = time.Duration(seconds) * time.Second
	for _, setting := range []struct{ key, value string }{
		{"USER_SERVICE_ADDR", cfg.UserServiceAddr}, {"ADMIN_SERVICE_ADDR", cfg.AdminServiceAddr}, {"PAYMENTS_SERVICE_ADDR", cfg.PaymentsServiceAddr},
	} {
		if !validUpstream(setting.value) {
			return nil, fmt.Errorf("%s: must be an absolute HTTP(S) URL with a valid host and port, without credentials or fragment", setting.key)
		}
	}
	if !validAddress(cfg.RedisAddr, false) {
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
