package domain

import (
	"errors"
	"math/big"
	"strings"
	"time"
)

var (
	ErrAuthorizationInvalid          = errors.New("SETTLEMENT_AUTHORIZATION_INVALID")
	ErrWalletOwnershipRequired       = errors.New("SETTLEMENT_WALLET_OWNERSHIP_REQUIRED")
	ErrAuthorizationInstructionStale = errors.New("SETTLEMENT_INSTRUCTION_STALE")
	ErrAuthorizationExpired          = errors.New("SETTLEMENT_AUTHORIZATION_EXPIRED")
	ErrAuthorizationNotFound         = errors.New("SETTLEMENT_AUTHORIZATION_NOT_FOUND")
	ErrPaymentNotFound               = errors.New("SETTLEMENT_PAYMENT_NOT_FOUND")
	ErrPaymentStateInvalid           = errors.New("SETTLEMENT_PAYMENT_STATE_INVALID")
	ErrTransactionInvalid            = errors.New("CHAIN_TRANSACTION_INVALID")
)

type PaymentStatus string

const (
	PaymentAuthorized          PaymentStatus = "AUTHORIZED"
	PaymentAwaitingAllowance   PaymentStatus = "AWAITING_ALLOWANCE"
	PaymentSubmitted           PaymentStatus = "PAYMENT_SUBMITTED"
	PaymentSubmissionUnknown   PaymentStatus = "SUBMISSION_UNKNOWN"
	PaymentSafe                PaymentStatus = "SAFE"
	PaymentFinalized           PaymentStatus = "FINALIZED"
	PaymentCompletionSubmitted PaymentStatus = "COMPLETION_SUBMITTED"
	PaymentCompleted           PaymentStatus = "COMPLETED"
	PaymentRefundPending       PaymentStatus = "REFUND_PENDING"
	PaymentRefunded            PaymentStatus = "REFUNDED"
	PaymentFailed              PaymentStatus = "FAILED"
)

// FeeRoundingSlackBaseUnits는 컨트랙트 v2의 FEE_ROUNDING_SLACK과 같은 값이다 —
// cent-ceil 정책이 base-unit 정확값보다 커질 수 있는 최대 반올림 허용(1 cent).
const FeeRoundingSlackBaseUnits = 10_000

// PaymentAuthorization v2 (ADR-0050): 서버가 확정한 정확값 {passThroughAmount,
// feeAmount}를 서명에 고정한다. 수수료는 pay 시점에 feeRecipient로 즉시 이체되고
// escrow에는 pass-through만 남는다.
type PaymentAuthorization struct {
	Payer                   string `json:"payer"`
	Token                   string `json:"token"`
	PassThroughAmount       string `json:"passThroughAmount"`
	FeeAmount               string `json:"feeAmount"`
	OrderHash               string `json:"orderHash"`
	MerchantID              string `json:"merchantId"`
	MerchantRegistryVersion uint64 `json:"merchantRegistryVersion"`
	FeeBps                  uint16 `json:"feeBps"`
	FeeRecipient            string `json:"feeRecipient"`
	PrincipalRecipient      string `json:"principalRecipient"`
	AssuranceLevel          string `json:"assuranceLevel"`
	Nonce                   string `json:"nonce"`
	PayDeadline             uint64 `json:"payDeadline"`
	RefundAfter             uint64 `json:"refundAfter"`
}

func (a PaymentAuthorization) Validate(now time.Time) error {
	passThrough, passOK := new(big.Int).SetString(a.PassThroughAmount, 10)
	fee, feeOK := new(big.Int).SetString(a.FeeAmount, 10)
	nonce, nonceOK := new(big.Int).SetString(a.Nonce, 10)
	if !passOK || passThrough.Sign() <= 0 || !feeOK || fee.Sign() < 0 ||
		!nonceOK || nonce.Sign() < 0 ||
		!isAddress(a.Payer) || !isAddress(a.Token) || !isAddress(a.FeeRecipient) ||
		!isAddress(a.PrincipalRecipient) || !isHash(a.OrderHash) || !isHash(a.MerchantID) ||
		!isHash(a.AssuranceLevel) || a.MerchantRegistryVersion == 0 ||
		a.RefundAfter <= a.PayDeadline {
		return ErrAuthorizationInvalid
	}
	// 컨트랙트의 FeeMismatch bound를 서명 전에 그대로 재현한다: 요율 + 반올림 슬랙.
	bound := new(big.Int).Mul(passThrough, big.NewInt(int64(a.FeeBps)))
	bound.Div(bound, big.NewInt(10_000))
	bound.Add(bound, big.NewInt(FeeRoundingSlackBaseUnits))
	if fee.Cmp(bound) > 0 {
		return ErrAuthorizationInvalid
	}
	if uint64(now.Unix()) > a.PayDeadline {
		return ErrAuthorizationExpired
	}
	return nil
}

// TotalAmount는 payer가 지불하는 총액(passThrough + fee) base units다.
func (a PaymentAuthorization) TotalAmount() (string, error) {
	passThrough, passOK := new(big.Int).SetString(a.PassThroughAmount, 10)
	fee, feeOK := new(big.Int).SetString(a.FeeAmount, 10)
	if !passOK || !feeOK {
		return "", ErrAuthorizationInvalid
	}
	return new(big.Int).Add(passThrough, fee).String(), nil
}

type AuthorizationRecord struct {
	ID            string               `json:"id"`
	AgencyOrderID string               `json:"agencyOrderId"`
	Authorization PaymentAuthorization `json:"authorization"`
	Domain        AuthorizationDomain  `json:"domain"`
	Signer        string               `json:"signer"`
	TypedDataHash string               `json:"typedDataHash"`
	Signature     string               `json:"signature"`
	CreatedAt     time.Time            `json:"createdAt"`
}

type AuthorizationDomain struct {
	Name              string `json:"name"`
	Version           string `json:"version"`
	ChainID           uint64 `json:"chainId"`
	VerifyingContract string `json:"verifyingContract"`
}

type Payment struct {
	ID                     string        `json:"id"`
	AgencyOrderID          string        `json:"agencyOrderId"`
	OrderHash              string        `json:"orderHash"`
	ChainID                uint64        `json:"chainId"`
	Settlement             string        `json:"settlementAddress"`
	Payer                  string        `json:"payer"`
	AmountBaseUnits        string        `json:"amountBaseUnits"`
	ClaimTxHash            string        `json:"claimTxHash,omitempty"`
	ApproveTxHash          string        `json:"approveTxHash,omitempty"`
	PayTxHash              string        `json:"payTxHash,omitempty"`
	CompleteTxHash         string        `json:"completeTxHash,omitempty"`
	RefundTxHash           string        `json:"refundTxHash,omitempty"`
	State                  PaymentStatus `json:"state"`
	SafeBlock              *uint64       `json:"safeBlock,omitempty"`
	FinalizedBlock         *uint64       `json:"finalizedBlock,omitempty"`
	LastReasonCode         string        `json:"lastReasonCode,omitempty"`
	ObservationExhaustedAt *time.Time    `json:"observationExhaustedAt,omitempty"`
	CreatedAt              time.Time     `json:"createdAt"`
	UpdatedAt              time.Time     `json:"updatedAt"`
}

type RefundIntent struct {
	AgencyOrderID   string `json:"agencyOrderId"`
	ChainID         uint64 `json:"chainId"`
	OrderHash       string `json:"orderHash"`
	Settlement      string `json:"settlementAddress"`
	Payer           string `json:"payer"`
	AmountBaseUnits string `json:"amountBaseUnits"`
	// self-refund escape hatch가 반환하는 몫은 escrow의 pass-through뿐이다.
	// 이미 이체된 fee는 refunder의 refundPartial 경로로 반환된다(ADR-0050).
	PassThroughBaseUnits string    `json:"passThroughBaseUnits,omitempty"`
	FeeBaseUnits         string    `json:"feeBaseUnits,omitempty"`
	RefundAfter          time.Time `json:"refundAfter"`
	Eligible             bool      `json:"eligible"`
}

func isAddress(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 42 || !strings.HasPrefix(value, "0x") {
		return false
	}
	_, ok := new(big.Int).SetString(value[2:], 16)
	return ok
}

func isHash(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 66 || !strings.HasPrefix(value, "0x") {
		return false
	}
	_, ok := new(big.Int).SetString(value[2:], 16)
	return ok
}
