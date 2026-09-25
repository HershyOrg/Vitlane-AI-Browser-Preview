package http

import "testing"

func TestEmailAllowlistNormalizesExactAddresses(t *testing.T) {
	list := NewEmailAllowlist([]string{" Admin@Example.com "})
	if !list.Allows("admin@example.com") {
		t.Fatal("normalized address should be allowed")
	}
	if list.Allows("other@gmail.com") {
		t.Fatal("unlisted address must be denied")
	}
}
