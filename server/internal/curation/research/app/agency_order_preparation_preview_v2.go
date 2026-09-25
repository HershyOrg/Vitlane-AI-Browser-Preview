package app

import (
	"context"
	"strings"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type CartItemInputV2 struct {
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

type PrepareAgencyOrderPreviewInputV2 struct {
	Country  string
	Currency string
	Items    []CartItemInputV2
}

type PreparedCartItemStatusV2 string

const (
	PreparedCartItemReadyV2        PreparedCartItemStatusV2 = "READY"
	PreparedCartItemPriceChangedV2 PreparedCartItemStatusV2 = "PRICE_CHANGED"
	PreparedCartItemUnavailableV2  PreparedCartItemStatusV2 = "UNAVAILABLE"
	PreparedCartItemUnresolvedV2   PreparedCartItemStatusV2 = "UNRESOLVED"
)

type PreparedCartItemV2 struct {
	CartItemID        string
	Status            PreparedCartItemStatusV2
	ProductTitle      string
	VariantID         string
	VariantTitle      string
	ProductURL        string
	CurrentPriceMinor int64
	Currency          string
	PreviewPriceMinor int64
	PreviewCurrency   string
	Quantity          int
	Available         bool
	PriceChanged      bool
	MediaURL          string
	SellerName        string
	SafeReasonCode    string
}

type AgencyOrderPreparationPreviewV2 struct {
	Status          string
	Lines           []PreparedCartItemV2
	SubtotalMinor   int64
	Currency        string
	ObservedAt      string
	Metrics         LiveCatalogReviewMetricsV2
	Provider        string
	ProtocolVersion string
}

// PrepareAgencyOrder performs the first exact read in the flow. It does not
// create an AgencyOrder, ResolvedOffer, CheckoutQuote, approval or payment.
// The response is an ephemeral preview for validating the Step 1 -> Step 2
// handoff; the next phase will persist the durable order preparation result.
func (service *LiveCatalogReviewServiceV2) PrepareAgencyOrder(
	ctx context.Context,
	input PrepareAgencyOrderPreviewInputV2,
) (AgencyOrderPreparationPreviewV2, error) {
	request, items, err := prepareAgencyOrderLookupRequestV2(input)
	if err != nil {
		return AgencyOrderPreparationPreviewV2{}, err
	}
	desiredCurrency := strings.ToUpper(strings.TrimSpace(input.Currency))
	startedAt := service.clock.Now()
	callBudget := providerLookupCallBudgetV2(len(request.Inputs))
	operation, err := service.beginProviderOperationV2(startedAt, callBudget)
	if err != nil {
		return AgencyOrderPreparationPreviewV2{}, err
	}
	defer operation.Close()
	request.ProviderCallAdmission = operation

	lookup, err := service.gateway.LookupOffers(ctx, request)
	completedAt := service.clock.Now()
	if err != nil {
		return AgencyOrderPreparationPreviewV2{}, err
	}
	providerCallCount, used, remaining := operation.Snapshot()
	if lookup.ProviderCallCount != providerCallCount {
		return AgencyOrderPreparationPreviewV2{}, fault.New(
			fault.InternalFailure, "PHASE8_OFFER_LOOKUP_CALL_COUNT_MISMATCH", false,
		)
	}
	byCartItem := make(map[string]CatalogOfferMatch, len(lookup.Matches))
	for _, match := range lookup.Matches {
		byCartItem[match.DraftID] = match
	}
	lines := make([]PreparedCartItemV2, 0, len(items))
	allReady := true
	var subtotal int64
	for _, item := range items {
		line := PreparedCartItemV2{
			CartItemID: item.CartItemID, Status: PreparedCartItemUnresolvedV2,
			ProductTitle: item.ProductTitle, VariantID: item.VariantID,
			VariantTitle: item.VariantTitle, ProductURL: item.ProductURL,
			PreviewPriceMinor: item.PreviewPriceMinor,
			PreviewCurrency:   item.PreviewCurrency, Quantity: item.Quantity,
			SafeReasonCode: "VARIANT_NOT_RESOLVED",
		}
		match, exists := byCartItem[item.CartItemID]
		if exists && strings.EqualFold(strings.TrimSpace(match.Match), "exact") &&
			strings.TrimSpace(match.Variant.ID) == item.VariantID {
			line.ProductTitle = match.Product.Title
			line.VariantTitle = match.Variant.Title
			line.CurrentPriceMinor = match.Variant.Price.AmountMinor
			line.Currency = match.Variant.Price.Currency
			line.ProductURL = freshProductURLV2(match.Product, match.Variant, item.ProductURL)
			line.Available = match.Variant.Availability.Available != nil &&
				*match.Variant.Availability.Available
			currencyMismatch := line.Currency != desiredCurrency
			line.PriceChanged = line.CurrentPriceMinor != item.PreviewPriceMinor ||
				line.Currency != item.PreviewCurrency || currencyMismatch
			if len(match.Variant.Media) > 0 {
				line.MediaURL = match.Variant.Media[0].URL
			} else if len(match.Product.Media) > 0 {
				line.MediaURL = match.Product.Media[0].URL
			}
			if match.Variant.Seller != nil {
				line.SellerName = match.Variant.Seller.Name
			}
			switch {
			case !line.Available:
				line.Status = PreparedCartItemUnavailableV2
				line.SafeReasonCode = "VARIANT_UNAVAILABLE"
			case currencyMismatch:
				line.Status = PreparedCartItemPriceChangedV2
				line.SafeReasonCode = "CURRENCY_CONFIRMATION_REQUIRED"
			case line.PriceChanged:
				line.Status = PreparedCartItemPriceChangedV2
				line.SafeReasonCode = "PRICE_CONFIRMATION_REQUIRED"
			default:
				line.Status = PreparedCartItemReadyV2
				line.SafeReasonCode = ""
			}
			if line.Currency == desiredCurrency {
				subtotal += line.CurrentPriceMinor * int64(line.Quantity)
			}
		}
		if line.Status != PreparedCartItemReadyV2 {
			allReady = false
		}
		lines = append(lines, line)
	}
	status := "ACTION_REQUIRED"
	if allReady {
		status = "READY_FOR_AGENCY_ORDER"
	}
	return AgencyOrderPreparationPreviewV2{
		Status: status, Lines: lines, SubtotalMinor: subtotal,
		Currency:   strings.ToUpper(strings.TrimSpace(input.Currency)),
		ObservedAt: completedAt.Format("2006-01-02T15:04:05.000Z07:00"),
		Provider:   lookup.Provider, ProtocolVersion: lookup.ProtocolVersion,
		Metrics: LiveCatalogReviewMetricsV2{
			PolicyVersion: LiveCatalogReviewPolicyVersionV2,
			StartedAt:     startedAt, CompletedAt: completedAt,
			Duration: completedAt.Sub(startedAt), AICallCount: 0,
			ShopifyCallCount: providerCallCount, LocalCallsUsed: used,
			LocalCallsRemaining:       remaining,
			LocalRateLimit:            service.config.MaximumCallsPerWindow,
			LocalRateWindow:           service.config.Window,
			ProviderCostStatus:        "NOT_REPORTED_BY_PROVIDER",
			ProviderBillingCredential: false,
			ExternalEffect:            "CATALOG_READ_ONLY",
		},
	}, nil
}

func providerLookupCallBudgetV2(inputCount int) int {
	if inputCount < 1 {
		return 1
	}
	return (inputCount + CatalogLookupDefaultBatchSize - 1) / CatalogLookupDefaultBatchSize
}

func prepareAgencyOrderLookupRequestV2(
	input PrepareAgencyOrderPreviewInputV2,
) (CatalogOfferLookupRequest, []CartItemInputV2, error) {
	country := strings.ToUpper(strings.TrimSpace(input.Country))
	currency := strings.ToUpper(strings.TrimSpace(input.Currency))
	if country != "US" || currency != "USD" || len(input.Items) == 0 ||
		len(input.Items) > 10 {
		return CatalogOfferLookupRequest{}, nil, fault.New(
			fault.InvalidInput, "AGENCY_ORDER_PREPARATION_DRAFTS_INVALID", false,
		)
	}
	seen := make(map[string]struct{}, len(input.Items))
	inputs := make([]CatalogOfferLookupInput, 0, len(input.Items))
	items := make([]CartItemInputV2, 0, len(input.Items))
	for _, item := range input.Items {
		item.CartItemID = strings.TrimSpace(item.CartItemID)
		item.CandidateID = strings.TrimSpace(item.CandidateID)
		item.ProductTitle = strings.TrimSpace(item.ProductTitle)
		item.ProductURL = strings.TrimSpace(item.ProductURL)
		item.VariantID = strings.TrimSpace(item.VariantID)
		item.VariantTitle = strings.TrimSpace(item.VariantTitle)
		item.PreviewCurrency = strings.ToUpper(strings.TrimSpace(item.PreviewCurrency))
		if item.CartItemID == "" || item.CandidateID == "" ||
			item.ProductTitle == "" || item.VariantID == "" ||
			item.VariantTitle == "" || item.PreviewPriceMinor < 0 ||
			!validCartCurrencyV2(item.PreviewCurrency) || item.Quantity < 1 || item.Quantity > 99 {
			return CatalogOfferLookupRequest{}, nil, fault.New(
				fault.InvalidInput, "AGENCY_ORDER_PREPARATION_DRAFT_INVALID", false,
			)
		}
		if _, exists := seen[item.CartItemID]; exists {
			return CatalogOfferLookupRequest{}, nil, fault.New(
				fault.InvalidInput, "AGENCY_ORDER_PREPARATION_DRAFT_DUPLICATED", false,
			)
		}
		seen[item.CartItemID] = struct{}{}
		inputs = append(inputs, CatalogOfferLookupInput{
			DraftID: item.CartItemID, Identifier: item.VariantID,
		})
		items = append(items, item)
	}
	request := CatalogOfferLookupRequest{Inputs: inputs, Context: CatalogBuyerContext{
		Country: country, Language: "en", Currency: currency,
		Intent: "prepare agency order from cart items",
	}}
	if err := request.Validate(); err != nil {
		return CatalogOfferLookupRequest{}, nil, fault.New(
			fault.InvalidInput, "AGENCY_ORDER_PREPARATION_DRAFTS_INVALID", false,
		)
	}
	return request, items, nil
}

func validCartCurrencyV2(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for _, character := range currency {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func freshProductURLV2(
	product CatalogProductObservation,
	variant CatalogPreviewVariant,
	fallback string,
) string {
	if strings.TrimSpace(variant.URL) != "" {
		return variant.URL
	}
	if product.Locator != nil && product.Locator.ProductURL != nil {
		return product.Locator.ProductURL.CanonicalURL
	}
	return fallback
}
