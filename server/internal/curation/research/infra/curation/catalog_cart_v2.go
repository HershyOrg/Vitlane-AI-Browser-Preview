package curation

import (
	"context"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

type CatalogCartAdapterV2 struct {
	service *curationapp.CatalogCartServiceV2
}

func NewCatalogCartAdapterV2(service *curationapp.CatalogCartServiceV2) *CatalogCartAdapterV2 {
	return &CatalogCartAdapterV2{service: service}
}

func (adapter *CatalogCartAdapterV2) ReadCatalogCartDraftV2(
	ctx context.Context,
	userID, curationID string,
) (researchapp.CatalogCartDraftSnapshotV2, error) {
	state, err := adapter.service.Get(ctx, userID, curationID)
	if err != nil {
		return researchapp.CatalogCartDraftSnapshotV2{}, err
	}
	result := researchapp.CatalogCartDraftSnapshotV2{
		Country: state.Country, Currency: state.Currency,
		Items: make([]researchapp.CartItemInputV2, 0, len(state.Items)),
	}
	for _, value := range state.Items {
		result.Items = append(result.Items, researchapp.CartItemInputV2{
			CartItemID: value.Item.ID, CandidateID: value.Item.CandidateID,
			ProductTitle:      value.Item.ProductTitleSnapshot,
			ProductURL:        value.Item.ProductURL,
			VariantID:         value.Item.VariantID,
			VariantTitle:      value.Item.VariantTitleSnapshot,
			SelectedOptions:   append([]string(nil), value.Item.SelectedOptions...),
			PreviewPriceMinor: value.Item.PreviewPriceMinor,
			PreviewCurrency:   value.Item.PreviewCurrency,
			Quantity:          value.Item.Quantity,
		})
	}
	return result, nil
}
