package main

import (
	"log"
	"net/http"

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

	log.Println("SecureGate API Gateway is starting on PORT: 8080")

	handler := proxy.NewProxyhandler()

	secureHandler := middlewares.MetricsMiddleware(
		middlewares.RateLimitMiddleware(
			middlewares.AuthMiddleware(handler),
		),
	)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", secureHandler)


	server := &http.Server {
		Addr: ":8080",
		Handler: mux,
	}

	err := server.ListenAndServe()
	if err != nil {
		log.Fatal("Server failed to start!!\nError: ",err)
	}
}