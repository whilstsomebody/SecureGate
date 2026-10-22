package ratelimit

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func skipIfNoRealRedis(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 300 * time.Millisecond})
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("skipping real Redis integration test: Redis unreachable at %s: %v", addr, err)
	}
	return addr
}

func TestRealRedisPingAndConnection(t *testing.T) {
	addr := skipIfNoRealRedis(t)

	original := Rdb
	defer func() { Rdb = original }()

	if err := InitRedis(context.Background(), addr); err != nil {
		t.Fatalf("InitRedis failed against real Redis at %s: %v", addr, err)
	}
	defer Rdb.Close()

	if err := Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}

func TestRealRedisRateLimitingAndTTL(t *testing.T) {
	addr := skipIfNoRealRedis(t)

	original := Rdb
	defer func() { Rdb = original }()

	if err := InitRedis(context.Background(), addr); err != nil {
		t.Fatalf("InitRedis failed: %v", err)
	}
	defer Rdb.Close()

	key := fmt.Sprintf("test-ip-%d", time.Now().UnixNano())
	redisKey := fmt.Sprintf("rate:%s", key)
	defer Rdb.Del(context.Background(), redisKey)

	limit := 3
	window := 2 * time.Second

	// First 3 requests should be allowed
	for i := 1; i <= limit; i++ {
		allowed, err := AllowRequest(context.Background(), key, limit, window)
		if err != nil {
			t.Fatalf("request %d error: %v", i, err)
		}
		if !allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	// 4th request must be rejected
	allowed, err := AllowRequest(context.Background(), key, limit, window)
	if err != nil {
		t.Fatalf("overlimit request error: %v", err)
	}
	if allowed {
		t.Fatalf("overlimit request should be rejected")
	}

	// Verify key has TTL in Redis
	ttl, err := Rdb.TTL(context.Background(), redisKey).Result()
	if err != nil {
		t.Fatalf("failed to get TTL: %v", err)
	}
	if ttl <= 0 || ttl > window {
		t.Fatalf("unexpected TTL in Redis: %v", ttl)
	}

	// After window expires, should be allowed again
	time.Sleep(window + 100*time.Millisecond)
	allowed, err = AllowRequest(context.Background(), key, limit, window)
	if err != nil {
		t.Fatalf("post-expiry request error: %v", err)
	}
	if !allowed {
		t.Fatalf("request after window expiry should be allowed")
	}
}

func TestRealRedisRevocation(t *testing.T) {
	addr := skipIfNoRealRedis(t)

	original := Rdb
	defer func() { Rdb = original }()

	if err := InitRedis(context.Background(), addr); err != nil {
		t.Fatalf("InitRedis failed: %v", err)
	}
	defer Rdb.Close()

	jti := fmt.Sprintf("jti-%d", time.Now().UnixNano())
	revokedKey := fmt.Sprintf("revoked:%s", jti)

	// Clean up after test
	defer Rdb.Del(context.Background(), revokedKey)

	// Initially not revoked
	revoked, err := IsRevoked(context.Background(), jti)
	if err != nil {
		t.Fatalf("IsRevoked error: %v", err)
	}
	if revoked {
		t.Fatalf("token should not be revoked initially")
	}

	// Revoke token by setting in Redis
	if err := Rdb.Set(context.Background(), revokedKey, "1", time.Hour).Err(); err != nil {
		t.Fatalf("failed to set revocation in Redis: %v", err)
	}

	// Now should report as revoked
	revoked, err = IsRevoked(context.Background(), jti)
	if err != nil {
		t.Fatalf("IsRevoked error: %v", err)
	}
	if !revoked {
		t.Fatalf("token should be reported as revoked after setting in Redis")
	}
}
