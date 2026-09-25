package http

import "testing"

func TestAgentProfileDeclaresOnlyImplementedCommerceCapabilities(t *testing.T) {
	document := AgentProfileDocument()
	ucp, ok := document["ucp"].(map[string]any)
	if !ok || ucp["version"] != "2026-04-08" {
		t.Fatalf("profile=%#v", document)
	}
	capabilities, ok := ucp["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities=%#v", ucp["capabilities"])
	}
	for _, required := range []string{"dev.ucp.shopping.cart", "dev.ucp.shopping.checkout", "dev.ucp.shopping.fulfillment"} {
		if _, exists := capabilities[required]; !exists {
			t.Fatalf("missing %s", required)
		}
	}
	for _, prohibited := range []string{"dev.ucp.shopping.order", "dev.ucp.shopping.discount", "dev.ucp.shopping.buyer_consent"} {
		if _, exists := capabilities[prohibited]; exists {
			t.Fatalf("prohibited %s", prohibited)
		}
	}
	if hash, err := AgentProfileHash(); err != nil || hash == "" {
		t.Fatalf("hash=%q err=%v", hash, err)
	}
}
