package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/policy"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

var (
	ErrInvalid                         = errors.New("AGENCY_ORDER_INVALID")
	ErrStateInvalid                    = errors.New("ORDER_SHEET_STATE_INVALID")
	ErrVersionConflict                 = errors.New("ORDER_SHEET_VERSION_CONFLICT")
	ErrNotReady                        = errors.New("AGENCY_ORDER_NOT_READY")
	ErrSnapshotChanged                 = errors.New("AGENCY_ORDER_SNAPSHOT_CHANGED")
	ErrIdempotencyConflict             = errors.New("AGENCY_ORDER_IDEMPOTENCY_CONFLICT")
	ErrNotFound                        = errors.New("AGENCY_ORDER_NOT_FOUND")
	ErrInstructionNotConsumable        = errors.New("AGENCY_ORDER_PAYMENT_INSTRUCTION_NOT_CONSUMABLE")
	ErrShippingRequired                = errors.New("SHIPPING_PROFILE_REQUIRED")
	ErrShippingInvalid                 = errors.New("SHIPPING_ADDRESS_INVALID")
	ErrProcurementAuthorizationInvalid = errors.New("PROCUREMENT_AUTHORIZATION_INVALID")
)

type SessionState string

const (
	SessionEditing                   SessionState = "EDITING"
	SessionDiscoveringDelivery       SessionState = "DISCOVERING_DELIVERY"
	SessionDeliverySelectionRequired SessionState = "DELIVERY_SELECTION_REQUIRED"
	SessionPreflighting              SessionState = "PREFLIGHTING"
	SessionReady                     SessionState = "READY"
	SessionIssuing                   SessionState = "ISSUING"
	SessionConsumed                  SessionState = "CONSUMED"
	SessionPriceChanged              SessionState = "PRICE_CHANGED"
	SessionRateLimited               SessionState = "RATE_LIMITED"
	SessionBlocked                   SessionState = "BLOCKED"
	SessionExpired                   SessionState = "EXPIRED"
)

type BlockReason string

const (
	BlockUnavailable                BlockReason = "UNAVAILABLE"
	BlockCrossBorderOrDuties        BlockReason = "CROSS_BORDER_OR_DUTIES"
	BlockCheckoutCustomerCorrection BlockReason = "CHECKOUT_CUSTOMER_CORRECTION_REQUIRED"
	BlockCheckoutImpossible         BlockReason = "CHECKOUT_IMPOSSIBLE"
	BlockCheckoutQuoteUnsafe        BlockReason = "CHECKOUT_QUOTE_UNSAFE"
	BlockUnsupportedShop            BlockReason = "UNSUPPORTED_SHOP"
	BlockTotalMismatch              BlockReason = "TOTAL_MISMATCH"
	BlockAuthUnavailable            BlockReason = "AUTH_UNAVAILABLE"
	BlockUnknownExternalEffect      BlockReason = "UNKNOWN_EXTERNAL_EFFECT"
	BlockCheckoutNotReady           BlockReason = "CHECKOUT_NOT_READY"
	BlockFinalSnapshotChanged       BlockReason = "FINAL_SNAPSHOT_CHANGED"
)

type PaymentRail string

const (
	PaymentRailTVITUSD       PaymentRail = "TVITUSD"
	PaymentRailPayPalSandbox PaymentRail = "PAYPAL_SANDBOX"
)

func ValidPaymentRail(rail PaymentRail) bool {
	return rail == PaymentRailTVITUSD || rail == PaymentRailPayPalSandbox ||
		rail == PaymentRailPayPalLive
}

type orderFeeQuote struct {
	PassThroughTotal     Money
	AgencyFee            FeeBreakdown
	CustomerPayableTotal Money
}

// agencyFeeFor는 exact MerchantCheckout 집합을 policy에 전달하고 MO별 immutable
// allocation까지 domain Money로 사상한다. PayPal fixed fee가 MO당 부과되므로 주문
// pass-through 합계만 받는 종전 API는 존재하지 않는다.
func agencyFeeFor(rail PaymentRail, checkouts []MerchantCheckout) (orderFeeQuote, error) {
	bases := make([]policy.MerchantFeeBase, len(checkouts))
	for index, checkout := range checkouts {
		bases[index] = policy.MerchantFeeBase{
			CheckoutOrdinal: index + 1, PassThroughMinor: checkout.AuthoritativeTotal.AmountMinor,
		}
	}
	quote, err := policy.CalculateAgencyFee(string(rail), bases)
	if err != nil {
		return orderFeeQuote{}, ErrInvalid
	}
	allocations := make([]MerchantOrderFeeAllocation, len(quote.MerchantOrders))
	for index, allocation := range quote.MerchantOrders {
		checkoutIndex := allocation.CheckoutOrdinal - 1
		if checkoutIndex < 0 || checkoutIndex >= len(checkouts) {
			return orderFeeQuote{}, ErrInvalid
		}
		allocations[index] = MerchantOrderFeeAllocation{
			CheckoutOrdinal: allocation.CheckoutOrdinal,
			ShopDomain:      checkouts[checkoutIndex].ShopDomain,
			PassThroughAmount: Money{
				AmountMinor: allocation.PassThroughMinor, Currency: "USD",
			},
			VariableAmount: Money{AmountMinor: allocation.VariableMinor, Currency: "USD"},
			FixedAmount:    Money{AmountMinor: allocation.FixedMinor, Currency: "USD"},
			Total:          Money{AmountMinor: allocation.TotalMinor, Currency: "USD"},
			CustomerPayableTotal: Money{
				AmountMinor: allocation.CustomerPayableMinor, Currency: "USD",
			},
		}
	}
	return orderFeeQuote{
		PassThroughTotal: Money{AmountMinor: quote.PassThroughMinor, Currency: "USD"},
		AgencyFee: FeeBreakdown{
			VariableAmount: Money{AmountMinor: quote.VariableMinor, Currency: "USD"},
			FixedAmount:    Money{AmountMinor: quote.FixedMinor, Currency: "USD"},
			Total:          Money{AmountMinor: quote.TotalMinor, Currency: "USD"},
			PolicyVersion:  quote.PolicyVersion,
			MerchantOrders: allocations,
		},
		CustomerPayableTotal: Money{AmountMinor: quote.CustomerPayableMinor, Currency: "USD"},
	}, nil
}

// paymentSelectionFor는 rail의 결제 축(ADR-0042 §6)을 반환한다.
func paymentSelectionFor(rail PaymentRail) (PaymentSelection, error) {
	profile, err := ExecutionProfileForRail(rail)
	if err != nil {
		return PaymentSelection{}, ErrInvalid
	}
	return profile.LegacyPaymentSelection(), nil
}

const (
	DutiesNoSignalAtPreflight = "NO_DUTY_OR_CROSS_BORDER_SIGNAL_AT_PREFLIGHT"
	CompletionManualHandoff   = "MANUAL_OPERATOR_HANDOFF"
	CompletionNotRequested    = "NOT_REQUESTED_STEP_2"

	PricingEstimated = "ESTIMATED"
	PricingConfirmed = "CONFIRMED"
	PricingUnsafe    = "UNSAFE"
	PricingStale     = "STALE"

	ProcurementHandlingNormal             = "NORMAL"
	ProcurementHandlingOperatorLater      = "OPERATOR_LATER"
	ProcurementHandlingCustomerCorrection = "CUSTOMER_CORRECTION"
	ProcurementHandlingImpossible         = "IMPOSSIBLE"
)

type Money struct {
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
}

func (m Money) Validate(allowZero bool) error {
	if strings.ToUpper(strings.TrimSpace(m.Currency)) != "USD" ||
		m.AmountMinor < 0 || (!allowZero && m.AmountMinor == 0) {
		return ErrInvalid
	}
	return nil
}

type CartItemSnapshot struct {
	CartItemID       string    `json:"cartItemId"`
	PlanTargetID     string    `json:"planTargetId"`
	CandidateID      string    `json:"candidateId"`
	ProductTitle     string    `json:"productTitle"`
	ProductURL       string    `json:"productUrl,omitempty"`
	ImageURL         string    `json:"imageUrl,omitempty"`
	VariantID        string    `json:"variantId"`
	VariantTitle     string    `json:"variantTitle"`
	SelectedOptions  []string  `json:"selectedOptions"`
	PreviewUnitPrice Money     `json:"previewUnitPrice"`
	Quantity         int       `json:"quantity"`
	SellerDomain     string    `json:"sellerDomain"`
	ObservedAt       time.Time `json:"observedAt"`
}

type SourceCartSnapshot struct {
	CartID       string             `json:"cartId"`
	CartVersion  int64              `json:"cartVersion"`
	SnapshotHash string             `json:"snapshotHash"`
	Items        []CartItemSnapshot `json:"items"`
}

type ExactLine struct {
	LineID           string    `json:"lineId"`
	SourceCartItemID string    `json:"sourceCartItemId"`
	PlanTargetID     string    `json:"planTargetId"`
	CandidateID      string    `json:"candidateId"`
	CatalogProvider  string    `json:"catalogProvider"`
	ShopDomain       string    `json:"shopDomain"`
	ProductURL       string    `json:"productUrl,omitempty"`
	ImageURL         string    `json:"imageUrl,omitempty"`
	VariantID        string    `json:"variantId"`
	ProductTitle     string    `json:"productTitle"`
	VariantTitle     string    `json:"variantTitle"`
	SelectedOptions  []string  `json:"selectedOptions"`
	Quantity         int       `json:"quantity"`
	UnitPrice        Money     `json:"unitPrice"`
	LineSubtotal     Money     `json:"lineSubtotal"`
	ObservedAt       time.Time `json:"observedAt"`
	EvidenceHash     string    `json:"evidenceHash"`
}

type ShippingSnapshot struct {
	SnapshotRef      string `json:"snapshotRef"`
	SnapshotRevision int64  `json:"snapshotRevision"`
	SnapshotHash     string `json:"snapshotHash"`
	MaskedSummary    string `json:"maskedSummary"`
	Country          string `json:"country"`
}

type DeliveryOption struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
}

type DeliveryGroup struct {
	ID                string           `json:"id"`
	LineRefs          []string         `json:"lineRefs"`
	Options           []DeliveryOption `json:"options"`
	SelectedOptionRef string           `json:"selectedOptionRef,omitempty"`
}

type Total struct {
	Type        string `json:"type"`
	AmountMinor int64  `json:"amountMinor"`
	DisplayText string `json:"displayText,omitempty"`
}

type AuthEvidence struct {
	Tier                 string `json:"tier"`
	ScopesHash           string `json:"scopesHash"`
	AgentProfileURL      string `json:"agentProfileUrl"`
	AgentProfileVersion  string `json:"agentProfileVersion"`
	AgentProfileHash     string `json:"agentProfileHash"`
	CompletionPermission string `json:"completionPermission"`
}

type MerchantCheckout struct {
	MerchantID                string               `json:"merchantId"`
	ShopDomain                string               `json:"shopDomain"`
	StorefrontCartSafeRef     string               `json:"storefrontCartSafeRef"`
	CheckoutSessionSafeRef    string               `json:"checkoutSessionSafeRef,omitempty"`
	BuyerContextSafeRef       string               `json:"buyerContextSafeRef,omitempty"`
	LineRefs                  []string             `json:"lineRefs"`
	DeliveryGroups            []DeliveryGroup      `json:"deliveryGroups"`
	DeliveryOptions           []DeliveryOption     `json:"deliveryOptions"`
	SelectedDeliveryOptionRef string               `json:"selectedDeliveryOptionRef,omitempty"`
	ProviderStatus            string               `json:"providerStatus"`
	ProviderNotices           []ProviderNotice     `json:"providerNotices,omitempty"`
	PolicyLinks               []ProviderPolicyLink `json:"policyLinks,omitempty"`
	ManualSiteSteps           []ManualSiteStep     `json:"manualSiteSteps,omitempty"`
	ProcurementHandling       string               `json:"procurementHandling"`
	Totals                    []Total              `json:"totals"`
	AuthoritativeTotal        Money                `json:"authoritativeTotal"`
	TaxTotal                  Money                `json:"taxTotal"`
	DutiesDisposition         string               `json:"dutiesDisposition"`
	FulfillmentOriginEvidence string               `json:"fulfillmentOriginEvidenceRef"`
	AuthEvidence              AuthEvidence         `json:"authEvidence"`
	CompletionRoute           string               `json:"completionRoute"`
	QuoteReadiness            string               `json:"quoteReadiness"`
	ContinueURLSafeRef        string               `json:"continueUrlSafeRef,omitempty"`
	ContinueURLHash           string               `json:"continueUrlHash,omitempty"`
	QuoteFingerprint          string               `json:"quoteFingerprint,omitempty"`
	ObservedAt                time.Time            `json:"observedAt"`
	ExpiresAt                 time.Time            `json:"expiresAt"`
	EvidenceHash              string               `json:"evidenceHash"`
}

func (m MerchantCheckout) DeliverySelectionComplete() bool {
	if len(m.DeliveryGroups) == 0 {
		return false
	}
	for _, group := range m.DeliveryGroups {
		if group.ID == "" || group.SelectedOptionRef == "" || len(group.Options) == 0 {
			return false
		}
		found := false
		for _, option := range group.Options {
			if option.ID == group.SelectedOptionRef {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

type OrderSheetSession struct {
	ID                    string             `json:"id"`
	UserID                string             `json:"-"`
	IssuedAgencyOrderID   string             `json:"issuedAgencyOrderId,omitempty"`
	SourceCart            SourceCartSnapshot `json:"sourceCart"`
	Lines                 []ExactLine        `json:"lines"`
	ShippingAddress       ShippingSnapshot   `json:"shippingAddress,omitzero"`
	PaymentSelection      PaymentRail        `json:"paymentSelection,omitempty"`
	MerchantCheckouts     []MerchantCheckout `json:"merchantCheckouts"`
	DisplayedSnapshotHash string             `json:"displayedSnapshotHash,omitempty"`
	PassThroughTotal      Money              `json:"passThroughTotal"`
	AgencyFee             Money              `json:"agencyFee"`
	CustomerPayableTotal  Money              `json:"customerPayableTotal"`
	Version               int64              `json:"version"`
	State                 SessionState       `json:"state"`
	BlockReason           BlockReason        `json:"blockReason,omitempty"`
	RetryAfter            *time.Time         `json:"retryAfter,omitempty"`
	CreatedAt             time.Time          `json:"createdAt"`
	ExpiresAt             time.Time          `json:"expiresAt"`
}

// OrderSheetAlreadyConsumedError preserves the successful order identity when
// a stale OrderSheet mutation arrives after the atomic issue transaction.
// Callers can recover to the existing payment route instead of guessing that
// the order creation failed and retrying with a new identity.
type OrderSheetAlreadyConsumedError struct {
	AgencyOrderID string
}

func (e *OrderSheetAlreadyConsumedError) Error() string {
	return "ORDER_SHEET_ALREADY_CONSUMED"
}

// OrderPreparationLineError identifies the exact Cart item that failed the
// authoritative fresh catalog check. ItemTitle is safe customer-visible
// merchant content; provider payloads and credentials must never be copied
// into this error.
type OrderPreparationLineError struct {
	ReasonCode string
	ItemTitle  string
	Retryable  bool
}

func (e *OrderPreparationLineError) Error() string {
	if e == nil || e.ReasonCode == "" {
		return "ORDER_PREPARATION_LINE_UNAVAILABLE"
	}
	return e.ReasonCode
}

type PaymentSelection struct {
	Rail                string `json:"rail"`
	ProviderEnvironment string `json:"providerEnvironment"`
	Asset               string `json:"asset"`
	EconomicEffect      string `json:"economicEffect"`
	MerchantExecution   string `json:"merchantExecution"`
}

type FeeBreakdown struct {
	VariableAmount Money                        `json:"variable"`
	FixedAmount    Money                        `json:"fixed"`
	Total          Money                        `json:"total"`
	PolicyVersion  string                       `json:"policyVersion"`
	MerchantOrders []MerchantOrderFeeAllocation `json:"merchantOrders,omitempty"`
}

// MerchantOrderFeeAllocation은 발행 시 checkout ordinal에 고정되는 MO 환불·
// capture 금액이다. 이후 merchant 실제 결제액이나 현재 policy로 재계산하지 않는다.
type MerchantOrderFeeAllocation struct {
	CheckoutOrdinal      int    `json:"checkoutOrdinal"`
	ShopDomain           string `json:"shopDomain"`
	PassThroughAmount    Money  `json:"passThrough"`
	VariableAmount       Money  `json:"variable"`
	FixedAmount          Money  `json:"fixed"`
	Total                Money  `json:"total"`
	CustomerPayableTotal Money  `json:"customerPayableTotal"`
}

type IssuanceEvidence struct {
	OrderSheetSessionID   string `json:"orderSheetSessionId"`
	DisplayedSnapshotHash string `json:"displayedSnapshotHash"`
	DisclosureVersion     string `json:"disclosureVersion"`
	IdempotencyKeyHash    string `json:"idempotencyKeyHash"`
}

type AgencyOrder struct {
	ID                       string                   `json:"id"`
	UserID                   string                   `json:"-"`
	Status                   string                   `json:"status"`
	SourceCart               SourceCartSnapshot       `json:"sourceCartSnapshot"`
	Lines                    []ExactLine              `json:"lines"`
	ShippingAddress          ShippingSnapshot         `json:"shippingAddress"`
	PaymentSelection         PaymentSelection         `json:"paymentSelection"`
	ExecutionProfile         OrderExecutionProfile    `json:"executionProfile"`
	ExecutionProfileHash     string                   `json:"executionProfileHash"`
	MerchantCheckouts        []MerchantCheckout       `json:"merchantCheckouts"`
	PassThroughTotal         Money                    `json:"passThroughTotal"`
	AgencyFee                FeeBreakdown             `json:"agencyFee"`
	CustomerPayableTotal     Money                    `json:"customerPayableTotal"`
	ProcurementAuthorization ProcurementAuthorization `json:"procurementAuthorization"`
	IssuanceEvidence         IssuanceEvidence         `json:"issuanceEvidence"`
	IssuedAt                 time.Time                `json:"issuedAt"`
	ExpiresAt                time.Time                `json:"expiresAt"`
	SnapshotHash             string                   `json:"snapshotHash"`
}

type PaymentInstruction struct {
	ID                      string           `json:"id"`
	AgencyOrderID           string           `json:"agencyOrderId"`
	AgencyOrderSnapshotHash string           `json:"agencyOrderSnapshotHash"`
	PaymentSelection        PaymentSelection `json:"paymentSelection"`
	ExecutionProfileHash    string           `json:"executionProfileHash"`
	CustomerPayableTotal    Money            `json:"customerPayableTotal"`
	PaymentPolicyVersion    string           `json:"paymentPolicyVersion"`
	ShippingSnapshotRef     string           `json:"shippingSnapshotRef"`
	ShippingSnapshotHash    string           `json:"shippingSnapshotHash"`
	ExpiresAt               time.Time        `json:"expiresAt"`
	UserID                  string           `json:"-"`
	IdempotencyKeyHash      string           `json:"idempotencyKeyHash"`
	State                   string           `json:"state"`
	CreatedAt               time.Time        `json:"createdAt"`
}

func (s OrderSheetSession) ValidateReady(now time.Time) error {
	if s.State != SessionReady || !ValidPaymentRail(s.PaymentSelection) ||
		!now.Before(s.ExpiresAt) || s.DisplayedSnapshotHash == "" ||
		len(s.Lines) == 0 || len(s.MerchantCheckouts) == 0 ||
		s.ShippingAddress.Country != "US" {
		return ErrNotReady
	}
	var total int64
	for _, checkout := range s.MerchantCheckouts {
		if !checkout.DeliverySelectionComplete() ||
			checkout.QuoteReadiness != PricingConfirmed ||
			(checkout.ProcurementHandling != ProcurementHandlingNormal &&
				checkout.ProcurementHandling != ProcurementHandlingOperatorLater) ||
			checkout.CompletionRoute != CompletionManualHandoff ||
			checkout.AuthEvidence.Tier != "TOKEN" ||
			checkout.AuthEvidence.CompletionPermission != CompletionNotRequested ||
			checkout.DutiesDisposition != DutiesNoSignalAtPreflight ||
			checkout.QuoteFingerprint == "" || checkout.EvidenceHash == "" || !now.Before(checkout.ExpiresAt) {
			return ErrNotReady
		}
		if checkout.AuthoritativeTotal.Validate(false) != nil || checkout.TaxTotal.Validate(true) != nil {
			return ErrNotReady
		}
		total += checkout.AuthoritativeTotal.AmountMinor
	}
	if total != s.PassThroughTotal.AmountMinor || s.PassThroughTotal.Currency != "USD" {
		return ErrNotReady
	}
	feeQuote, err := agencyFeeFor(s.PaymentSelection, s.MerchantCheckouts)
	if err != nil {
		return ErrNotReady
	}
	if feeQuote.PassThroughTotal.AmountMinor != total ||
		s.AgencyFee != feeQuote.AgencyFee.Total ||
		s.CustomerPayableTotal != feeQuote.CustomerPayableTotal {
		return ErrNotReady
	}
	return nil
}

func (s *OrderSheetSession) ApplyPreflight(checkouts []MerchantCheckout, now time.Time) error {
	if !ValidPaymentRail(s.PaymentSelection) ||
		(s.State != SessionDeliverySelectionRequired && s.State != SessionPreflighting &&
			s.State != SessionPriceChanged && s.State != SessionRateLimited && s.State != SessionEditing &&
			s.State != SessionReady) {
		return ErrStateInvalid
	}
	if len(checkouts) == 0 {
		return ErrInvalid
	}
	// The freshly observed checkouts are kept even when a block follows, so the
	// projection can say which shop raised which typed signal. A blocked session
	// clears DisplayedSnapshotHash and can never issue, so this changes nothing
	// about snapshot integrity (ADR-0049).
	s.MerchantCheckouts = append([]MerchantCheckout(nil), checkouts...)
	for _, checkout := range checkouts {
		switch {
		case checkout.DutiesDisposition != DutiesNoSignalAtPreflight:
			s.Block(BlockCrossBorderOrDuties)
			return nil
		case checkout.ProcurementHandling == ProcurementHandlingCustomerCorrection:
			s.Block(BlockCheckoutCustomerCorrection)
			return nil
		case checkout.ProcurementHandling == ProcurementHandlingImpossible:
			s.Block(BlockCheckoutImpossible)
			return nil
		case checkout.QuoteReadiness != PricingConfirmed:
			s.Block(BlockCheckoutQuoteUnsafe)
			return nil
		case checkout.CompletionRoute != CompletionManualHandoff:
			s.Block(BlockCheckoutNotReady)
			return nil
		case checkout.AuthEvidence.Tier != "TOKEN" ||
			checkout.AuthEvidence.CompletionPermission != CompletionNotRequested:
			s.Block(BlockCheckoutNotReady)
			return nil
		}
	}
	feeQuote, err := agencyFeeFor(s.PaymentSelection, checkouts)
	if err != nil {
		return err
	}
	s.PassThroughTotal = feeQuote.PassThroughTotal
	s.AgencyFee = feeQuote.AgencyFee.Total
	s.CustomerPayableTotal = feeQuote.CustomerPayableTotal
	hash, err := s.DisplayHash()
	if err != nil {
		return err
	}
	s.DisplayedSnapshotHash = hash
	s.State = SessionReady
	s.BlockReason = ""
	s.RetryAfter = nil
	s.Version++
	return s.ValidateReady(now)
}

func (s *OrderSheetSession) SelectRail(rail PaymentRail) error {
	if !ValidPaymentRail(rail) {
		return ErrInvalid
	}
	if s.State == SessionConsumed || s.State == SessionIssuing || s.State == SessionExpired {
		return ErrStateInvalid
	}
	s.PaymentSelection = rail
	s.State = SessionPreflighting
	s.BlockReason = ""
	s.Version++
	return nil
}

func (s *OrderSheetSession) Block(reason BlockReason) {
	s.State = SessionBlocked
	s.BlockReason = reason
	s.DisplayedSnapshotHash = ""
	s.Version++
}

func (s OrderSheetSession) DisplayHash() (string, error) {
	return shareddomain.CanonicalJSONHash(struct {
		SourceCart        SourceCartSnapshot `json:"sourceCart"`
		Lines             []ExactLine        `json:"lines"`
		ShippingAddress   ShippingSnapshot   `json:"shippingAddress"`
		PaymentSelection  PaymentRail        `json:"paymentSelection"`
		MerchantCheckouts []MerchantCheckout `json:"merchantCheckouts"`
		PassThroughTotal  Money              `json:"passThroughTotal"`
		AgencyFee         Money              `json:"agencyFee"`
		Payable           Money              `json:"customerPayableTotal"`
	}{s.SourceCart, s.Lines, s.ShippingAddress, s.PaymentSelection, s.MerchantCheckouts,
		s.PassThroughTotal, s.AgencyFee, s.CustomerPayableTotal})
}

type IssueInput struct {
	OrderID             string
	InstructionID       string
	IdempotencyKeyHash  string
	DisclosureVersion   string
	ProcurementApproval ProcurementApproval
	Now                 time.Time
}

func Issue(session OrderSheetSession, input IssueInput) (AgencyOrder, PaymentInstruction, error) {
	// PostgreSQL timestamptz stores microseconds. Seal every timestamp that is
	// embedded in an immutable JSON/hash and persisted in a timestamptz column at
	// that same precision; otherwise the JSON cast and driver encoding can round
	// opposite ways and intermittently violate the payload/column CHECK.
	now := input.Now.UTC().Round(time.Microsecond)
	if err := session.ValidateReady(now); err != nil || input.OrderID == "" ||
		input.InstructionID == "" || input.IdempotencyKeyHash == "" || input.DisclosureVersion == "" {
		return AgencyOrder{}, PaymentInstruction{}, ErrNotReady
	}
	profile, err := ExecutionProfileForRail(session.PaymentSelection)
	if err != nil {
		return AgencyOrder{}, PaymentInstruction{}, ErrNotReady
	}
	selection := profile.LegacyPaymentSelection()
	executionProfileHash, err := profile.Hash()
	if err != nil {
		return AgencyOrder{}, PaymentInstruction{}, ErrNotReady
	}
	authorization, err := BuildProcurementAuthorization(
		session, input.ProcurementApproval, executionProfileHash, now,
	)
	if err != nil {
		return AgencyOrder{}, PaymentInstruction{}, ErrProcurementAuthorizationInvalid
	}
	feeQuote, err := agencyFeeFor(session.PaymentSelection, session.MerchantCheckouts)
	if err != nil {
		return AgencyOrder{}, PaymentInstruction{}, ErrNotReady
	}
	order := AgencyOrder{
		ID: input.OrderID, UserID: session.UserID, Status: "ISSUED",
		SourceCart: session.SourceCart, Lines: append([]ExactLine(nil), session.Lines...),
		ShippingAddress:          session.ShippingAddress,
		PaymentSelection:         selection,
		ExecutionProfile:         profile,
		ExecutionProfileHash:     executionProfileHash,
		MerchantCheckouts:        append([]MerchantCheckout(nil), session.MerchantCheckouts...),
		PassThroughTotal:         session.PassThroughTotal,
		AgencyFee:                feeQuote.AgencyFee,
		CustomerPayableTotal:     session.CustomerPayableTotal,
		ProcurementAuthorization: authorization,
		IssuanceEvidence: IssuanceEvidence{OrderSheetSessionID: session.ID,
			DisplayedSnapshotHash: session.DisplayedSnapshotHash, DisclosureVersion: input.DisclosureVersion,
			IdempotencyKeyHash: input.IdempotencyKeyHash},
		IssuedAt: now, ExpiresAt: session.ExpiresAt,
	}
	hash, err := shareddomain.CanonicalJSONHash(struct {
		ID                       string                   `json:"id"`
		Status                   string                   `json:"status"`
		SourceCart               SourceCartSnapshot       `json:"sourceCartSnapshot"`
		Lines                    []ExactLine              `json:"lines"`
		ShippingAddress          ShippingSnapshot         `json:"shippingAddress"`
		PaymentSelection         PaymentSelection         `json:"paymentSelection"`
		ExecutionProfile         OrderExecutionProfile    `json:"executionProfile"`
		ExecutionProfileHash     string                   `json:"executionProfileHash"`
		MerchantCheckouts        []MerchantCheckout       `json:"merchantCheckouts"`
		PassThroughTotal         Money                    `json:"passThroughTotal"`
		AgencyFee                FeeBreakdown             `json:"agencyFee"`
		CustomerPayableTotal     Money                    `json:"customerPayableTotal"`
		ProcurementAuthorization ProcurementAuthorization `json:"procurementAuthorization"`
		IssuanceEvidence         IssuanceEvidence         `json:"issuanceEvidence"`
		IssuedAt                 time.Time                `json:"issuedAt"`
		ExpiresAt                time.Time                `json:"expiresAt"`
	}{order.ID, order.Status, order.SourceCart, order.Lines, order.ShippingAddress,
		order.PaymentSelection, order.ExecutionProfile, order.ExecutionProfileHash,
		order.MerchantCheckouts, order.PassThroughTotal,
		order.AgencyFee, order.CustomerPayableTotal, order.ProcurementAuthorization,
		order.IssuanceEvidence, order.IssuedAt, order.ExpiresAt})
	if err != nil {
		return AgencyOrder{}, PaymentInstruction{}, fmt.Errorf("hash AgencyOrder: %w", err)
	}
	order.SnapshotHash = hash
	instruction := PaymentInstruction{
		ID: input.InstructionID, AgencyOrderID: order.ID, AgencyOrderSnapshotHash: hash,
		PaymentSelection: order.PaymentSelection, ExecutionProfileHash: order.ExecutionProfileHash,
		CustomerPayableTotal: order.CustomerPayableTotal,
		PaymentPolicyVersion: order.AgencyFee.PolicyVersion,
		ShippingSnapshotRef:  order.ShippingAddress.SnapshotRef,
		ShippingSnapshotHash: order.ShippingAddress.SnapshotHash, ExpiresAt: order.ExpiresAt,
		UserID: order.UserID, IdempotencyKeyHash: input.IdempotencyKeyHash,
		State: "PENDING", CreatedAt: input.Now,
	}
	return order, instruction, nil
}
