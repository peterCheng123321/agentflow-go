package server

import (
	"net"
	"strings"
)

// wsLoopbackEquivalent reports whether two host[:port] strings refer to the same
// loopback listener (e.g. localhost:8080 vs 127.0.0.1:8080).
func wsLoopbackEquivalent(a, b string) bool {
	ha, pa := splitHostPortFlexible(a)
	hb, pb := splitHostPortFlexible(b)
	if pa != pb {
		return false
	}
	return isLoopbackHost(ha) && isLoopbackHost(hb)
}

func splitHostPortFlexible(s string) (host, port string) {
	s = strings.TrimSpace(s)
	if h, p, err := net.SplitHostPort(s); err == nil {
		return strings.Trim(h, "[]"), p
	}
	return s, ""
}

func isLoopbackHost(h string) bool {
	h = strings.Trim(strings.ToLower(strings.Trim(h, "[]")), " ")
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}
