package adapters

import (
	"context"
	"errors"
	"testing"

	curation "github.com/vitlane/vitlane/server/internal/curation"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

type catalogPreparerStub struct {
	result curation.CatalogPreparation
}

func (s catalogPreparerStub) PrepareAgencyOrder(
	context.Context,
	string,
	string,
	[]curation.CatalogLineInput,
) (curation.CatalogPreparation, error) {
	return s.result, nil
}

func TestExactLineResolverPreservesBlockedItemTitleAndReason(t *testing.T) {
	for _, test := range []struct {
		name       string
		reasonCode string
		retryable  bool
	}{
		{name: "unavailable", reasonCode: "VARIANT_UNAVAILABLE", retryable: false},
		{name: "not resolved", reasonCode: "VARIANT_NOT_RESOLVED", retryable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver := NewExactLineResolver(catalogPreparerStub{result: curation.CatalogPreparation{
				Lines: []curation.PreparedCatalogLine{{
					CartItemID: "cart-item-1", Ready: false, ReasonCode: test.reasonCode,
				}},
			}})
			_, err := resolver.ResolveExactLines(context.Background(), agencydomain.SourceCartSnapshot{
				Items: []agencydomain.CartItemSnapshot{{
					CartItemID: "cart-item-1", ProductTitle: "Classic Rim Dinnerware Set",
					VariantID:        "gid://shopify/ProductVariant/1",
					PreviewUnitPrice: agencydomain.Money{AmountMinor: 1000, Currency: "USD"},
					Quantity:         1,
				}},
			})
			var lineFailure *agencydomain.OrderPreparationLineError
			if !errors.As(err, &lineFailure) {
				t.Fatalf("expected OrderPreparationLineError, got %v", err)
			}
			if lineFailure.ReasonCode != test.reasonCode ||
				lineFailure.ItemTitle != "Classic Rim Dinnerware Set" ||
				lineFailure.Retryable != test.retryable {
				t.Fatalf("line failure = %#v", lineFailure)
			}
		})
	}
}
