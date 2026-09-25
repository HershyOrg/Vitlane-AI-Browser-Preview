package app

import (
	"context"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/infra/paypal"
)

type MOFundingActivation struct {
	MerchantOrderID         string
	PositionID              string
	AllocationID            string
	AgencyOrderID           string
	CustomerPaymentID       string
	AuthorizationID         string
	ProviderAuthorizationID string
	AuthorizationState      domain.PayPalAuthorizationState
	PayPalOrderID           string
	Rail                    string
	ProviderEnvironment     string
	AmountMinor             int64
	Currency                string
	ExecutionProfileHash    string
	State                   domain.MOFundingState
	OperationID             string
	OperationState          domain.OperationState
	OperationIdempotencyKey string
	OperationRequestHash    string
	ProviderCaptureID       string
	InvoiceID               string
	FinalCapture            bool
}

type MOFundingActivationResult struct {
	Position domain.MOFundingPosition `json:"position"`
	Receipt  *domain.MOCashReceipt    `json:"receipt,omitempty"`
	Replay   bool                     `json:"replay"`
}

type moFundingRepository interface {
	PrepareMOFundingActivation(
		context.Context, string, domain.ExternalOperation, time.Time,
	) (MOFundingActivation, bool, error)
	MarkMOFundingActivationSent(
		context.Context, string, string, time.Time, time.Time,
	) error
	RecordMOFundingActivationCompleted(
		context.Context, MOFundingActivation, domain.MOCashReceipt, time.Time,
	) (domain.MOFundingPosition, bool, error)
	RecordMOFundingActivationOutcome(
		context.Context, MOFundingActivation, domain.OperationState,
		domain.MOFundingState, string, time.Time,
	) (domain.MOFundingPosition, error)
	GetMOFundingActivation(
		context.Context, string,
	) (MOFundingActivation, *domain.MOCashReceipt, error)
}

func (s *Service) adoptMOFundingCapture(
	ctx context.Context,
	repository moFundingRepository,
	activation MOFundingActivation,
	capture paypal.Capture,
	replay bool,
) (MOFundingActivationResult, error) {
	now := s.clock.Now()
	activation.ProviderCaptureID = capture.ID
	if capture.ID == "" || capture.AmountMinor != activation.AmountMinor ||
		capture.Currency != activation.Currency || capture.InvoiceID != activation.InvoiceID ||
		capture.FinalCapture != activation.FinalCapture {
		position, err := repository.RecordMOFundingActivationOutcome(
			ctx, activation, domain.OperationUnknown, domain.MOFundingActivationUnknown,
			"CAPTURE_NONCONFORMING", now,
		)
		if err != nil {
			return MOFundingActivationResult{}, err
		}
		return MOFundingActivationResult{Position: position, Replay: replay},
			domain.ErrInstructionMismatch
	}
	switch capture.Status {
	case "PENDING":
		position, err := repository.RecordMOFundingActivationOutcome(
			ctx, activation, domain.OperationSent, domain.MOFundingActivationPending,
			"CAPTURE_PENDING", now,
		)
		return MOFundingActivationResult{Position: position, Replay: replay}, err
	case "DECLINED", "FAILED":
		position, err := repository.RecordMOFundingActivationOutcome(
			ctx, activation, domain.OperationFailed, domain.MOFundingFailed,
			"CAPTURE_"+capture.Status, now,
		)
		if err != nil {
			return MOFundingActivationResult{}, err
		}
		return MOFundingActivationResult{Position: position, Replay: replay},
			domain.ErrFundingNotAvailable
	case "COMPLETED":
		// Continue to the immutable cash receipt below.
	default:
		// REFUNDED/PARTIALLY_REFUNDED and unknown future statuses describe
		// money movement that cannot safely be inferred into a fresh receipt.
		position, err := repository.RecordMOFundingActivationOutcome(
			ctx, activation, domain.OperationUnknown, domain.MOFundingActivationUnknown,
			"CAPTURE_STATUS_REQUIRES_RECONCILIATION", now,
		)
		if err != nil {
			return MOFundingActivationResult{}, err
		}
		return MOFundingActivationResult{Position: position, Replay: replay},
			domain.ErrFundingOutcomeUnknown
	}
	occurredAt := now
	if strings.TrimSpace(capture.CreateTime) != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, capture.CreateTime); err == nil {
			occurredAt = parsed.UTC()
		}
	}
	receipt := domain.MOCashReceipt{
		ID: s.ids.NewID(), FundingPositionID: activation.PositionID,
		AllocationID: activation.AllocationID, AgencyOrderID: activation.AgencyOrderID,
		CustomerPaymentID:     activation.CustomerPaymentID,
		PayPalAuthorizationID: activation.AuthorizationID,
		ProviderEnvironment:   activation.ProviderEnvironment,
		ProviderCaptureID:     capture.ID, GrossMinor: capture.AmountMinor,
		EconomicsReconciled: capture.EconomicsReconciled,
		ProcessorFeeMinor:   capture.ProcessorFeeMinor,
		NetReceivableMinor:  capture.NetReceivableMinor,
		Currency:            capture.Currency, OccurredAt: occurredAt, CreatedAt: now,
	}
	position, applied, err := repository.RecordMOFundingActivationCompleted(
		ctx, activation, receipt, now,
	)
	if err != nil {
		return MOFundingActivationResult{}, err
	}
	if !applied {
		_, persisted, getErr := repository.GetMOFundingActivation(
			ctx, activation.MerchantOrderID,
		)
		if getErr != nil {
			return MOFundingActivationResult{}, getErr
		}
		return MOFundingActivationResult{
			Position: position, Receipt: persisted, Replay: true,
		}, nil
	}
	return MOFundingActivationResult{
		Position: position, Receipt: &receipt, Replay: replay || !applied,
	}, nil
}

func fundingPositionFromActivation(value MOFundingActivation) domain.MOFundingPosition {
	return domain.MOFundingPosition{
		ID: value.PositionID, AllocationID: value.AllocationID,
		AgencyOrderID: value.AgencyOrderID, CustomerPaymentID: value.CustomerPaymentID,
		PayPalAuthorizationID: value.AuthorizationID,
		Rail:                  value.Rail, Source: fundingSource(value.Rail),
		ProviderEnvironment: value.ProviderEnvironment,
		AmountMinor:         value.AmountMinor, Currency: value.Currency, State: value.State,
	}
}

func fundingSource(rail string) string {
	if rail == "PAYPAL" {
		return "PAYPAL_AUTHORIZATION"
	}
	return "GIWA_PREPAID"
}
