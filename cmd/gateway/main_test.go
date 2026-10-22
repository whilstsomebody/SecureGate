package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/ratelimit"
	"github.com/whilstsomebody/securegate/internal/routing"
	"github.com/whilstsomebody/securegate/internal/version"
)

type unreadableBody struct{ t *testing.T }

func (b unreadableBody) Read([]byte) (int, error) {
	b.t.Error("retired endpoint read request body")
	return 0, errors.New("must not read")
}
func (b unreadableBody) Close() error { return nil }
func TestRevocationEndpointRetired(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT"} {
		req := httptest.NewRequest(method, "/auth/revoke", nil)
		req.Body = unreadableBody{t}
		rr := httptest.NewRecorder()
		newHandler().ServeHTTP(rr, req)
		if rr.Code != 404 {
			t.Fatalf("%s: got %d", method, rr.Code)
		}
	}
}

func TestManagementIsolationAndLiveness(t *testing.T) {
	original := ratelimit.Rdb
	ratelimit.Rdb = nil
	t.Cleanup(func() { ratelimit.Rdb = original })
	for _, path := range []string{"/health", "/ready", "/metrics"} {
		rr := httptest.NewRecorder()
		newHandler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != 404 {
			t.Fatalf("public %s: %d", path, rr.Code)
		}
	}
	handler := newManagementHandler()
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/health", 200}, {"GET", "/ready", 503}, {"GET", "/metrics", 200}, {"POST", "/health", 405}, {"GET", "/users", 404},
	} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
		if rr.Code != tc.status {
			t.Fatalf("%s %s: got %d want %d", tc.method, tc.path, rr.Code, tc.status)
		}
	}
}

func TestVersionAndCheckConfig(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	cfg, err := config.Check()
	if err != nil {
		t.Fatalf("expected valid check: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	info := version.Info()
	if !strings.Contains(info, "SecureGate") {
		t.Fatalf("version info invalid: %s", info)
	}
}

func TestServePublicTLS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	certificate := testTLSCertificate(t)
	server := &http.Server{
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS == nil {
				t.Error("request was not terminated as TLS")
			}
			_, _ = w.Write([]byte("ok"))
		}),
	}
	done := make(chan error, 1)
	go func() { done <- servePublic(server, listener, true) }()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // #nosec G402 -- self-signed test certificate
	response, err := client.Get("https://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("TLS response status = %d", response.StatusCode)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != http.ErrServerClosed {
		t.Fatalf("ServeTLS returned %v", err)
	}
}

func testTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "securegate.test"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"securegate.test"}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func TestPolicyFreeGatewayForwardsWithoutRedisOrAuthorization(t *testing.T) {
	cfg := &config.Config{AuthEnabled: false, RateLimitEnabled: false, Limits: config.DefaultLimits()}
	called := false
	routes, err := routing.NewTable([]routing.Route{{Prefix: "/users"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := newGatewayHandlerFor(cfg, routes, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/users/hello", nil))
	if rr.Code != http.StatusNoContent || !called {
		t.Fatalf("status=%d forwarded=%t", rr.Code, called)
	}

	rr = httptest.NewRecorder()
	newManagementHandlerFor(cfg).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"redis":"disabled"`) {
		t.Fatalf("policy-free readiness = %d %q", rr.Code, rr.Body.String())
	}
}

func TestPolicyFreeGatewayEndToEndWithRealUpstream(t *testing.T) {
	receivedHeader := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"upstream":"handled"}`))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		AuthEnabled:         false,
		RateLimitEnabled:    false,
		UserServiceAddr:     upstream.URL,
		AdminServiceAddr:    upstream.URL,
		PaymentsServiceAddr: upstream.URL,
		Limits:              config.DefaultLimits(),
	}

	cfg.Routes = []config.RouteConfig{
		{Prefix: "/users", Target: upstream.URL},
		{Prefix: "/admin", Target: upstream.URL},
		{Prefix: "/payments", Target: upstream.URL},
	}
	gateway, _, err := buildGatewayHandler(cfg, cfg.Routes)
	if err != nil {
		t.Fatalf("buildGatewayHandler failed: %v", err)
	}

	// 1. Unauthenticated request forwards directly and succeeds without requiring tokens or Redis
	req := httptest.NewRequest(http.MethodGet, "/users/profile", nil)
	rr := httptest.NewRecorder()
	gateway.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"upstream":"handled"`) {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}

	// 2. Request with arbitrary/invalid auth header is not blocked or rejected by gateway
	req = httptest.NewRequest(http.MethodGet, "/users/profile", nil)
	req.Header.Set("Authorization", "Bearer invalid-or-arbitrary-token")
	rr = httptest.NewRecorder()
	gateway.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if receivedHeader != "Bearer invalid-or-arbitrary-token" {
		t.Fatalf("expected authorization header forwarded, got: %q", receivedHeader)
	}

	// 3. Burst requests are not rate limited
	for i := 0; i < 10; i++ {
		req = httptest.NewRequest(http.MethodGet, "/users/profile", nil)
		rr = httptest.NewRecorder()
		gateway.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("burst request %d rejected with %d", i+1, rr.Code)
		}
	}

	// 4. Management readiness reports Redis disabled
	rr = httptest.NewRecorder()
	newManagementHandlerFor(cfg).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"redis":"disabled"`) {
		t.Fatalf("expected redis disabled in readiness, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestReloadGatewayHandlerPublishesAtomically(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("slow"))
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fast"))
	}))
	defer fast.Close()

	path := filepath.Join(t.TempDir(), "routes.json")
	writeRoutes := func(target string) {
		t.Helper()
		contents := `{"routes":[{"prefix":"/api","target":"` + target + `"}]}`
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRoutes(slow.URL)
	cfg := &config.Config{AuthEnabled: false, RateLimitEnabled: false, RoutesFile: path, Limits: config.DefaultLimits()}
	routes, err := cfg.LoadRoutes()
	if err != nil {
		t.Fatal(err)
	}
	initial, table, err := buildGatewayHandler(cfg, routes)
	if err != nil {
		t.Fatal(err)
	}
	originalTable := routing.Current()
	routing.Replace(table)
	t.Cleanup(func() { routing.Replace(originalTable) })
	active := newSwappableHandler(initial)

	inFlight := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rr := httptest.NewRecorder()
		active.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/item", nil))
		inFlight <- rr
	}()
	<-started

	writeRoutes(fast.URL)
	count, err := reloadGatewayHandler(cfg, active)
	if err != nil || count != 1 {
		t.Fatalf("reload count=%d err=%v", count, err)
	}
	current := httptest.NewRecorder()
	active.ServeHTTP(current, httptest.NewRequest(http.MethodGet, "/api/item", nil))
	if current.Code != http.StatusOK || current.Body.String() != "fast" {
		t.Fatalf("new request used old routes: status=%d body=%q", current.Code, current.Body.String())
	}

	close(release)
	old := <-inFlight
	if old.Code != http.StatusOK || old.Body.String() != "slow" {
		t.Fatalf("in-flight request was interrupted: status=%d body=%q", old.Code, old.Body.String())
	}

	writeRoutes("ftp://invalid.example")
	if _, err := reloadGatewayHandler(cfg, active); err == nil {
		t.Fatal("invalid reload was accepted")
	}
	stillCurrent := httptest.NewRecorder()
	active.ServeHTTP(stillCurrent, httptest.NewRequest(http.MethodGet, "/api/item", nil))
	if stillCurrent.Code != http.StatusOK || stillCurrent.Body.String() != "fast" {
		t.Fatalf("invalid reload changed active routes: status=%d body=%q", stillCurrent.Code, stillCurrent.Body.String())
	}
}
