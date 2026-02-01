package main

import (
	"log"
	"net/http"

	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/middlewares"
	"github.com/whilstsomebody/securegate/internal/proxy"
)

func main() {
	config.LoadENV()

	log.Println("SecureGate API Gateway is starting on PORT: 8080")

	handler := proxy.NewProxyhandler()

	secureHandler := middlewares.AuthMiddleware(handler)

	server := &http.Server {
		Addr: ":8080",
		Handler: secureHandler,
	}

	err := server.ListenAndServe()
	if err != nil {
		log.Fatal("Server failed to start!!\nError: ",err)
	}
}