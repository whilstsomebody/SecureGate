package circuitbreaker

import (
	"testing"
	"time"
)

func TestNewBreakerIsClosedAndAllows(t *testing.T) {
	b := New("svc", 3, 30*time.Second)
	if b.CurrentState() != StateClosed {
		t.Fatalf("expected CLOSED, got %s", b.CurrentState())
	}
	if !b.Allow() {
		t.Error("new breaker should allow requests")
	}
}

func TestBreakerOpensAfterThreshold(t *testing.T) {
	b := New("svc", 3, 30*time.Second)
	b.RecordFailure()
	b.RecordFailure()
	if b.CurrentState() != StateClosed {
		t.Error("should still be CLOSED after 2 failures (threshold=3)")
	}
	b.RecordFailure()
	if b.CurrentState() != StateOpen {
		t.Errorf("expected OPEN after 3 failures, got %s", b.CurrentState())
	}
	if b.Allow() {
		t.Error("OPEN breaker must reject requests")
	}
}

func TestBreakerTransitionsToHalfOpenAfterCooldown(t *testing.T) {
	b := New("svc", 1, 50*time.Millisecond)
	b.RecordFailure()
	if b.CurrentState() != StateOpen {
		t.Fatal("expected OPEN")
	}
	time.Sleep(60 * time.Millisecond)
	if !b.Allow() {
		t.Error("should allow probe request after cooldown")
	}
	if b.CurrentState() != StateHalfOpen {
		t.Errorf("expected HALF_OPEN, got %s", b.CurrentState())
	}
}

func TestBreakerClosesOnSuccessFromHalfOpen(t *testing.T) {
	b := New("svc", 1, 50*time.Millisecond)
	b.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	b.Allow() // transitions to HALF_OPEN
	b.RecordSuccess()
	if b.CurrentState() != StateClosed {
		t.Errorf("expected CLOSED after success, got %s", b.CurrentState())
	}
	if b.failures != 0 {
		t.Error("failure counter should reset on success")
	}
}

func TestBreakerReopensOnFailureFromHalfOpen(t *testing.T) {
	b := New("svc", 1, 50*time.Millisecond)
	b.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	b.Allow() // transitions to HALF_OPEN
	b.RecordFailure()
	if b.CurrentState() != StateOpen {
		t.Errorf("expected OPEN after failure in HALF_OPEN, got %s", b.CurrentState())
	}
}

func TestSuccessResetsFailureCounter(t *testing.T) {
	b := New("svc", 5, 30*time.Second)
	b.RecordFailure()
	b.RecordFailure()
	b.RecordSuccess()
	if b.failures != 0 {
		t.Errorf("expected failure counter = 0 after success, got %d", b.failures)
	}
}
