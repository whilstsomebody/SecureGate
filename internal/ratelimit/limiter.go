package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Atomically increments the counter and sets the TTL on first request,
// avoiding the INCR/EXPIRE race condition when using two separate commands.
var rateLimitScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
    redis.call('EXPIRE', KEYS[1], tonumber(ARGV[1]))
end
return count
`)

func AllowRequest(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if Rdb == nil {
		return false, errors.New("rate limit store unavailable")
	}
	redisKey := fmt.Sprintf("rate:%s", key)

	count, err := rateLimitScript.Run(ctx, Rdb, []string{redisKey}, int(window.Seconds())).Int64()
	if err != nil {
		return false, err
	}

	return count <= int64(limit), nil
}
