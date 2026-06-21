package main

import (
	"context"
	"log"
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
	config.LoadENV()

	ratelimit.InitRedis()

	metrics.RegisterMetrics()

	handler := proxy.NewProxyhandler()

	secureHandler := middlewares.RecoveryMiddleware(
		middlewares.MetricsMiddleware(
			middlewares.RateLimitMiddleware(
				middlewares.AuthMiddleware(handler),
			),
		),
	)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", secureHandler)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	go func() {
		log.Println("SecureGate API Gateway is starting on PORT: 8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Server failed to start: ", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Forced shutdown: ", err)
	}
	log.Println("Server exited cleanly")
}