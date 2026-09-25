package app

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

type LookupIdentifierType string

const (
	LookupAuto                LookupIdentifierType = "AUTO"
	LookupAgencyOrderID       LookupIdentifierType = "AGENCY_ORDER_ID"
	LookupPaymentID           LookupIdentifierType = "PAYMENT_ID"
	LookupPayPalOrderID       LookupIdentifierType = "PAYPAL_ORDER_ID"
	LookupPayPalCaptureID     LookupIdentifierType = "PAYPAL_CAPTURE_ID"
	LookupPayPalRefundID      LookupIdentifierType = "PAYPAL_REFUND_ID"
	LookupMerchantOrderID     LookupIdentifierType = "MERCHANT_ORDER_ID"
	LookupMerchantExternalRef LookupIdentifierType = "MERCHANT_ORDER_REF"
	LookupShipmentID          LookupIdentifierType = "SHIPMENT_ID"
	LookupTrackingRef         LookupIdentifierType = "TRACKING_REF"
	LookupGIWATransactionHash LookupIdentifierType = "GIWA_TX_HASH"
)

type LookupEnvironment string

const (
	LookupEnvironmentAny     LookupEnvironment = "ANY"
	LookupEnvironmentSandbox LookupEnvironment = "SANDBOX"
	LookupEnvironmentTestnet LookupEnvironment = "TESTNET"
	LookupEnvironmentLive    LookupEnvironment = "LIVE"
)

var (
	ErrOrderLookupDisabled  = errors.New("ORDERING_OPERATOR_LOOKUP_DISABLED")
	ErrOrderLookupInvalid   = errors.New("ORDERING_OPERATOR_LOOKUP_INVALID")
	ErrOrderLookupNotFound  = errors.New("ORDERING_OPERATOR_LOOKUP_NOT_FOUND")
	ErrOrderLookupAmbiguous = errors.New("ORDERING_OPERATOR_LOOKUP_AMBIGUOUS")
)

type OrderLookupInput struct {
	IdentifierType LookupIdentifierType `json:"identifierType"`
	Value          string               `json:"value"`
	Environment    LookupEnvironment    `json:"environment"`
	ShopDomain     string               `json:"shopDomain,omitempty"`
	Carrier        string               `json:"carrier,omitempty"`
}

type OrderLookupMatch struct {
	AgencyOrderID string               `json:"agencyOrderId"`
	MatchedBy     LookupIdentifierType `json:"matchedBy"`
	Environment   string               `json:"environment"`
	PaymentRail   string               `json:"paymentRail"`
}

// OrderIdentifier는 identity rail의 safe field다. provider 응답 원문이나
// capability URL은 싣지 않고, 운영자가 대사할 수 있는 식별자만 싣는다.
type OrderIdentifier struct {
	Kind              LookupIdentifierType `json:"kind"`
	Value             string               `json:"value"`
	Qualifier         string               `json:"qualifier,omitempty"`
	RelatedResourceID string               `json:"relatedResourceId,omitempty"`
}

type OrderLookupRepository interface {
	ResolveAndAudit(
		ctx context.Context, input OrderLookupInput, operatorUserID string, now time.Time,
	) (OrderLookupMatch, error)
	ListIdentifiers(ctx context.Context, agencyOrderID string) ([]OrderIdentifier, error)
	AuditDetailView(ctx context.Context, agencyOrderID, operatorUserID string, now time.Time) error
}

type OrderEvidenceReader interface {
	GetOperatorProjection(context.Context, string) (agencydomain.Projection, error)
}

type OrderLineEvidence struct {
	LineID       string    `json:"lineId"`
	ProductTitle string    `json:"productTitle"`
	VariantTitle string    `json:"variantTitle,omitempty"`
	Quantity     int       `json:"quantity"`
	ShopDomain   string    `json:"shopDomain"`
	ObservedAt   time.Time `json:"observedAt"`
	EvidenceHash string    `json:"evidenceHash"`
}

type OrderEvidenceSummary struct {
	AgencyOrderID        string                             `json:"agencyOrderId"`
	Status               string                             `json:"status"`
	IssuedAt             time.Time                          `json:"issuedAt"`
	ExpiresAt            time.Time                          `json:"expiresAt"`
	SnapshotHash         string                             `json:"snapshotHash"`
	ExecutionProfile     agencydomain.OrderExecutionProfile `json:"executionProfile"`
	ExecutionProfileHash string                             `json:"executionProfileHash"`
	CustomerPayableTotal agencydomain.Money                 `json:"customerPayableTotal"`
	PassThroughTotal     agencydomain.Money                 `json:"passThroughTotal"`
	AgencyFeeTotal       agencydomain.Money                 `json:"agencyFeeTotal"`
	ShippingMasked       string                             `json:"shippingMasked"`
	ShippingCountry      string                             `json:"shippingCountry"`
	IssuanceEvidence     agencydomain.IssuanceEvidence      `json:"issuanceEvidence"`
	Lines                []OrderLineEvidence                `json:"lines"`
}

type PaymentInstructionEvidence struct {
	ID                      string             `json:"id"`
	State                   string             `json:"state"`
	AgencyOrderSnapshotHash string             `json:"agencyOrderSnapshotHash"`
	ExecutionProfileHash    string             `json:"executionProfileHash"`
	CustomerPayableTotal    agencydomain.Money `json:"customerPayableTotal"`
	PaymentPolicyVersion    string             `json:"paymentPolicyVersion"`
	IdempotencyKeyHash      string             `json:"idempotencyKeyHash"`
	CreatedAt               time.Time          `json:"createdAt"`
	ExpiresAt               time.Time          `json:"expiresAt"`
}

type ReceiptEvidence struct {
	ID                    string    `json:"id"`
	Kind                  string    `json:"kind"`
	PaymentRail           string    `json:"paymentRail"`
	ProviderEnvironment   string    `json:"providerEnvironment"`
	EconomicEffect        string    `json:"economicEffect"`
	MerchantExecutionMode string    `json:"merchantExecutionMode"`
	ExecutionProfileHash  string    `json:"executionProfileHash"`
	TerminalState         string    `json:"terminalState"`
	TerminalTxHash        string    `json:"terminalTxHash,omitempty"`
	ReceiptHash           string    `json:"receiptHash"`
	CreatedAt             time.Time `json:"createdAt"`
}

type EvidenceCheckpoint struct {
	Kind       string    `json:"kind"`
	State      string    `json:"state"`
	ReasonCode string    `json:"reasonCode,omitempty"`
	Reference  string    `json:"reference,omitempty"`
	ObservedAt time.Time `json:"observedAt"`
}

type OrderInvestigation struct {
	Order              OrderEvidenceSummary                `json:"order"`
	Process            agencydomain.Process                `json:"process"`
	PaymentInstruction PaymentInstructionEvidence          `json:"paymentInstruction"`
	Payment            *agencydomain.PaymentProjection     `json:"payment,omitempty"`
	Identifiers        []OrderIdentifier                   `json:"identifiers"`
	ChainTransactions  []agencydomain.ChainTransaction     `json:"chainTransactions"`
	MerchantOrders     []agencydomain.MerchantOrderSummary `json:"merchantOrders"`
	Shipments          []agencydomain.ShipmentSummary      `json:"shipments"`
	Units              []agencydomain.UnitView             `json:"units"`
	RefundRequests     []agencydomain.RefundRequest        `json:"refundRequests,omitempty"`
	Receipt            *ReceiptEvidence                    `json:"receipt,omitempty"`
	Checkpoints        []EvidenceCheckpoint                `json:"checkpoints"`
}

func (s *Service) EnableOrderLookup(repository OrderLookupRepository, reader OrderEvidenceReader) {
	s.lookup = repository
	s.orderEvidence = reader
}

func normalizeLookupInput(input OrderLookupInput) (OrderLookupInput, error) {
	input.IdentifierType = LookupIdentifierType(strings.ToUpper(strings.TrimSpace(string(input.IdentifierType))))
	input.Environment = LookupEnvironment(strings.ToUpper(strings.TrimSpace(string(input.Environment))))
	input.Value = strings.TrimSpace(input.Value)
	input.ShopDomain = strings.ToLower(strings.TrimSpace(input.ShopDomain))
	input.Carrier = strings.TrimSpace(input.Carrier)
	if input.IdentifierType == "" {
		input.IdentifierType = LookupAuto
	}
	if input.Environment == "" {
		input.Environment = LookupEnvironmentAny
	}
	if input.Value == "" || len(input.Value) > 256 || len(input.ShopDomain) > 200 || len(input.Carrier) > 100 {
		return OrderLookupInput{}, ErrOrderLookupInvalid
	}
	switch input.IdentifierType {
	case LookupAuto, LookupAgencyOrderID, LookupPaymentID, LookupPayPalOrderID,
		LookupPayPalCaptureID, LookupPayPalRefundID, LookupMerchantOrderID,
		LookupMerchantExternalRef, LookupShipmentID, LookupTrackingRef,
		LookupGIWATransactionHash:
	default:
		return OrderLookupInput{}, ErrOrderLookupInvalid
	}
	switch input.Environment {
	case LookupEnvironmentAny, LookupEnvironmentSandbox, LookupEnvironmentTestnet, LookupEnvironmentLive:
	default:
		return OrderLookupInput{}, ErrOrderLookupInvalid
	}
	return input, nil
}

func (s *Service) LookupOrder(
	ctx context.Context, input OrderLookupInput, operatorUserID string,
) (OrderLookupMatch, error) {
	if s.lookup == nil || s.orderEvidence == nil {
		return OrderLookupMatch{}, ErrOrderLookupDisabled
	}
	input, err := normalizeLookupInput(input)
	if err != nil || strings.TrimSpace(operatorUserID) == "" {
		return OrderLookupMatch{}, ErrOrderLookupInvalid
	}
	return s.lookup.ResolveAndAudit(ctx, input, strings.TrimSpace(operatorUserID), s.clock.Now())
}

func (s *Service) InvestigateOrder(
	ctx context.Context, agencyOrderID, operatorUserID string,
) (OrderInvestigation, error) {
	agencyOrderID = strings.TrimSpace(agencyOrderID)
	operatorUserID = strings.TrimSpace(operatorUserID)
	if s.lookup == nil || s.orderEvidence == nil {
		return OrderInvestigation{}, ErrOrderLookupDisabled
	}
	if agencyOrderID == "" || operatorUserID == "" {
		return OrderInvestigation{}, ErrOrderLookupInvalid
	}
	projection, err := s.orderEvidence.GetOperatorProjection(ctx, agencyOrderID)
	if errors.Is(err, agencydomain.ErrNotFound) {
		return OrderInvestigation{}, ErrOrderLookupNotFound
	}
	if err != nil {
		return OrderInvestigation{}, err
	}
	identifiers, err := s.lookup.ListIdentifiers(ctx, agencyOrderID)
	if err != nil {
		return OrderInvestigation{}, err
	}
	if err := s.lookup.AuditDetailView(ctx, agencyOrderID, operatorUserID, s.clock.Now()); err != nil {
		return OrderInvestigation{}, err
	}
	return composeInvestigation(projection, identifiers), nil
}

// AuditOrderDetailView는 상세 열람(조사·timeline)의 append-only audit이다 —
// 주문 존재 검증을 포함한다(없으면 ErrOrderLookupNotFound).
func (s *Service) AuditOrderDetailView(
	ctx context.Context, agencyOrderID, operatorUserID string,
) error {
	agencyOrderID = strings.TrimSpace(agencyOrderID)
	operatorUserID = strings.TrimSpace(operatorUserID)
	if s.lookup == nil || s.orderEvidence == nil {
		return ErrOrderLookupDisabled
	}
	if agencyOrderID == "" || operatorUserID == "" {
		return ErrOrderLookupInvalid
	}
	if _, err := s.orderEvidence.GetOperatorProjection(ctx, agencyOrderID); err != nil {
		if errors.Is(err, agencydomain.ErrNotFound) {
			return ErrOrderLookupNotFound
		}
		return err
	}
	return s.lookup.AuditDetailView(ctx, agencyOrderID, operatorUserID, s.clock.Now())
}

func composeInvestigation(
	projection agencydomain.Projection, identifiers []OrderIdentifier,
) OrderInvestigation {
	order := projection.AgencyOrder
	lines := make([]OrderLineEvidence, 0, len(order.Lines))
	for _, line := range order.Lines {
		lines = append(lines, OrderLineEvidence{
			LineID: line.LineID, ProductTitle: line.ProductTitle,
			VariantTitle: line.VariantTitle, Quantity: line.Quantity,
			ShopDomain: line.ShopDomain, ObservedAt: line.ObservedAt,
			EvidenceHash: line.EvidenceHash,
		})
	}
	instruction := projection.PaymentInstruction
	result := OrderInvestigation{
		Order: OrderEvidenceSummary{
			AgencyOrderID: order.ID, Status: order.Status, IssuedAt: order.IssuedAt,
			ExpiresAt: order.ExpiresAt, SnapshotHash: order.SnapshotHash,
			ExecutionProfile:     order.ExecutionProfile,
			ExecutionProfileHash: order.ExecutionProfileHash,
			CustomerPayableTotal: order.CustomerPayableTotal,
			PassThroughTotal:     order.PassThroughTotal, AgencyFeeTotal: order.AgencyFee.Total,
			ShippingMasked:   order.ShippingAddress.MaskedSummary,
			ShippingCountry:  order.ShippingAddress.Country,
			IssuanceEvidence: order.IssuanceEvidence, Lines: lines,
		},
		Process: projection.Process,
		PaymentInstruction: PaymentInstructionEvidence{
			ID: instruction.ID, State: instruction.State,
			AgencyOrderSnapshotHash: instruction.AgencyOrderSnapshotHash,
			ExecutionProfileHash:    instruction.ExecutionProfileHash,
			CustomerPayableTotal:    instruction.CustomerPayableTotal,
			PaymentPolicyVersion:    instruction.PaymentPolicyVersion,
			IdempotencyKeyHash:      instruction.IdempotencyKeyHash,
			CreatedAt:               instruction.CreatedAt, ExpiresAt: instruction.ExpiresAt,
		},
		Payment: projection.Payment, Identifiers: identifiers,
		ChainTransactions: projection.ChainTransactions,
		MerchantOrders:    projection.MerchantOrders, Shipments: projection.Shipments,
		Units: projection.Units, RefundRequests: projection.RefundRequests,
	}
	if projection.Receipt != nil {
		receipt := projection.Receipt
		result.Receipt = &ReceiptEvidence{
			ID: receipt.ID, Kind: receipt.Kind, PaymentRail: receipt.PaymentRail,
			ProviderEnvironment:   receipt.ProviderEnvironment,
			EconomicEffect:        receipt.EconomicEffect,
			MerchantExecutionMode: receipt.MerchantExecutionMode,
			ExecutionProfileHash:  receipt.ExecutionProfileHash,
			TerminalState:         receipt.TerminalState, TerminalTxHash: receipt.TerminalTxHash,
			ReceiptHash: receipt.ReceiptHash, CreatedAt: receipt.CreatedAt,
		}
	}
	result.Checkpoints = composeCheckpoints(projection)
	return result
}

func composeCheckpoints(projection agencydomain.Projection) []EvidenceCheckpoint {
	items := []EvidenceCheckpoint{{
		Kind: "ORDER_ISSUED", State: projection.AgencyOrder.Status,
		Reference: projection.AgencyOrder.SnapshotHash, ObservedAt: projection.AgencyOrder.IssuedAt,
	}, {
		Kind: "PROCESS", State: string(projection.Process.State),
		ReasonCode: projection.Process.LastReasonCode, ObservedAt: projection.Process.UpdatedAt,
	}}
	if projection.Payment != nil && projection.Payment.UpdatedAt != nil {
		items = append(items, EvidenceCheckpoint{
			Kind: "PAYMENT", State: projection.Payment.State,
			ReasonCode: projection.Payment.LastReasonCode,
			Reference:  projection.Payment.ID, ObservedAt: *projection.Payment.UpdatedAt,
		})
	}
	for _, merchantOrder := range projection.MerchantOrders {
		items = append(items, EvidenceCheckpoint{
			Kind: "MERCHANT_ORDER", State: merchantOrder.State,
			ReasonCode: merchantOrder.FailureCode, Reference: merchantOrder.ID,
			ObservedAt: merchantOrder.UpdatedAt,
		})
	}
	for _, shipment := range projection.Shipments {
		items = append(items, EvidenceCheckpoint{
			Kind: "SHIPMENT", State: shipment.State, Reference: shipment.ID,
			ObservedAt: shipment.UpdatedAt,
		})
	}
	if projection.Receipt != nil {
		items = append(items, EvidenceCheckpoint{
			Kind: "RECEIPT", State: projection.Receipt.TerminalState,
			Reference: projection.Receipt.ReceiptHash, ObservedAt: projection.Receipt.CreatedAt,
		})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ObservedAt.After(items[j].ObservedAt) })
	return items
}
