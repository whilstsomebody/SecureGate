package ratelimit

import (
	"fmt"
	"time"
)

// RevokeToken adds a token's JTI to Redis with a TTL matching the token's remaining lifetime.
func RevokeToken(jti string, ttl time.Duration) error {
	key := fmt.Sprintf("revoked:%s", jti)
	return Rdb.Set(Ctx, key, "1", ttl).Err()
}

// IsRevoked checks whether a JTI has been revoked.
// Returns false (not revoked) when Redis is unavailable so auth is not blocked by infra failures.
func IsRevoked(jti string) (bool, error) {
	if Rdb == nil {
		return false, nil
	}
	key := fmt.Sprintf("revoked:%s", jti)
	n, err := Rdb.Exists(Ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
