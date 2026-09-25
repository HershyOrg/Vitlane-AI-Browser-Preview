package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/infra/paypal"
)

type fakePayPalMORefundAdoptionRepository struct {
	*fakeMOCompensationRepository
	plan          PayPalMORefundAdoptionPlan
	prepareErr    error
	prepareCalls  int
	recordCalls   int
	recorded      domain.PayPalRefundAdoption
	recordedState domain.MOCompensationState
	recordedOp    domain.OperationState
	replay        bool
}

func (r *fakePayPalMORefundAdoptionRepository) PreparePayPalMORefundAdoption(
	_ context.Context,
	compensationID, providerRefundID string,
	_ time.Time,
) (PayPalMORefundAdoptionPlan, error) {
	r.prepareCalls++
	if r.prepareErr != nil {
		return PayPalMORefundAdoptionPlan{}, r.prepareErr
	}
	if compensationID != r.plan.Execution.Compensation.ID || providerRefundID == "" {
		return PayPalMORefundAdoptionPlan{}, domain.ErrPayPalRefundAdoptionMissing
	}
	return r.plan, nil
}

func (r *fakePayPalMORefundAdoptionRepository) RecordPayPalMORefundAdoption(
	ctx context.Context,
	plan PayPalMORefundAdoptionPlan,
	adoption domain.PayPalRefundAdoption,
	compensationState domain.MOCompensationState,
	operationState domain.OperationState,
	reason string,
	now time.Time,
) (domain.MOCompensation, domain.PayPalRefundAdoption, bool, error) {
	r.recordCalls++
	r.recorded = adoption
	r.recordedState = compensationState
	r.recordedOp = operationState
	compensation, err := r.RecordMOCompensationOutcome(
		ctx, plan.Execution, compensationState, operationState,
		adoption.ProviderRefundID, reason, now,
	)
	return compensation, adoption, r.replay, err
}

type manualRefundProvider struct {
	*fakeProvider
	refund   paypal.Refund
	err      error
	getCalls int
}

func (p *manualRefundProvider) GetRefund(_ context.Context, _ string) (paypal.Refund, error) {
	p.getCalls++
	return p.refund, p.err
}

func refundAdoptionPlan(environment string) PayPalMORefundAdoptionPlan {
	return PayPalMORefundAdoptionPlan{
		OperationFirstSentAt: time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC),
		IdempotencyDeadline:  time.Date(2026, 8, 27, 11, 0, 0, 0, time.UTC),
		Execution: MOCompensationExecution{
			Compensation: domain.MOCompensation{
				ID: "compensation-1", FundingPositionID: "position-1",
				Rail: "PAYPAL", ProviderEnvironment: environment,
				Action:      domain.MOCompensationRefund,
				State:       domain.MOCompensationOutcomeUnknown,
				AmountMinor: 5523, Currency: "USD",
			},
			ProviderCaptureID: "PP-CAPTURE-MO-1",
			OperationID:       "operation-1", OperationState: domain.OperationUnknown,
			OperationIdempotencyKey: "paypal:mo-refund:compensation-1:1",
		}}
}

func refundAdoptionInput(now time.Time) AdoptPayPalMORefundInput {
	return AdoptPayPalMORefundInput{
		CompensationID: "compensation-1", ProviderRefundID: "PP-REFUND-FOUND-1",
		OperatorUserID:  "operator-1",
		PublicRationale: "PayPal activity shows the original refund resource.",
		EvidenceSource:  domain.PayPalRefundAdoptionEvidenceDashboard,
		EvidenceHash:    "0x" + strings.Repeat("a", 64), ObservedAt: now.Add(-time.Minute),
	}
}

func exactAdoptedRefund(status string) paypal.Refund {
	return paypal.Refund{
		ID: "PP-REFUND-FOUND-1", Status: status, AmountMinor: 5523,
		Currency: "USD", ParentCaptureID: "PP-CAPTURE-MO-1",
		InvoiceID: "paypal:mo-refund:compensation-1:1",
	}
}

func newRefundAdoptionService(
	repository *fakePayPalMORefundAdoptionRepository,
	registrations ...ProviderRegistration,
) *Service {
	registry, err := NewProviderRegistry(registrations...)
	if err != nil {
		panic(err)
	}
	return NewServiceWithProviderRegistry(
		repository, registry, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", PublicBaseURL: "https://app.vitlane.test"},
		&fakeClock{now: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)}, &fakeIDs{},
	)
}

func TestAdoptPayPalMORefundUsesStoredEnvironmentGETAndNeverPOSTs(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	plan := refundAdoptionPlan("LIVE")
	repository := &fakePayPalMORefundAdoptionRepository{
		fakeMOCompensationRepository: &fakeMOCompensationRepository{
			fakeRepository: newFakeRepository(), execution: plan.Execution,
		},
		plan: plan,
	}
	sandbox := &manualRefundProvider{fakeProvider: &fakeProvider{}}
	live := &manualRefundProvider{
		fakeProvider: &fakeProvider{}, refund: exactAdoptedRefund("COMPLETED"),
	}
	service := newRefundAdoptionService(repository,
		ProviderRegistration{Environment: "SANDBOX", Client: sandbox, WebhookID: "WH-S"},
		ProviderRegistration{Environment: "LIVE", Client: live, WebhookID: "WH-L"},
	)

	result, err := service.AdoptPayPalMORefund(
		context.Background(), refundAdoptionInput(now),
	)
	if err != nil {
		t.Fatalf("AdoptPayPalMORefund: %v", err)
	}
	if sandbox.getCalls != 0 || live.getCalls != 1 ||
		sandbox.refundCalls != 0 || live.refundCalls != 0 {
		t.Fatalf("adoption provider calls sandboxGET=%d liveGET=%d sandboxPOST=%d livePOST=%d",
			sandbox.getCalls, live.getCalls, sandbox.refundCalls, live.refundCalls)
	}
	if result.Compensation.State != domain.MOCompensationSucceeded ||
		result.Compensation.ProviderResourceID != "PP-REFUND-FOUND-1" ||
		repository.recordedState != domain.MOCompensationSucceeded ||
		repository.recordedOp != domain.OperationSucceeded {
		t.Fatalf("completed refund was not adopted: result=%+v", result)
	}
	if result.Adoption.ProviderEnvironment != "LIVE" ||
		result.Adoption.ParentCaptureID != plan.Execution.ProviderCaptureID ||
		result.Adoption.InvoiceID != plan.Execution.OperationIdempotencyKey ||
		result.Adoption.RequestHash == "" {
		t.Fatalf("incomplete adoption audit: %+v", result.Adoption)
	}
}

func TestAdoptPayPalMORefundMapsExactProviderStatuses(t *testing.T) {
	for _, tc := range []struct {
		status string
		state  domain.MOCompensationState
		op     domain.OperationState
		err    error
	}{
		{status: "PENDING", state: domain.MOCompensationOutcomeUnknown,
			op: domain.OperationUnknown, err: domain.ErrCompensationOutcomeUnknown},
		{status: "FAILED", state: domain.MOCompensationFailed,
			op: domain.OperationFailed, err: domain.ErrCompensationAttemptFailed},
		{status: "CANCELLED", state: domain.MOCompensationFailed,
			op: domain.OperationFailed, err: domain.ErrCompensationAttemptFailed},
	} {
		t.Run(tc.status, func(t *testing.T) {
			now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
			plan := refundAdoptionPlan("SANDBOX")
			repository := &fakePayPalMORefundAdoptionRepository{
				fakeMOCompensationRepository: &fakeMOCompensationRepository{
					fakeRepository: newFakeRepository(), execution: plan.Execution,
				}, plan: plan,
			}
			provider := &manualRefundProvider{
				fakeProvider: &fakeProvider{}, refund: exactAdoptedRefund(tc.status),
			}
			service := newRefundAdoptionService(repository, ProviderRegistration{
				Environment: "SANDBOX", Client: provider, WebhookID: "WH-S",
			})

			result, err := service.AdoptPayPalMORefund(
				context.Background(), refundAdoptionInput(now),
			)
			if !errors.Is(err, tc.err) || result.Compensation.State != tc.state ||
				repository.recordedOp != tc.op || provider.refundCalls != 0 {
				t.Fatalf("status=%s result=%+v state=%s op=%s err=%v",
					tc.status, result, repository.recordedState, repository.recordedOp, err)
			}
		})
	}
}

func TestAdoptPayPalMORefundRejectsEveryAttributionMismatchBeforeRecording(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*paypal.Refund)
	}{
		{name: "resource id", mutate: func(v *paypal.Refund) { v.ID = "PP-REFUND-OTHER" }},
		{name: "amount", mutate: func(v *paypal.Refund) { v.AmountMinor++ }},
		{name: "currency", mutate: func(v *paypal.Refund) { v.Currency = "EUR" }},
		{name: "parent capture", mutate: func(v *paypal.Refund) { v.ParentCaptureID = "PP-CAPTURE-OTHER" }},
		{name: "invoice", mutate: func(v *paypal.Refund) { v.InvoiceID = "paypal:mo-refund:other:1" }},
		{name: "status", mutate: func(v *paypal.Refund) { v.Status = "UNKNOWN" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
			plan := refundAdoptionPlan("SANDBOX")
			repository := &fakePayPalMORefundAdoptionRepository{
				fakeMOCompensationRepository: &fakeMOCompensationRepository{
					fakeRepository: newFakeRepository(), execution: plan.Execution,
				}, plan: plan,
			}
			refund := exactAdoptedRefund("COMPLETED")
			tc.mutate(&refund)
			provider := &manualRefundProvider{fakeProvider: &fakeProvider{}, refund: refund}
			service := newRefundAdoptionService(repository, ProviderRegistration{
				Environment: "SANDBOX", Client: provider, WebhookID: "WH-S",
			})

			_, err := service.AdoptPayPalMORefund(
				context.Background(), refundAdoptionInput(now),
			)
			if !errors.Is(err, domain.ErrPayPalRefundAdoptionMismatch) ||
				repository.recordCalls != 0 || provider.refundCalls != 0 {
				t.Fatalf("mismatch %s was adopted: records=%d post=%d err=%v",
					tc.name, repository.recordCalls, provider.refundCalls, err)
			}
		})
	}
}

func TestAdoptPayPalMORefundRejectsInvalidEvidenceBeforeProviderGET(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*AdoptPayPalMORefundInput)
	}{
		{name: "hash", mutate: func(input *AdoptPayPalMORefundInput) {
			input.EvidenceHash = "not-a-sha256"
		}},
		{name: "source", mutate: func(input *AdoptPayPalMORefundInput) {
			input.EvidenceSource = "UNVERIFIED"
		}},
		{name: "future observation", mutate: func(input *AdoptPayPalMORefundInput) {
			input.ObservedAt = now.Add(6 * time.Minute)
		}},
		{name: "refund reference", mutate: func(input *AdoptPayPalMORefundInput) {
			input.ProviderRefundID = "refund/id"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := refundAdoptionPlan("SANDBOX")
			repository := &fakePayPalMORefundAdoptionRepository{
				fakeMOCompensationRepository: &fakeMOCompensationRepository{
					fakeRepository: newFakeRepository(), execution: plan.Execution,
				}, plan: plan,
			}
			provider := &manualRefundProvider{
				fakeProvider: &fakeProvider{}, refund: exactAdoptedRefund("COMPLETED"),
			}
			service := newRefundAdoptionService(repository, ProviderRegistration{
				Environment: "SANDBOX", Client: provider, WebhookID: "WH-S",
			})
			input := refundAdoptionInput(now)
			tc.mutate(&input)

			_, err := service.AdoptPayPalMORefund(context.Background(), input)
			if !errors.Is(err, domain.ErrPayPalRefundAdoptionInvalid) ||
				provider.getCalls != 0 || repository.prepareCalls != 0 || repository.recordCalls != 0 {
				t.Fatalf("invalid evidence result prepare=%d get=%d records=%d err=%v",
					repository.prepareCalls, provider.getCalls, repository.recordCalls, err)
			}
		})
	}
}
