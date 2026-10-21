package httpio

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestListenerCapacityShutdownAndActiveConnections(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := LimitListener(raw, 1, time.Second)
	defer listener.Close()
	defer listener.CloseConnections()
	connect := func() net.Conn {
		c, err := net.DialTimeout("tcp", raw.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	connect()
	first, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	secondClient := connect()
	accepted := make(chan net.Conn, 1)
	go func() { c, _ := listener.Accept(); accepted <- c }()
	select {
	case <-accepted:
		t.Fatal("exceeded connection limit")
	case <-time.After(30 * time.Millisecond):
	}
	first.Close()
	first.Close() // Double close must not release another slot.
	var second net.Conn
	select {
	case second = <-accepted:
		if second == nil {
			t.Fatal("accept failed")
		}
	case <-time.After(time.Second):
		t.Fatal("slot not released")
	}
	blocked := make(chan error, 1)
	go func() { _, err := listener.Accept(); blocked <- err }()
	listener.Close()
	select {
	case err := <-blocked:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("close err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not interrupt accept")
	}
	listener.CloseConnections()
	secondClient.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := secondClient.Read(make([]byte, 1)); err == nil {
		t.Fatal("active connection left open")
	}
	second.Close()
}
func TestListenerWriteDeadlineBoundsBlockedPeer(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	owner := &LimitedListener{slots: make(chan struct{}, 1), active: map[*limitedConn]struct{}{}, writeTimeout: 20 * time.Millisecond}
	owner.slots <- struct{}{}
	c := &limitedConn{Conn: a, owner: owner}
	defer c.Close()
	_, err := c.Write([]byte("blocked"))
	if e, ok := err.(net.Error); !ok || !e.Timeout() {
		t.Fatalf("expected write timeout, got %v", err)
	}
}
