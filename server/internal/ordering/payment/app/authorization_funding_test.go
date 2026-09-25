package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/infra/paypal"
)

func TestApprovedCheckoutAuthorizesWithoutCapturing(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1"}
	consumer := &fakeInstructionGate{}
	service := newTestServiceWithInstructionGate(repository, provider, consumer)

	started, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	provider.orderStatus = "APPROVED"

	resumed, err := service.Resume(context.Background(), ResumeInput{
		UserID: "user-1", AgencyOrderID: "order-1", ReturnNonce: started.ReturnNonce,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Payment.State != domain.PaymentAuthorized ||
		resumed.Attempt.State != domain.AttemptAuthorizeCompleted {
		t.Fatalf("authorization was not adopted: %+v", resumed)
	}
	if provider.authorizeCalls != 1 {
		t.Fatalf("authorize calls=%d want=1", provider.authorizeCalls)
	}
	if provider.captureCalls != 0 || len(provider.captureAuthorizationInputs) != 0 ||
		len(repository.receipts) != 0 {
		t.Fatalf("approval captured funds: whole-order=%d MO=%d receipts=%d",
			provider.captureCalls, len(provider.captureAuthorizationInputs),
			len(repository.receipts))
	}
	if repository.authorization == nil ||
		repository.authorization.PayPalAuthorizationID != "PP-AUTH-1" {
		t.Fatalf("authorization fact missing: %+v", repository.authorization)
	}
	if consumer.calls != 1 {
		t.Fatalf("instruction consume calls=%d want=1", consumer.calls)
	}

	if _, err := service.Resume(context.Background(), ResumeInput{
		UserID: "user-1", AgencyOrderID: "order-1", ReturnNonce: started.ReturnNonce,
	}); err != nil {
		t.Fatalf("Resume replay: %v", err)
	}
	if provider.authorizeCalls != 1 || consumer.calls != 1 {
		t.Fatalf("authorization replay wrote again: authorize=%d consume=%d",
			provider.authorizeCalls, consumer.calls)
	}
}

func TestUnknownAuthorizationRetriesSameProviderRequestAfterAbsentDiscovery(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{
		payee: "MERCHANT-1", authorizeErr: paypal.ErrOutcomeUnknown,
	}
	service := newTestService(repository, provider)
	started, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatal(err)
	}
	provider.orderStatus = "APPROVED"
	unknown, err := service.Resume(context.Background(), ResumeInput{
		UserID: "user-1", AgencyOrderID: "order-1", ReturnNonce: started.ReturnNonce,
	})
	if err != nil || unknown.Payment.State != domain.PaymentOutcomeUnknown ||
		unknown.Attempt.State != domain.AttemptAuthorizeOutcomeUnknown {
		t.Fatalf("first ambiguous authorization=%+v err=%v", unknown, err)
	}

	provider.authorizeErr = nil
	recovered, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil || recovered.Payment.State != domain.PaymentAuthorized {
		t.Fatalf("authorization recovery=%+v err=%v", recovered, err)
	}
	if provider.authorizeCalls != 2 || len(provider.authorizeRequestIDs) != 2 ||
		provider.authorizeRequestIDs[0] != provider.authorizeRequestIDs[1] {
		t.Fatalf("authorization recovery changed provider key: %v",
			provider.authorizeRequestIDs)
	}
}

func TestSubmittedAuthorizationWithRecoveredSenderClaimRetriesSameProviderRequest(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1"}
	service := newTestService(repository, provider)
	started, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatal(err)
	}
	provider.orderStatus = "APPROVED"
	key := "paypal:authorize:" + started.Attempt.ID + ":1"
	operation := service.newOperation(
		domain.OperationPayPalAuthorize, "PAYPAL_ATTEMPT", started.Attempt.ID,
		key, started.Payment.AmountMinor,
	)
	operation.ID = "authorize-operation-recovered"
	operation.State = domain.OperationUnknown
	repository.operations[key] = &operation
	repository.attempt.State = domain.AttemptAuthorizeSubmitted
	repository.payment.State = domain.PaymentProcessing

	recovered, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil || recovered.Payment.State != domain.PaymentAuthorized {
		t.Fatalf("stale submitted authorization recovery=%+v err=%v", recovered, err)
	}
	if provider.authorizeCalls != 1 || len(provider.authorizeRequestIDs) != 1 ||
		provider.authorizeRequestIDs[0] != key {
		t.Fatalf("recovered sender changed provider identity: calls=%d keys=%v",
			provider.authorizeCalls, provider.authorizeRequestIDs)
	}
}

func TestAuthorizationAdoptionQuarantinesUnexpectedPriorCapture(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1"}
	service := newTestService(repository, provider)
	started, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	provider.orderStatus = "COMPLETED"
	provider.authorizationStatus = "PARTIALLY_CAPTURED"

	view, err := service.Resume(context.Background(), ResumeInput{
		UserID: "user-1", AgencyOrderID: "order-1", ReturnNonce: started.ReturnNonce,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if view.Payment.State != domain.PaymentOutcomeUnknown ||
		view.Attempt.State != domain.AttemptAuthorizeOutcomeUnknown ||
		repository.authorization != nil {
		t.Fatalf("unexpected capture was adopted: view=%+v authorization=%+v",
			view, repository.authorization)
	}
	if len(repository.receipts) != 0 || len(provider.captureAuthorizationInputs) != 0 {
		t.Fatalf("unexpected prior capture opened MO effects: receipts=%d captures=%d",
			len(repository.receipts), len(provider.captureAuthorizationInputs))
	}
}

func TestAuthorizationAdoptionRollsBackWhenInstructionCannotBeConsumed(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1"}
	consumer := &fakeInstructionGate{err: errors.New("instruction expired")}
	service := newTestServiceWithDependencies(
		repository, provider, consumer, repositoryTransactor{repository: repository},
	)
	started, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	provider.orderStatus = "APPROVED"

	_, err = service.Resume(context.Background(), ResumeInput{
		UserID: "user-1", AgencyOrderID: "order-1", ReturnNonce: started.ReturnNonce,
	})
	if !errors.Is(err, domain.ErrInstructionNotConsumable) {
		t.Fatalf("Resume error=%v want=%v", err, domain.ErrInstructionNotConsumable)
	}
	if repository.authorization != nil ||
		repository.payment.State == domain.PaymentAuthorized ||
		repository.attempt.State == domain.AttemptAuthorizeCompleted {
		t.Fatalf("authorization escaped transaction rollback: payment=%s attempt=%s auth=%+v",
			repository.payment.State, repository.attempt.State, repository.authorization)
	}
	if provider.authorizeCalls != 1 || provider.captureCalls != 0 {
		t.Fatalf("unexpected provider writes: authorize=%d capture=%d",
			provider.authorizeCalls, provider.captureCalls)
	}
}

type fakeMOFundingRepository struct {
	*fakeRepository
	activation        MOFundingActivation
	receipt           *domain.MOCashReceipt
	outcomeStates     []domain.OperationState
	outcomeCaptureIDs []string
}

type fakeMOReauthorizationFundingRepository struct {
	*fakeMOFundingRepository
	plan                MOReauthorizationPlan
	recordedStates      []domain.OperationState
	recordedResourceIDs []string
}

func (r *fakeMOReauthorizationFundingRepository) PrepareMOReauthorization(
	_ context.Context, merchantOrderID, operationID string, _ time.Time,
) (MOReauthorizationPlan, error) {
	if merchantOrderID != r.activation.MerchantOrderID {
		return MOReauthorizationPlan{}, domain.ErrFundingNotAvailable
	}
	if !r.plan.Required {
		return r.plan, nil
	}
	if r.plan.PayPalOrderID == "" {
		r.plan.PayPalOrderID = r.activation.PayPalOrderID
		if r.plan.PayPalOrderID == "" {
			r.plan.PayPalOrderID = "PP-ORDER-1"
		}
	}
	if r.plan.PayeeMerchantID == "" {
		r.plan.PayeeMerchantID = "MERCHANT-1"
	}
	if r.plan.OperationID == "" {
		r.plan.OperationID = operationID
		r.plan.OperationState = domain.OperationPrepared
	}
	return r.plan, nil
}

func (r *fakeMOReauthorizationFundingRepository) MarkMOReauthorizationSent(
	_ context.Context, plan MOReauthorizationPlan, _, _ time.Time,
) error {
	if plan.OperationID != r.plan.OperationID {
		return domain.ErrConflict
	}
	r.plan.OperationState = domain.OperationSent
	return nil
}

func (r *fakeMOReauthorizationFundingRepository) RecordMOReauthorizationOutcome(
	_ context.Context,
	plan MOReauthorizationPlan,
	state domain.OperationState,
	providerAuthorizationID string,
	amountMinor int64,
	currency string,
	_ time.Time,
	_ string,
	_ time.Time,
) error {
	if plan.OperationID != r.plan.OperationID {
		return domain.ErrConflict
	}
	r.recordedStates = append(r.recordedStates, state)
	r.recordedResourceIDs = append(r.recordedResourceIDs, providerAuthorizationID)
	if state == domain.OperationSucceeded &&
		(amountMinor != plan.RemainingCapturableMinor || currency != plan.Currency) {
		return domain.ErrInstructionMismatch
	}
	r.plan.OperationState = state
	r.plan.OperationResourceID = providerAuthorizationID
	if state == domain.OperationSucceeded {
		r.plan.Required = false
		r.activation.ProviderAuthorizationID = providerAuthorizationID
	} else if state == domain.OperationFailed {
		r.activation.State = domain.MOFundingFailed
	}
	return nil
}

func (r *fakeMOFundingRepository) PrepareMOFundingActivation(
	_ context.Context,
	merchantOrderID string,
	operation domain.ExternalOperation,
	_ time.Time,
) (MOFundingActivation, bool, error) {
	if merchantOrderID != r.activation.MerchantOrderID {
		return MOFundingActivation{}, false, domain.ErrFundingNotAvailable
	}
	if r.activation.State == domain.MOFundingActive {
		return r.activation, true, nil
	}
	if r.activation.State != domain.MOFundingAvailable {
		return r.activation, true, nil
	}
	if r.activation.Rail == "GIWA" {
		r.activation.State = domain.MOFundingActive
		return r.activation, false, nil
	}
	r.activation.State = domain.MOFundingActivationPending
	r.activation.OperationID = operation.ID
	r.activation.OperationState = domain.OperationPrepared
	r.activation.OperationIdempotencyKey =
		"paypal:mo-capture:" + r.activation.PositionID + ":v1"
	return r.activation, false, nil
}

func TestMerchantOrderFundingActivatesGIWALocallyWithEmptyProviderRegistry(t *testing.T) {
	repository := &fakeMOFundingRepository{
		fakeRepository: fixtureRepository(),
		activation: MOFundingActivation{
			MerchantOrderID: "mo-giwa", PositionID: "position-giwa",
			AllocationID: "allocation-giwa", AgencyOrderID: "order-giwa",
			CustomerPaymentID: "payment-giwa", Rail: "GIWA",
			ProviderEnvironment: "TESTNET", AmountMinor: 4237, Currency: "USD",
			State: domain.MOFundingAvailable,
		},
	}
	registry, err := NewProviderRegistry()
	if err != nil {
		t.Fatalf("empty provider registry: %v", err)
	}
	service := NewServiceWithProviderRegistry(
		repository, registry, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX"},
		&fakeClock{now: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)

	result, err := service.runFundingEffectContract(
		context.Background(), "mo-giwa", "procurement-effect-giwa",
	)
	if err != nil {
		t.Fatalf("ActivateMerchantOrderFunding: %v", err)
	}
	if result.Position.State != domain.MOFundingActive || result.Receipt != nil {
		t.Fatalf("GIWA local activation=%+v", result)
	}
	if repository.activation.OperationID != "" ||
		repository.activation.OperationState != "" {
		t.Fatalf("GIWA must not create a PayPal operation: %+v", repository.activation)
	}
}

func (r *fakeMOFundingRepository) MarkMOFundingActivationSent(
	_ context.Context,
	positionID, operationID string,
	_, _ time.Time,
) error {
	if positionID != r.activation.PositionID || operationID != r.activation.OperationID {
		return domain.ErrConflict
	}
	if r.activation.OperationState != domain.OperationPrepared &&
		r.activation.OperationState != domain.OperationUnknown {
		return domain.ErrConflict
	}
	r.activation.OperationState = domain.OperationSent
	return nil
}

func (r *fakeMOFundingRepository) RecordMOFundingActivationCompleted(
	_ context.Context,
	activation MOFundingActivation,
	receipt domain.MOCashReceipt,
	_ time.Time,
) (domain.MOFundingPosition, bool, error) {
	if r.receipt != nil {
		return fundingPositionFromActivation(r.activation), false, nil
	}
	if activation.PositionID != r.activation.PositionID ||
		receipt.GrossMinor != r.activation.AmountMinor {
		return domain.MOFundingPosition{}, false, domain.ErrInstructionMismatch
	}
	copy := receipt
	r.receipt = &copy
	r.activation.State = domain.MOFundingActive
	r.activation.ProviderCaptureID = receipt.ProviderCaptureID
	return fundingPositionFromActivation(r.activation), true, nil
}

func (r *fakeMOFundingRepository) RecordMOFundingActivationOutcome(
	_ context.Context,
	activation MOFundingActivation,
	operationState domain.OperationState,
	fundingState domain.MOFundingState,
	_ string,
	_ time.Time,
) (domain.MOFundingPosition, error) {
	r.outcomeStates = append(r.outcomeStates, operationState)
	r.outcomeCaptureIDs = append(r.outcomeCaptureIDs, activation.ProviderCaptureID)
	r.activation = activation
	r.activation.OperationState = operationState
	r.activation.State = fundingState
	return fundingPositionFromActivation(r.activation), nil
}

func TestMerchantOrderFundingCheckpointsNonconformingCaptureIdentityBeforeGET(t *testing.T) {
	base := fixtureRepository()
	repository := &fakeMOFundingRepository{
		fakeRepository: base,
		activation: MOFundingActivation{
			MerchantOrderID: "mo-checkpoint", PositionID: "position-checkpoint",
			AllocationID: "allocation-checkpoint", AgencyOrderID: "order-1",
			CustomerPaymentID: "payment-1", AuthorizationID: "authorization-1",
			ProviderAuthorizationID: "PP-AUTH-1", PayPalOrderID: "PP-ORDER-1",
			Rail: "PAYPAL", ProviderEnvironment: "SANDBOX",
			AmountMinor: 4237, Currency: "USD",
			ExecutionProfileHash: domain.PayPalSandboxExecutionProfileHash,
			State:                domain.MOFundingAvailable,
			InvoiceID:            "VIT-MO-allocation-checkpoint",
		},
	}
	provider := &fakeProvider{
		payee:                      "MERCHANT-1",
		captureAuthorizationResult: paypal.Capture{ID: "PP-CAPTURE-CHECKPOINT"},
		captureAuthorizationErr:    paypal.ErrNonconformingResponse,
		getCaptureResult: paypal.Capture{
			ID: "PP-CAPTURE-CHECKPOINT", Status: "COMPLETED",
			AmountMinor: 4237, Currency: "USD",
			InvoiceID: "VIT-MO-allocation-checkpoint",
		},
	}
	service := NewService(
		repository, provider, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", WebhookID: "WH-1"},
		&fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)

	result, err := service.runFundingEffectContract(
		context.Background(), "mo-checkpoint", "procurement-effect-checkpoint",
	)
	if err != nil || result.Position.State != domain.MOFundingActive ||
		result.Receipt == nil || result.Receipt.ProviderCaptureID != "PP-CAPTURE-CHECKPOINT" {
		t.Fatalf("known capture was not recovered: result=%+v err=%v", result, err)
	}
	if len(repository.outcomeStates) != 1 ||
		repository.outcomeStates[0] != domain.OperationUnknown ||
		repository.outcomeCaptureIDs[0] != "PP-CAPTURE-CHECKPOINT" {
		t.Fatalf("capture identity was not checkpointed before GET: states=%v ids=%v",
			repository.outcomeStates, repository.outcomeCaptureIDs)
	}
}

func (r *fakeMOFundingRepository) GetMOFundingActivation(
	_ context.Context,
	merchantOrderID string,
) (MOFundingActivation, *domain.MOCashReceipt, error) {
	if merchantOrderID != r.activation.MerchantOrderID {
		return MOFundingActivation{}, nil, domain.ErrFundingNotAvailable
	}
	return r.activation, r.receipt, nil
}

func TestMerchantOrderFundingCapturesExactAllocationAgainstAuthorization(t *testing.T) {
	base := fixtureRepository()
	repository := &fakeMOFundingRepository{
		fakeRepository: base,
		activation: MOFundingActivation{
			MerchantOrderID: "mo-1", PositionID: "position-1",
			AllocationID: "allocation-1", AgencyOrderID: "order-1",
			CustomerPaymentID: "payment-1", AuthorizationID: "authorization-1",
			ProviderAuthorizationID: "PP-AUTH-1", PayPalOrderID: "PP-ORDER-1",
			Rail: "PAYPAL", ProviderEnvironment: "SANDBOX",
			AmountMinor: 4237, Currency: "USD",
			ExecutionProfileHash: domain.PayPalSandboxExecutionProfileHash,
			State:                domain.MOFundingAvailable,
			InvoiceID:            "VIT-MO-allocation-1", FinalCapture: false,
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1"}
	service := NewService(
		repository, provider, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", WebhookID: "WH-1"},
		&fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)

	result, err := service.runFundingEffectContract(
		context.Background(), "mo-1", "procurement-effect-mo-1",
	)
	if err != nil {
		t.Fatalf("ActivateMerchantOrderFunding: %v", err)
	}
	if len(provider.captureAuthorizationInputs) != 1 {
		t.Fatalf("authorization captures=%d want=1", len(provider.captureAuthorizationInputs))
	}
	input := provider.captureAuthorizationInputs[0]
	if input.AuthorizationID != "PP-AUTH-1" || input.AmountMinor != 4237 ||
		input.Currency != "USD" || input.InvoiceID != "VIT-MO-allocation-1" ||
		input.FinalCapture {
		t.Fatalf("capture input is not exact MO allocation: %+v", input)
	}
	if provider.captureCalls != 0 {
		t.Fatalf("legacy whole-order capture called %d times", provider.captureCalls)
	}
	if result.Position.State != domain.MOFundingActive || result.Receipt == nil ||
		result.Receipt.GrossMinor != 4237 ||
		result.Receipt.ProviderCaptureID != "PP-MO-CAPTURE-1" {
		t.Fatalf("funding activation not adopted: %+v", result)
	}
}

func TestMerchantOrderFundingClosesDeclinedCapture(t *testing.T) {
	testMerchantOrderFundingClosesTerminalCapture(t, "DECLINED")
}

func TestMerchantOrderFundingClosesFailedCapture(t *testing.T) {
	testMerchantOrderFundingClosesTerminalCapture(t, "FAILED")
}

func testMerchantOrderFundingClosesTerminalCapture(t *testing.T, status string) {
	t.Helper()
	base := fixtureRepository()
	repository := &fakeMOFundingRepository{
		fakeRepository: base,
		activation: MOFundingActivation{
			MerchantOrderID: "mo-declined", PositionID: "position-declined",
			AllocationID: "allocation-declined", AgencyOrderID: "order-1",
			CustomerPaymentID: "payment-1", AuthorizationID: "authorization-1",
			ProviderAuthorizationID: "PP-AUTH-1", PayPalOrderID: "PP-ORDER-1",
			Rail: "PAYPAL", ProviderEnvironment: "SANDBOX",
			AmountMinor: 4237, Currency: "USD",
			ExecutionProfileHash: domain.PayPalSandboxExecutionProfileHash,
			State:                domain.MOFundingAvailable,
			InvoiceID:            "VIT-MO-allocation-declined",
		},
	}
	terminal := paypal.Capture{
		ID: "PP-MO-CAPTURE-" + status, Status: status,
		AmountMinor: 4237, Currency: "USD",
		InvoiceID: "VIT-MO-allocation-declined", FinalCapture: false,
	}
	provider := &fakeProvider{
		payee: "MERCHANT-1", captureAuthorizationResult: terminal,
		getCaptureResult: terminal,
	}
	service := NewService(
		repository, provider, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", WebhookID: "WH-1"},
		&fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)

	result, err := service.runFundingEffectContract(
		context.Background(), "mo-declined", "procurement-effect-mo-declined",
	)
	if err != nil ||
		result.Position.State != domain.MOFundingFailed || result.Receipt != nil {
		t.Fatalf("terminal %s capture did not close funding: result=%+v err=%v",
			status, result, err)
	}
}

func TestUnknownMOCaptureRetriesSameProviderRequestAfterAbsentDiscovery(t *testing.T) {
	base := fixtureRepository()
	repository := &fakeMOFundingRepository{
		fakeRepository: base,
		activation: MOFundingActivation{
			MerchantOrderID: "mo-retry", PositionID: "position-retry",
			AllocationID: "allocation-retry", AgencyOrderID: "order-1",
			CustomerPaymentID: "payment-1", AuthorizationID: "authorization-1",
			ProviderAuthorizationID: "PP-AUTH-1", PayPalOrderID: "PP-ORDER-1",
			Rail: "PAYPAL", ProviderEnvironment: "SANDBOX",
			AmountMinor: 4237, Currency: "USD",
			ExecutionProfileHash: domain.PayPalSandboxExecutionProfileHash,
			State:                domain.MOFundingAvailable,
			InvoiceID:            "VIT-MO-allocation-retry",
		},
	}
	provider := &fakeProvider{
		payee: "MERCHANT-1", captureAuthorizationErr: paypal.ErrOutcomeUnknown,
	}
	service := NewService(
		repository, provider, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", WebhookID: "WH-1"},
		&fakeClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)

	unknown, err := service.runFundingEffectContract(
		context.Background(), "mo-retry", "procurement-effect-mo-retry",
	)
	if err != nil || unknown.Position.State != domain.MOFundingActivationUnknown {
		t.Fatalf("first ambiguous capture=%+v err=%v", unknown, err)
	}
	provider.captureAuthorizationErr = nil
	recovered, err := service.runFundingEffectContract(
		context.Background(), "mo-retry", "procurement-effect-mo-retry",
	)
	if err != nil || recovered.Position.State != domain.MOFundingActive {
		t.Fatalf("capture recovery=%+v err=%v", recovered, err)
	}
	if len(provider.captureAuthorizationInputs) != 2 ||
		provider.captureAuthorizationInputs[0].RequestID !=
			provider.captureAuthorizationInputs[1].RequestID {
		t.Fatalf("capture recovery changed provider key: %+v",
			provider.captureAuthorizationInputs)
	}
}

func TestMerchantOrderFundingReauthorizesAfterHonorPeriodBeforeCapture(t *testing.T) {
	base := fixtureRepository()
	funding := &fakeMOFundingRepository{
		fakeRepository: base,
		activation: MOFundingActivation{
			MerchantOrderID: "mo-late", PositionID: "position-late",
			AllocationID: "allocation-late", AgencyOrderID: "order-1",
			CustomerPaymentID: "payment-1", AuthorizationID: "authorization-1",
			ProviderAuthorizationID: "PP-AUTH-1", PayPalOrderID: "PP-ORDER-1",
			Rail: "PAYPAL", ProviderEnvironment: "SANDBOX",
			AmountMinor: 4237, Currency: "USD",
			ExecutionProfileHash: domain.PayPalSandboxExecutionProfileHash,
			State:                domain.MOFundingAvailable,
			InvoiceID:            "VIT-MO-allocation-late",
		},
	}
	repository := &fakeMOReauthorizationFundingRepository{
		fakeMOFundingRepository: funding,
		plan: MOReauthorizationPlan{
			Required: true, AuthorizationID: "authorization-1",
			TargetPositionID:                "position-late",
			ProviderEnvironment:             "SANDBOX",
			PreviousProviderAuthorizationID: "PP-AUTH-1",
			AuthorizedAmountMinor:           10600,
			RemainingCapturableMinor:        6375, Currency: "USD",
			OperationIdempotencyKey: "paypal:reauthorize:authorization-1:1",
		},
	}
	provider := &fakeProvider{
		payee: "MERCHANT-1", authorizationStatus: "CREATED",
		authorizationCreateTime: "not-a-provider-time",
	}
	service := NewService(
		repository, provider, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", WebhookID: "WH-1"},
		&fakeClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)},
		&fakeIDs{},
	)

	result, err := service.runFundingEffectContract(
		context.Background(), "mo-late", "procurement-effect-mo-late",
	)
	if err != nil {
		t.Fatalf("ActivateMerchantOrderFunding: %v", err)
	}
	if provider.reauthorizeCalls != 1 || len(provider.captureAuthorizationInputs) != 1 {
		t.Fatalf("reauthorize=%d captures=%d", provider.reauthorizeCalls,
			len(provider.captureAuthorizationInputs))
	}
	if len(provider.reauthorizeInputs) != 1 ||
		provider.reauthorizeInputs[0].AuthorizationID != "PP-AUTH-1" ||
		provider.reauthorizeInputs[0].AmountMinor != 6375 ||
		provider.reauthorizeInputs[0].Currency != "USD" {
		t.Fatalf("reauthorization did not use exact remaining amount: %+v",
			provider.reauthorizeInputs)
	}
	if provider.captureAuthorizationInputs[0].AuthorizationID != "PP-AUTH-2" {
		t.Fatalf("capture did not use refreshed authorization: %+v",
			provider.captureAuthorizationInputs[0])
	}
	if result.Position.State != domain.MOFundingActive {
		t.Fatalf("late funding did not activate: %+v", result)
	}
	if len(repository.recordedStates) != 2 ||
		repository.recordedStates[0] != domain.OperationUnknown ||
		repository.recordedStates[1] != domain.OperationSucceeded {
		t.Fatalf("fresh provider ID was not checkpointed before GET: %+v",
			repository.recordedStates)
	}
	if len(repository.recordedResourceIDs) != 2 ||
		repository.recordedResourceIDs[0] != "PP-AUTH-2" ||
		repository.recordedResourceIDs[1] != "PP-AUTH-2" {
		t.Fatalf("fresh provider ID checkpoint mismatch: %+v",
			repository.recordedResourceIDs)
	}
}

func TestMerchantOrderFundingRejectsReauthorizationFromWrongOrderOrPayee(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wrongOrder bool
		wrongPayee bool
	}{
		{name: "wrong order", wrongOrder: true},
		{name: "wrong payee", wrongPayee: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := fixtureRepository()
			funding := &fakeMOFundingRepository{
				fakeRepository: base,
				activation: MOFundingActivation{
					MerchantOrderID: "mo-late", PositionID: "position-late",
					AllocationID: "allocation-late", AgencyOrderID: "order-1",
					ProviderAuthorizationID: "PP-AUTH-1", PayPalOrderID: "PP-ORDER-1",
					Rail: "PAYPAL", ProviderEnvironment: "SANDBOX",
					AmountMinor: 4237, Currency: "USD", State: domain.MOFundingAvailable,
				},
			}
			repository := &fakeMOReauthorizationFundingRepository{
				fakeMOFundingRepository: funding,
				plan: MOReauthorizationPlan{
					Required: true, AuthorizationID: "authorization-1",
					TargetPositionID: "position-late", ProviderEnvironment: "SANDBOX",
					PayPalOrderID: "PP-ORDER-1", PayeeMerchantID: "MERCHANT-1",
					PreviousProviderAuthorizationID: "PP-AUTH-1",
					AuthorizedAmountMinor:           10600, RemainingCapturableMinor: 6375,
					Currency: "USD", OperationIdempotencyKey: "paypal:reauthorize:authorization-1:1",
				},
			}
			provider := &fakeProvider{
				payee: "MERCHANT-1", authorizationStatus: "CREATED",
				authorizationParentMismatch: tc.wrongOrder,
				authorizationPayeeMismatch:  tc.wrongPayee,
			}
			service := NewService(
				repository, provider, &fakeInstructionGate{}, directTransactor{},
				Config{Environment: "SANDBOX", WebhookID: "WH-1"},
				&fakeClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)},
				&fakeIDs{},
			)

			_, err := service.runFundingEffectContract(
				context.Background(), "mo-late", "procurement-effect-mo-late",
			)
			if err != nil ||
				len(provider.captureAuthorizationInputs) != 0 ||
				repository.plan.OperationState != domain.OperationUnknown {
				t.Fatalf("wrong reauthorization identity was adopted: plan=%+v captures=%d err=%v",
					repository.plan, len(provider.captureAuthorizationInputs), err)
			}
		})
	}
}

func TestMerchantOrderFundingDoesNotRepostUnresolvedReauthorization(t *testing.T) {
	base := fixtureRepository()
	funding := &fakeMOFundingRepository{
		fakeRepository: base,
		activation: MOFundingActivation{
			MerchantOrderID: "mo-late", PositionID: "position-late",
			AllocationID: "allocation-late", AgencyOrderID: "order-1",
			Rail: "PAYPAL", ProviderEnvironment: "SANDBOX",
			State: domain.MOFundingAvailable, AmountMinor: 4237, Currency: "USD",
		},
	}
	repository := &fakeMOReauthorizationFundingRepository{
		fakeMOFundingRepository: funding,
		plan: MOReauthorizationPlan{
			Required: true, AuthorizationID: "authorization-1",
			TargetPositionID: "position-late", ProviderEnvironment: "SANDBOX",
			PreviousProviderAuthorizationID: "PP-AUTH-1",
			AuthorizedAmountMinor:           10600, RemainingCapturableMinor: 6375,
			Currency: "USD", OperationID: "operation-1",
			OperationState:          domain.OperationSent,
			OperationIdempotencyKey: "paypal:reauthorize:authorization-1:1",
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1", authorizationStatus: "CREATED"}
	service := NewService(
		repository, provider, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", WebhookID: "WH-1"},
		&fakeClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)},
		&fakeIDs{},
	)

	_, err := service.runFundingEffectContract(
		context.Background(), "mo-late", "procurement-effect-mo-late",
	)
	if err != nil {
		t.Fatalf("error=%v want funding outcome unknown", err)
	}
	if provider.reauthorizeCalls != 0 || len(provider.captureAuthorizationInputs) != 0 {
		t.Fatalf("unresolved reauthorization was reposted: reauthorize=%d capture=%d",
			provider.reauthorizeCalls, len(provider.captureAuthorizationInputs))
	}
}

func TestMerchantOrderFundingKeepsPreviousRequestInProgressUnknown(t *testing.T) {
	base := fixtureRepository()
	funding := &fakeMOFundingRepository{
		fakeRepository: base,
		activation: MOFundingActivation{
			MerchantOrderID: "mo-late", PositionID: "position-late",
			AllocationID: "allocation-late", AgencyOrderID: "order-1",
			Rail: "PAYPAL", ProviderEnvironment: "SANDBOX",
			State: domain.MOFundingAvailable, AmountMinor: 4237, Currency: "USD",
		},
	}
	repository := &fakeMOReauthorizationFundingRepository{
		fakeMOFundingRepository: funding,
		plan: MOReauthorizationPlan{
			Required: true, AuthorizationID: "authorization-1",
			TargetPositionID: "position-late", ProviderEnvironment: "SANDBOX",
			PreviousProviderAuthorizationID: "PP-AUTH-1",
			AuthorizedAmountMinor:           10600, RemainingCapturableMinor: 6375,
			Currency:                "USD",
			OperationIdempotencyKey: "paypal:reauthorize:authorization-1:1",
		},
	}
	apiErr := &paypal.APIError{
		StatusCode: 409, Name: "RESOURCE_CONFLICT",
		IssueCode: "PREVIOUS_REQUEST_IN_PROGRESS",
	}
	provider := &fakeProvider{
		payee: "MERCHANT-1", authorizationStatus: "CREATED",
		reauthorizeErr: fmt.Errorf("%w: %w", paypal.ErrOutcomeUnknown, apiErr),
	}
	service := NewService(
		repository, provider, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", WebhookID: "WH-1"},
		&fakeClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)},
		&fakeIDs{},
	)

	_, err := service.runFundingEffectContract(
		context.Background(), "mo-late", "procurement-effect-mo-late",
	)
	if err != nil {
		t.Fatalf("error=%v want funding outcome unknown", err)
	}
	if provider.reauthorizeCalls != 1 ||
		repository.plan.OperationState != domain.OperationUnknown {
		t.Fatalf("in-progress request not preserved as unknown: calls=%d plan=%+v",
			provider.reauthorizeCalls, repository.plan)
	}
	provider.reauthorizeErr = nil
	provider.authorizationStatus = "CREATED"
	result, err := service.runFundingEffectContract(
		context.Background(), "mo-late", "procurement-effect-mo-late",
	)
	if err != nil || result.Position.State != domain.MOFundingActive {
		t.Fatalf("same-key reauthorization recovery failed: result=%+v err=%v", result, err)
	}
	if provider.reauthorizeCalls != 2 ||
		provider.reauthorizeInputs[0].RequestID != provider.reauthorizeInputs[1].RequestID {
		t.Fatalf("reauthorization did not retry with same PayPal-Request-Id: %+v",
			provider.reauthorizeInputs)
	}
}

func TestMerchantOrderFundingClosesTerminalReauthorization(t *testing.T) {
	base := fixtureRepository()
	funding := &fakeMOFundingRepository{
		fakeRepository: base,
		activation: MOFundingActivation{
			MerchantOrderID: "mo-terminal", PositionID: "position-terminal",
			AllocationID: "allocation-terminal", AgencyOrderID: "order-1",
			Rail: "PAYPAL", ProviderEnvironment: "SANDBOX",
			State: domain.MOFundingAvailable, AmountMinor: 4237, Currency: "USD",
		},
	}
	repository := &fakeMOReauthorizationFundingRepository{
		fakeMOFundingRepository: funding,
		plan: MOReauthorizationPlan{
			Required: true, AuthorizationID: "authorization-1",
			TargetPositionID: "position-terminal", ProviderEnvironment: "SANDBOX",
			PreviousProviderAuthorizationID: "PP-AUTH-1",
			AuthorizedAmountMinor:           10600, RemainingCapturableMinor: 6375,
			Currency:                "USD",
			OperationIdempotencyKey: "paypal:reauthorize:authorization-1:1",
		},
	}
	provider := &fakeProvider{payee: "MERCHANT-1", authorizationStatus: "DENIED"}
	service := NewService(
		repository, provider, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", WebhookID: "WH-1"},
		&fakeClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)},
		&fakeIDs{},
	)

	result, err := service.runFundingEffectContract(
		context.Background(), "mo-terminal", "procurement-effect-mo-terminal",
	)
	if err != nil ||
		result.Position.State != domain.MOFundingFailed ||
		len(provider.captureAuthorizationInputs) != 0 {
		t.Fatalf("terminal reauthorization did not close target: result=%+v err=%v", result, err)
	}
}

func TestPendingInstructionKeepsAuthorizationPrivateUntilConfirmation(t *testing.T) {
	repository := fixtureRepository()
	provider := &fakeProvider{payee: "MERCHANT-1"}
	gate := &fakeInstructionGate{err: procmsg.ErrInstructionPending}
	service := newTestServiceWithInstructionGate(repository, provider, gate)
	started, err := service.StartCheckout(context.Background(), "user-1", "order-1")
	if err != nil {
		t.Fatal(err)
	}
	provider.orderStatus = "APPROVED"
	input := ResumeInput{UserID: "user-1", AgencyOrderID: "order-1", ReturnNonce: started.ReturnNonce}
	result, err := service.Resume(context.Background(), input)
	if err != nil || result.Payment.State != domain.PaymentProcessing || result.Payment.LastReasonCode != procmsg.ErrInstructionPending.Error() {
		t.Fatalf("pending receipt=%+v err=%v", result, err)
	}
	if repository.authorization != nil || len(repository.receipts) != 0 || provider.captureCalls != 0 || len(provider.captureAuthorizationInputs) != 0 {
		t.Fatal("pending instruction opened funding authority")
	}
	gate.err = nil
	result, err = service.Resume(context.Background(), input)
	if err != nil || result.Payment.State != domain.PaymentAuthorized || repository.authorization == nil {
		t.Fatalf("confirmed resume=%+v err=%v", result, err)
	}
	if provider.authorizeCalls != 1 || provider.captureCalls != 0 {
		t.Fatalf("confirmation duplicated provider execution: authorize=%d capture=%d", provider.authorizeCalls, provider.captureCalls)
	}
}
