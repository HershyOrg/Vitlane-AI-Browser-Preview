package shopifyucp

import (
	"encoding/json"
	"testing"
)

func TestDecodeStorefrontDeferredCarrierRates(t *testing.T) {
	body := "--graphql\r\nContent-Type: application/json\r\n\r\n" +
		`{"data":{"cartDeliveryAddressesAdd":{"cart":{"id":"cart-1"},"userErrors":[]}},"hasNext":true}` +
		"\r\n--graphql\r\nContent-Type: application/json\r\n\r\n" +
		`{"incremental":[{"path":["cartDeliveryAddressesAdd","cart"],"data":{"deliveryGroups":{"nodes":[{"id":"group-1","deliveryOptions":[{"handle":"ground"}]}]}}}],"hasNext":false}` +
		"\r\n--graphql--\r\n"
	data, graphQLErrors, err := decodeStorefrontResponse("multipart/mixed; boundary=graphql", []byte(body))
	if err != nil || len(graphQLErrors) != 0 {
		t.Fatalf("decode deferred response: errors=%#v err=%v", graphQLErrors, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	operation := decoded["cartDeliveryAddressesAdd"].(map[string]any)
	cart := operation["cart"].(map[string]any)
	if cart["id"] != "cart-1" || cart["deliveryGroups"] == nil {
		t.Fatalf("deferred carrier rates were not merged: %#v", decoded)
	}
}

func TestDecodeStorefrontDeferredResponseRejectsInvalidPath(t *testing.T) {
	body := "--graphql\r\nContent-Type: application/json\r\n\r\n" +
		`{"data":{"cart":{"id":"cart-1"}},"hasNext":true}` +
		"\r\n--graphql\r\nContent-Type: application/json\r\n\r\n" +
		`{"incremental":[{"path":[0],"data":{"deliveryGroups":{}}}],"hasNext":false}` +
		"\r\n--graphql--\r\n"
	if _, _, err := decodeStorefrontResponse("multipart/mixed; boundary=graphql", []byte(body)); err == nil {
		t.Fatal("invalid deferred response path was accepted")
	}
}
