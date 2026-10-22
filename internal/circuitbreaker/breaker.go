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

// OnStateChange is an optional package-level listener for breaker transitions.
var OnStateChange func(name string, state State)

type Breaker struct {
	name                string
	mu                  sync.Mutex
	state               State
	failures, threshold int
	generation          uint64
	probe               bool
	openedAt            time.Time
	resetTimeout        time.Duration
	now                 func() time.Time
	onStateChange       func(string, State)
}

func New(name string, threshold int, resetTimeout time.Duration) *Breaker {
	b := &Breaker{name: name, threshold: threshold, resetTimeout: resetTimeout, now: time.Now, onStateChange: OnStateChange}
	b.notify(StateClosed)
	return b
}

func (b *Breaker) notify(s State) {
	if b.onStateChange != nil {
		b.onStateChange(b.name, s)
	}
}

// Acquire admits a request and returns its exactly-once completion callback.
// Completions from an older state generation cannot change current health.
func (b *Breaker) Acquire() (func(Outcome), bool) {
	b.mu.Lock()
	var transitioned bool
	if b.state == StateOpen {
		if b.now().Sub(b.openedAt) < b.resetTimeout {
			b.mu.Unlock()
			return nil, false
		}
		b.state = StateHalfOpen
		transitioned = true
	}
	if b.state == StateHalfOpen {
		if b.probe {
			b.mu.Unlock()
			return nil, false
		}
		b.probe = true
	}
	generation := b.generation
	b.mu.Unlock()
	if transitioned {
		b.notify(StateHalfOpen)
	}
	var once sync.Once
	return func(outcome Outcome) { once.Do(func() { b.complete(generation, outcome) }) }, true
}
func (b *Breaker) complete(generation uint64, outcome Outcome) {
	b.mu.Lock()
	if generation != b.generation {
		b.mu.Unlock()
		return
	}
	if b.state == StateHalfOpen {
		b.probe = false
		if outcome == Neutral {
			b.mu.Unlock()
			return
		}
		if outcome == Success {
			b.state = StateClosed
			b.failures = 0
			b.generation++
			b.mu.Unlock()
			b.notify(StateClosed)
			return
		}
		b.open()
		b.mu.Unlock()
		b.notify(StateOpen)
		return
	}
	switch outcome {
	case Success:
		b.failures = 0
		b.mu.Unlock()
	case Failure:
		b.failures++
		if b.failures >= b.threshold {
			b.open()
			b.mu.Unlock()
			b.notify(StateOpen)
			return
		}
		b.mu.Unlock()
	default:
		b.mu.Unlock()
	}
}
func (b *Breaker) open()               { b.state = StateOpen; b.openedAt = b.now(); b.generation++; b.probe = false }
func (b *Breaker) CurrentState() State { b.mu.Lock(); defer b.mu.Unlock(); return b.state }
