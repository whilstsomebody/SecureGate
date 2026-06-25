package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/middlewares"
	"github.com/whilstsomebody/securegate/internal/proxy"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := config.Load()
	ratelimit.InitRedis(cfg.RedisAddr)
	metrics.RegisterMetrics()

	mux := http.NewServeMux()

	// Public endpoints — no auth required.
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/auth/revoke", revokeHandler)

	// All other traffic goes through the full middleware chain.
	secureHandler := middlewares.RecoveryMiddleware(
		middlewares.RequestIDMiddleware(
			middlewares.MetricsMiddleware(
				middlewares.RateLimitMiddleware(
					middlewares.AuthMiddleware(
						proxy.NewProxyhandler(),
					),
				),
			),
		),
	)
	mux.Handle("/", secureHandler)

	server := &http.Server{
		Addr:    cfg.GatewayPort,
		Handler: mux,
	}

	go func() {
		slog.Info("SecureGate starting", "port", cfg.GatewayPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	slog.Info("server exited cleanly")
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	redisStatus := "ok"
	httpStatus := http.StatusOK

	if _, err := ratelimit.Rdb.Ping(ratelimit.Ctx).Result(); err != nil {
		slog.Error("health check: Redis unreachable", "error", err)
		redisStatus = "error"
		httpStatus = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)

	status := "ok"
	if httpStatus != http.StatusOK {
		status = "degraded"
	}
	fmt.Fprintf(w, `{"status":%q,"redis":%q}`, status, redisStatus)
}

type revokeRequest struct {
	Token string `json:"token"`
}

func revokeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body revokeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		http.Error(w, "request body must be {\"token\":\"<jwt>\"}", http.StatusBadRequest)
		return
	}

	// Parse without signature validation — we're revoking regardless of validity.
	parser := jwt.NewParser()
	token, _, err := parser.ParseUnverified(body.Token, jwt.MapClaims{})
	if err != nil {
		http.Error(w, "invalid token format", http.StatusBadRequest)
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		http.Error(w, "invalid token claims", http.StatusBadRequest)
		return
	}

	jti, _ := claims["jti"].(string)
	if jti == "" {
		http.Error(w, "token has no jti claim; cannot revoke", http.StatusBadRequest)
		return
	}

	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		http.Error(w, "token has no expiry; cannot revoke", http.StatusBadRequest)
		return
	}

	ttl := time.Until(exp.Time)
	if ttl <= 0 {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"already_expired"}`)
		return
	}

	if err := ratelimit.RevokeToken(jti, ttl); err != nil {
		slog.Error("failed to revoke token", "jti", jti, "error", err)
		http.Error(w, "failed to revoke token", http.StatusInternalServerError)
		return
	}

	slog.Info("token revoked", "jti", jti, "ttl_seconds", int(ttl.Seconds()))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"revoked"}`)
}
