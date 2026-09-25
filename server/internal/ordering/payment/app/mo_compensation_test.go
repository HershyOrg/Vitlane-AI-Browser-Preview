package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

type fakeMOCompensationRepository struct {
	*fakeRepository
	execution      MOCompensationExecution
	prepared       int
	markedSent     int
	recorded       int
	recordedState  domain.MOCompensationState
	recordedOp     domain.OperationState
	recordedReason string
	generation     int
}

func (r *fakeMOCompensationRepository) PrepareMOCompensation(
	_ context.Context,
	request MOCompensationRequest,
	compensationID, operationID string,
	now time.Time,
) (MOCompensationExecution, bool, error) {
	r.prepared++
	if r.execution.Compensation.State == domain.MOCompensationFailed &&
		r.execution.Compensation.Action == domain.MOCompensationRefund {
		r.generation++
		r.execution.Compensation.State = domain.MOCompensationApproved
		r.execution.Compensation.ProviderResourceID = ""
		r.execution.OperationID = operationID
		r.execution.OperationState = domain.OperationPrepared
		r.execution.OperationResourceID = ""
		r.execution.OperationIdempotencyKey = fmt.Sprintf(
			"paypal:mo-refund:%s:%d", r.execution.Compensation.ID, r.generation,
		)
		return r.execution, true, nil
	}
	if r.execution.Compensation.ID == "" {
		r.generation = 1
		r.execution.Compensation.ID = compensationID
		r.execution.Compensation.AllocationID = request.AllocationID
		r.execution.Compensation.AgencyOrderID = request.AgencyOrderID
		r.execution.Compensation.Cause = request.Cause
		r.execution.Compensation.IdempotencyKey = request.IdempotencyKey
		r.execution.Compensation.State = domain.MOCompensationApproved
		r.execution.Compensation.ApprovedAt = now
		r.execution.OperationID = operationID
		r.execution.OperationState = domain.OperationPrepared
		if r.execution.Compensation.Action == domain.MOCompensationRefund {
			r.execution.OperationIdempotencyKey =
				"paypal:mo-refund:" + compensationID + ":1"
		} else if r.execution.ProviderVoidRequired {
			r.execution.OperationIdempotencyKey =
				"paypal:auth-void:" + compensationID + ":1"
		}
		return r.execution, false, nil
	}
	return r.execution, true, nil
}

func (r *fakeMOCompensationRepository) MarkMOCompensationSent(
	_ context.Context,
	execution MOCompensationExecution,
	_, _ time.Time,
) error {
	if r.execution.OperationState != domain.OperationPrepared &&
		r.execution.OperationState != domain.OperationUnknown {
		return domain.ErrConflict
	}
	if r.execution.OperationResourceID != "" {
		return domain.ErrConflict
	}
	r.markedSent++
	r.execution = execution
	r.execution.OperationState = domain.OperationSent
	r.execution.Compensation.State = domain.MOCompensationExecutionPending
	return nil
}

func (r *fakeMOCompensationRepository) RecordMOCompensationOutcome(
	_ context.Context,
	execution MOCompensationExecution,
	state domain.MOCompensationState,
	operationState domain.OperationState,
	providerResourceID, reason string,
	_ time.Time,
) (domain.MOCompensation, error) {
	r.recorded++
	r.recordedState = state
	r.recordedOp = operationState
	r.recordedReason = reason
	r.execution = execution
	r.execution.Compensation.State = state
	r.execution.Compensation.ProviderResourceID = providerResourceID
	r.execution.OperationState = operationState
	r.execution.OperationResourceID = providerResourceID
	return r.execution.Compensation, nil
}

func newMOCompensationService(
	repository *fakeMOCompensationRepository,
	provider *fakeProvider,
) *Service {
	return NewService(
		repository, provider, &fakeInstructionGate{}, directTransactor{}, Config{
			Environment: "SANDBOX", WebhookID: "WH-1",
			PublicBaseURL: "https://app.vitlane.test",
		}, &fakeClock{now: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)},
		&fakeIDs{},
	)
}

func compensationRequest() MOCompensationRequest {
	return MOCompensationRequest{
		AgencyOrderID: "order-1", MerchantOrderID: "mo-1",
		AllocationID:   "allocation-1",
		Cause:          domain.MOCompensationCustomerCancelPreEffect,
		IdempotencyKey: "payment.execute_mo_compensation.v1:order-1:mo-1",
	}
}

func TestPayPalPreEffectCompensationReleasesMOWithoutPrematureAuthorizationVoid(
	t *testing.T,
) {
	repository := &fakeMOCompensationRepository{
		fakeRepository: newFakeRepository(),
		execution: MOCompensationExecution{
			Compensation: domain.MOCompensation{
				FundingPositionID: "position-1", Rail: "PAYPAL",
				ProviderEnvironment: "SANDBOX", Action: domain.MOCompensationVoid,
				AmountMinor: 5523, Currency: "USD",
			},
			ProviderAuthorizationID: "PP-AUTH-1", ProviderVoidRequired: false,
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1", authorizationStatus: "CREATED"}
	service := newMOCompensationService(repository, provider)

	result, err := service.runCompensationEffectContract(context.Background(), compensationRequest())
	if err != nil {
		t.Fatalf("ExecuteMOCompensation: %v", err)
	}
	if result.Compensation.State != domain.MOCompensationSucceeded ||
		provider.voidCalls != 0 || repository.markedSent != 0 {
		t.Fatalf("logical release wrote provider: result=%+v void=%d sent=%d",
			result, provider.voidCalls, repository.markedSent)
	}
}

func TestPayPalLastPreEffectCompensationVoidsResidualAuthorization(t *testing.T) {
	repository := &fakeMOCompensationRepository{
		fakeRepository: newFakeRepository(),
		execution: MOCompensationExecution{
			Compensation: domain.MOCompensation{
				FundingPositionID: "position-1", Rail: "PAYPAL",
				ProviderEnvironment: "SANDBOX", Action: domain.MOCompensationVoid,
				AmountMinor: 5523, Currency: "USD",
			},
			ProviderAuthorizationID: "PP-AUTH-1", ProviderVoidRequired: true,
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1", authorizationStatus: "CREATED"}
	service := newMOCompensationService(repository, provider)

	result, err := service.runCompensationEffectContract(context.Background(), compensationRequest())
	if err != nil {
		t.Fatalf("ExecuteMOCompensation: %v", err)
	}
	if provider.voidCalls != 1 || repository.markedSent != 1 ||
		result.Compensation.State != domain.MOCompensationSucceeded {
		t.Fatalf("void flow mismatch: result=%+v void=%d sent=%d",
			result, provider.voidCalls, repository.markedSent)
	}
}

func TestPayPalUnknownResidualVoidReconcilesThenRetriesSameOperation(t *testing.T) {
	repository := &fakeMOCompensationRepository{
		fakeRepository: newFakeRepository(),
		execution: MOCompensationExecution{
			Compensation: domain.MOCompensation{
				ID: "compensation-void-1", FundingPositionID: "position-1", Rail: "PAYPAL",
				ProviderEnvironment: "SANDBOX", Action: domain.MOCompensationVoid,
				Cause:       domain.MOCompensationCustomerCancelPreEffect,
				State:       domain.MOCompensationOutcomeUnknown,
				AmountMinor: 5523, Currency: "USD",
				IdempotencyKey: compensationRequest().IdempotencyKey,
			},
			ProviderAuthorizationID: "PP-AUTH-1", ProviderVoidRequired: true,
			OperationID: "operation-void-1", OperationState: domain.OperationUnknown,
			OperationIdempotencyKey: "paypal:auth-void:compensation-void-1:1",
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1", authorizationStatus: "CREATED"}

	result, err := newMOCompensationService(repository, provider).
		runCompensationEffectContract(context.Background(), compensationRequest())
	if err != nil || provider.voidCalls != 1 || repository.markedSent != 1 ||
		result.Compensation.State != domain.MOCompensationSucceeded {
		t.Fatalf("unknown VOID did not recover with same operation: result=%+v void=%d sent=%d err=%v",
			result, provider.voidCalls, repository.markedSent, err)
	}
}

func TestPayPalResidualVoidAdoptsDeniedAuthorizationAsReleased(t *testing.T) {
	repository := &fakeMOCompensationRepository{
		fakeRepository: newFakeRepository(),
		execution: MOCompensationExecution{
			Compensation: domain.MOCompensation{
				ID: "compensation-denied-1", FundingPositionID: "position-1", Rail: "PAYPAL",
				ProviderEnvironment: "SANDBOX", Action: domain.MOCompensationVoid,
				Cause:       domain.MOCompensationCustomerCancelPreEffect,
				State:       domain.MOCompensationExecutionPending,
				AmountMinor: 5523, Currency: "USD",
				IdempotencyKey: compensationRequest().IdempotencyKey,
			},
			ProviderAuthorizationID: "PP-AUTH-1", ProviderVoidRequired: true,
			OperationID: "operation-denied-1", OperationState: domain.OperationSent,
			OperationIdempotencyKey: "paypal:auth-void:compensation-denied-1:1",
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1", authorizationStatus: "DENIED"}

	result, err := newMOCompensationService(repository, provider).
		runCompensationEffectContract(context.Background(), compensationRequest())
	if err != nil || provider.voidCalls != 0 ||
		repository.recordedReason != "AUTHORIZATION_DENIED" ||
		result.Compensation.State != domain.MOCompensationSucceeded {
		t.Fatalf("denied authorization did not close release: result=%+v void=%d reason=%q err=%v",
			result, provider.voidCalls, repository.recordedReason, err)
	}
}

func TestPayPalCapturedMOCompensationRefundsExactImmutableGross(t *testing.T) {
	repository := &fakeMOCompensationRepository{
		fakeRepository: newFakeRepository(),
		execution: MOCompensationExecution{
			Compensation: domain.MOCompensation{
				FundingPositionID: "position-1", Rail: "PAYPAL",
				ProviderEnvironment: "SANDBOX", Action: domain.MOCompensationRefund,
				AmountMinor: 5523, Currency: "USD",
			},
			ProviderCaptureID: "PP-CAPTURE-MO-1",
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1", refundStatus: "COMPLETED"}
	service := newMOCompensationService(repository, provider)
	request := compensationRequest()
	request.Cause = domain.MOCompensationCustomerRefundPostEffect

	result, err := service.runCompensationEffectContract(context.Background(), request)
	if err != nil {
		t.Fatalf("ExecuteMOCompensation: %v", err)
	}
	if provider.refundCalls != 1 || provider.refundMinor != 5523 ||
		result.Compensation.State != domain.MOCompensationSucceeded ||
		result.Compensation.ProviderResourceID != "PP-REFUND-1" {
		t.Fatalf("exact refund mismatch: result=%+v calls=%d amount=%d",
			result, provider.refundCalls, provider.refundMinor)
	}
}

func TestPayPalCapturedMOCompensationRejectsWrongRefundAttribution(t *testing.T) {
	for _, tc := range []struct {
		name             string
		parentMismatch   bool
		metadataMismatch bool
	}{
		{name: "wrong parent capture", parentMismatch: true},
		{name: "wrong operation metadata", metadataMismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &fakeMOCompensationRepository{
				fakeRepository: newFakeRepository(),
				execution: MOCompensationExecution{
					Compensation: domain.MOCompensation{
						FundingPositionID: "position-1", Rail: "PAYPAL",
						ProviderEnvironment: "SANDBOX", Action: domain.MOCompensationRefund,
						AmountMinor: 5523, Currency: "USD",
					},
					ProviderCaptureID: "PP-CAPTURE-MO-1",
				},
			}
			provider := &fakeProvider{
				payee: "MERCHANT-1", refundStatus: "COMPLETED",
				refundParentMismatch:   tc.parentMismatch,
				refundMetadataMismatch: tc.metadataMismatch,
			}
			request := compensationRequest()
			request.Cause = domain.MOCompensationCustomerRefundPostEffect

			result, err := newMOCompensationService(repository, provider).
				runCompensationEffectContract(context.Background(), request)
			if err != nil ||
				result.Compensation.State != domain.MOCompensationOutcomeUnknown ||
				repository.recordedReason != "REFUND_RESPONSE_MISMATCH" {
				t.Fatalf("wrong refund attribution was accepted: result=%+v reason=%q err=%v",
					result, repository.recordedReason, err)
			}
		})
	}
}

func TestPayPalCapturedMOCompensationDoesNotDuplicateInFlightRefund(t *testing.T) {
	repository := &fakeMOCompensationRepository{
		fakeRepository: newFakeRepository(),
		execution: MOCompensationExecution{
			Compensation: domain.MOCompensation{
				ID: "compensation-1", FundingPositionID: "position-1", Rail: "PAYPAL",
				ProviderEnvironment: "SANDBOX", Action: domain.MOCompensationRefund,
				Cause:       domain.MOCompensationCustomerRefundPostEffect,
				State:       domain.MOCompensationExecutionPending,
				AmountMinor: 5523, Currency: "USD",
				IdempotencyKey: compensationRequest().IdempotencyKey,
			},
			ProviderCaptureID: "PP-CAPTURE-MO-1",
			OperationID:       "operation-1", OperationState: domain.OperationSent,
			OperationIdempotencyKey: "paypal:mo-refund:compensation-1:1",
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1", refundStatus: "COMPLETED"}
	request := compensationRequest()
	request.Cause = domain.MOCompensationCustomerRefundPostEffect

	result, err := newMOCompensationService(repository, provider).
		runCompensationEffectContract(context.Background(), request)
	if err != nil ||
		result.Compensation.State != domain.MOCompensationOutcomeUnknown || provider.refundCalls != 0 || repository.markedSent != 0 {
		t.Fatalf("in-flight refund duplicated: result=%+v calls=%d sent=%d err=%v",
			result, provider.refundCalls, repository.markedSent, err)
	}
}

func TestPayPalKillSwitchStillAllowsExistingMORefund(t *testing.T) {
	repository := &fakeMOCompensationRepository{
		fakeRepository: newFakeRepository(),
		execution: MOCompensationExecution{
			Compensation: domain.MOCompensation{
				FundingPositionID: "position-1", Rail: "PAYPAL",
				ProviderEnvironment: "SANDBOX", Action: domain.MOCompensationRefund,
				AmountMinor: 5523, Currency: "USD",
			},
			ProviderCaptureID: "PP-CAPTURE-MO-1",
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1", refundStatus: "COMPLETED"}
	registry, err := NewProviderRegistry(ProviderRegistration{
		Environment: "SANDBOX", Client: provider, WebhookID: "WH-1",
		IssueEnabled: false, CaptureEnabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithProviderRegistry(
		repository, registry, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", PublicBaseURL: "https://app.vitlane.test"},
		&fakeClock{now: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)},
		&fakeIDs{},
	)
	request := compensationRequest()
	request.Cause = domain.MOCompensationCustomerRefundPostEffect

	result, err := service.runCompensationEffectContract(context.Background(), request)
	if err != nil || provider.refundCalls != 1 ||
		result.Compensation.State != domain.MOCompensationSucceeded {
		t.Fatalf("kill switch blocked historical refund: result=%+v calls=%d err=%v",
			result, provider.refundCalls, err)
	}
}

func TestPayPalRefundPendingRemainsUnknownAndBlocksTerminal(t *testing.T) {
	repository := &fakeMOCompensationRepository{
		fakeRepository: newFakeRepository(),
		execution: MOCompensationExecution{
			Compensation: domain.MOCompensation{
				FundingPositionID: "position-1", Rail: "PAYPAL",
				ProviderEnvironment: "SANDBOX", Action: domain.MOCompensationRefund,
				AmountMinor: 5523, Currency: "USD",
			},
			ProviderCaptureID: "PP-CAPTURE-MO-1",
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1", refundStatus: "PENDING"}
	service := newMOCompensationService(repository, provider)
	request := compensationRequest()
	request.Cause = domain.MOCompensationDeliveryException

	result, err := service.runCompensationEffectContract(context.Background(), request)
	if err != nil ||
		result.Compensation.State != domain.MOCompensationOutcomeUnknown {
		t.Fatalf("pending refund result=%+v err=%v", result, err)
	}
}

func TestPayPalTerminalRefundFailureRequiresFreshExplicitAttempt(t *testing.T) {
	repository := &fakeMOCompensationRepository{
		fakeRepository: newFakeRepository(),
		execution: MOCompensationExecution{
			Compensation: domain.MOCompensation{
				FundingPositionID: "position-1", Rail: "PAYPAL",
				ProviderEnvironment: "SANDBOX", Action: domain.MOCompensationRefund,
				AmountMinor: 5523, Currency: "USD",
			},
			ProviderCaptureID: "PP-CAPTURE-MO-1",
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1", refundStatus: "FAILED"}
	service := newMOCompensationService(repository, provider)
	request := compensationRequest()
	request.Cause = domain.MOCompensationCustomerRefundPostEffect

	failed, err := service.runCompensationEffectContract(context.Background(), request)
	if err != nil ||
		failed.Compensation.State != domain.MOCompensationFailed {
		t.Fatalf("terminal refund was not closed: result=%+v err=%v", failed, err)
	}
	provider.refundStatus = "COMPLETED"
	succeeded, err := service.runCompensationEffectContract(context.Background(), request)
	if err != nil || succeeded.Compensation.State != domain.MOCompensationSucceeded {
		t.Fatalf("explicit refund retry failed: result=%+v err=%v", succeeded, err)
	}
	if len(provider.refundRequestIDs) != 2 ||
		provider.refundRequestIDs[0] == provider.refundRequestIDs[1] {
		t.Fatalf("refund attempt reused PayPal-Request-Id: %v", provider.refundRequestIDs)
	}
	if succeeded.Compensation.ProviderResourceID != "PP-REFUND-2" {
		t.Fatalf("fresh refund resource was not adopted: %+v", succeeded.Compensation)
	}
}

func TestTVitCompensationStopsAtDurableWorkerBoundary(t *testing.T) {
	repository := &fakeMOCompensationRepository{
		fakeRepository: newFakeRepository(),
		execution: MOCompensationExecution{Compensation: domain.MOCompensation{
			FundingPositionID: "position-1", Rail: "GIWA",
			ProviderEnvironment: "TESTNET", Action: domain.MOCompensationTVitRefund,
			AmountMinor: 5050, Currency: "USD",
		}},
	}
	provider := &fakeProvider{payee: "MERCHANT-1"}
	result, err := newMOCompensationService(repository, provider).
		runCompensationEffectContract(context.Background(), compensationRequest())
	if err != nil ||
		result.Compensation.State != domain.MOCompensationApproved ||
		repository.markedSent != 0 || provider.refundCalls != 0 {
		t.Fatalf("GIWA worker boundary result=%+v err=%v", result, err)
	}
}
