package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/httpio"
	"github.com/whilstsomebody/securegate/internal/logging"
	"github.com/whilstsomebody/securegate/internal/metrics"
	"github.com/whilstsomebody/securegate/internal/middlewares"
	"github.com/whilstsomebody/securegate/internal/proxy"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
)

func main() {
	slog.SetDefault(slog.New(logging.NewHandler(os.Stdout)))

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	if err := ratelimit.InitRedis(context.Background(), cfg.RedisAddr); err != nil {
		log.Fatal("Redis initialization failed")
	}
	defer ratelimit.Rdb.Close()
	metrics.RegisterMetrics()
	limits := cfg.Limits
	proxy.ConfigureTransport(limits)

	server := &http.Server{
		Addr:              cfg.GatewayPort,
		Handler:           newHandler(),
		ConnContext:       httpio.ConnectionContext,
		ReadHeaderTimeout: limits.HeaderTimeout,
		ReadTimeout:       limits.RequestTimeout,
		IdleTimeout:       limits.KeepAliveTimeout,
		MaxHeaderBytes:    limits.MaxHeaderBytes,
	}

	listener, err := net.Listen("tcp", cfg.GatewayPort)
	if err != nil {
		log.Fatalf("listen failed: %v", err)
	}
	limited := httpio.LimitListener(listener, limits.MaxConnections, limits.ClientWriteTimeout)
	defer limited.CloseConnections()
	defer proxy.CloseIdleConnections()

	managementListener, err := net.Listen("tcp", cfg.ManagementAddr)
	if err != nil {
		log.Fatalf("management listen failed: %v", err)
	}
	managementLimited := httpio.LimitListener(managementListener, 32, 5*time.Second)
	defer managementLimited.CloseConnections()
	management := &http.Server{Handler: newManagementHandler(), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10}
	go func() {
		if err := management.Serve(managementLimited); err != nil && err != http.ErrServerClosed {
			log.Fatalf("management server failed: %v", err)
		}
	}()

	go func() {
		slog.Info("SecureGate starting", "port", cfg.GatewayPort)
		if err := server.Serve(limited); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), limits.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("drain deadline reached", "error", err)
		_ = server.Close()
	}
	if err := management.Shutdown(ctx); err != nil {
		_ = management.Close()
	}
	slog.Info("server exited cleanly")
}

func readinessHandler(w http.ResponseWriter, r *http.Request) {
	redisStatus := "ok"
	httpStatus := http.StatusOK

	if err := ratelimit.Ping(r.Context()); err != nil {
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

	// Management endpoints are never served on the public listener.
	for _, path := range []string{"/metrics", "/health", "/ready"} {
		mux.HandleFunc(path, http.NotFound)
	}
	// Token management belongs to the identity service. Never read this body.
	mux.HandleFunc("/auth/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		http.NotFound(w, r)
	})

	// All other traffic goes through the full middleware chain.
	secureHandler := middlewares.RequestIDMiddleware(
		middlewares.MetricsMiddleware(
			middlewares.RecoveryMiddleware(
				middlewares.RateLimitMiddleware(
					middlewares.AuthMiddleware(
						proxy.NewProxyhandler(),
					),
				),
			),
		),
	)
	mux.Handle("/", secureHandler)

	limits := config.EffectiveLimits()
	return httpio.LimitRequestBodyReads(middlewares.LimitRequests(middlewares.LimitUpgrades(mux, limits.MaxUpgradeRequests), limits.MaxRequests), limits.ClientReadIdle)
}

func newManagementHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{MaxRequestsInFlight: 2, Timeout: 5 * time.Second}))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.Handle("GET /ready", middlewares.LimitRequests(http.HandlerFunc(readinessHandler), 2))
	return middlewares.LimitRequests(mux, 8)
}
