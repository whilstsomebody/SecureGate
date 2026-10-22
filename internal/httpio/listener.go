package httpio

import (
	"net"
	"sync"
	"time"

	"github.com/whilstsomebody/securegate/internal/metrics"
)

// LimitedListener bounds accepted sockets, including idle and hijacked ones.
// Extra connections remain in the OS backlog; Close interrupts a full listener.
type LimitedListener struct {
	net.Listener
	slots        chan struct{}
	done         chan struct{}
	once         sync.Once
	mu           sync.Mutex
	active       map[*limitedConn]struct{}
	writeTimeout time.Duration
}

func LimitListener(listener net.Listener, limit int, writeTimeout time.Duration) *LimitedListener {
	if limit < 1 {
		panic("connection limit must be positive")
	}
	return &LimitedListener{Listener: listener, slots: make(chan struct{}, limit), done: make(chan struct{}), active: make(map[*limitedConn]struct{}), writeTimeout: writeTimeout}
}
func (l *LimitedListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	c := &limitedConn{Conn: conn, owner: l}
	l.mu.Lock()
	select {
	case <-l.done:
		l.mu.Unlock()
		c.Close()
		return nil, net.ErrClosed
	default:
	}
	l.active[c] = struct{}{}
	metrics.ActiveConnections.Inc()
	l.mu.Unlock()
	return c, nil
}
func (l *LimitedListener) Close() error {
	var err error
	l.once.Do(func() { close(l.done); err = l.Listener.Close() })
	return err
}

// CloseConnections also closes upgrades, which http.Server.Shutdown does not own.
func (l *LimitedListener) CloseConnections() {
	l.mu.Lock()
	connections := make([]*limitedConn, 0, len(l.active))
	for c := range l.active {
		connections = append(connections, c)
	}
	l.mu.Unlock()
	for _, c := range connections {
		c.Close()
	}
}

type limitedConn struct {
	net.Conn
	owner    *LimitedListener
	once     sync.Once
	closeErr error
}

func (c *limitedConn) Write(p []byte) (int, error) {
	if c.owner.writeTimeout > 0 {
		if err := c.Conn.SetWriteDeadline(time.Now().Add(c.owner.writeTimeout)); err != nil {
			return 0, err
		}
	}
	n, err := c.Conn.Write(p)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			metrics.Timeouts.WithLabelValues("client_write").Inc()
		}
	}
	return n, err
}
func (c *limitedConn) Close() error {
	c.once.Do(func() {
		c.closeErr = c.Conn.Close()
		c.owner.mu.Lock()
		delete(c.owner.active, c)
		metrics.ActiveConnections.Dec()
		c.owner.mu.Unlock()
		<-c.owner.slots
	})
	return c.closeErr
}
