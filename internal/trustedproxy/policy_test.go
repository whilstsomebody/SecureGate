package trustedproxy

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func testPolicy() Policy {
	return New([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.0/24")})
}

func TestClientIPUsesTrustedProxyChain(t *testing.T) {
	r := httptest.NewRequest("GET", "http://gateway.test", nil)
	r.RemoteAddr = "10.1.2.3:443"
	r.Header.Set("X-Forwarded-For", "198.51.100.10, 192.0.2.7")
	if got := testPolicy().ClientIP(r); got != "198.51.100.10" {
		t.Fatalf("client IP = %q", got)
	}
	if got := testPolicy().ForwardedFor(r); got != "198.51.100.10, 192.0.2.7, 10.1.2.3" {
		t.Fatalf("forwarded chain = %q", got)
	}
}

func TestUntrustedOrMalformedForwardingHeadersAreIgnored(t *testing.T) {
	policy := testPolicy()
	for _, tc := range []struct{ remote, xff, want string }{
		{"203.0.113.4:443", "198.51.100.10", "203.0.113.4"},
		{"10.1.2.3:443", "198.51.100.10, unknown", "10.1.2.3"},
		{"10.1.2.3:443", "", "10.1.2.3"},
	} {
		r := httptest.NewRequest("GET", "http://gateway.test", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Forwarded-For", tc.xff)
		if got := policy.ClientIP(r); got != tc.want {
			t.Errorf("ClientIP(%q, %q) = %q, want %q", tc.remote, tc.xff, got, tc.want)
		}
		if got := policy.ForwardedFor(r); got != tc.want {
			t.Errorf("ForwardedFor(%q, %q) = %q, want %q", tc.remote, tc.xff, got, tc.want)
		}
	}
}

func TestIPv4MappedIPv6AndBracketedAddresses(t *testing.T) {
	policy := testPolicy()
	r := httptest.NewRequest("GET", "http://gateway.test", nil)
	r.RemoteAddr = "[::ffff:10.1.2.3]:443"
	r.Header.Set("X-Forwarded-For", "198.51.100.10, [::ffff:192.0.2.7]")
	if got := policy.ClientIP(r); got != "198.51.100.10" {
		t.Fatalf("client IP = %q, want %q", got, "198.51.100.10")
	}
	if got := policy.ForwardedFor(r); got != "198.51.100.10, 192.0.2.7, 10.1.2.3" {
		t.Fatalf("forwarded chain = %q", got)
	}

	// Bracketed IPv6 peer without port
	r = httptest.NewRequest("GET", "http://gateway.test", nil)
	r.RemoteAddr = "[2001:db8::1]"
	if got := policy.ClientIP(r); got != "2001:db8::1" {
		t.Fatalf("bracketed peer = %q", got)
	}
}

func TestAllTrustedProxyChain(t *testing.T) {
	policy := testPolicy()
	r := httptest.NewRequest("GET", "http://gateway.test", nil)
	r.RemoteAddr = "10.1.2.3:443"
	r.Header.Set("X-Forwarded-For", "10.9.8.7, 192.0.2.1")
	if got := policy.ClientIP(r); got != "10.9.8.7" {
		t.Fatalf("client IP = %q, want %q", got, "10.9.8.7")
	}
}

func TestEmptyPolicyNeverTrustsHeaders(t *testing.T) {
	policy := Policy{}
	r := httptest.NewRequest("GET", "http://gateway.test", nil)
	r.RemoteAddr = "10.1.2.3:443"
	r.Header.Set("X-Forwarded-For", "198.51.100.10")
	if got := policy.ClientIP(r); got != "10.1.2.3" {
		t.Fatalf("client IP = %q", got)
	}
	if got := policy.ForwardedFor(r); got != "10.1.2.3" {
		t.Fatalf("forwarded = %q", got)
	}
}

