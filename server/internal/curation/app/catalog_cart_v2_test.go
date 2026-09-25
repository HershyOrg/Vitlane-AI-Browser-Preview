package app

import (
	"context"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type catalogCartRepositoryV2 struct {
	state      CatalogCartStateV2
	replaceErr error
	replaces   int
}

func (repository *catalogCartRepositoryV2) GetCatalogCartV2(
	_ context.Context, userID, curationID string,
) (CatalogCartStateV2, error) {
	state := repository.state
	state.UserID = userID
	state.CurationID = curationID
	return state, nil
}

func (repository *catalogCartRepositoryV2) ReplaceCatalogCartV2(
	_ context.Context,
	userID, curationID string,
	expectedVersion int64,
	items []CatalogCartItemV2,
	now time.Time,
) (CatalogCartStateV2, error) {
	if repository.replaceErr != nil {
		return CatalogCartStateV2{}, repository.replaceErr
	}
	if expectedVersion != repository.state.Version {
		return CatalogCartStateV2{}, ErrCatalogCartVersionConflict
	}
	repository.replaces++
	repository.state = CatalogCartStateV2{
		UserID: userID, CurationID: curationID, Version: expectedVersion + 1,
		Country: "US", Currency: "USD", Items: items, UpdatedAt: now,
	}
	return repository.state, nil
}

func TestCatalogCartReplacePersistsFallibleDraftWithoutProviderResolution(t *testing.T) {
	now := time.Date(2026, 8, 13, 4, 5, 6, 0, time.UTC)
	repository := &catalogCartRepositoryV2{state: CatalogCartStateV2{Version: 0}}
	service, err := NewCatalogCartServiceV2(repository, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}

	state, err := service.Replace(context.Background(), "user-1", "curation-1", 0, []CatalogCartItemV2{{
		TargetID: "target-1", Merchant: "Merchant", IntentPoint: "Fits the commute.",
		Item: curationdomain.CartItemV2{
			ID: "cart-1", CandidateID: "candidate-1", ProductTitleSnapshot: "Pack",
			VariantID: "gid://shopify/ProductVariant/1", VariantTitleSnapshot: "Black",
			SelectedOptions: []string{"Color: Black"}, PreviewPriceMinor: 7600,
			PreviewCurrency: "usd", Quantity: 1, ObservedAt: now.Add(-time.Minute),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if repository.replaces != 1 || state.Version != 1 || len(state.Items) != 1 ||
		state.Items[0].Item.PreviewCurrency != "USD" ||
		!state.Items[0].Item.AddedAt.Equal(now) {
		t.Fatalf("persisted state=%+v replaces=%d", state, repository.replaces)
	}
	// The use case has no Shopify, token, OfferResolution, ResolvedOffer or
	// expiry port. Those checks intentionally begin at Prepare Agency Order.
}

func TestCatalogCartReplaceMapsOptimisticVersionConflict(t *testing.T) {
	repository := &catalogCartRepositoryV2{state: CatalogCartStateV2{Version: 2}}
	service, err := NewCatalogCartServiceV2(
		repository, fixedClock{now: time.Date(2026, 8, 13, 4, 5, 6, 0, time.UTC)},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Replace(context.Background(), "user-1", "curation-1", 1, nil)
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.Conflict || failure.Reason != "PHASE8_CART_VERSION_CONFLICT" ||
		!failure.Retryable {
		t.Fatalf("failure=%+v err=%v", failure, err)
	}
}
