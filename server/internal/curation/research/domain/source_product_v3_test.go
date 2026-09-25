package domain

import (
	"testing"
	"time"
)

func TestSourceProductIdentityPreservesShopifyAndSeparatesAmazon(t *testing.T) {
	shop := SourceProductRef{Source: SourceShopify, ProductID: "opaque-Shopify-ID"}
	if shop.IdentityKey() != "shopify-product:opaque-Shopify-ID" {
		t.Fatal("Shopify identity changed")
	}
	amazon := SourceProductRef{Source: SourceAmazon, Marketplace: "US", AnchorASIN: "B012345678"}
	if amazon.IdentityKey() != "amazon:US:B012345678" {
		t.Fatal("Amazon identity incorrect")
	}
	amazon.Source = SourceShopify
	if amazon.Validate() == nil {
		t.Fatal("mixed source reference accepted")
	}
}
func TestAmazonObservationCannotAuthorizeCheckoutOrAnotherASIN(t *testing.T) {
	now := time.Now().UTC()
	price := int64(12345)
	v := VariantObservation{ObservationID: "observed", VariantRef: SourceVariantRef{Source: SourceAmazon, Marketplace: "US", ASIN: "B012345678"}, Price: VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &price, Currency: "USD"}, Availability: "UNKNOWN", DeliveryEligibility: "UNCONFIRMED", Seller: ObservedSeller{Kind: "UNKNOWN"}, PurchaseRoute: "EXTERNAL", ProductURL: "https://www.amazon.com/dp/B012345678", ObservedAt: now, RefreshAfter: now.Add(15 * time.Minute)}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"https://www.amazon.com/dp/B999999999", "https://www.amazon.com.evil.example/dp/B012345678", "https://user@www.amazon.com/dp/B012345678", "http://www.amazon.com/dp/B012345678", "https://www.amazon.com:444/dp/B012345678", "https://www.amazon.com/dp/B012345678?redirect=evil"} {
		bad := v
		bad.ProductURL = raw
		if bad.Validate() == nil {
			t.Fatal("invalid URL accepted")
		}
	}
	v.PurchaseRoute = "VITLANE_CHECKOUT"
	if v.Validate() == nil {
		t.Fatal("external observation authorized checkout")
	}
}
