package catalogstub

import (
	"context"
	"testing"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

func TestSearchProductsHonorsTheHardFiltersItAttests(t *testing.T) {
	searcher, err := New("test")
	if err != nil {
		t.Fatal(err)
	}
	available := true
	maximumMinor := int64(3_000)
	request := researchapp.CatalogProductSearchRequest{
		Query: "travel mug",
		Context: researchapp.CatalogBuyerContext{
			Country: "US", Currency: "USD", Language: "en", Intent: "travel mug",
		},
		Filters: researchapp.CatalogProductSearchFilters{
			Available:  &available,
			ShipsTo:    &researchapp.CatalogDestination{Country: "US"},
			Conditions: []string{"new"},
			Price:      &researchapp.CatalogPriceFilter{MaximumMinor: &maximumMinor},
		},
		Limit: 50,
	}
	result, err := searcher.SearchProducts(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.AppliedFilters.ValidateForRequest(
		request, researchapp.CatalogProviderShopifyGlobalV2, "2026-04-08",
	); err != nil {
		t.Fatalf("applied-filter proof does not match the request: %v", err)
	}
	if len(result.Products) != 3 {
		t.Fatalf("products=%#v", result.Products)
	}
	for index, product := range result.Products {
		if product.ProviderOrder != index || product.PreviewVariant == nil ||
			product.PreviewVariant.Availability.Available == nil ||
			!*product.PreviewVariant.Availability.Available ||
			product.PriceRange.Minimum.AmountMinor > maximumMinor {
			t.Fatalf("hard-filter violation at %d: %#v", index, product)
		}
		if product.ProviderProductID == "stub-product-hollow" ||
			product.ProviderProductID == "stub-product-glacier" {
			t.Fatalf("filtered fixture leaked: %s", product.ProviderProductID)
		}
	}
}

func TestLookupOffersRehydratesSearchProductForCandidatePoolRead(t *testing.T) {
	searcher, err := New("test")
	if err != nil {
		t.Fatal(err)
	}
	admission := &stubTestAdmission{}
	result, err := searcher.LookupOffers(context.Background(), researchapp.CatalogOfferLookupRequest{
		Inputs: []researchapp.CatalogOfferLookupInput{
			{DraftID: "candidate-1", Identifier: "https://aurora.example.com/products/aurora"},
			{DraftID: "candidate-2", Identifier: "stub-variant-basalt"},
			{DraftID: "candidate-3", Identifier: "unknown"},
		},
		Context:               researchapp.CatalogBuyerContext{Country: "US", Currency: "USD"},
		ProviderCallAdmission: admission,
	})
	if err != nil {
		t.Fatal(err)
	}
	if admission.calls != 1 || result.ProviderCallCount != 1 || len(result.Matches) != 2 ||
		len(result.UnresolvedIDs) != 1 || result.UnresolvedIDs[0] != "candidate-3" {
		t.Fatalf("lookup=%#v calls=%d", result, admission.calls)
	}
	if result.Matches[0].DraftID != "candidate-1" ||
		result.Matches[0].Product.ProviderProductID != "stub-product-aurora" ||
		result.Matches[1].Variant.ID != "stub-variant-basalt" {
		t.Fatalf("matches=%#v", result.Matches)
	}
}

type stubTestAdmission struct{ calls int }

func (admission *stubTestAdmission) AcquireCatalogProviderCall(
	context.Context,
) (func(), error) {
	admission.calls++
	return func() {}, nil
}
