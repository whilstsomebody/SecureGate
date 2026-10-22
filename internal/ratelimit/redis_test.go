package ratelimit

import (
	"context"
	"errors"
	"github.com/redis/go-redis/v9"
	"testing"
	"time"
)

type blockingHook struct{}

func (blockingHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (blockingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (blockingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { <-ctx.Done(); return ctx.Err() }
}
func TestOperationsRespectCancellationAndDeadlines(t *testing.T) {
	original := Rdb
	Rdb = redis.NewClient(clientOptions("unused.invalid:6379"))
	Rdb.AddHook(blockingHook{})
	t.Cleanup(func() { Rdb.Close(); Rdb = original })
	operations := map[string]func(context.Context) error{
		"rate":       func(ctx context.Context) error { _, err := AllowRequest(ctx, "ip", 5, time.Second); return err },
		"revocation": func(ctx context.Context) error { _, err := IsRevoked(ctx, "id"); return err },
		"ping":       Ping,
	}
	for name, op := range operations {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := op(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation: %v", err)
			}
			start := time.Now()
			if err := op(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unbounded operation: %v", err)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("deadline not enforced")
			}
			ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			start = time.Now()
			_ = op(ctx)
			if time.Since(start) > operationTimeout {
				t.Fatal("parent deadline ignored")
			}
		})
	}
}
