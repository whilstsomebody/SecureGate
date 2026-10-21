package proxy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/middlewares"
)

func setUpstream(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(handler)
	old := config.Cfg
	config.Cfg = &config.Config{UserServiceAddr: upstream.URL}
	t.Cleanup(func() { upstream.Close(); config.Cfg = old })
	return upstream
}
func proxyServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(middlewares.MetricsMiddleware(middlewares.RecoveryMiddleware(NewProxyhandler())))
	t.Cleanup(s.Close)
	return s
}
func TestTransportFailuresCountOnce(t *testing.T) {
	attempts := 0
	h := newProxyHandler(roundTripFunc(func(*http.Request) (*http.Response, error) { attempts++; return nil, errors.New("backend unavailable") }))
	for i := 0; i < 6; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/users/x", nil))
		want := 502
		if i == 5 {
			want = 503
		}
		if rr.Code != want {
			t.Fatalf("request %d: got %d want %d", i+1, rr.Code, want)
		}
	}
	if attempts != 5 {
		t.Fatalf("attempts=%d", attempts)
	}
}
func TestTruncatedResponsesAbortAndOpenCircuit(t *testing.T) {
	setUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(rw, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Type: text/plain\r\n\r\n4\r\npart\r\n")
		rw.Flush()
	}))
	s := proxyServer(t)
	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 5; i++ {
		resp, err := client.Get(s.URL + "/users/x")
		if err != nil {
			continue
		} // Aborting before headers arrive is also a valid failure.
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || err == nil || strings.Contains(string(body), "Internal server error") {
			t.Fatalf("status=%d body=%q err=%v", resp.StatusCode, body, err)
		}
	}
	resp, err := client.Get(s.URL + "/users/x")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("body failures not counted: %d", resp.StatusCode)
	}
}
func TestClientDisconnectsDoNotOpenCircuit(t *testing.T) {
	entered := make(chan struct{}, 10)
	setUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cancel" {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, "healthy")
	}))
	completed := make(chan struct{}, 10)
	h := middlewares.MetricsMiddleware(middlewares.RecoveryMiddleware(NewProxyhandler()))
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { completed <- struct{}{} }()
		h.ServeHTTP(w, r)
	}))
	defer s.Close()
	for i := 0; i < 6; i++ {
		c, err := net.DialTimeout("tcp", strings.TrimPrefix(s.URL, "http://"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(c, "GET /users/cancel HTTP/1.1\r\nHost: test.local\r\n\r\n")
		select {
		case <-entered:
		case <-time.After(time.Second):
			c.Close()
			t.Fatal("backend not entered")
		}
		c.Close()
		select {
		case <-completed:
		case <-time.After(time.Second):
			t.Fatal("canceled request stuck")
		}
	}
	resp, err := (&http.Client{Timeout: time.Second}).Get(s.URL + "/users/healthy")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("healthy backend blocked: %d", resp.StatusCode)
	}
}
func TestStreamingEventArrivesBeforeUpstreamCloses(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	setUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	s := proxyServer(t)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(s.URL + "/users/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: ready\n" {
		t.Fatalf("event=%q err=%v", line, err)
	}
}
func TestUpgradeSupportsBidirectionalTraffic(t *testing.T) {
	setUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		rw.Flush()
		io.Copy(c, rw)
	}))
	s := proxyServer(t)

	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(s.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprint(conn, "GET /users/socket HTTP/1.1\r\nHost: test.local\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 101 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 4)
	if _, err := io.ReadFull(reader, data); err != nil {
		t.Fatal(err)
	}
	if string(data) != "ping" {
		t.Fatalf("echo=%q", data)
	}

}
func TestForwardingIdentityIsRebuiltAndInputUnchanged(t *testing.T) {
	h := newProxyHandler(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Forwarded-For") != "192.0.2.10" || r.Header.Get("X-Real-IP") != "192.0.2.10" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-Proto") != "http" {
			t.Errorf("untrusted identity: %v", r.Header)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	}))
	req := httptest.NewRequest("GET", "/users/x", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	for _, name := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "X-Forwarded-Proto"} {
		req.Header.Set(name, "spoofed")
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	if req.URL.Path != "/users/x" {
		t.Errorf("input path mutated: %s", req.URL.Path)
	}
}
