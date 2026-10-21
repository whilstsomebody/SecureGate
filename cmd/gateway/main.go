package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	server := &http.Server{
		Addr:    cfg.GatewayPort,
		Handler: newHandler(),
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

func newHandler() http.Handler {
	mux := http.NewServeMux()

	// Public endpoints — no auth required.
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", healthHandler)
	// Token management belongs to the identity service. Never read this body.
	mux.HandleFunc("/auth/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		http.NotFound(w, r)
	})

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

	return mux
}
