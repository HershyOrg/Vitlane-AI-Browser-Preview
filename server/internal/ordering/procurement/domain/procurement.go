// Package domain은 Procurement Bounded Context의 자기 축이다(ADR-0052, 계약 v7
// §4.4·§8). Vitlane이 Shop에 실행하는 구매의 명세(manifest/unit), Shop-checkout별
// MerchantOrder와 실행 Task를 소유한다. 고객 결제·환불 원본 상태와 물류 사실은
// 소유하지 않는다.
package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/policy"
)

var (
	ErrTaskNotFound       = errors.New("PROCUREMENT_TASK_NOT_FOUND")
	ErrTaskStateInvalid   = errors.New("PROCUREMENT_TASK_STATE_INVALID")
	ErrAssignmentConflict = errors.New("PROCUREMENT_ASSIGNMENT_CONFLICT")
	ErrAssignmentRequired = errors.New("PROCUREMENT_ASSIGNMENT_REQUIRED")
	ErrLeaseExpired       = errors.New("PROCUREMENT_LEASE_EXPIRED")
	ErrResultInvalid      = errors.New("PROCUREMENT_RESULT_INVALID")
	ErrPIIAccessDenied    = errors.New("PROCUREMENT_PII_ACCESS_DENIED")
	ErrLiveModeClosed     = errors.New("PROCUREMENT_LIVE_MODE_CLOSED")
	ErrLogisticsNotReady  = errors.New("PROCUREMENT_LOGISTICS_NOT_READY")
	ErrFundingNotReady    = errors.New("PROCUREMENT_FUNDING_NOT_READY")
	ErrContinueURLGone    = errors.New("PROCUREMENT_CONTINUE_URL_UNAVAILABLE")
	// ErrOrderInException — RESOLUTION·ATTENTION 국면의 주문은 예외 처리가
	// 소유한다(ADR-0057 2차 D-d). 조달 명령은 트랜잭션에서 process 국면을
	// 재검사해 거절한다(자문 공간만 닫으면 TOCTOU 창이 남는다).
	ErrOrderInException = errors.New("ORDER_IN_EXCEPTION_RESOLUTION")
)

// ExecutionMode는 evidence의 실효성 축이다(ADR-0053 — 상태기계를 바꾸지
// 않는다). SIMULATED_NO_EFFECT(Sandbox)는 가짜 결제 위에서 placement·배송을
// "처리했다 치고" 기록하고, LIVE_MERCHANT_EFFECT는 Step 6 activation 뒤에만
// 열리는 실제 Shop effect다. 워크플로·상태 경로는 양 모드 동일하다.
type ExecutionMode string

const (
	ModeSimulatedNoEffect  ExecutionMode = "SIMULATED_NO_EFFECT"
	ModeLiveMerchantEffect ExecutionMode = "LIVE_MERCHANT_EFFECT"
)

type MerchantOrderState string

const (
	OrderPlanned          MerchantOrderState = "PLANNED"
	OrderReadyToPlace     MerchantOrderState = "READY_TO_PLACE"
	OrderPlacementPending MerchantOrderState = "PLACEMENT_PENDING"
	OrderPlaced           MerchantOrderState = "PLACED"
	OrderPlacementUnknown MerchantOrderState = "PLACEMENT_UNKNOWN"
	OrderFailed           MerchantOrderState = "FAILED"
	OrderCancelled        MerchantOrderState = "CANCELLED"
)

// TerminalOrderState는 unit coverage 판정에 쓰이는 조달-종결 집합이다.
// PLACED 이후의 물리 종결은 Logistics가 소유한다(ADR-0053).
func TerminalOrderState(state MerchantOrderState) bool {
	switch state {
	case OrderPlaced, OrderFailed, OrderCancelled:
		return true
	default:
		return false
	}
}

type UnitDisposition string

const (
	UnitPending                 UnitDisposition = "PENDING"
	UnitCustomerRefundDue       UnitDisposition = "CUSTOMER_REFUND_DUE"
	UnitCustomerRefundSatisfied UnitDisposition = "CUSTOMER_REFUND_SATISFIED"
	UnitZeroValueSatisfied      UnitDisposition = "ZERO_VALUE_SATISFIED"
	UnitNoPaymentEffect         UnitDisposition = "NO_PAYMENT_EFFECT"
)

type TaskState string

const (
	TaskQueued         TaskState = "QUEUED"
	TaskClaimed        TaskState = "CLAIMED"
	TaskInProgress     TaskState = "IN_PROGRESS"
	TaskSucceeded      TaskState = "SUCCEEDED"
	TaskFailed         TaskState = "FAILED"
	TaskCancelled      TaskState = "CANCELLED"
	TaskOutcomeUnknown TaskState = "OUTCOME_UNKNOWN"
)

type MerchantOrder struct {
	ID                string             `json:"id"`
	AgencyOrderID     string             `json:"agencyOrderId"`
	ManifestID        string             `json:"manifestId"`
	AllocationID      string             `json:"allocationId"`
	MerchantID        string             `json:"merchantId"`
	ShopDomain        string             `json:"shopDomain"`
	CheckoutOrdinal   int                `json:"checkoutOrdinal"`
	CheckoutSnapshot  json.RawMessage    `json:"checkoutSnapshot"`
	ExecutionMode     ExecutionMode      `json:"executionMode"`
	State             MerchantOrderState `json:"state"`
	FailureCode       string             `json:"failureCode,omitempty"`
	ExternalOrderRef  string             `json:"externalOrderRef,omitempty"`
	PlacementEvidence *PlacementEvidence `json:"placementEvidence,omitempty"`
	ResultHash        string             `json:"resultHash,omitempty"`
	Version           int64              `json:"version"`
	CreatedAt         time.Time          `json:"createdAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
}

type MerchantOrderUnit struct {
	ID              string          `json:"id"`
	MerchantOrderID string          `json:"merchantOrderId"`
	AgencyOrderID   string          `json:"agencyOrderId"`
	LineID          string          `json:"lineId"`
	UnitIndex       int             `json:"unitIndex"`
	Disposition     UnitDisposition `json:"disposition"`
	Version         int64           `json:"version"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

type ExecutionTask struct {
	ID                     string     `json:"id"`
	MerchantOrderID        string     `json:"merchantOrderId"`
	AgencyOrderID          string     `json:"agencyOrderId"`
	State                  TaskState  `json:"state"`
	AssignedOperatorUserID string     `json:"assignedOperatorUserId,omitempty"`
	AssignedAt             *time.Time `json:"assignedAt,omitempty"`
	LeaseUntil             *time.Time `json:"leaseUntil,omitempty"`
	HandledAt              *time.Time `json:"handledAt,omitempty"`
	Version                int64      `json:"version"`
	CreatedAt              time.Time  `json:"createdAt"`
	UpdatedAt              time.Time  `json:"updatedAt"`
}

// DeriveExecutionMode는 immutable snapshot의 PaymentSelection 축에서 실행 모드를
// 파생한다(ADR-0052 §4). 매핑 밖 조합은 fail-close다.
func DeriveExecutionMode(rail, providerEnvironment, economicEffect string) (ExecutionMode, error) {
	rail = strings.ToUpper(strings.TrimSpace(rail))
	environment := strings.ToUpper(strings.TrimSpace(providerEnvironment))
	effect := strings.ToUpper(strings.TrimSpace(economicEffect))
	switch {
	case effect == "NO_REAL_VALUE" &&
		(environment == "TESTNET" || environment == "SANDBOX" || environment == "TEST"):
		return ModeSimulatedNoEffect, nil
	case rail == "PAYPAL" && environment == "LIVE" && effect == "REAL_MONEY":
		return ModeLiveMerchantEffect, nil
	default:
		return "", ErrResultInvalid
	}
}

// ValidateFailureCode는 운영자 실패 사유를 검증한다(발행 스냅샷 밖 재량 사유
// 금지). 어휘의 유일한 표현은 policy.ProcurementFailureCodes다.
func ValidateFailureCode(value string) error {
	if !policy.ValidProcurementFailureCode(value) {
		return ErrResultInvalid
	}
	return nil
}

// ValidatePIIReveal은 배송지·continue_url 열람 사유를 검증한다(감사 필수 필드).
func ValidatePIIReveal(reasonCode, reasonDetail string) error {
	switch strings.TrimSpace(reasonCode) {
	case "PLACE_MERCHANT_ORDER", "VERIFY_MERCHANT_ORDER", "CUSTOMER_SUPPORT":
	default:
		return ErrPIIAccessDenied
	}
	detailLength := len([]rune(strings.TrimSpace(reasonDetail)))
	if detailLength < 8 || detailLength > 500 {
		return ErrPIIAccessDenied
	}
	return nil
}

var (
	ErrCancelNotEligible = errors.New("PROCUREMENT_CANCEL_NOT_ELIGIBLE")
	// ErrPlanNotEligible은 plan Effect의 업무 거절이다 — 비수납 receipt 또는
	// PaymentSelection의 ExecutionMode 매핑 불가(fail-close, ADR-0052 §4).
	ErrPlanNotEligible = errors.New("PROCUREMENT_PLAN_NOT_ELIGIBLE")
	ErrOrderNotFound   = errors.New("PROCUREMENT_ORDER_NOT_FOUND")
	ErrEvidenceInvalid = errors.New("PROCUREMENT_EVIDENCE_INVALID")
)

// 취소 환불의 금액 basis는 ordering/policy가 소유한다(ADR-0055 §2 —
// policy.RefundBasis·CustomerCancelBasisFor).
