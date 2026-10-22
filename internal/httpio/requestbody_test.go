package httpio

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestBodyIdleAllowsProgressAndBoundsStalls(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	body := &idleRequestBody{ReadCloser: a, conn: a, timeout: 100 * time.Millisecond}
	go func() {
		for i := 0; i < 8; i++ {
			time.Sleep(25 * time.Millisecond)
			if _, err := b.Write([]byte("x")); err != nil {
				return
			}
		}
	}()
	for i := 0; i < 8; i++ {
		if _, err := body.Read(make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := body.Read(make([]byte, 1)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled read: %v", err)
	}
}
func TestBodyIdleDoesNotLimitResponseDuration(t *testing.T) {
	handler := LimitRequestBodyReads(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		for i := 0; i < 8; i++ {
			w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			time.Sleep(25 * time.Millisecond)
		}
	}), 50*time.Millisecond)
	server := httptest.NewUnstartedServer(handler)
	server.Config.ConnContext = ConnectionContext
	server.Start()
	defer server.Close()
	response, err := server.Client().Post(server.URL, "text/plain", strings.NewReader("upload"))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || string(data) != "xxxxxxxx" {
		t.Fatalf("stream interrupted: %q %v", data, err)
	}
}
