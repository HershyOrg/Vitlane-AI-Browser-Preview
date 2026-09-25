package adapters

import (
	"context"
	"net/url"
	"strings"
	"time"

	curation "github.com/vitlane/vitlane/server/internal/curation"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

type catalogOrderPreparer interface {
	PrepareAgencyOrder(
		context.Context,
		string,
		string,
		[]curation.CatalogLineInput,
	) (curation.CatalogPreparation, error)
}

type ExactLineResolver struct {
	preparer catalogOrderPreparer
}

func NewExactLineResolver(preparer catalogOrderPreparer) *ExactLineResolver {
	return &ExactLineResolver{preparer: preparer}
}

func (r *ExactLineResolver) ResolveExactLines(ctx context.Context, cart agencydomain.SourceCartSnapshot) ([]agencydomain.ExactLine, error) {
	inputs := make([]curation.CatalogLineInput, 0, len(cart.Items))
	byID := make(map[string]agencydomain.CartItemSnapshot, len(cart.Items))
	for _, item := range cart.Items {
		byID[item.CartItemID] = item
		inputs = append(inputs, curation.CatalogLineInput{
			CartItemID: item.CartItemID, CandidateID: item.CandidateID,
			ProductTitle: item.ProductTitle, ProductURL: item.ProductURL,
			VariantID: item.VariantID, VariantTitle: item.VariantTitle,
			SelectedOptions:   append([]string(nil), item.SelectedOptions...),
			PreviewPriceMinor: item.PreviewUnitPrice.AmountMinor,
			PreviewCurrency:   item.PreviewUnitPrice.Currency, Quantity: item.Quantity,
		})
	}
	result, err := r.preparer.PrepareAgencyOrder(ctx, "US", "USD", inputs)
	if err != nil {
		return nil, err
	}
	lines := make([]agencydomain.ExactLine, 0, len(result.Lines))
	for _, prepared := range result.Lines {
		if !prepared.Ready {
			cartItem := byID[prepared.CartItemID]
			itemTitle := strings.TrimSpace(prepared.ProductTitle)
			if itemTitle == "" {
				itemTitle = strings.TrimSpace(cartItem.ProductTitle)
			}
			return nil, &agencydomain.OrderPreparationLineError{
				ReasonCode: prepared.ReasonCode,
				ItemTitle:  itemTitle,
				Retryable:  prepared.ReasonCode != "VARIANT_UNAVAILABLE",
			}
		}
		cartItem := byID[prepared.CartItemID]
		shopDomain := canonicalProductHost(prepared.ProductURL)
		if shopDomain == "" {
			shopDomain = strings.ToLower(strings.TrimSpace(cartItem.SellerDomain))
		}
		line := agencydomain.ExactLine{
			SourceCartItemID: prepared.CartItemID, PlanTargetID: cartItem.PlanTargetID,
			CandidateID:     cartItem.CandidateID,
			CatalogProvider: result.Provider, ShopDomain: shopDomain,
			ProductURL: prepared.ProductURL, ImageURL: prepared.MediaURL, VariantID: prepared.VariantID,
			ProductTitle: prepared.ProductTitle, VariantTitle: prepared.VariantTitle,
			SelectedOptions: append([]string(nil), cartItem.SelectedOptions...), Quantity: prepared.Quantity,
			UnitPrice:    agencydomain.Money{AmountMinor: prepared.CurrentPriceMinor, Currency: prepared.Currency},
			LineSubtotal: agencydomain.Money{AmountMinor: prepared.CurrentPriceMinor * int64(prepared.Quantity), Currency: prepared.Currency},
		}
		if observedAt, parseErr := time.Parse(time.RFC3339Nano, result.ObservedAt); parseErr == nil {
			line.ObservedAt = observedAt
		}
		hash, hashErr := shareddomain.CanonicalJSONHash(struct {
			CartItemID string             `json:"cartItemId"`
			VariantID  string             `json:"variantId"`
			ShopDomain string             `json:"shopDomain"`
			Quantity   int                `json:"quantity"`
			UnitPrice  agencydomain.Money `json:"unitPrice"`
			ObservedAt time.Time          `json:"observedAt"`
		}{line.SourceCartItemID, line.VariantID, line.ShopDomain, line.Quantity, line.UnitPrice, line.ObservedAt})
		if hashErr != nil {
			return nil, hashErr
		}
		line.EvidenceHash = hash
		lines = append(lines, line)
	}
	return lines, nil
}

func canonicalProductHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}
