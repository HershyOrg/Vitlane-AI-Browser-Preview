package domain

import (
	"testing"
	"time"
)

func TestLikedVariantV2ValidatesDurableUserPreferenceSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 13, 4, 30, 0, 0, time.UTC)
	value, err := NewLikedVariantV2(LikedVariantV2{
		UserID:      "e5000000-0000-4000-8000-000000000103",
		CurationID:  "e5100000-0000-4000-8000-000000000002",
		CandidateID: "gid://shopify/Product/1", VariantID: "gid://shopify/ProductVariant/2",
		ProductTitle: " Commuter pack ", VariantTitle: " Black ",
		ProductURL: "https://shop.example/products/pack?variant=2",
		Merchant:   " Shop Example ", PriceMinor: 0, Currency: "usd",
		TargetTitle: " Commuter backpack ", UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.Currency != "USD" || value.ProductTitle != "Commuter pack" || value.PriceMinor != 0 {
		t.Fatalf("unexpected normalized value: %#v", value)
	}

	for name, mutate := range map[string]func(*LikedVariantV2){
		"cross-scheme URL": func(value *LikedVariantV2) { value.ProductURL = "http://shop.example/pack" },
		"negative price":   func(value *LikedVariantV2) { value.PriceMinor = -1 },
		"invalid curation": func(value *LikedVariantV2) { value.CurationID = "curation-1" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := value
			mutate(&invalid)
			if _, err := NewLikedVariantV2(invalid); err == nil {
				t.Fatal("expected invalid liked Variant snapshot")
			}
		})
	}
}
