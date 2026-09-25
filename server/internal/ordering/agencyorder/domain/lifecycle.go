package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrProcessStateInvalid = errors.New("AGENCY_ORDER_PROCESS_STATE_INVALID")
	ErrExecutionNotFound   = errors.New("AGENCY_ORDER_EXECUTION_NOT_FOUND")
	ErrExecutionInvalid    = errors.New("AGENCY_ORDER_EXECUTION_INVALID")
	ErrAssignmentRequired  = errors.New("AGENCY_ORDER_ASSIGNMENT_REQUIRED")
	ErrAssignmentConflict  = errors.New("AGENCY_ORDER_ASSIGNMENT_CONFLICT")
	ErrPIIAccessDenied     = errors.New("AGENCY_ORDER_PII_ACCESS_DENIED")
	ErrReceiptNotFound     = errors.New("AGENCY_ORDER_RECEIPT_NOT_FOUND")
	// ErrExecutionMixedTerminal은 완료된 unit이 있는 주문의 전체 환불을 막는다.
	// 현행 정산은 주문 단위 전액 complete/refund뿐이라, 완료 unit과 전액 환불이
	// 공존하면 사용자가 상품과 환불을 동시에 얻는 재무 모순이 생긴다. unit 단위
	// 부분 환불(Settlement v2)이 도입되면 이 제한은 제거된다.
	ErrExecutionMixedTerminal = errors.New("AGENCY_ORDER_MIXED_TERMINAL_UNSUPPORTED")
)

// ProcessState는 AgencyOrderProcess의 stage cursor다(ADR-0052, 계약 v7 §12.1).
// source 상태(CustomerPayment/MerchantOrder/Shipment)를 복제하지 않는 조율
// 상태이며, TERMINAL은 "더 발급할 command가 없다"는 판정이지 성공 복제가 아니다.
type ProcessState string

const (
	ProcessWaitingCustomerPayment ProcessState = "WAITING_CUSTOMER_PAYMENT"
	ProcessPaymentReconciliation  ProcessState = "PAYMENT_RECONCILIATION"
	ProcessProcurementInProgress  ProcessState = "PROCUREMENT_IN_PROGRESS"
	ProcessLogisticsInProgress    ProcessState = "LOGISTICS_IN_PROGRESS"
	ProcessResolutionInProgress   ProcessState = "RESOLUTION_IN_PROGRESS"
	ProcessAttentionRequired      ProcessState = "ATTENTION_REQUIRED"
	ProcessTerminal               ProcessState = "TERMINAL"
)

// TerminalReason은 TERMINAL stage의 파생 사유다. TERMINAL일 때만 존재한다.
type TerminalReason string

const (
	TerminalReasonCompletedAll     TerminalReason = "COMPLETED_ALL"
	TerminalReasonCompletedPartial TerminalReason = "COMPLETED_PARTIAL"
	TerminalReasonRefundedAll      TerminalReason = "REFUNDED_ALL"
	TerminalReasonCancelled        TerminalReason = "CANCELLED"
	TerminalReasonExpired          TerminalReason = "EXPIRED"
)

func CompletedTerminalReason(reason TerminalReason) bool {
	return reason == TerminalReasonCompletedAll || reason == TerminalReasonCompletedPartial
}

type Process struct {
	AgencyOrderID  string         `json:"agencyOrderId"`
	State          ProcessState   `json:"state"`
	TerminalReason TerminalReason `json:"terminalReason,omitempty"`
	Version        int64          `json:"version"`
	LastReasonCode string         `json:"lastReasonCode,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

type PaymentProjection struct {
	ID    string `json:"id"`
	State string `json:"state"`
	// Rail은 "GIWA"|"PAYPAL"이다. 빈 값은 GIWA(역사적 row)로 해석한다.
	Rail            string  `json:"rail,omitempty"`
	AmountBaseUnits string  `json:"amountBaseUnits"`
	PayTxHash       string  `json:"payTxHash,omitempty"`
	CompleteTxHash  string  `json:"completeTxHash,omitempty"`
	RefundTxHash    string  `json:"refundTxHash,omitempty"`
	SafeBlock       *uint64 `json:"safeBlock,omitempty"`
	FinalizedBlock  *uint64 `json:"finalizedBlock,omitempty"`
	// PayPal rail detail — chain 필드와 섞지 않는 compact 표현이다.
	PayPalOrderID  string `json:"paypalOrderId,omitempty"`
	CaptureID      string `json:"captureId,omitempty"`
	PayPalRefundID string `json:"paypalRefundId,omitempty"`
	// RefundedTotalCent는 정산 완료된 고객 환불 합계다. 주문 국면(완료/환불)은
	// Payment가 아니라 AgencyOrderProcess stage/terminalReason이 소유한다(ADR-0052).
	RefundedTotalCent int64      `json:"refundedTotalCent,omitempty"`
	LastReasonCode    string     `json:"lastReasonCode,omitempty"`
	UpdatedAt         *time.Time `json:"updatedAt,omitempty"`
}

// MerchantOrderSummary는 고객 사영용 Procurement 합성이다(계약 v7 §12.1 — 원본
// 상태는 Procurement가 소유하고 여기서는 read model로만 합성한다).
type MerchantOrderSummary struct {
	ID                  string `json:"id"`
	AllocationID        string `json:"allocationId"`
	ShopDomain          string `json:"shopDomain"`
	MerchantID          string `json:"merchantId"`
	CheckoutOrdinal     int    `json:"checkoutOrdinal"`
	CustomerGrossAmount Money  `json:"customerGrossAmount"`
	ExecutionMode       string `json:"executionMode"`
	State               string `json:"state"`
	FailureCode         string `json:"failureCode,omitempty"`
	FundingState        string `json:"fundingState,omitempty"`
	CancellationState   string `json:"cancellationState,omitempty"`
	RefundRequestState  string `json:"refundRequestState,omitempty"`
	CompensationAction  string `json:"compensationAction,omitempty"`
	CompensationState   string `json:"compensationState,omitempty"`
	DisputeState        string `json:"disputeState,omitempty"`
	DisputeOutcome      string `json:"disputeOutcome,omitempty"`
	// Phase는 OrderProcessor의 MO 결정 어휘다(ADR-0070 §3.2 — PLANNED…
	// CANCELLED). 비어 있으면 아직 결정 사영이 없다(발행 직후 ≤1 tick).
	Phase string `json:"phase,omitempty"`
	// CancelIntent는 고객 취소 intent의 리듀서 판정이다 — 접수(202) 뒤에도
	// 사영에 남아 진행 중·거절·미룸을 보여준다(ADR-0056 §6 약속의 이행).
	CancelIntent *MerchantOrderCancelIntent         `json:"cancelIntent,omitempty"`
	Operational  MerchantOrderOperationalProjection `json:"operational"`
	Units        []MerchantOrderUnitSummary         `json:"units"`
	UpdatedAt    time.Time                          `json:"updatedAt"`
}

// MerchantOrderCancelIntent는 MO별 취소 intent의 마지막 판정이다.
// Outcome: EFFECT_ISSUED(실행 대기) | DEFERRED(구매 진행 뒤 재평가) | REJECTED |
// SUCCEEDED | SUPERSEDED.
type MerchantOrderCancelIntent struct {
	Kind    string `json:"kind"`
	Outcome string `json:"outcome"`
	Code    string `json:"code,omitempty"`
}

// CancelIntentPending은 아직 종결되지 않은 취소 intent다 — 같은 MO에 다른
// 취소·환불 명령을 열지 않는다.
func (m MerchantOrderSummary) CancelIntentPending() bool {
	return m.CancelIntent != nil &&
		(m.CancelIntent.Outcome == "EFFECT_ISSUED" || m.CancelIntent.Outcome == "DEFERRED")
}

type MerchantOrderUnitSummary struct {
	ID          string `json:"id"`
	LineID      string `json:"lineId"`
	UnitIndex   int    `json:"unitIndex"`
	Disposition string `json:"disposition"`
}

// ShipmentSummary는 고객 사영용 Logistics 합성이다(계약 v7 §9 — 원본 상태는
// Logistics가 소유). 실물 패키지 단위이며 배정 unit 참조로 분할 배송을 보인다.
type ShipmentSummary struct {
	ID              string                     `json:"id"`
	MerchantOrderID string                     `json:"merchantOrderId"`
	Carrier         string                     `json:"carrier"`
	TrackingRef     string                     `json:"trackingRef"`
	State           string                     `json:"state"`
	Units           []MerchantOrderUnitSummary `json:"units"`
	UpdatedAt       time.Time                  `json:"updatedAt"`
}

// CustomerNotice는 주문 스코프 고지함 항목이다(ADR-0052 §2.6 — 회신 없음,
// PII·provider 원문 금지). ADR-0059 이후 운영자 발신은 Support 대화로
// 이동했고, 이 채널에는 SYSTEM 지연 rule 고지만 남아 projection.notices로
// 내려간다(process 정합 결합 유지 — watchdog·notice.recorded).
type CustomerNotice struct {
	ID              string     `json:"id"`
	AgencyOrderID   string     `json:"agencyOrderId"`
	UserID          string     `json:"-"`
	Kind            string     `json:"kind"`
	Body            string     `json:"body"`
	CreatedByUserID string     `json:"-"`
	IdempotencyKey  string     `json:"-"`
	CreatedAt       time.Time  `json:"createdAt"`
	ReadAt          *time.Time `json:"readAt,omitempty"`
}

type Receipt struct {
	ID                    string          `json:"id"`
	AgencyOrderID         string          `json:"agencyOrderId"`
	SettlementPaymentID   string          `json:"settlementPaymentId,omitempty"`
	CustomerPaymentID     string          `json:"customerPaymentId,omitempty"`
	Kind                  string          `json:"kind"`
	PaymentRail           string          `json:"paymentRail"`
	ProviderEnvironment   string          `json:"providerEnvironment"`
	Asset                 string          `json:"asset"`
	EconomicEffect        string          `json:"economicEffect"`
	MerchantExecutionMode string          `json:"merchantExecutionMode"`
	ExecutionProfileHash  string          `json:"executionProfileHash"`
	LegalSale             bool            `json:"legalSale"`
	TerminalState         string          `json:"terminalState"`
	TerminalTxHash        string          `json:"terminalTxHash"`
	ReceiptHash           string          `json:"receiptHash"`
	Payload               json.RawMessage `json:"payload"`
	CreatedAt             time.Time       `json:"createdAt"`
}

const (
	ReceiptKindTest            = "TEST"
	ReceiptKindLiveOrderRecord = "LIVE_ORDER_RECORD"
)

// HasFinalizedTerminalTransaction checks the proof frozen into the receipt,
// not a broadcast hash or a later mutable payment projection.
func (r Receipt) HasFinalizedTerminalTransaction() bool {
	if r.PaymentRail != PaymentProviderGIWA || r.TerminalTxHash == "" {
		return false
	}
	var proof struct {
		Payment      PaymentProjection  `json:"payment"`
		Transactions []ChainTransaction `json:"transactions"`
	}
	if json.Unmarshal(r.Payload, &proof) != nil {
		return false
	}
	refunded := r.TerminalState == string(TerminalReasonRefundedAll)
	if !refunded && proof.Payment.State != "COMPLETED" {
		return false
	}
	for _, transaction := range proof.Transactions {
		purposeMatches := transaction.Purpose == "COMPLETE"
		if refunded {
			purposeMatches = transaction.Purpose == "REFUND_PARTIAL" || transaction.Purpose == "REFUND"
		}
		if purposeMatches && transaction.State == "FINALIZED" && strings.EqualFold(transaction.TxHash, r.TerminalTxHash) {
			return true
		}
	}
	return false
}

type ChainTransaction struct {
	Purpose     string    `json:"purpose"`
	TxHash      string    `json:"txHash"`
	State       string    `json:"state"`
	BlockNumber uint64    `json:"blockNumber"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Projection struct {
	AgencyOrder        AgencyOrder        `json:"agencyOrder"`
	PaymentInstruction PaymentInstruction `json:"paymentInstruction"`
	Process            Process            `json:"process"`
	// AvailableActions는 서버가 계산한 고객 명령 공간이다(ADR-0055 §5 —
	// AvailableCustomerActions). Web은 자격 판정 없이 이 공간을 렌더한다.
	AvailableActions  []CustomerAction       `json:"availableActions"`
	Payment           *PaymentProjection     `json:"payment,omitempty"`
	ChainTransactions []ChainTransaction     `json:"chainTransactions"`
	MerchantOrders    []MerchantOrderSummary `json:"merchantOrders"`
	Shipments         []ShipmentSummary      `json:"shipments"`
	// Units는 physical unit 단위 추적 사영이다 — 고객 카드의 유일한
	// unit 소스. 판정은 domain.DeriveUnitStage 한 곳이 소유한다.
	Units          []UnitView       `json:"units"`
	RefundRequests []RefundRequest  `json:"refundRequests,omitempty"`
	Notices        []CustomerNotice `json:"notices,omitempty"`
	Receipt        *Receipt         `json:"receipt,omitempty"`
}
