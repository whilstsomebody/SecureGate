package main

import (
	"log"
	"net/http"

	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/middlewares"
	"github.com/whilstsomebody/securegate/internal/proxy"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
)

func main() {
	config.LoadENV()

	ratelimit.InitRedis()

	log.Println("SecureGate API Gateway is starting on PORT: 8080")

	handler := proxy.NewProxyhandler()

	secureHandler := middlewares.RateLimitMiddleware(
		middlewares.AuthMiddleware(handler),
	)

	server := &http.Server {
		Addr: ":8080",
		Handler: secureHandler,
	}

	err := server.ListenAndServe()
	if err != nil {
		log.Fatal("Server failed to start!!\nError: ",err)
	}
}