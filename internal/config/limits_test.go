package config

import (
	"strings"
	"testing"
	"time"
)

func TestResourceLimitsValidation(t *testing.T) {
	for _, key := range []string{"MAX_CONNECTIONS", "MAX_REQUESTS", "MAX_UPGRADE_REQUESTS", "MAX_UPSTREAM_CONNECTIONS", "MAX_HEADER_BYTES", "HEADER_TIMEOUT_SECONDS", "CLIENT_READ_IDLE_SECONDS", "CLIENT_WRITE_TIMEOUT_SECONDS", "KEEPALIVE_TIMEOUT_SECONDS", "UPSTREAM_DIAL_TIMEOUT_SECONDS", "UPSTREAM_HEADER_TIMEOUT_SECONDS", "UPSTREAM_READ_IDLE_SECONDS", "SHUTDOWN_TIMEOUT_SECONDS"} {
		for _, value := range []string{"0", "-1", "invalid", "9999999999999999999999"} {
			_, err := parseLimits(func(k string) string {
				if k == key {
					return value
				}
				return ""
			})
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("%s=%s: %v", key, value, err)
			}
		}
	}
	env := map[string]string{"REQUEST_TIMEOUT_SECONDS": "0", "MAX_REQUESTS": "64", "MAX_UPGRADE_REQUESTS": "8", "CLIENT_READ_IDLE_SECONDS": "120"}
	limits, err := parseLimits(func(k string) string { return env[k] })
	if err != nil || limits.RequestTimeout != 0 || limits.ClientReadIdle != 120*time.Second || limits.MaxRequests != 64 || limits.MaxUpgradeRequests != 8 {
		t.Fatalf("valid streaming config: %+v %v", limits, err)
	}
	env["MAX_UPGRADE_REQUESTS"] = "65"
	if _, err := parseLimits(func(k string) string { return env[k] }); err == nil {
		t.Fatal("accepted upgrade budget larger than total")
	}
	for _, value := range []string{"-1", "86401", "oops"} {
		if _, err := parseLimits(func(k string) string {
			if k == "REQUEST_TIMEOUT_SECONDS" {
				return value
			}
			return ""
		}); err == nil {
			t.Fatalf("accepted timeout %q", value)
		}
	}
}
