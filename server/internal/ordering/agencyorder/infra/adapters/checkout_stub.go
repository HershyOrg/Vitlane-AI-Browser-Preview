package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

// CheckoutStub is an explicit development/test substitute. It is deliberately
// production-shaped: it returns token-tier, domestic and stable checkout facts,
// but it never contacts Shopify and refuses to construct in production.
type CheckoutStub struct {
	now         func() time.Time
	profileURL  string
	profileHash string
}

func NewCheckoutStub(environment, profileURL, profileHash string) (*CheckoutStub, error) {
	if strings.EqualFold(strings.TrimSpace(environment), "production") {
		return nil, fmt.Errorf("production cannot use AgencyOrder checkout stub")
	}
	return &CheckoutStub{now: time.Now, profileURL: profileURL, profileHash: profileHash}, nil
}

func (s *CheckoutStub) Explore(_ context.Context, input agencyapp.MerchantRequest) (agencydomain.MerchantCheckout, error) {
	lineRefs := make([]string, 0, len(input.Lines))
	for _, line := range input.Lines {
		lineRefs = append(lineRefs, line.LineID)
	}
	return agencydomain.MerchantCheckout{
		MerchantID: input.ShopDomain, ShopDomain: input.ShopDomain,
		StorefrontCartSafeRef: "stub-cart:" + input.OrderSheetSessionID + ":" + input.ShopDomain,
		LineRefs:              lineRefs,
		DeliveryGroups: []agencydomain.DeliveryGroup{{ID: "stub-group", LineRefs: lineRefs,
			Options:           []agencydomain.DeliveryOption{{ID: "standard", Title: "Standard delivery", AmountMinor: 700, Currency: "USD"}},
			SelectedOptionRef: "standard"}},
		DeliveryOptions:           []agencydomain.DeliveryOption{{ID: "standard", Title: "Standard delivery", AmountMinor: 700, Currency: "USD"}},
		SelectedDeliveryOptionRef: "standard", ProviderStatus: "cart_ready",
		QuoteReadiness: agencydomain.PricingEstimated, ProcurementHandling: agencydomain.ProcurementHandlingNormal,
		ObservedAt: s.now().UTC(), ExpiresAt: s.now().UTC().Add(20 * time.Minute),
	}, nil
}

func (s *CheckoutStub) SelectDelivery(_ context.Context, input agencyapp.MerchantRequest, selections map[string]string) (agencydomain.MerchantCheckout, error) {
	if input.Existing == nil || len(selections) != len(input.Existing.DeliveryGroups) {
		return agencydomain.MerchantCheckout{}, agencydomain.ErrInvalid
	}
	value := *input.Existing
	value.DeliveryGroups = append([]agencydomain.DeliveryGroup(nil), input.Existing.DeliveryGroups...)
	for index := range value.DeliveryGroups {
		optionID := selections[value.DeliveryGroups[index].ID]
		valid := false
		for _, option := range value.DeliveryGroups[index].Options {
			if option.ID == optionID {
				valid = true
				break
			}
		}
		if !valid {
			return agencydomain.MerchantCheckout{}, agencydomain.ErrInvalid
		}
		value.DeliveryGroups[index].SelectedOptionRef = optionID
	}
	return value, nil
}

func (s *CheckoutStub) Preflight(_ context.Context, input agencyapp.MerchantRequest) (agencydomain.MerchantCheckout, error) {
	value := *input.Existing
	var subtotal int64
	for _, line := range input.Lines {
		subtotal += line.LineSubtotal.AmountMinor
	}
	shipping := int64(700)
	taxable := subtotal + shipping
	tax := (taxable*825 + 9999) / 10000
	total := subtotal + shipping + tax
	value.CheckoutSessionSafeRef = "stub-checkout:" + input.OrderSheetSessionID + ":" + input.ShopDomain
	value.ProviderStatus = "ready_for_complete"
	value.Totals = []agencydomain.Total{
		{Type: "subtotal", AmountMinor: subtotal, DisplayText: "Subtotal"},
		{Type: "fulfillment", AmountMinor: shipping, DisplayText: "Shipping"},
		{Type: "tax", AmountMinor: tax, DisplayText: "Tax"},
		{Type: "total", AmountMinor: total, DisplayText: "Total"},
	}
	value.AuthoritativeTotal = agencydomain.Money{AmountMinor: total, Currency: "USD"}
	value.TaxTotal = agencydomain.Money{AmountMinor: tax, Currency: "USD"}
	value.DutiesDisposition = agencydomain.DutiesNoSignalAtPreflight
	value.AuthEvidence = agencydomain.AuthEvidence{Tier: "TOKEN", ScopesHash: "development-token-tier",
		AgentProfileURL: s.profileURL, AgentProfileVersion: "2026-04-08",
		AgentProfileHash: s.profileHash, CompletionPermission: agencydomain.CompletionNotRequested}
	value.CompletionRoute = agencydomain.CompletionManualHandoff
	value.QuoteReadiness = agencydomain.PricingConfirmed
	value.ProcurementHandling = agencydomain.ProcurementHandlingNormal
	value.ContinueURLSafeRef = "stub-continue:" + input.OrderSheetSessionID + ":" + input.ShopDomain
	value.ContinueURLHash = "stub-continue-hash"
	value.ProviderNotices = nil
	value.ObservedAt = s.now().UTC()
	value.ExpiresAt = value.ObservedAt.Add(20 * time.Minute)
	hash, err := shareddomain.CanonicalJSONHash(struct {
		Shop   string               `json:"shop"`
		Lines  []string             `json:"lines"`
		Totals []agencydomain.Total `json:"totals"`
		Status string               `json:"status"`
	}{value.ShopDomain, value.LineRefs, value.Totals, value.ProviderStatus})
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	value.QuoteFingerprint = hash
	value.EvidenceHash = hash
	return value, nil
}

func (s *CheckoutStub) FinalGet(ctx context.Context, input agencyapp.MerchantRequest) (agencydomain.MerchantCheckout, error) {
	return s.Preflight(ctx, input)
}

func (*CheckoutStub) Cancel(context.Context, agencyapp.MerchantRequest) error { return nil }
