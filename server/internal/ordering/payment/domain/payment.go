// Package domain은 rail-neutral 고객 결제(Payment Bounded Context)의 상태와
// 불변 조건을 소유한다(ADR-0050). PR-1 범위는 PayPal Sandbox rail이며, 수납 성공
// 이후의 주문 처리는 기존 AgencyOrder lifecycle이 소유한다.
package domain

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalid                    = errors.New("PAYMENT_INVALID")
	ErrNotFound                   = errors.New("PAYMENT_NOT_FOUND")
	ErrConflict                   = errors.New("PAYMENT_CONFLICT")
	ErrIdempotencyExpired         = errors.New("PAYMENT_IDEMPOTENCY_WINDOW_EXPIRED")
	ErrInstructionExpired         = errors.New("PAYMENT_INSTRUCTION_EXPIRED")
	ErrInstructionMismatch        = errors.New("PAYMENT_INSTRUCTION_MISMATCH")
	ErrInstructionNotConsumable   = errors.New("PAYMENT_INSTRUCTION_NOT_CONSUMABLE")
	ErrRailUnavailable            = errors.New("PAYMENT_RAIL_UNAVAILABLE")
	ErrCaptureBlocked             = errors.New("PAYMENT_CAPTURE_BLOCKED")
	ErrAuthorizationBlocked       = errors.New("PAYMENT_AUTHORIZATION_BLOCKED")
	ErrFundingNotAvailable        = errors.New("PAYMENT_MO_FUNDING_NOT_AVAILABLE")
	ErrFundingOutcomeUnknown      = errors.New("PAYMENT_MO_FUNDING_OUTCOME_UNKNOWN")
	ErrCompensationNotAvailable   = errors.New("PAYMENT_MO_COMPENSATION_NOT_AVAILABLE")
	ErrCompensationOutcomeUnknown = errors.New("PAYMENT_MO_COMPENSATION_OUTCOME_UNKNOWN")
	ErrCompensationAttemptFailed  = errors.New("PAYMENT_MO_COMPENSATION_ATTEMPT_FAILED")
	ErrBindingMismatch            = errors.New("PAYPAL_ACCOUNT_BINDING_MISMATCH")
)

const (
	PayPalSandboxExecutionProfileHash = "0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665"
	PayPalLiveExecutionProfileHash    = "0xba51a8eb9a32c1c6ede94a0ad7b8eb75b81ab1a1028dd7c536219895c37f096a"
)

type PaymentState string

const (
	PaymentCreated        PaymentState = "CREATED"
	PaymentActionRequired PaymentState = "ACTION_REQUIRED"
	PaymentProcessing     PaymentState = "PROCESSING"
	PaymentOutcomeUnknown PaymentState = "OUTCOME_UNKNOWN"
	// PaymentAuthorized means the customer approved and PayPal verified one
	// full-order authorization. No MO capture is implied.
	PaymentAuthorized        PaymentState = "AUTHORIZED"
	PaymentPartiallyCaptured PaymentState = "PARTIALLY_CAPTURED"
	PaymentCaptured          PaymentState = "CAPTURED"
	PaymentClosed            PaymentState = "CLOSED"
	PaymentFailed            PaymentState = "FAILED"
	PaymentAbandoned         PaymentState = "ABANDONED"
	PaymentExpired           PaymentState = "EXPIRED"
	PaymentSuperseded        PaymentState = "SUPERSEDED"
)

// 주문 terminal 국면(완료/환불)은 Payment 소유가 아니다 — AgencyOrderProcess
// stage cursor가 소유한다(ADR-0052). Payment는 수납 사실(state)과 환불 원장만
// 소유한다.

type AttemptState string

const (
	AttemptOrderPrepared             AttemptState = "ORDER_PREPARED"
	AttemptCancelledBeforeCreate     AttemptState = "CANCELLED_BEFORE_CREATE"
	AttemptOrderCreateSubmitted      AttemptState = "ORDER_CREATE_SUBMITTED"
	AttemptOrderCreateUnknown        AttemptState = "ORDER_CREATE_UNKNOWN"
	AttemptOrderCreateFailed         AttemptState = "ORDER_CREATE_FAILED"
	AttemptPayerActionRequired       AttemptState = "PAYER_ACTION_REQUIRED"
	AttemptPayerApproved             AttemptState = "PAYER_APPROVED"
	AttemptApprovalReversed          AttemptState = "APPROVAL_REVERSED"
	AttemptCancelledByUser           AttemptState = "CANCELLED_BY_USER"
	AttemptExpired                   AttemptState = "EXPIRED"
	AttemptSupersededBeforeAuthorize AttemptState = "SUPERSEDED_BEFORE_AUTHORIZE"
	AttemptAbandonedBeforeAuthorize  AttemptState = "ABANDONED_BEFORE_AUTHORIZE"
	AttemptAuthorizeSubmitted        AttemptState = "AUTHORIZE_SUBMITTED"
	AttemptAuthorizePending          AttemptState = "AUTHORIZE_PENDING"
	AttemptAuthorizeOutcomeUnknown   AttemptState = "AUTHORIZE_OUTCOME_UNKNOWN"
	AttemptAuthorizeCompleted        AttemptState = "AUTHORIZE_COMPLETED"
	AttemptAuthorizeDeclined         AttemptState = "AUTHORIZE_DECLINED"
	AttemptAuthorizeFailed           AttemptState = "AUTHORIZE_FAILED"
)

// AttemptTerminal은 같은 CustomerPayment 아래 새 attempt를 허용하기 전에 확정돼야
// 하는 definitive 상태다 (§7.2 attempt/retry 불변 조건).
func AttemptTerminal(state AttemptState) bool {
	switch state {
	case AttemptCancelledBeforeCreate, AttemptOrderCreateFailed,
		AttemptExpired, AttemptApprovalReversed, AttemptSupersededBeforeAuthorize,
		AttemptAbandonedBeforeAuthorize, AttemptAuthorizeCompleted,
		AttemptAuthorizeDeclined, AttemptAuthorizeFailed:
		return true
	default:
		return false
	}
}

// PaymentStateFor는 §7.2 공통 CustomerPayment projection 표를 구현한다.
func PaymentStateFor(attempt AttemptState) PaymentState {
	switch attempt {
	case AttemptOrderPrepared, AttemptOrderCreateSubmitted, AttemptOrderCreateUnknown,
		AttemptPayerApproved, AttemptAuthorizeSubmitted, AttemptAuthorizePending:
		return PaymentProcessing
	case AttemptPayerActionRequired, AttemptCancelledByUser:
		return PaymentActionRequired
	case AttemptAuthorizeOutcomeUnknown:
		return PaymentOutcomeUnknown
	case AttemptAuthorizeCompleted:
		return PaymentAuthorized
	case AttemptOrderCreateFailed, AttemptAuthorizeDeclined, AttemptAuthorizeFailed,
		AttemptExpired, AttemptApprovalReversed:
		return PaymentFailed
	case AttemptSupersededBeforeAuthorize:
		return PaymentSuperseded
	case AttemptCancelledBeforeCreate, AttemptAbandonedBeforeAuthorize:
		return PaymentAbandoned
	default:
		return PaymentProcessing
	}
}

type OperationState string

const (
	OperationPrepared  OperationState = "PREPARED"
	OperationSent      OperationState = "SENT"
	OperationSucceeded OperationState = "SUCCEEDED"
	OperationFailed    OperationState = "FAILED"
	OperationUnknown   OperationState = "UNKNOWN"
	OperationCancelled OperationState = "CANCELLED"
)

type OperationPurpose string

const (
	OperationPayPalOrderCreate OperationPurpose = "PAYPAL_ORDER_CREATE"
	OperationPayPalAuthorize   OperationPurpose = "PAYPAL_AUTHORIZE"
	OperationPayPalMOCapture   OperationPurpose = "PAYPAL_MO_CAPTURE"
	OperationPayPalAuthVoid    OperationPurpose = "PAYPAL_AUTH_VOID"
	OperationPayPalReauthorize OperationPurpose = "PAYPAL_REAUTHORIZE"
	OperationPayPalMORefund    OperationPurpose = "PAYPAL_MO_REFUND"
)

type CustomerPayment struct {
	ID                    string       `json:"id"`
	AgencyOrderID         string       `json:"agencyOrderId"`
	UserID                string       `json:"-"`
	Rail                  string       `json:"rail"`
	ProviderEnvironment   string       `json:"providerEnvironment"`
	Asset                 string       `json:"asset"`
	EconomicEffect        string       `json:"economicEffect"`
	MerchantExecutionMode string       `json:"merchantExecutionMode"`
	ExecutionProfileHash  string       `json:"executionProfileHash"`
	AmountMinor           int64        `json:"amountMinor"`
	Currency              string       `json:"currency"`
	State                 PaymentState `json:"state"`
	LastReasonCode        string       `json:"lastReasonCode,omitempty"`
	Version               int64        `json:"version"`
	CreatedAt             time.Time    `json:"createdAt"`
	UpdatedAt             time.Time    `json:"updatedAt"`
}

type PayPalAttempt struct {
	ID                string       `json:"id"`
	CustomerPaymentID string       `json:"customerPaymentId"`
	Sequence          int          `json:"sequence"`
	State             AttemptState `json:"state"`
	PayPalOrderID     string       `json:"paypalOrderId,omitempty"`
	ApprovalURL       string       `json:"approvalUrl,omitempty"`
	ReturnNonce       string       `json:"-"`
	LastReasonCode    string       `json:"lastReasonCode,omitempty"`
	Version           int64        `json:"version"`
	CreatedAt         time.Time    `json:"createdAt"`
	UpdatedAt         time.Time    `json:"updatedAt"`
}

type ExternalOperation struct {
	ID                  string           `json:"id"`
	Purpose             OperationPurpose `json:"purpose"`
	OwnerKind           string           `json:"ownerKind"`
	OwnerID             string           `json:"ownerId"`
	IdempotencyKey      string           `json:"idempotencyKey"`
	RequestHash         string           `json:"requestHash"`
	State               OperationState   `json:"state"`
	ProviderResourceID  string           `json:"providerResourceId,omitempty"`
	FirstSentAt         *time.Time       `json:"firstSentAt,omitempty"`
	IdempotencyDeadline *time.Time       `json:"idempotencyDeadline,omitempty"`
	LastReasonCode      string           `json:"lastReasonCode,omitempty"`
}

type FundsReceipt struct {
	ID                   string     `json:"id"`
	CustomerPaymentID    string     `json:"customerPaymentId"`
	AgencyOrderID        string     `json:"agencyOrderId"`
	Kind                 string     `json:"kind"`
	ProviderEnvironment  string     `json:"providerEnvironment"`
	ExecutionProfileHash string     `json:"executionProfileHash"`
	CaptureID            string     `json:"captureId"`
	PayPalOrderID        string     `json:"paypalOrderId"`
	AmountMinor          int64      `json:"amountMinor"`
	EconomicsReconciled  bool       `json:"economicsReconciled"`
	ProcessorFeeMinor    int64      `json:"processorFeeMinor"`
	NetReceivableMinor   int64      `json:"netReceivableMinor"`
	Currency             string     `json:"currency"`
	Accepted             bool       `json:"accepted"`
	OccurredAt           *time.Time `json:"occurredAt,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
}

// AccountBinding은 environment별 사전 검증된 non-secret merchant identity다.
// email로 merchant를 추론하지 않고 모든 provider resource의 payee와 대조한다.
type AccountBinding struct {
	Environment         string    `json:"environment"`
	MerchantID          string    `json:"merchantId"`
	ClientIDFingerprint string    `json:"clientIdFingerprint"`
	WebhookID           string    `json:"webhookId"`
	VerifiedBy          string    `json:"verifiedBy"`
	EvidenceNote        string    `json:"evidenceNote,omitempty"`
	VerifiedAt          time.Time `json:"verifiedAt"`
}

// WebhookEvent는 서명 검증을 통과한 event의 safe field만 담는다.
// raw payload와 PII는 저장·전달하지 않는다.
type WebhookEvent struct {
	Environment    string    `json:"environment"`
	WebhookID      string    `json:"webhookId"`
	EventID        string    `json:"eventId"`
	EventType      string    `json:"eventType"`
	TransmissionID string    `json:"transmissionId"`
	ResourceKind   string    `json:"resourceKind,omitempty"`
	ResourceID     string    `json:"resourceId,omitempty"`
	ReceivedAt     time.Time `json:"receivedAt"`
}

// PayableInstruction은 AgencyOrder가 발행한 PaymentInstruction 중 Payment가
// 소비하는 최소 projection이다.
type PayableInstruction struct {
	AgencyOrderID         string
	UserID                string
	Rail                  string
	ProviderEnvironment   string
	Asset                 string
	EconomicEffect        string
	MerchantExecutionMode string
	ExecutionProfileHash  string
	SnapshotHash          string
	CustomerPayableMinor  int64
	Currency              string
	ExpiresAt             time.Time
}

func (i PayableInstruction) ValidateForPayPal(environment string, now time.Time) error {
	environment = strings.ToUpper(strings.TrimSpace(environment))
	if i.ProviderEnvironment != environment || validatePayPalExecutionProfile(
		i.Rail, i.ProviderEnvironment, i.Asset, i.EconomicEffect,
		i.MerchantExecutionMode, i.ExecutionProfileHash,
	) != nil {
		return ErrInstructionMismatch
	}
	if i.CustomerPayableMinor <= 0 || i.Currency != "USD" {
		return ErrInstructionMismatch
	}
	if !now.Before(i.ExpiresAt) {
		return ErrInstructionExpired
	}
	return nil
}

func (i PayableInstruction) ValidateForPayPalSandbox(now time.Time) error {
	return i.ValidateForPayPal("SANDBOX", now)
}

func (p CustomerPayment) MatchesInstruction(i PayableInstruction) bool {
	return p.AgencyOrderID == i.AgencyOrderID && p.Rail == i.Rail &&
		p.ProviderEnvironment == i.ProviderEnvironment && p.Asset == i.Asset &&
		p.EconomicEffect == i.EconomicEffect &&
		p.MerchantExecutionMode == i.MerchantExecutionMode &&
		p.ExecutionProfileHash == i.ExecutionProfileHash &&
		p.AmountMinor == i.CustomerPayableMinor && p.Currency == i.Currency
}

func validatePayPalExecutionProfile(
	rail, environment, asset, economicEffect, merchantExecutionMode, profileHash string,
) error {
	if rail != "PAYPAL" || asset != "USD" {
		return ErrInstructionMismatch
	}
	switch environment {
	case "SANDBOX":
		if economicEffect != "NO_REAL_VALUE" ||
			merchantExecutionMode != "SIMULATED_NO_EFFECT" ||
			profileHash != PayPalSandboxExecutionProfileHash {
			return ErrInstructionMismatch
		}
	case "LIVE":
		if economicEffect != "REAL_MONEY" ||
			merchantExecutionMode != "LIVE_MERCHANT_EFFECT" ||
			profileHash != PayPalLiveExecutionProfileHash {
			return ErrInstructionMismatch
		}
	default:
		return ErrInstructionMismatch
	}
	return nil
}

func (p CustomerPayment) ValidatePayPalExecutionProfile() error {
	if p.AmountMinor <= 0 || p.Currency != "USD" {
		return ErrInstructionMismatch
	}
	return validatePayPalExecutionProfile(
		p.Rail, p.ProviderEnvironment, p.Asset, p.EconomicEffect,
		p.MerchantExecutionMode, p.ExecutionProfileHash,
	)
}

func (r FundsReceipt) ValidatePayPalExecutionProfile() error {
	if r.Kind != "PAYPAL_CAPTURE" || !r.Accepted || strings.TrimSpace(r.CaptureID) == "" {
		return ErrInstructionMismatch
	}
	switch r.ProviderEnvironment {
	case "SANDBOX":
		if r.ExecutionProfileHash != PayPalSandboxExecutionProfileHash {
			return ErrInstructionMismatch
		}
	case "LIVE":
		if r.ExecutionProfileHash != PayPalLiveExecutionProfileHash {
			return ErrInstructionMismatch
		}
	default:
		return ErrInstructionMismatch
	}
	return nil
}
