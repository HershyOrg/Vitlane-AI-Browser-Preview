package adapters

import (
	"context"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

type CartReader struct {
	service *curationapp.CatalogCartServiceV2
}

func NewCartReader(service *curationapp.CatalogCartServiceV2) *CartReader {
	return &CartReader{service: service}
}

func (r *CartReader) ReadCartSnapshot(ctx context.Context, userID, curationID string) (agencydomain.SourceCartSnapshot, error) {
	state, err := r.service.Get(ctx, userID, curationID)
	if err != nil {
		return agencydomain.SourceCartSnapshot{}, err
	}
	items := make([]agencydomain.CartItemSnapshot, 0, len(state.Items))
	for _, item := range state.Items {
		items = append(items, agencydomain.CartItemSnapshot{
			CartItemID: item.Item.ID, PlanTargetID: item.TargetID,
			CandidateID:  item.Item.CandidateID,
			ProductTitle: item.Item.ProductTitleSnapshot, ProductURL: item.Item.ProductURL,
			VariantID: item.Item.VariantID, VariantTitle: item.Item.VariantTitleSnapshot,
			SelectedOptions:  append([]string(nil), item.Item.SelectedOptions...),
			PreviewUnitPrice: agencydomain.Money{AmountMinor: item.Item.PreviewPriceMinor, Currency: item.Item.PreviewCurrency},
			Quantity:         item.Item.Quantity, SellerDomain: item.SellerDomain, ObservedAt: item.Item.ObservedAt,
		})
	}
	snapshot := agencydomain.SourceCartSnapshot{CartID: state.CurationID, CartVersion: state.Version, Items: items}
	hash, err := shareddomain.CanonicalJSONHash(struct {
		CartID      string                          `json:"cartId"`
		CartVersion int64                           `json:"cartVersion"`
		Items       []agencydomain.CartItemSnapshot `json:"items"`
	}{snapshot.CartID, snapshot.CartVersion, snapshot.Items})
	if err != nil {
		return agencydomain.SourceCartSnapshot{}, err
	}
	snapshot.SnapshotHash = hash
	return snapshot, nil
}
