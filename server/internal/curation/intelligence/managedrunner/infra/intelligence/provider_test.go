package intelligence

import (
	"context"
	"fmt"
	"testing"
	"time"

	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

type providerTestClock struct{ now time.Time }

func (c providerTestClock) Now() time.Time { return c.now }

type providerTestIDs struct{ next int }

func (i *providerTestIDs) NewID() string {
	i.next++
	return fmt.Sprintf("id-%d", i.next)
}

type providerTestTransactor struct{}

func (providerTestTransactor) WithinTransaction(
	ctx context.Context, fn func(context.Context) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(ctx)
}

var _ sharedapp.Transactor = providerTestTransactor{}

type providerTestLedger struct {
	reservations   map[string]runnerdomain.Reservation
	settleCloseErr error
}

func (l *providerTestLedger) LockCounters(
	context.Context, time.Time, string, time.Time,
) (runnerdomain.UsageCounters, runnerdomain.UsageCounters, error) {
	return runnerdomain.UsageCounters{}, runnerdomain.UsageCounters{}, nil
}
func (l *providerTestLedger) ReadCounters(
	context.Context, time.Time, string,
) (runnerdomain.UsageCounters, runnerdomain.UsageCounters, error) {
	return runnerdomain.UsageCounters{}, runnerdomain.UsageCounters{}, nil
}
func (l *providerTestLedger) ListServerUsage(
	context.Context, time.Time, time.Time,
) ([]runnerdomain.DailyUsage, error) {
	return nil, nil
}
func (l *providerTestLedger) AddReserved(
	context.Context, time.Time, string, int64, time.Time,
) error {
	return nil
}
func (l *providerTestLedger) Settle(
	context.Context, time.Time, string, int64, int64,
	runnerdomain.TokenUsage, time.Time,
) error {
	return nil
}
func (l *providerTestLedger) InsertReservation(
	_ context.Context, reservation runnerdomain.Reservation,
) error {
	if l.reservations == nil {
		l.reservations = map[string]runnerdomain.Reservation{}
	}
	for _, existing := range l.reservations {
		if existing.RequestKey == reservation.RequestKey {
			return runnerdomain.ErrReservationExists
		}
	}
	l.reservations[reservation.ID] = reservation
	return nil
}
func (l *providerTestLedger) CloseReservation(
	ctx context.Context, id string, status runnerdomain.ReservationStatus, now time.Time,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if status == runnerdomain.ReservationSettled && l.settleCloseErr != nil {
		return false, l.settleCloseErr
	}
	reservation := l.reservations[id]
	if reservation.Status != runnerdomain.ReservationHeld {
		return false, nil
	}
	reservation.Status = status
	reservation.CompletedAt = &now
	l.reservations[id] = reservation
	return true, nil
}
func (l *providerTestLedger) ExpiredReservations(
	context.Context, time.Time, int,
) ([]runnerdomain.Reservation, error) {
	return nil, nil
}

func (l *providerTestLedger) ReservationForUpdate(
	context.Context, string,
) (runnerdomain.Reservation, bool, error) {
	return runnerdomain.Reservation{}, false, nil
}

func (l *providerTestLedger) ResolveUnknownReservation(
	context.Context, string, runnerdomain.ReservationStatus, time.Time,
) (bool, error) {
	return false, nil
}

func (l *providerTestLedger) InsertReservationResolution(
	context.Context, runnerdomain.ReservationResolution,
) error {
	return nil
}

type providerTestModel struct {
	cancel   context.CancelFunc
	err      error
	calls    *int
	response runnerapp.ModelResponse
}

func (m providerTestModel) Complete(
	context.Context, runnerapp.ModelRequest,
) (runnerapp.ModelResponse, error) {
	if m.calls != nil {
		*m.calls++
	}
	if m.cancel != nil {
		m.cancel()
	}
	return m.response, m.err
}

func TestAmbiguousModelCallFinalizesUnknownAfterCallerCancellation(t *testing.T) {
	caller, cancelCaller := context.WithCancel(context.Background())
	lifecycle, stopLifecycle := context.WithCancel(context.Background())
	defer stopLifecycle()
	ledger := &providerTestLedger{}
	ids := &providerTestIDs{}
	budget := runnerapp.NewBudgetService(
		ledger, runnerdomain.DefaultLimits(), providerTestTransactor{},
		providerTestClock{now: time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)}, ids,
	)
	registry, err := runnerdomain.NewRegistry([]runnerdomain.Model{{
		Key: "test", ProviderModelID: "provider-model",
		InputMicrosPerMTok: 1, OutputMicrosPerMTok: 1,
		MaxOutputTokens: 100,
	}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	modelFailure := fault.New(
		fault.ExternalEffectUnknown, "HTTP_EFFECT_UNKNOWN", false,
	)
	modelCalls := 0
	provider, err := NewProvider(
		budget, providerTestModel{cancel: cancelCaller, err: modelFailure, calls: &modelCalls}, registry,
		runtimepolicy.NewFinalizer(lifecycle, time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Complete(caller, intelligenceapp.CompletionRequest{
		UserID: "user-1", AttemptID: "attempt-1", RequestKey: "request-1",
		ModelKey: "test",
	})
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.ExternalEffectUnknown {
		t.Fatalf("completion failure=%#v err=%v", failure, err)
	}
	if len(ledger.reservations) != 1 {
		t.Fatalf("reservations=%#v", ledger.reservations)
	}
	for _, reservation := range ledger.reservations {
		if reservation.Status != runnerdomain.ReservationUnknown ||
			reservation.RequestKey != "request-1" {
			t.Fatalf("reservation=%#v", reservation)
		}
	}

	_, err = provider.Complete(context.Background(), intelligenceapp.CompletionRequest{
		UserID: "user-1", AttemptID: "attempt-1", RequestKey: "request-1",
		ModelKey: "test",
	})
	failure, ok = fault.As(err)
	if !ok || failure.Code != fault.ExternalEffectUnknown || modelCalls != 1 ||
		len(ledger.reservations) != 1 {
		t.Fatalf("duplicate result=%#v err=%v calls=%d reservations=%d",
			failure, err, modelCalls, len(ledger.reservations))
	}
}

func TestSettlementFailureMarksReservationUnknown(t *testing.T) {
	lifecycle, stopLifecycle := context.WithCancel(context.Background())
	defer stopLifecycle()
	ledger := &providerTestLedger{settleCloseErr: fmt.Errorf("settle unavailable")}
	budget := runnerapp.NewBudgetService(
		ledger, runnerdomain.DefaultLimits(), providerTestTransactor{},
		providerTestClock{now: time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)},
		&providerTestIDs{},
	)
	registry, err := runnerdomain.NewRegistry([]runnerdomain.Model{{
		Key: "test", ProviderModelID: "provider-model",
		InputMicrosPerMTok: 1, OutputMicrosPerMTok: 1,
		MaxOutputTokens: 100,
	}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewProvider(
		budget,
		providerTestModel{response: runnerapp.ModelResponse{
			Content: "{}", Usage: runnerdomain.TokenUsage{InputTokens: 1, OutputTokens: 1},
		}},
		registry, runtimepolicy.NewFinalizer(lifecycle, time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Complete(context.Background(), intelligenceapp.CompletionRequest{
		UserID: "user-1", AttemptID: "attempt-1", RequestKey: "request-settle",
		ModelKey: "test",
	})
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.ExternalEffectUnknown || failure.Retryable {
		t.Fatalf("settlement failure=%#v err=%v", failure, err)
	}
	for _, reservation := range ledger.reservations {
		if reservation.Status != runnerdomain.ReservationUnknown {
			t.Fatalf("reservation=%#v", reservation)
		}
	}
}

type imageFallbackModel struct {
	requests []runnerapp.ModelRequest
	failure  error
}

func (m *imageFallbackModel) Complete(_ context.Context, r runnerapp.ModelRequest) (runnerapp.ModelResponse, error) {
	m.requests = append(m.requests, r)
	if len(m.requests) == 1 {
		return runnerapp.ModelResponse{}, m.failure
	}
	return runnerapp.ModelResponse{Content: "{}", Usage: runnerdomain.TokenUsage{InputTokens: 12, OutputTokens: 4}}, nil
}
func TestImageFallbackReservesSeparatelyAndNeverRetriesUnknownEffect(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			ledger := &providerTestLedger{}
			budget := runnerapp.NewBudgetService(ledger, runnerdomain.DefaultLimits(), providerTestTransactor{}, providerTestClock{now: time.Now()}, &providerTestIDs{})
			registry, err := runnerdomain.NewRegistry([]runnerdomain.Model{{Key: "test", ProviderModelID: "test", InputMicrosPerMTok: 200000, OutputMicrosPerMTok: 1200000, MaxOutputTokens: 8000, SupportsLowImages: true, LowImageInputTokens: 320, AssessmentOutputTokens: 16000}}, "test")
			if err != nil {
				t.Fatal(err)
			}
			model := &imageFallbackModel{failure: fault.New(fault.ProviderRejected, "MANAGED_IMAGE_REJECTED", false)}
			if unknown {
				model.failure = fault.New(fault.ExternalEffectUnknown, "HTTP_EFFECT_UNKNOWN", false)
			}
			provider, err := NewProvider(budget, model, registry, runtimepolicy.NewFinalizer(context.Background(), time.Second))
			if err != nil {
				t.Fatal(err)
			}
			images := make([]intelligenceapp.CompletionImage, 50)
			for i := range images {
				images[i] = intelligenceapp.CompletionImage{ObservationID: fmt.Sprint(i), URL: "https://example.com/a.jpg"}
			}
			_, err = provider.Complete(context.Background(), intelligenceapp.CompletionRequest{UserID: "user", AttemptID: "attempt", RequestKey: "image", ModelKey: "test", SchemaName: intelligenceapp.SchemaCandidateRanking, Images: images})
			if unknown {
				if err == nil || len(model.requests) != 1 || len(ledger.reservations) != 1 {
					t.Fatalf("unknown retried: %v", err)
				}
				return
			}
			if err != nil || len(model.requests) != 2 || len(ledger.reservations) != 2 {
				t.Fatalf("fallback %v calls=%d reservations=%d", err, len(model.requests), len(ledger.reservations))
			}
			if len(model.requests[0].Images) != 50 || len(model.requests[1].Images) != 0 || model.requests[1].RequestKey != "image:text-fallback" || model.requests[0].Model.MaxOutputTokens != 16000 {
				t.Fatalf("fallback requests %+v", model.requests)
			}
			for _, r := range ledger.reservations {
				if r.Status == runnerdomain.ReservationHeld {
					t.Fatal("reservation not finalized")
				}
			}
		})
	}
}

func TestAssessmentSchemaDrivesModelBudget(t *testing.T) {
	for _, tc := range []struct {
		candidates, axes int
		want             int64
	}{{15, 3, 6000}, {50, 8, 40000}} {
		schema := intelligenceapp.CandidateRankingSchema(tc.candidates, tc.axes)
		count, axes := assessmentSchemaSize(schema)
		model := runnerdomain.DefaultModels()[1].AssessmentModel(count, axes)
		if model.MaxOutputTokens != tc.want {
			t.Fatalf("count=%d axes=%d budget=%d", count, axes, model.MaxOutputTokens)
		}
	}
}
