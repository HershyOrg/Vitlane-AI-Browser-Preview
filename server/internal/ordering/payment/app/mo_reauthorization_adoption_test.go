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

type fakeMOReauthorizationAdoptionRepository struct {
	*fakeRepository
	plan            MOReauthorizationAdoptionPlan
	existing        *PayPalReauthorizationAdoption
	prepareErr      error
	recordErr       error
	prepareCalls    int
	recordCalls     int
	preparedRequest PayPalReauthorizationAdoption
	recorded        PayPalReauthorizationAdoption
}

func (r *fakeMOReauthorizationAdoptionRepository) PrepareMOReauthorizationAdoption(
	_ context.Context,
	_ string,
	request PayPalReauthorizationAdoption,
) (MOReauthorizationAdoptionPlan, *PayPalReauthorizationAdoption, error) {
	r.prepareCalls++
	r.preparedRequest = request
	return r.plan, r.existing, r.prepareErr
}

func (r *fakeMOReauthorizationAdoptionRepository) RecordMOReauthorizationAdoption(
	_ context.Context,
	_ MOReauthorizationAdoptionPlan,
	adoption PayPalReauthorizationAdoption,
	_ time.Time,
) (PayPalReauthorizationAdoption, bool, error) {
	r.recordCalls++
	r.recorded = adoption
	return adoption, false, r.recordErr
}

type fakeMOReauthorizationAdoptionProvider struct {
	*fakeProvider
	authorization paypal.Authorization
	getErr        error
	getCalls      int
}

func (p *fakeMOReauthorizationAdoptionProvider) GetAuthorization(
	_ context.Context,
	_ string,
) (paypal.Authorization, error) {
	p.getCalls++
	return p.authorization, p.getErr
}

func reauthorizationAdoptionFixture(
	t *testing.T,
) (*Service, *fakeMOReauthorizationAdoptionRepository,
	*fakeMOReauthorizationAdoptionProvider, MOReauthorizationAdoptionPlan,
	AdoptMOReauthorizationInput, time.Time,
) {
	t.Helper()
	originalAuthorizedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	firstSentAt := originalAuthorizedAt.Add(4 * 24 * time.Hour)
	deadline := firstSentAt.Add(72 * time.Hour)
	now := deadline.Add(24 * time.Hour)
	providerCreatedAt := firstSentAt.Add(time.Minute)
	plan := MOReauthorizationAdoptionPlan{
		MerchantOrderID:     "merchant-order-1",
		FirstSentAt:         firstSentAt,
		IdempotencyDeadline: deadline,
		MOReauthorizationPlan: MOReauthorizationPlan{
			Required:                        true,
			AuthorizationID:                 "authorization-1",
			TargetPositionID:                "position-1",
			ProviderEnvironment:             "SANDBOX",
			PayPalOrderID:                   "PAYPAL-ORDER-1",
			PayeeMerchantID:                 "MERCHANT-1",
			PreviousProviderAuthorizationID: "AUTH-OLD",
			AuthorizedAmountMinor:           12708,
			RemainingCapturableMinor:        2138,
			Currency:                        "USD",
			OriginalAuthorizedAt:            originalAuthorizedAt,
			HonorRefreshedAt:                originalAuthorizedAt,
			ReauthorizationCount:            0,
			OperationID:                     "operation-1",
			OperationState:                  domain.OperationUnknown,
			OperationIdempotencyKey:         "paypal:reauthorize:authorization-1:1",
		},
	}
	repository := &fakeMOReauthorizationAdoptionRepository{
		fakeRepository: newFakeRepository(), plan: plan,
	}
	provider := &fakeMOReauthorizationAdoptionProvider{
		fakeProvider: &fakeProvider{},
		authorization: paypal.Authorization{
			ID: "AUTH-NEW", Status: " created ", AmountMinor: 2138,
			Currency: "USD", ParentOrderID: "PAYPAL-ORDER-1",
			PayeeMerchant: "MERCHANT-1",
			CreateTime:    providerCreatedAt.Format(time.RFC3339Nano),
		},
	}
	service := NewService(
		repository, provider, &fakeInstructionGate{}, directTransactor{},
		Config{Environment: "SANDBOX", WebhookID: "WH-1"},
		&fakeClock{now: now}, &fakeIDs{},
	)
	input := AdoptMOReauthorizationInput{
		ProviderAuthorizationID: " AUTH-NEW ",
		EvidenceSource:          " paypal_dashboard ",
		EvidenceHash:            "0x" + strings.Repeat("A", 64),
		InternalNote:            " verified in dashboard ",
		ObservedAt:              now,
	}
	return service, repository, provider, plan, input, now
}

func TestAdoptMOReauthorizationGetsAndRecordsExactCreatedResource(t *testing.T) {
	service, repository, provider, plan, input, _ :=
		reauthorizationAdoptionFixture(t)
	result, err := service.AdoptMOReauthorization(
		context.Background(), plan.MerchantOrderID, "operator-1",
		"reauthorization-adoption-key", input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Replay || provider.getCalls != 1 ||
		provider.reauthorizeCalls != 0 || repository.recordCalls != 1 {
		t.Fatalf("unexpected calls/result: result=%+v GET=%d POST=%d record=%d",
			result, provider.getCalls, provider.reauthorizeCalls,
			repository.recordCalls)
	}
	got := repository.recorded
	if got.ProviderAuthorizationID != "AUTH-NEW" ||
		got.PreviousProviderAuthorizationID != "AUTH-OLD" ||
		got.OperationState != domain.OperationSucceeded ||
		got.ProviderStatus != "CREATED" || got.AmountMinor != 2138 ||
		got.Currency != "USD" || got.PayPalOrderID != plan.PayPalOrderID ||
		got.PayeeMerchantID != plan.PayeeMerchantID ||
		got.EvidenceSource != PayPalReauthorizationEvidenceDashboard ||
		got.EvidenceHash != "0x"+strings.Repeat("a", 64) ||
		got.InternalNote != "verified in dashboard" ||
		!got.OperationFirstSentAt.Equal(plan.FirstSentAt) ||
		!got.OperationIdempotencyDeadline.Equal(plan.IdempotencyDeadline) ||
		!got.OriginalAuthorizedAt.Equal(plan.OriginalAuthorizedAt) {
		t.Fatalf("wrong exact adoption: %+v", got)
	}
}

func TestAdoptMOReauthorizationRejectsNonExactCandidateWithoutProviderWrite(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(*MOReauthorizationAdoptionPlan, *AdoptMOReauthorizationInput,
			*paypal.Authorization)
	}{
		{"previous id", func(plan *MOReauthorizationAdoptionPlan,
			input *AdoptMOReauthorizationInput, _ *paypal.Authorization) {
			input.ProviderAuthorizationID = plan.PreviousProviderAuthorizationID
		}},
		{"resource id", func(_ *MOReauthorizationAdoptionPlan,
			_ *AdoptMOReauthorizationInput, value *paypal.Authorization) {
			value.ID = "AUTH-OTHER"
		}},
		{"amount", func(_ *MOReauthorizationAdoptionPlan,
			_ *AdoptMOReauthorizationInput, value *paypal.Authorization) {
			value.AmountMinor++
		}},
		{"currency", func(_ *MOReauthorizationAdoptionPlan,
			_ *AdoptMOReauthorizationInput, value *paypal.Authorization) {
			value.Currency = "EUR"
		}},
		{"parent order", func(_ *MOReauthorizationAdoptionPlan,
			_ *AdoptMOReauthorizationInput, value *paypal.Authorization) {
			value.ParentOrderID = "PAYPAL-ORDER-OTHER"
		}},
		{"payee", func(_ *MOReauthorizationAdoptionPlan,
			_ *AdoptMOReauthorizationInput, value *paypal.Authorization) {
			value.PayeeMerchant = "MERCHANT-OTHER"
		}},
		{"before first send", func(plan *MOReauthorizationAdoptionPlan,
			_ *AdoptMOReauthorizationInput, value *paypal.Authorization) {
			value.CreateTime = plan.FirstSentAt.Add(-time.Nanosecond).
				Format(time.RFC3339Nano)
		}},
		{"outside original window", func(plan *MOReauthorizationAdoptionPlan,
			_ *AdoptMOReauthorizationInput, value *paypal.Authorization) {
			value.CreateTime = plan.OriginalAuthorizedAt.Add(29 * 24 * time.Hour).
				Format(time.RFC3339Nano)
		}},
		{"observed before provider", func(_ *MOReauthorizationAdoptionPlan,
			input *AdoptMOReauthorizationInput, value *paypal.Authorization) {
			created, _ := time.Parse(time.RFC3339Nano, value.CreateTime)
			input.ObservedAt = created.Add(-time.Nanosecond)
		}},
		{"missing status", func(_ *MOReauthorizationAdoptionPlan,
			_ *AdoptMOReauthorizationInput, value *paypal.Authorization) {
			value.Status = " "
		}},
		{"malformed create time", func(_ *MOReauthorizationAdoptionPlan,
			_ *AdoptMOReauthorizationInput, value *paypal.Authorization) {
			value.CreateTime = "not-a-time"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, repository, provider, plan, input, _ :=
				reauthorizationAdoptionFixture(t)
			test.mutate(&plan, &input, &provider.authorization)
			repository.plan = plan
			_, err := service.AdoptMOReauthorization(
				context.Background(), plan.MerchantOrderID, "operator-1",
				"reauthorization-adoption-key", input,
			)
			if !errors.Is(err, domain.ErrInstructionMismatch) {
				t.Fatalf("error=%v want instruction mismatch", err)
			}
			if provider.reauthorizeCalls != 0 || repository.recordCalls != 0 {
				t.Fatalf("non-exact candidate caused side effect: POST=%d record=%d",
					provider.reauthorizeCalls, repository.recordCalls)
			}
		})
	}
}

func TestAdoptMOReauthorizationPersistsTerminalAndUnknownGETOutcomes(t *testing.T) {
	tests := []struct {
		status string
		state  domain.OperationState
		err    error
	}{
		{"DENIED", domain.OperationFailed, domain.ErrFundingNotAvailable},
		{"PENDING", domain.OperationUnknown, domain.ErrFundingOutcomeUnknown},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			service, repository, provider, plan, input, _ :=
				reauthorizationAdoptionFixture(t)
			provider.authorization.Status = test.status
			result, err := service.AdoptMOReauthorization(
				context.Background(), plan.MerchantOrderID, "operator-1",
				"reauthorization-adoption-key", input,
			)
			if !errors.Is(err, test.err) || repository.recordCalls != 1 ||
				result.Adoption.OperationState != test.state {
				t.Fatalf("result=%+v record=%d error=%v", result,
					repository.recordCalls, err)
			}
			if provider.reauthorizeCalls != 0 {
				t.Fatalf("adoption issued reauthorization POST: %d",
					provider.reauthorizeCalls)
			}
		})
	}
}

func TestAdoptMOReauthorizationReplaySkipsProviderGETAndPreservesOutcome(t *testing.T) {
	service, repository, provider, plan, input, now :=
		reauthorizationAdoptionFixture(t)
	repository.existing = &PayPalReauthorizationAdoption{
		ID: "adoption-existing", MerchantOrderID: plan.MerchantOrderID,
		OperationState: domain.OperationUnknown, CreatedAt: now,
	}
	result, err := service.AdoptMOReauthorization(
		context.Background(), plan.MerchantOrderID, "operator-1",
		"reauthorization-adoption-key", input,
	)
	if !errors.Is(err, domain.ErrFundingOutcomeUnknown) || !result.Replay ||
		provider.getCalls != 0 || repository.recordCalls != 0 {
		t.Fatalf("replay result=%+v error=%v GET=%d record=%d",
			result, err, provider.getCalls, repository.recordCalls)
	}
}

func TestAdoptMOReauthorizationRejectsInvalidEvidenceBeforeGET(t *testing.T) {
	service, repository, provider, plan, input, _ :=
		reauthorizationAdoptionFixture(t)
	input.EvidenceHash = "not-a-hash"
	_, err := service.AdoptMOReauthorization(
		context.Background(), plan.MerchantOrderID, "operator-1",
		"reauthorization-adoption-key", input,
	)
	if !errors.Is(err, domain.ErrInvalid) || repository.prepareCalls != 0 ||
		provider.getCalls != 0 || provider.reauthorizeCalls != 0 {
		t.Fatalf("invalid evidence error=%v prepare=%d GET=%d POST=%d", err,
			repository.prepareCalls, provider.getCalls, provider.reauthorizeCalls)
	}
}
