package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ResolveClientAddress trusts exactly one X-Forwarded-For literal only when
// the immediate peer belongs to a configured proxy prefix. Other forwarding
// headers and comma-separated chains are intentionally ignored.
func ResolveClientAddress(r *http.Request, trustedProxies []netip.Prefix) (netip.Addr, bool) {
	remote := parseRemoteAddress(r.RemoteAddr)
	if remote.IsValid() && prefixContains(trustedProxies, remote) {
		forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
		if forwarded != "" && !strings.Contains(forwarded, ",") {
			if client, err := netip.ParseAddr(forwarded); err == nil {
				return client.Unmap(), true
			}
		}
		return netip.Addr{}, false
	}
	if remote.IsValid() {
		return remote.Unmap(), true
	}
	return netip.Addr{}, false
}

func ClientAddressKey(r *http.Request, trustedProxies []netip.Prefix) string {
	if address, ok := ResolveClientAddress(r, trustedProxies); ok {
		return address.String()
	}
	return "unknown"
}

func parseRemoteAddress(value string) netip.Addr {
	host := strings.TrimSpace(value)
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	address, _ := netip.ParseAddr(host)
	return address
}

func prefixContains(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
