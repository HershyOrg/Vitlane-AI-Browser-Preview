package app

import (
	"testing"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func validUSAddress() ShippingAddress {
	return ShippingAddress{
		RecipientName: "Test Buyer", AddressLine1: "131 Greene Street",
		City: "New York", Region: "ny", PostalCode: "10012", Country: "us",
		Phone: "(202) 555-0123",
	}
}

func TestNormalizeUSShippingAddressNormalizesLooseUSInput(t *testing.T) {
	normalized, err := NormalizeUSShippingAddress(validUSAddress())
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Region != "NY" || normalized.Country != "US" ||
		normalized.Phone != "+12025550123" {
		t.Fatalf("normalized=%#v", normalized)
	}
}

func TestNormalizeUSShippingAddressAcceptsPrefixedPhoneAndZIP9(t *testing.T) {
	address := validUSAddress()
	address.Phone = "+1 202-555-0123"
	address.PostalCode = "10012-1234"
	normalized, err := NormalizeUSShippingAddress(address)
	if err != nil || normalized.Phone != "+12025550123" ||
		normalized.PostalCode != "10012-1234" {
		t.Fatalf("normalized=%#v err=%v", normalized, err)
	}
}

func TestNormalizeUSShippingAddressRejectsFieldwise(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ShippingAddress)
		reason string
	}{
		{"korean phone", func(a *ShippingAddress) { a.Phone = "01012345678" }, "SHIPPING_PHONE_US_FORMAT"},
		{"kr country phone", func(a *ShippingAddress) { a.Phone = "+821012345678" }, "SHIPPING_PHONE_US_FORMAT"},
		{"empty phone", func(a *ShippingAddress) { a.Phone = "" }, "SHIPPING_PHONE_US_FORMAT"},
		{"nanp invalid area", func(a *ShippingAddress) { a.Phone = "+11025550123" }, "SHIPPING_PHONE_US_FORMAT"},
		{"non us", func(a *ShippingAddress) { a.Country = "CA" }, "SHIPPING_ADDRESS_US_ONLY"},
		{"region name", func(a *ShippingAddress) { a.Region = "New York" }, "SHIPPING_REGION_US_FORMAT"},
		{"region unknown", func(a *ShippingAddress) { a.Region = "ZZ" }, "SHIPPING_REGION_US_FORMAT"},
		{"postal alpha", func(a *ShippingAddress) { a.PostalCode = "M5V 3A8" }, "SHIPPING_POSTAL_US_FORMAT"},
		{"postal short", func(a *ShippingAddress) { a.PostalCode = "1234" }, "SHIPPING_POSTAL_US_FORMAT"},
		{"missing recipient", func(a *ShippingAddress) { a.RecipientName = " " }, "SHIPPING_ADDRESS_FIELDS_INVALID"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			address := validUSAddress()
			testCase.mutate(&address)
			_, err := NormalizeUSShippingAddress(address)
			failure, ok := fault.As(err)
			if !ok || failure.Reason != testCase.reason {
				t.Fatalf("expected %s, got %v", testCase.reason, err)
			}
		})
	}
}
