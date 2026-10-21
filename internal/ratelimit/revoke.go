package ratelimit

import (
	"errors"
	"fmt"
)

// IsRevoked checks whether a JTI has been revoked.
// A missing or unavailable store is an error, never evidence of authorization.
func IsRevoked(jti string) (bool, error) {
	if Rdb == nil {
		return false, errors.New("revocation store unavailable")
	}
	key := fmt.Sprintf("revoked:%s", jti)
	n, err := Rdb.Exists(Ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
