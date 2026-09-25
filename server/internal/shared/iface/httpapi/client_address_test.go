package httpapi

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestResolveClientAddressTrustBoundary(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.30.0.0/24")}
	tests := []struct {
		name, remote, forwarded, want string
		ok                            bool
	}{
		{"trusted single", "172.30.0.4:1234", "203.0.113.9", "203.0.113.9", true},
		{"trusted chain rejected", "172.30.0.4:1234", "203.0.113.9, 172.30.0.2", "", false},
		{"trusted invalid rejected", "172.30.0.4:1234", "attacker", "", false},
		{"untrusted spoof ignored", "198.51.100.8:1234", "203.0.113.9", "198.51.100.8", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://example.test", nil)
			r.RemoteAddr = test.remote
			r.Header.Set("X-Forwarded-For", test.forwarded)
			got, ok := ResolveClientAddress(r, trusted)
			if ok != test.ok || (ok && got.String() != test.want) {
				t.Fatalf("got=%s ok=%v", got, ok)
			}
		})
	}
}
