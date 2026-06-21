package ratelimit

import (
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

func AllowRequest(key string, limit int, window time.Duration) (bool, error) {
	redisKey := fmt.Sprintf("rate:%s", key)

	count, err := rateLimitScript.Run(Ctx, Rdb, []string{redisKey}, int(window.Seconds())).Int64()
	if err != nil {
		return false, err
	}

	return count <= int64(limit), nil
}