package http

import (
	"net/http"
	"net/netip"

	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

// registrationClientKey identifies the caller for abuse limiting. A forwarded
// address is only believed when the immediate peer is a trusted proxy, and only
// when it names a single hop: a client-supplied chain would otherwise let an
// attacker pick their own rate-limit bucket.
func registrationClientKey(r *http.Request, trustedProxies []netip.Prefix) string {
	return httpapi.ClientAddressKey(r, trustedProxies)
}
