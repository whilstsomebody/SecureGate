// Package trustedproxy evaluates forwarding headers only when the direct peer
// belongs to an explicitly configured proxy network.
package trustedproxy

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Policy contains immutable trusted proxy networks.
type Policy struct{ networks []netip.Prefix }

func New(networks []netip.Prefix) Policy {
	return Policy{networks: append([]netip.Prefix(nil), networks...)}
}

func (p Policy) Networks() []netip.Prefix { return append([]netip.Prefix(nil), p.networks...) }

func (p Policy) Len() int { return len(p.networks) }

func (p Policy) IsTrusted(addr netip.Addr) bool {
	for _, network := range p.networks {
		if network.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP returns the direct peer unless it is trusted. For trusted peers it
// walks X-Forwarded-For from right to left, skipping trusted proxy hops.
func (p Policy) ClientIP(r *http.Request) string {
	peer, ok := peerAddr(r.RemoteAddr)
	if !ok || !p.IsTrusted(peer) {
		return fallbackPeer(r.RemoteAddr, peer, ok)
	}
	chain, valid := forwardedChain(r.Header.Values("X-Forwarded-For"))
	if !valid || len(chain) == 0 {
		return peer.String()
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if !p.IsTrusted(chain[i]) {
			return chain[i].String()
		}
	}
	// A trusted proxy may itself be the originating client in private networks.
	return chain[0].String()
}

// ForwardedFor returns a safe X-Forwarded-For value for the next hop. An
// untrusted peer, malformed chain, or absent chain never reaches upstream.
func (p Policy) ForwardedFor(r *http.Request) string {
	peer, ok := peerAddr(r.RemoteAddr)
	if !ok {
		return ""
	}
	if !p.IsTrusted(peer) {
		return peer.String()
	}
	chain, valid := forwardedChain(r.Header.Values("X-Forwarded-For"))
	if !valid {
		return peer.String()
	}
	chain = append(chain, peer)
	parts := make([]string, len(chain))
	for i, addr := range chain {
		parts[i] = addr.String()
	}
	return strings.Join(parts, ", ")
}

func peerAddr(remote string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func fallbackPeer(remote string, peer netip.Addr, parsed bool) string {
	if parsed {
		return peer.String()
	}
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return strings.Trim(strings.TrimSpace(host), "[]")
	}
	return remote
}

func forwardedChain(values []string) ([]netip.Addr, bool) {
	if len(values) == 0 {
		return nil, true
	}
	var chain []netip.Addr
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			raw := strings.Trim(strings.TrimSpace(item), "[]")
			addr, err := netip.ParseAddr(raw)
			if err != nil {
				return nil, false
			}
			chain = append(chain, addr.Unmap())
		}
	}
	return chain, true
}
