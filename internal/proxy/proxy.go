package proxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
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

func getTargetService(path string) string {
	if strings.HasPrefix(path, "/users") {
		return "http://localhost:9001"
	}

	if strings.HasPrefix(path, "/payments") {
		return "http://localhost:9002"
	}

	if strings.HasPrefix(path, "/admin") {
		return "http://localhost:9003"
	}

	return ""
} 

func rewritePath(path string) string {
	if strings.HasPrefix(path, "/users") {
		return strings.TrimPrefix(path, "/users")
	}

	if strings.HasPrefix(path, "payments") {
		return strings.TrimPrefix(path, "/payments")
	}

	if strings.HasPrefix(path, "/admin") {
		return strings.TrimPrefix(path, "/admin")
	}

	return path
}