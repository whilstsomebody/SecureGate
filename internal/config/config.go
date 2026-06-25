package config

import (
	"log"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	JWTSecret           string
	RedisAddr           string
	RateLimitCount      int
	RateLimitWindow     time.Duration
	UserServiceAddr     string
	AdminServiceAddr    string
	PaymentsServiceAddr string
	GatewayPort         string
}

// Cfg is the global config instance loaded at startup.
var Cfg *Config

// Load reads .env (if present) and populates Cfg from environment variables.
func Load() *Config {
	if err := godotenv.Load(); err != nil {
		slog.Warn("no .env file found, reading from environment")
	}

	cfg := &Config{
		JWTSecret:           mustGetenv("JWT_SECRET"),
		RedisAddr:           getenvOrDefault("REDIS_ADDR", "127.0.0.1:6379"),
		RateLimitCount:      getenvIntOrDefault("RATE_LIMIT_COUNT", 5),
		RateLimitWindow:     time.Duration(getenvIntOrDefault("RATE_LIMIT_WINDOW_SECONDS", 10)) * time.Second,
		UserServiceAddr:     getenvOrDefault("USER_SERVICE_ADDR", "http://localhost:9001"),
		AdminServiceAddr:    getenvOrDefault("ADMIN_SERVICE_ADDR", "http://localhost:9003"),
		PaymentsServiceAddr: getenvOrDefault("PAYMENTS_SERVICE_ADDR", "http://localhost:9002"),
		GatewayPort:         getenvOrDefault("GATEWAY_PORT", ":8080"),
	}
	Cfg = cfg
	return cfg
}

// LoadENV is kept for the token CLI tool which only needs the .env loaded.
func LoadENV() {
	if err := godotenv.Load(); err != nil {
		slog.Warn("no .env file found, reading from environment")
	}
}

// GetJWTSecret is kept for backward compatibility with the token CLI tool.
func GetJWTSecret() string {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		log.Fatal("JWT_SECRET not set")
	}
	return secret
}

func mustGetenv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", key)
	}
	return v
}

func getenvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvIntOrDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
