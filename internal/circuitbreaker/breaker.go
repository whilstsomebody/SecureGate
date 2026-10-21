package circuitbreaker

import (
	"sync"
	"time"
)

type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF_OPEN"
	}
	return "UNKNOWN"
}

type Outcome int

const (
	Neutral Outcome = iota // Client cancellation or local failure: no backend health signal.
	Success
	Failure
)

type Breaker struct {
	mu                  sync.Mutex
	state               State
	failures, threshold int
	generation          uint64
	probe               bool
	openedAt            time.Time
	resetTimeout        time.Duration
	now                 func() time.Time
}

func New(_ string, threshold int, resetTimeout time.Duration) *Breaker {
	return &Breaker{threshold: threshold, resetTimeout: resetTimeout, now: time.Now}
}

// Acquire admits a request and returns its exactly-once completion callback.
// Completions from an older state generation cannot change current health.
func (b *Breaker) Acquire() (func(Outcome), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateOpen {
		if b.now().Sub(b.openedAt) < b.resetTimeout {
			return nil, false
		}
		b.state = StateHalfOpen
	}
	if b.state == StateHalfOpen {
		if b.probe {
			return nil, false
		}
		b.probe = true
	}
	generation := b.generation
	var once sync.Once
	return func(outcome Outcome) { once.Do(func() { b.complete(generation, outcome) }) }, true
}
func (b *Breaker) complete(generation uint64, outcome Outcome) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if generation != b.generation {
		return
	}
	if b.state == StateHalfOpen {
		b.probe = false
		if outcome == Neutral {
			return
		}
		if outcome == Success {
			b.state = StateClosed
			b.failures = 0
			b.generation++
			return
		}
		b.open()
		return
	}
	switch outcome {
	case Success:
		b.failures = 0
	case Failure:
		b.failures++
		if b.failures >= b.threshold {
			b.open()
		}
	}
}
func (b *Breaker) open()               { b.state = StateOpen; b.openedAt = b.now(); b.generation++; b.probe = false }
func (b *Breaker) CurrentState() State { b.mu.Lock(); defer b.mu.Unlock(); return b.state }
