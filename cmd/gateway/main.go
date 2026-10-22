package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
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
	"github.com/whilstsomebody/securegate/internal/routing"
	"github.com/whilstsomebody/securegate/internal/version"
)

func main() {
	var checkConfig bool
	var showVersion bool
	flag.BoolVar(&checkConfig, "check-config", false, "Validate configuration and resource limits without starting services")
	flag.BoolVar(&checkConfig, "t", false, "Validate configuration (shorthand)")
	flag.BoolVar(&showVersion, "version", false, "Print version and build information")
	flag.BoolVar(&showVersion, "v", false, "Print version (shorthand)")
	flag.Parse()

	if showVersion || (len(os.Args) > 1 && os.Args[1] == "version") {
		fmt.Println(version.Info())
		return
	}

	if checkConfig || (len(os.Args) > 1 && os.Args[1] == "check-config") {
		cfg, err := config.Check()
		if err != nil {
			fmt.Fprintf(os.Stderr, "configuration check failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("configuration is valid (%s)\n\n", version.Info())
		fmt.Print(cfg.Summary())
		return
	}

	slog.SetDefault(slog.New(logging.NewHandler(os.Stdout)))

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	if cfg.RequiresRedis() {
		if err := ratelimit.InitRedis(context.Background(), cfg.RedisAddr); err != nil {
			log.Fatal("Redis initialization failed")
		}
		defer ratelimit.Rdb.Close()
	}
	metrics.RegisterMetrics()
	limits := cfg.Limits
	proxy.ConfigureTransport(limits)
	gatewayHandler, table, err := buildGatewayHandler(cfg, cfg.Routes)
	if err != nil {
		log.Fatalf("invalid proxy routes: %v", err)
	}
	routing.Replace(table)
	activeHandler := newSwappableHandler(gatewayHandler)

	server := &http.Server{
		Addr:              cfg.GatewayPort,
		Handler:           activeHandler,
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
	management := &http.Server{Handler: newManagementHandlerFor(cfg), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10}
	go func() {
		if err := management.Serve(managementLimited); err != nil && err != http.ErrServerClosed {
			log.Fatalf("management server failed: %v", err)
		}
	}()

	go func() {
		slog.Info("SecureGate starting", "version", version.Version, "port", cfg.GatewayPort)
		if err := server.Serve(limited); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	for {
		sig := <-signals
		if sig != syscall.SIGHUP {
			break
		}
		count, err := reloadGatewayHandler(cfg, activeHandler)
		if err != nil {
			slog.Error("route reload rejected", "error", err)
			continue
		}
		slog.Info("routes reloaded", "count", count)
	}

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

func readinessHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.RequiresRedis() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok","redis":"disabled"}`)
			return
		}
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
}

type handlerValue struct{ handler http.Handler }

type swappableHandler struct{ current atomic.Value }

func newSwappableHandler(initial http.Handler) *swappableHandler {
	h := &swappableHandler{}
	h.Store(initial)
	return h
}

func (h *swappableHandler) Store(next http.Handler) { h.current.Store(handlerValue{handler: next}) }
func (h *swappableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.current.Load().(handlerValue).handler.ServeHTTP(w, r)
}

func buildGatewayHandler(cfg *config.Config, routes []config.RouteConfig) (http.Handler, *routing.Table, error) {
	if len(routes) == 0 {
		var err error
		routes, err = cfg.LoadRoutes()
		if err != nil {
			return nil, nil, err
		}
	}
	routingRoutes := make([]routing.Route, 0, len(routes))
	proxyRoutes := make([]proxy.Route, 0, len(routes))
	for _, route := range routes {
		routingRoutes = append(routingRoutes, routing.Route{Prefix: route.Prefix, Role: route.Role})
		proxyRoutes = append(proxyRoutes, proxy.Route{Prefix: route.Prefix, Target: route.Target, Role: route.Role})
	}
	table, err := routing.NewTable(routingRoutes)
	if err != nil {
		return nil, nil, err
	}
	proxyHandler, err := proxy.NewHandler(proxyRoutes, proxy.UpstreamTransport())
	if err != nil {
		return nil, nil, err
	}
	return newGatewayHandlerFor(cfg, table, proxyHandler), table, nil
}

// reloadGatewayHandler publishes a fully validated replacement only after all
// its policy middleware and proxy state have been constructed. Requests that
// already selected the old handler continue there without interruption.
func reloadGatewayHandler(cfg *config.Config, active *swappableHandler) (int, error) {
	routes, err := cfg.LoadRoutes()
	if err != nil {
		return 0, err
	}
	next, table, err := buildGatewayHandler(cfg, routes)
	if err != nil {
		return 0, err
	}
	active.Store(next)
	routing.Replace(table)
	if cfg != nil {
		cfg.Routes = routes
	}
	return len(routes), nil
}

// newHandler is retained for package tests. Production startup builds an
// explicit proxy core through buildGatewayHandler before opening listeners.
func newHandler() http.Handler {
	cfg := config.Cfg
	if cfg == nil {
		cfg = &config.Config{
			AuthEnabled:         true,
			RateLimitEnabled:    true,
			UserServiceAddr:     "http://localhost:9001",
			AdminServiceAddr:    "http://localhost:9003",
			PaymentsServiceAddr: "http://localhost:9002",
			Limits:              config.DefaultLimits(),
		}
	}
	handler, _, err := buildGatewayHandler(cfg, cfg.Routes)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpio.WriteError(w, r, http.StatusInternalServerError, "INVALID_PROXY_CONFIG", "Invalid proxy configuration")
		})
	}
	return handler
}

func newGatewayHandlerFor(cfg *config.Config, routes *routing.Table, proxyHandler http.Handler) http.Handler {
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

	// Policies are optional. Observability and recovery always wrap the core.
	handler := proxyHandler
	if cfg != nil && cfg.AuthEnabled {
		handler = middlewares.AuthMiddlewareFor(handler, cfg, routes)
	}
	if cfg != nil && cfg.RateLimitEnabled {
		handler = middlewares.RateLimitMiddlewareFor(handler, cfg, routes)
	}
	handler = middlewares.RequestIDMiddleware(
		middlewares.MetricsMiddlewareFor(
			middlewares.RecoveryMiddleware(handler),
			cfg,
			routes,
		),
	)
	mux.Handle("/", handler)

	limits := config.DefaultLimits()
	if cfg != nil && cfg.Limits.MaxConnections > 0 {
		limits = cfg.Limits
	}
	return httpio.LimitRequestBodyReads(middlewares.LimitRequests(middlewares.LimitUpgrades(mux, limits.MaxUpgradeRequests), limits.MaxRequests), limits.ClientReadIdle)
}

func newManagementHandler() http.Handler { return newManagementHandlerFor(config.Cfg) }

func newManagementHandlerFor(cfg *config.Config) http.Handler {
	if cfg == nil {
		cfg = &config.Config{AuthEnabled: true, RateLimitEnabled: true}
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{MaxRequestsInFlight: 2, Timeout: 5 * time.Second}))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.Handle("GET /ready", middlewares.LimitRequests(readinessHandler(cfg), 2))
	return middlewares.LimitRequests(mux, 8)
}
