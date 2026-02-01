package ratelimit

import (
	"fmt"
	"time"
)

func AllowRequest(key string, limit int, window time.Duration) (bool, error) {
	redisKey := fmt.Sprintf("rate:%s", key)

	count, err := Rdb.Incr(Ctx, redisKey).Result()
	if err != nil {
		return false, err
	}

	if count == 1 {
		Rdb.Expire(Ctx, redisKey, window)
	}

	if count > int64(limit) {
		return false, nil
	}

	return true, nil
}