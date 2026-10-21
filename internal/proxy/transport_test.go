package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUpstreamReadIdleTimeoutAbortsStalledBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	transport := boundedTransport()
	defer transport.CloseIdleConnections()
	req, _ := http.NewRequest("GET", upstream.URL, nil)
	response, err := readIdleTransport(transport, 30*time.Millisecond).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, err = io.ReadAll(response.Body)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected explicit timeout, got %v", err)
	}
}
func TestUpstreamReadTimeoutDoesNotLimitTotalStreamDuration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 5; i++ {
			fmt.Fprint(w, "x")
			w.(http.Flusher).Flush()
			time.Sleep(25 * time.Millisecond)
		}
	}))
	defer upstream.Close()
	transport := boundedTransport()
	defer transport.CloseIdleConnections()
	req, _ := http.NewRequest("GET", upstream.URL, nil)
	response, err := readIdleTransport(transport, 100*time.Millisecond).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "xxxxx" {
		t.Fatalf("stream=%q err=%v", body, err)
	}
}

func TestUpgradeReadIdleTimeoutClosesConnection(t *testing.T) {
	closed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer close(closed)
		defer c.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		rw.Flush()
		c.Read(make([]byte, 1))
	}))
	defer upstream.Close()
	transport := boundedTransport()
	defer transport.CloseIdleConnections()
	req, _ := http.NewRequest("GET", upstream.URL, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	response, err := readIdleTransport(transport, 30*time.Millisecond).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, ok := response.Body.(io.ReadWriteCloser); !ok {
		t.Fatal("upgrade lost write capability")
	}
	_, err = response.Body.Read(make([]byte, 1))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("upgrade connection not closed")
	}
}
