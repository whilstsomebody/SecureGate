package circuitbreaker

import (
	"log/slog"
	"sync"
	"time"
)

// State represents the current state of a circuit breaker.
type State int

const (
	StateClosed   State = iota // normal operation — requests flow through
	StateOpen                  // too many failures — requests are rejected immediately
	StateHalfOpen              // cooldown elapsed — one probe request is let through
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// Breaker is a thread-safe circuit breaker for a single upstream service.
type Breaker struct {
	mu           sync.Mutex
	name         string
	state        State
	failures     int
	threshold    int    // how many consecutive failures open the circuit
	lastFailure  time.Time
	resetTimeout time.Duration // how long to wait in OPEN before probing
}

func New(name string, threshold int, resetTimeout time.Duration) *Breaker {
	return &Breaker{
		name:         name,
		threshold:    threshold,
		resetTimeout: resetTimeout,
		state:        StateClosed,
	}
}

// Allow returns true if the request should be forwarded to the upstream.
// When OPEN, it transitions to HALF_OPEN after resetTimeout elapses.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(b.lastFailure) >= b.resetTimeout {
			slog.Info("circuit breaker half-open, probing upstream", "service", b.name)
			b.state = StateHalfOpen
			return true
		}
		return false
	case StateHalfOpen:
		return true
	}
	return false
}

// RecordSuccess resets the breaker to CLOSED.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != StateClosed {
		slog.Info("circuit breaker closed — upstream recovered", "service", b.name)
	}
	b.state = StateClosed
	b.failures = 0
}

// RecordFailure increments the failure counter and opens the breaker when threshold is crossed.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.lastFailure = time.Now()
	if b.failures >= b.threshold {
		if b.state != StateOpen {
			slog.Warn("circuit breaker opened", "service", b.name, "failures", b.failures)
		}
		b.state = StateOpen
	}
}

// State returns the current circuit breaker state (for testing / health checks).
func (b *Breaker) CurrentState() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
