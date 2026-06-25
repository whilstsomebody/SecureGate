package ratelimit

import (
	"context"
	"log"
	"log/slog"
	"os"

	"github.com/redis/go-redis/v9"
)

var Ctx = context.Background()
var Rdb *redis.Client

func InitRedis(addr string) {
	if addr == "" {
		addr = os.Getenv("REDIS_ADDR")
	}
	if addr == "" {
		log.Fatal("REDIS_ADDR is not set")
	}

	Rdb = redis.NewClient(&redis.Options{Addr: addr})

	if _, err := Rdb.Ping(Ctx).Result(); err != nil {
		log.Fatalf("failed to connect to Redis at %s: %v", addr, err)
	}

	slog.Info("connected to Redis", "addr", addr)
}
