// Package domain은 AgencyOrderProcess의 권위 모델이다(ADR-0056, ADR-0070).
//
// 이 패키지는 procmsg(메시지 규격)와 ordering/policy만 안다 — owner 제품의
// domain을 import하지 않는다. process 행의 생성·갱신은 ordering/process가
// 유일한 writer이며(ADR-0056 §1), 업무 전이의 진입점은 단일 이벤트 Reduce다.
package domain

import (
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"time"
)

// State는 stage cursor다(ADR-0052, 계약 v7 §12.1 — 어휘 불변).
type State string

const (
	StateWaitingCustomerPayment State = "WAITING_CUSTOMER_PAYMENT"
	StatePaymentReconciliation  State = "PAYMENT_RECONCILIATION"
	StateProcurementInProgress  State = "PROCUREMENT_IN_PROGRESS"
	StateLogisticsInProgress    State = "LOGISTICS_IN_PROGRESS"
	StateResolutionInProgress   State = "RESOLUTION_IN_PROGRESS"
	StateAttentionRequired      State = "ATTENTION_REQUIRED"
	StateTerminal               State = "TERMINAL"
)

// TerminalReason은 TERMINAL stage의 파생 사유다. CANCELLED/EXPIRED는 어휘 예약
// 상태로, 현행 동작에 쓰는 경로가 없다(ADR-0056 — 행위 보존).
type TerminalReason string

const (
	TerminalReasonCompletedAll     TerminalReason = "COMPLETED_ALL"
	TerminalReasonCompletedPartial TerminalReason = "COMPLETED_PARTIAL"
	TerminalReasonRefundedAll      TerminalReason = "REFUNDED_ALL"
	TerminalReasonCancelled        TerminalReason = "CANCELLED"
	TerminalReasonExpired          TerminalReason = "EXPIRED"
)

// Process는 소비한 이벤트의 fold다(ADR-0070 §4.1 — Order·MO·Unit 3층).
// 주문 단위 필드는 결제·정산·발행·고지·영수증 관찰이고, MerchantOrders·Units는
// identity로 접은 결정 단위 관찰이다. 종전 owner 집계 payload(총수·미종결
// 수·보상 수·물리 미종결 수)는 이 집합에서 파생한다 — 산술 누적이 아니라
// 멱등 덮어쓰기라 드리프트가 없다. JSON으로 process 행에 저장된다.
type ProcessState struct {
	UserID                 string                       `json:"userId,omitempty"`
	Authorization          procmsg.OrderAuthorization   `json:"authorization"`
	Effects                map[string]EffectExpectation `json:"effects,omitempty"`
	EntityVersions         map[string]int64             `json:"entityVersions,omitempty"`
	CommunicationAttention map[string]string            `json:"communicationAttention,omitempty"`
	SettlementState        string                       `json:"settlementState,omitempty"`
	SettlementReason       string                       `json:"settlementReason,omitempty"`

	PaymentSucceeded  bool   `json:"paymentSucceeded,omitempty"`
	PayPalSucceeded   bool   `json:"paypalSucceeded,omitempty"`
	OpenPaymentState  string `json:"openPaymentState,omitempty"`
	OpenPaymentReason string `json:"openPaymentReason,omitempty"`

	// 발행 사실과 시간 결정·결과 기록의 북키핑.
	Rail     string     `json:"rail,omitempty"`
	IssuedAt *time.Time `json:"issuedAt,omitempty"`
	// DelayNoticeDue는 발행+30일 타이머가 발화했음이다 — 종전 스캔처럼 발화
	// "이후의 어느 관찰에서든" 미배송 작업이 보이면 고지한다(one-shot 스냅샷
	// 검사가 아니라 상태다). DelayNoticeRecorded가 봉인한다.
	DelayNoticeDue      bool `json:"delayNoticeDue,omitempty"`
	DelayNoticeRecorded bool `json:"delayNoticeRecorded,omitempty"`
	ReceiptIssued       bool `json:"receiptIssued,omitempty"`
	// ReceiptState는 발급된 영수증의 terminal 상태다 — 재종결로 사유가 갱신되면
	// 영수증도 갱신 발급한다(ADR-0057 2차 D-e).
	ReceiptState string `json:"receiptState,omitempty"`
	// ReceiptTerminalTxFinalized는 Receipt Owner가 저장 payload의 확정을 검증한 사실이다.
	ReceiptTerminalTxFinalized bool `json:"receiptTerminalTxFinalized,omitempty"`

	// ModelVersion은 원자적 이행으로 적용하는 ProcessState 저장 규격의 세대다.
	ModelVersion int `json:"modelVersion,omitempty"`
	// MerchantOrders는 결정 단위 fold(key = merchant order id)다.
	MerchantOrders map[string]*MOState `json:"merchantOrders,omitempty"`
	// Units는 관찰 단위 fold(key = logistics expected unit id)다.
	Units map[string]*UnitFold `json:"units,omitempty"`
	// FundingByAllocation은 MO 생성 전(AUTHORIZE 직후) 관찰된 funding position
	// 상태다(key = allocation id) — MO 이벤트가 도착하면 fold에 채택한다.
	FundingByAllocation map[string]procmsg.FundingResult `json:"fundingByAllocation,omitempty"`
	// OrderAttention은 MO scope가 없는 개입 국면(발산·정산 환불 충돌·주문 단위
	// Effect 소진)의 마지막 사유다.
	OrderAttention string `json:"orderAttention,omitempty"`
	// agencyOrderIDHint는 결정 중 카드 key 등에 쓰는 주문 id다(저장되지 않는다).
	agencyOrderIDHint string
}

// Process는 권위 상태 행이다. Version은 모든 소비·결정 적용에서 증가한다 —
// stage가 그대로여도 커서(LastAppliedSeq)와 Process가 전진하면 결정이다.
type Process struct {
	AgencyOrderID  string
	State          State
	TerminalReason TerminalReason
	LastReasonCode string
	Version        int64
	LastAppliedSeq int64
	WakeAt         *time.Time
	ProcessState   ProcessState
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Event는 소비 측의 이벤트 행이다(payload는 procmsg 구조체로 파싱한다).
type Event struct {
	EntityKey           string
	SourceEntityVersion int64
	ID                  int64
	Seq                 int64
	Source              string
	Type                string
	Payload             []byte
	OccurredAt          time.Time
}

// PendingEffect is derived solely from unresolved business expectations.
type PendingEffect struct {
	ID              string
	Type            string
	NeedsAttention  bool
	MerchantOrderID string
}

// EffectDraft is a deterministic, immutable request for one Owner.
type EffectDraft struct {
	CausedByEventID int64
	Target          string
	Type            string
	Payload         any
	IdempotencyKey  string
}

// Decision은 이벤트 하나를 소비한 결론이다.
type Decision struct {
	Receipts       []procmsg.RequestReceipt
	State          State
	TerminalReason TerminalReason
	LastReasonCode string
	ProcessState   ProcessState
	Effects        []EffectDraft
	WakeAt         *time.Time
	// StageChanged는 stage/terminalReason/lastReasonCode 변경 여부다(로그용).
	// 적용(version 증가·커서 전진)은 StageChanged와 무관하게 일어난다.
	StageChanged bool
	// MerchantOrders는 이 결정의 MO 단위 사영(전후 phase·intent·attention)이다.
	MerchantOrders []MerchantOrderDecision
}
