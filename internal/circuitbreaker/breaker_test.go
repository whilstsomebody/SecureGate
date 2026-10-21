package circuitbreaker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func admit(t *testing.T, b *Breaker) func(Outcome) {
	t.Helper()
	done, ok := b.Acquire()
	if !ok {
		t.Fatal("unexpected rejection")
	}
	return done
}
func TestThresholdAndExactlyOnce(t *testing.T) {
	b := New("test", 3, time.Hour)
	done := admit(t, b)
	done(Failure)
	done(Failure)
	admit(t, b)(Failure)
	if b.CurrentState() != StateClosed {
		t.Fatal("duplicate completion counted")
	}
	admit(t, b)(Failure)
	if _, ok := b.Acquire(); ok {
		t.Fatal("open breaker admitted request")
	}
}
func TestStaleCompletionDoesNotCloseOpenCircuit(t *testing.T) {
	b := New("test", 1, time.Hour)
	old := admit(t, b)
	admit(t, b)(Failure)
	old(Success)
	if b.CurrentState() != StateOpen {
		t.Fatal("old success closed circuit")
	}
}
func TestOneHalfOpenProbeAndNeutralRelease(t *testing.T) {
	b := New("test", 1, time.Second)
	now := time.Now()
	b.now = func() time.Time { return now }
	admit(t, b)(Failure)
	now = now.Add(time.Second)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	done := make(chan func(Outcome), 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f, ok := b.Acquire(); ok {
				accepted.Add(1)
				done <- f
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("admitted %d probes", accepted.Load())
	}
	(<-done)(Neutral)
	admit(t, b)(Failure)
	if b.CurrentState() != StateOpen {
		t.Fatal("failed probe did not reopen")
	}
	now = now.Add(time.Second)
	admit(t, b)(Success)
	if b.CurrentState() != StateClosed {
		t.Fatal("successful probe did not close")
	}
}
func TestNeutralDoesNotCountAsFailure(t *testing.T) {
	b := New("test", 2, time.Hour)
	for i := 0; i < 20; i++ {
		admit(t, b)(Neutral)
	}
	admit(t, b)(Failure)
	admit(t, b)(Success)
	admit(t, b)(Failure)
	if b.CurrentState() != StateClosed {
		t.Fatal("neutral outcomes counted or success did not reset failures")
	}
}
