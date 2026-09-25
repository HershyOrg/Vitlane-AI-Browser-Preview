package app

import (
	"context"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

// MOCompensationRequest is the rail-neutral command boundary. The amount is
// intentionally absent: Payment always returns the immutable allocation gross.
type MOCompensationRequest struct {
	AgencyOrderID   string
	MerchantOrderID string
	AllocationID    string
	Cause           domain.MOCompensationCause
	IdempotencyKey  string
}

type MOCompensationExecution struct {
	Compensation            domain.MOCompensation
	FundingState            domain.MOFundingState
	ProviderAuthorizationID string
	MOCashReceiptID         string
	ProviderCaptureID       string
	ProviderVoidRequired    bool
	OperationID             string
	OperationState          domain.OperationState
	OperationIdempotencyKey string
	OperationResourceID     string
}

type MOCompensationResult struct {
	Compensation domain.MOCompensation `json:"compensation"`
	Replay       bool                  `json:"replay"`
}

type moCompensationRepository interface {
	PrepareMOCompensation(
		context.Context, MOCompensationRequest, string, string, time.Time,
	) (MOCompensationExecution, bool, error)
	MarkMOCompensationSent(context.Context, MOCompensationExecution, time.Time, time.Time) error
	RecordMOCompensationOutcome(
		context.Context, MOCompensationExecution, domain.MOCompensationState,
		domain.OperationState, string, string, time.Time,
	) (domain.MOCompensation, error)
}

type moCompensationEffectLocker interface {
	WithMOCompensationEffectLock(
		context.Context, MOCompensationExecution, func(context.Context) error,
	) error
}
