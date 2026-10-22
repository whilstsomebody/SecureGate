package ratelimit

import (
	"context"
	"errors"
	"github.com/redis/go-redis/v9"
	"time"
)

const operationTimeout = 500 * time.Millisecond

var Rdb *redis.Client

func clientOptions(addr string) *redis.Options {
	return &redis.Options{Addr: addr, DialTimeout: operationTimeout, ReadTimeout: operationTimeout,
		WriteTimeout: operationTimeout, ContextTimeoutEnabled: true, MaxRetries: -1,
		PoolSize: 32, MaxActiveConns: 32, PoolTimeout: 100 * time.Millisecond}
}

func InitRedis(ctx context.Context, addr string) error {
	client := redis.NewClient(clientOptions(addr))
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return err
	}
	Rdb = client
	return nil
}

func Ping(ctx context.Context) error {
	if Rdb == nil {
		return errors.New("Redis unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	return Rdb.Ping(ctx).Err()
}
