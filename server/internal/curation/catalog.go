package curation

import (
	"context"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

// CatalogLineInput is the stable Curation boundary consumed by AgencyOrder.
// Research implementation types do not cross the product boundary.
type CatalogLineInput struct {
	CartItemID        string
	CandidateID       string
	ProductTitle      string
	ProductURL        string
	VariantID         string
	VariantTitle      string
	SelectedOptions   []string
	PreviewPriceMinor int64
	PreviewCurrency   string
	Quantity          int
}

type PreparedCatalogLine struct {
	CartItemID        string
	Ready             bool
	ReasonCode        string
	ProductTitle      string
	VariantID         string
	VariantTitle      string
	ProductURL        string
	CurrentPriceMinor int64
	Currency          string
	Quantity          int
	MediaURL          string
}

type CatalogPreparation struct {
	Provider   string
	ObservedAt string
	Lines      []PreparedCatalogLine
}

type CatalogOrderPreparer struct {
	service *researchapp.LiveCatalogReviewServiceV2
}

func NewCatalogOrderPreparer(service *researchapp.LiveCatalogReviewServiceV2) *CatalogOrderPreparer {
	return &CatalogOrderPreparer{service: service}
}

func (p *CatalogOrderPreparer) PrepareAgencyOrder(
	ctx context.Context,
	country, currency string,
	items []CatalogLineInput,
) (CatalogPreparation, error) {
	inputs := make([]researchapp.CartItemInputV2, 0, len(items))
	for _, item := range items {
		inputs = append(inputs, researchapp.CartItemInputV2{
			CartItemID: item.CartItemID, CandidateID: item.CandidateID,
			ProductTitle: item.ProductTitle, ProductURL: item.ProductURL,
			VariantID: item.VariantID, VariantTitle: item.VariantTitle,
			SelectedOptions:   append([]string(nil), item.SelectedOptions...),
			PreviewPriceMinor: item.PreviewPriceMinor,
			PreviewCurrency:   item.PreviewCurrency,
			Quantity:          item.Quantity,
		})
	}
	result, err := p.service.PrepareAgencyOrder(ctx, researchapp.PrepareAgencyOrderPreviewInputV2{
		Country: country, Currency: currency, Items: inputs,
	})
	if err != nil {
		return CatalogPreparation{}, err
	}
	prepared := CatalogPreparation{
		Provider: result.Provider, ObservedAt: result.ObservedAt,
		Lines: make([]PreparedCatalogLine, 0, len(result.Lines)),
	}
	for _, line := range result.Lines {
		prepared.Lines = append(prepared.Lines, PreparedCatalogLine{
			CartItemID:   line.CartItemID,
			Ready:        line.Status == researchapp.PreparedCartItemReadyV2,
			ReasonCode:   line.SafeReasonCode,
			ProductTitle: line.ProductTitle, VariantID: line.VariantID,
			VariantTitle: line.VariantTitle, ProductURL: line.ProductURL,
			CurrentPriceMinor: line.CurrentPriceMinor, Currency: line.Currency,
			Quantity: line.Quantity, MediaURL: line.MediaURL,
		})
	}
	return prepared, nil
}
