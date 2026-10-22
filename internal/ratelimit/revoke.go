package ratelimit

import (
	"context"
	"errors"
	"fmt"
)

// IsRevoked checks whether a JTI has been revoked.
// A missing or unavailable store is an error, never evidence of authorization.
func IsRevoked(ctx context.Context, jti string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if Rdb == nil {
		return false, errors.New("revocation store unavailable")
	}
	key := fmt.Sprintf("revoked:%s", jti)
	n, err := Rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
