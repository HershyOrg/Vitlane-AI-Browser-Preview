package app

import "testing"

func TestCatalogProductSearchRequestKeepsPriceOptional(t *testing.T) {
	t.Parallel()
	request := CatalogProductSearchRequest{Query: "trail shoes", Limit: 20}
	if err := request.Validate(); err != nil {
		t.Fatalf("price-free request should be valid: %v", err)
	}
	maximum := int64(15000)
	request.Filters.Price = &CatalogPriceFilter{MaximumMinor: &maximum}
	if err := request.Validate(); err == nil {
		t.Fatal("priced request without currency context should fail")
	}
	request.Context.Currency = "USD"
	if err := request.Validate(); err != nil {
		t.Fatalf("priced request with currency context should be valid: %v", err)
	}
}

func TestCatalogAppliedFilterProofBindsAbsentAndExplicitZeroPrice(t *testing.T) {
	t.Parallel()
	request := CatalogProductSearchRequest{
		Query: "trail shoes", Context: CatalogBuyerContext{Currency: "USD"}, Limit: 20,
	}
	none, err := NewCatalogAppliedFilterProofV2(
		request, CatalogProviderShopifyGlobalV2, "2026-04-08", "request:none",
		"dev.ucp.shopping.catalog.search", "2026-04-08", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if none.Price != nil || none.PriceCurrency != "" {
		t.Fatalf("absent price gained proof data: %+v", none)
	}
	if err := none.ValidateForRequest(request, CatalogProviderShopifyGlobalV2, "2026-04-08"); err != nil {
		t.Fatalf("validate absent price proof: %v", err)
	}

	zero := int64(0)
	request.Filters.Price = &CatalogPriceFilter{
		MinimumMinor: &zero, MaximumMinor: &zero,
	}
	explicit, err := NewCatalogAppliedFilterProofV2(
		request, CatalogProviderShopifyGlobalV2, "2026-04-08", "request:zero",
		"dev.ucp.shopping.catalog.search", "2026-04-08", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Price == nil || explicit.Price.MinimumMinor == nil ||
		explicit.Price.MaximumMinor == nil || *explicit.Price.MinimumMinor != 0 ||
		*explicit.Price.MaximumMinor != 0 || explicit.PriceCurrency != "USD" {
		t.Fatalf("explicit zero was not preserved: %+v", explicit)
	}
	if explicit.EvidenceHash == none.EvidenceHash || explicit.RequestHash == none.RequestHash {
		t.Fatal("absent and explicit zero price produced the same proof hash")
	}
	if err := explicit.ValidateForRequest(request, CatalogProviderShopifyGlobalV2, "2026-04-08"); err != nil {
		t.Fatalf("validate explicit zero proof: %v", err)
	}
	tampered := explicit
	wrong := int64(1)
	tampered.Price = &CatalogPriceFilter{MinimumMinor: &wrong, MaximumMinor: &wrong}
	if err := tampered.ValidateForRequest(request, CatalogProviderShopifyGlobalV2, "2026-04-08"); err == nil {
		t.Fatal("tampered price proof was accepted")
	}
}

func TestCatalogProductLocatorIsATaggedUnion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		locator CatalogProductLocator
		valid   bool
	}{
		{
			name: "product URL",
			locator: CatalogProductLocator{
				Kind: CatalogLocatorProductURL,
				ProductURL: &CatalogProductURLLocator{
					CanonicalURL: "https://merchant.example/products/shoe",
				},
			},
			valid: true,
		},
		{
			name: "merchant variant",
			locator: CatalogProductLocator{
				Kind: CatalogLocatorMerchantVariant,
				MerchantVariant: &CatalogMerchantVariantLocator{
					VariantID:    "gid://shopify/ProductVariant/1",
					SellerDomain: "merchant.example",
				},
			},
			valid: true,
		},
		{
			name: "both payloads",
			locator: CatalogProductLocator{
				Kind:       CatalogLocatorProductURL,
				ProductURL: &CatalogProductURLLocator{CanonicalURL: "https://merchant.example/p"},
				MerchantVariant: &CatalogMerchantVariantLocator{
					VariantID: "variant", SellerDomain: "merchant.example",
				},
			},
		},
		{name: "empty", locator: CatalogProductLocator{}},
		{
			name: "unsafe URL",
			locator: CatalogProductLocator{
				Kind:       CatalogLocatorProductURL,
				ProductURL: &CatalogProductURLLocator{CanonicalURL: "javascript:alert(1)"},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.locator.Validate()
			if test.valid && err != nil {
				t.Fatalf("expected valid locator: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("expected invalid locator")
			}
		})
	}
}

func TestCatalogMediaLookupRequestRequiresUniqueCorrelationKeys(t *testing.T) {
	t.Parallel()
	request := CatalogMediaLookupRequest{Inputs: []CatalogMediaLookupInput{
		{CorrelationKey: "candidate-1", Identifier: "product-a"},
		{CorrelationKey: "candidate-1", Identifier: "product-b"},
	}}
	if err := request.Validate(); err == nil {
		t.Fatal("duplicate correlation key should fail")
	}
	request.Inputs[1].CorrelationKey = "candidate-2"
	if err := request.Validate(); err != nil {
		t.Fatalf("unique correlation keys should pass: %v", err)
	}
}
