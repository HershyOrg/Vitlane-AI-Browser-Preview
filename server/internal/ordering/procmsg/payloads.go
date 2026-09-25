package procmsg

import (
	"encoding/json"
	"time"
)

// 타입별 payload 구조체 — owner가 만들고 process가 파싱하는 공유 shape다.
// 필드는 owner 저장 어휘 문자열·수치·시각만 담는다(PII 금지).

type OrderIssuedPayload struct {
	Authorization OrderAuthorization `json:"authorization"`
	UserID        string             `json:"userId"`
	Rail          string             `json:"rail"` // PaymentSelection rail: "GIWA"|"PAYPAL"
	IssuedAt      time.Time          `json:"issuedAt"`
}

type RefundReviewDecidedPayload struct {
	RequestID       string `json:"requestId"`
	MerchantOrderID string `json:"merchantOrderId"`
	AllocationID    string `json:"allocationId"`
	Decision        string `json:"decision"` // APPROVED|REJECTED
	ReasonCode      string `json:"reasonCode,omitempty"`
}

type ReceiptIssuedPayload struct {
	ReceiptID           string `json:"receiptId"`
	TerminalState       string `json:"terminalState"`
	TerminalTxFinalized bool   `json:"terminalTxFinalized,omitempty"`
}

type NoticeRecordedPayload struct {
	Kind           string `json:"kind"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type RefundRequestedPayload struct {
	RequestID       string `json:"requestId"`
	MerchantOrderID string `json:"merchantOrderId"`
	AllocationID    string `json:"allocationId"`
	ReasonCode      string `json:"reasonCode,omitempty"`
}

// Open* 필드는 미결 시도 대표 1행의 전이 후 관찰이다(대사 필요 상태 우선 —
// 종전 ProcessFacts.OpenPaymentState 계약과 동일, owner가 같은 transaction에서
// 계산한다). 대표가 없으면 "".
type CustomerPaymentStateChangedPayload struct {
	CustomerPaymentID string `json:"customerPaymentId"`
	State             string `json:"state"`
	Reason            string `json:"reason,omitempty"`
	OpenState         string `json:"openState,omitempty"`
	OpenReason        string `json:"openReason,omitempty"`
}

// CustomerFundingReadyPayload names the durable customer-funding source that
// allows Procurement roots to be planned. For PayPal this is an authorization,
// not a capture receipt; each MO still requires its own activation before a
// merchant effect can start.
type CustomerFundingReadyPayload struct {
	CustomerPaymentID   string                    `json:"customerPaymentId"`
	Rail                string                    `json:"rail"`
	Source              string                    `json:"source"` // PAYPAL_AUTHORIZATION|GIWA_PREPAID
	ProviderEnvironment string                    `json:"providerEnvironment"`
	AmountMinor         int64                     `json:"amountMinor"`
	FundsReceiptID      string                    `json:"fundsReceiptId,omitempty"`
	Positions           []FundingPositionSnapshot `json:"positions"`
}

type FundingPositionSnapshot struct {
	PositionID   string `json:"positionId"`
	AllocationID string `json:"allocationId"`
	AmountMinor  int64  `json:"amountMinor"`
	State        string `json:"state"`
}

type FundsReceiptRecordedPayload struct {
	FundsReceiptID    string `json:"fundsReceiptId"`
	CustomerPaymentID string `json:"customerPaymentId"`
}

// MOCompensationStateChangedPayload is recomputed by Payment in the same
// transaction as a whole-MO compensation transition. Process adopts these
// counts; it never performs +1/-1 bookkeeping.
type MOCompensationStateChangedPayload struct {
	CompensationID               string `json:"compensationId"`
	MerchantOrderID              string `json:"merchantOrderId"`
	AllocationID                 string `json:"allocationId"`
	State                        string `json:"state"`
	Action                       string `json:"action,omitempty"` // VOID|REFUND|TVIT_REFUND
	Cause                        string `json:"cause"`
	ActiveCount                  int    `json:"activeCount"`
	SucceededCount               int    `json:"succeededCount"`
	AllMerchantOrdersCompensated bool   `json:"allMerchantOrdersCompensated"`
}

type SettlementStateChangedPayload struct {
	SettlementPaymentID string `json:"settlementPaymentId"`
	State               string `json:"state"`
	Reason              string `json:"reason,omitempty"`
}

type SettlementRefundConflictPayload struct {
	SettlementPaymentID string `json:"settlementPaymentId"`
	Reason              string `json:"reason"`
}

type MerchantOrderStateChangedPayload struct {
	TaskID            string              `json:"taskId,omitempty"`
	UnitIDs           []string            `json:"unitIds,omitempty"`
	Units             []UnitManifestEntry `json:"units,omitempty"`
	MerchantOrderID   string              `json:"merchantOrderId"`
	AllocationID      string              `json:"allocationId"`
	ShopDomain        string              `json:"shopDomain"`
	State             string              `json:"state"`
	FailureCode       string              `json:"failureCode,omitempty"`
	CompensationCause string              `json:"compensationCause,omitempty"`
	// UnitCount는 이 MerchantOrder의 물리 unit 수(owner 자기 행)다 — 등록 전
	// 물류 기대의 미종결 수를 fold가 파생하는 근거다(ADR-0070 §4.1).
	UnitCount int `json:"unitCount"`
	// TotalCount·OpenCount는 전이 후 이 주문의 MerchantOrder 총수와
	// 조달-미종결(PLACED/FAILED/CANCELLED 밖) 수다 — owner가 COUNT한 값.
	// v2 fold는 identity 집합에서 같은 값을 파생하므로 읽지 않는다(호환 유지).
	TotalCount int `json:"totalCount"`
	OpenCount  int `json:"openCount"`
}

// MOFundingStateChangedPayload는 MO funding position 전이다(ADR-0070 §4.2).
// AVAILABLE→ACTIVATION_PENDING/UNKNOWN→ACTIVE→RELEASE_*→RELEASED / FAILED.
// MerchantOrderID는 plan 전(AUTHORIZE 직후 position 생성) 비어 있을 수 있다.
type MOFundingStateChangedPayload struct {
	PositionID      string `json:"positionId"`
	MerchantOrderID string `json:"merchantOrderId,omitempty"`
	AllocationID    string `json:"allocationId"`
	Rail            string `json:"rail"`
	State           string `json:"state"`
}

// DisputeStateChangedPayload는 PayPal dispute case 전이(webhook·수동 action)다.
type DisputeStateChangedPayload struct {
	CaseID          string `json:"caseId"`
	MerchantOrderID string `json:"merchantOrderId,omitempty"`
	MOCashReceiptID string `json:"moCashReceiptId,omitempty"`
	State           string `json:"state"`
	ProviderStatus  string `json:"providerStatus,omitempty"`
	Outcome         string `json:"outcome,omitempty"`
	ActionID        string `json:"actionId,omitempty"`
	Version         int64  `json:"version"`
}

// EffectLockStateChangedPayload는 procurement effect lock(캡처↔구매 펜스)
// 전이다: FUNDING_PENDING/FUNDING_UNKNOWN→STARTED→PLACED / FAILED / OUTCOME_UNKNOWN.
type EffectLockStateChangedPayload struct {
	MerchantOrderID string `json:"merchantOrderId"`
	AllocationID    string `json:"allocationId"`
	TaskID          string `json:"taskId"`
	State           string `json:"state"`
	FundingState    string `json:"fundingState,omitempty"`
	OperatorUserID  string `json:"operatorUserId,omitempty"`
}

// CustomerRequestStateChangedPayload는 조달 고객 요청(정보·동의)의 생성·응답·
// 거절·종결이다. 카드 발행과 MO "요청 열림" 플래그의 원천이다.
type CustomerRequestStateChangedPayload struct {
	RequestID        string `json:"requestId"`
	MerchantOrderID  string `json:"merchantOrderId"`
	Kind             string `json:"kind"`
	State            string `json:"state"`
	SourceDecisionID string `json:"sourceDecisionId,omitempty"`
	ActorUserID      string `json:"actorUserId,omitempty"`
	CustomerUserID   string `json:"customerUserId"`
	Version          int64  `json:"version"`
}

// ProcurementDecisionRecordedPayload는 운영자 수동 판단 기록이다. UNABLE만
// 고객 카드 대상이고 나머지는 타임라인·감사다.
type ProcurementDecisionRecordedPayload struct {
	DecisionRecordID string `json:"decisionRecordId"`
	MerchantOrderID  string `json:"merchantOrderId"`
	Decision         string `json:"decision"`
	OperatorUserID   string `json:"operatorUserId,omitempty"`
}

// ShipmentStateChangedPayload는 실물 패키지(Shipment) 전이다. Unit fold는
// fulfillment 이벤트가 소유하고, 이 이벤트는 FULFILLING 세분·타임라인용이다.
type ShipmentStateChangedPayload struct {
	ShipmentID      string   `json:"shipmentId"`
	MerchantOrderID string   `json:"merchantOrderId"`
	State           string   `json:"state"`
	ExpectedUnitIDs []string `json:"expectedUnitIds,omitempty"`
	Version         int64    `json:"version"`
}

// ReturnStateChangedPayload는 수동 회수 lane 전이다.
type ReturnStateChangedPayload struct {
	ReturnID            string `json:"returnId"`
	ExpectedUnitID      string `json:"expectedUnitId"`
	MerchantOrderID     string `json:"merchantOrderId"`
	State               string `json:"state"`
	MerchantDisposition string `json:"merchantDisposition,omitempty"`
	Version             int64  `json:"version"`
}

// MerchantOrdersSnapshotPayload는 v2 부트스트랩 기준선이다 — 컷오버 시점의
// MO·Unit 관찰을 owner facts에서 합성한다(효과 없음, fold 값만 채운다).
type MerchantOrdersSnapshotPayload struct {
	MerchantOrders []MerchantOrderSnapshot `json:"merchantOrders"`
}

type MerchantOrderSnapshot struct {
	MerchantOrderID    string           `json:"merchantOrderId"`
	AllocationID       string           `json:"allocationId"`
	ShopDomain         string           `json:"shopDomain"`
	State              string           `json:"state"`
	FailureCode        string           `json:"failureCode,omitempty"`
	UnitCount          int              `json:"unitCount"`
	FundingState       string           `json:"fundingState,omitempty"`
	EffectLockState    string           `json:"effectLockState,omitempty"`
	CompensationState  string           `json:"compensationState,omitempty"`
	CompensationAction string           `json:"compensationAction,omitempty"`
	CompensationCause  string           `json:"compensationCause,omitempty"`
	Units              []UnitSnapshot   `json:"units,omitempty"`
	OpenRequestIDs     []string         `json:"openRequestIds,omitempty"`
	CancellationKind   string           `json:"cancellationKind,omitempty"`
	RefundRequestState string           `json:"refundRequestState,omitempty"`
	Dispute            *DisputeSnapshot `json:"dispute,omitempty"`
}

type UnitSnapshot struct {
	ExpectedUnitID string `json:"expectedUnitId"`
	Fulfillment    string `json:"fulfillment"`
}

type DisputeSnapshot struct {
	CaseID  string `json:"caseId"`
	State   string `json:"state"`
	Outcome string `json:"outcome,omitempty"`
}

type UnitFulfillmentChangedPayload struct {
	ExpectedUnitID  string `json:"expectedUnitId"`
	MerchantOrderID string `json:"merchantOrderId"`
	Fulfillment     string `json:"fulfillment"`
	// PendingPhysicalCount는 전이 후 PLACED unit 중 물리 배송 미종결 수,
	// DeliveredValueExists는 수령·해소된 가치 존재다(계약 v7 §9.1 coverage).
	PendingPhysicalCount int  `json:"pendingPhysicalCount"`
	DeliveredValueExists bool `json:"deliveredValueExists"`
}

type DeliveryFaultJudgedPayload struct {
	ResolutionID    string `json:"resolutionId"`
	ExpectedUnitID  string `json:"expectedUnitId"`
	MerchantOrderID string `json:"merchantOrderId"`
	AllocationID    string `json:"allocationId"`
	Cause           string `json:"cause"`
	Judgment        string `json:"judgment"`
}

type TimerFiredPayload struct {
	Kind string `json:"kind"`
}

type WatchdogDivergencePayload struct {
	ExpectedState  string `json:"expectedState"`
	ExpectedReason string `json:"expectedReason,omitempty"`
	Detail         string `json:"detail,omitempty"`
}

// MigratedPayload는 컷오버 부트스트랩 스냅샷이다 — 종전 ProcessFacts 관찰을
// 그대로 담아 첫 결정이 항등으로 수렴하게 한다(ADR-0056 §5).

// Effect payload — Manager가 만들고 target executor가 파싱한다.

// ExecuteMOCompensationPayload targets one immutable MO allocation. Payment
// selects VOID/REFUND/TVIT_REFUND from the rail and funding milestone.
type ExecuteMOCompensationPayload struct {
	MerchantOrderID string `json:"merchantOrderId"`
	AllocationID    string `json:"allocationId"`
	Cause           string `json:"cause"`
	RequestID       string `json:"requestId,omitempty"`
	ExpectedUnitID  string `json:"expectedUnitId,omitempty"`
}

const (
	CompensationCauseCustomerCancelPreEffect  = "CUSTOMER_CANCEL_PRE_EFFECT"
	CompensationCauseCustomerRefundPostEffect = "CUSTOMER_REFUND_POST_EFFECT"
	CompensationCauseProcurementFailure       = "PROCUREMENT_FAILURE"
	CompensationCauseDeliveryException        = "DELIVERY_EXCEPTION"
	CompensationCauseDelayRule                = "DELAY_RULE"
)

type PlanFromFundingPayload struct {
	CustomerPaymentID string                    `json:"customerPaymentId"`
	Rail              string                    `json:"rail"`
	AmountMinor       int64                     `json:"amountMinor"`
	FundsReceiptID    string                    `json:"fundsReceiptId,omitempty"`
	Positions         []FundingPositionSnapshot `json:"positions"`
}

type RegisterExpectedUnitsPayload struct {
	MerchantOrderID string              `json:"merchantOrderId"`
	State           string              `json:"state"`
	Units           []UnitManifestEntry `json:"units"`
}

type SendNoticePayload struct {
	Kind           string `json:"kind"`
	IdempotencyKey string `json:"idempotencyKey"`
}

// PublishSupportCardPayload는 고객 통신 카드 발행 Effect다(ADR-0070 §4.5).
// SupportKey는 Support 발신 멱등 키(종전 어댑터 규칙 그대로)라 컷오버 전후
// 같은 카드가 중복되지 않는다. Detail은 owner 행에 없는 결정 사실(취소 거절
// 사유 등)이다.
type PublishSupportCardPayload struct {
	Card            string            `json:"card"`
	ReferenceID     string            `json:"referenceId"`
	MerchantOrderID string            `json:"merchantOrderId,omitempty"`
	ActionID        string            `json:"actionId,omitempty"`
	SupportKey      string            `json:"supportKey"`
	Detail          map[string]string `json:"detail,omitempty"`
}

// ParsePayload는 소비 측의 공용 파서다.
func ParsePayload[T any](raw []byte) (T, error) {
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, err
	}
	return value, nil
}
