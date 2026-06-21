package proxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

func NewProxyhandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		log.Println("Incoming request: ", r.Method, r.URL.Path)

		target := getTargetService(r.URL.Path)
		if target == "" {
			http.Error(w, "Service not Found", http.StatusNotFound)
			return
		}

		targetURL, err := url.Parse(target)
		if err != nil {
			http.Error(w, "Invalid Target URL", http.StatusInternalServerError)
			return
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		r.URL.Path = rewritePath(r.URL.Path)

		proxy.ServeHTTP(w, r)
	})
}

func serviceAddr(envKey, fallback string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return fallback
}

func getTargetService(path string) string {
	if strings.HasPrefix(path, "/users") {
		return serviceAddr("USER_SERVICE_ADDR", "http://localhost:9001")
	}

	if strings.HasPrefix(path, "/payments") {
		return serviceAddr("PAYMENTS_SERVICE_ADDR", "http://localhost:9002")
	}

	if strings.HasPrefix(path, "/admin") {
		return serviceAddr("ADMIN_SERVICE_ADDR", "http://localhost:9003")
	}

	return ""
}

func rewritePath(path string) string {
	for _, prefix := range []string{"/users", "/payments", "/admin"} {
		if after, ok := strings.CutPrefix(path, prefix); ok {
			return after
		}
	}
	return path
}