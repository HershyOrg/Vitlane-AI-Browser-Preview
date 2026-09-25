package app

import (
	"net"
	"net/url"
	"strings"
)

// Only public HTTPS observations may be forwarded. Local review fixtures stay
// text-only; this boundary never downloads images from arbitrary user input.
func safeAssessmentImageURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || len(raw) > 2048 {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "" || !strings.Contains(h, ".") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") || strings.HasSuffix(h, ".localhost") {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
	}
	return true
}
func removePriceFact(ids []string) []string {
	v := []string{}
	for _, id := range ids {
		if id != "price" {
			v = append(v, id)
		}
	}
	return v
}
