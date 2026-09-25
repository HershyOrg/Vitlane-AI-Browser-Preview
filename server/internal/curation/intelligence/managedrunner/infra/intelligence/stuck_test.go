package intelligence

import (
	"context"
	"errors"
	"testing"
	"time"

	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

type timeoutCause struct{}

func (timeoutCause) Error() string   { return "net/http: request canceled (Client.Timeout exceeded)" }
func (timeoutCause) Timeout() bool   { return true }
func (timeoutCause) Temporary() bool { return true }

// A model call cut off by the client timeout after the request was written
// keeps its reservation UNKNOWN for the ledger but is retryable for the
// attempt: no product state changed, and parking the job would block the
// curation until someone cancelled it.
func TestModelTimeoutKeepsReservationUnknownButRetriesTheAttempt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cause     error
		reason    string
		wantCode  fault.Code
		retryable bool
	}{
		{"client timeout", fault.Wrap(timeoutCause{}, fault.ExternalEffectUnknown, "HTTP_EFFECT_UNKNOWN", false), "HTTP_EFFECT_UNKNOWN", fault.DeadlineExceeded, true},
		{"attempt deadline", fault.Wrap(context.DeadlineExceeded, fault.ExternalEffectUnknown, "HTTP_EFFECT_UNKNOWN", false), "HTTP_EFFECT_UNKNOWN", fault.DeadlineExceeded, true},
		{"provider 5xx", fault.New(fault.ExternalEffectUnknown, "MANAGED_MODEL_HTTP_5XX_UNKNOWN", false), "MANAGED_MODEL_HTTP_5XX_UNKNOWN", fault.ProviderUnavailable, true},
		{"caller cancelled", fault.Wrap(context.Canceled, fault.ExternalEffectUnknown, "HTTP_EFFECT_UNKNOWN", false), "HTTP_EFFECT_UNKNOWN", fault.ExternalEffectUnknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lifecycle, stop := context.WithCancel(context.Background())
			defer stop()
			ledger := &providerTestLedger{}
			budget := runnerapp.NewBudgetService(ledger, runnerdomain.DefaultLimits(), providerTestTransactor{}, providerTestClock{now: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)}, &providerTestIDs{})
			registry, err := runnerdomain.NewRegistry([]runnerdomain.Model{{Key: "test", ProviderModelID: "m", InputMicrosPerMTok: 1, OutputMicrosPerMTok: 1, MaxOutputTokens: 100}}, "test")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			provider, err := NewProvider(budget, providerTestModel{err: tc.cause, calls: &calls}, registry, runtimepolicy.NewFinalizer(lifecycle, time.Second))
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Complete(context.Background(), intelligenceapp.CompletionRequest{UserID: "user-1", AttemptID: "attempt-1", RequestKey: "request-" + tc.name, ModelKey: "test"})
			failure, ok := fault.As(err)
			if !ok || failure.Code != tc.wantCode || failure.Retryable != tc.retryable {
				t.Fatalf("classified=%#v err=%v", failure, err)
			}
			if !errors.Is(err, tc.cause) {
				t.Fatal("the original cause must stay in the chain")
			}
			for _, reservation := range ledger.reservations {
				if reservation.Status != runnerdomain.ReservationUnknown {
					t.Fatalf("reservation must stay UNKNOWN for accounting: %#v", reservation)
				}
			}
		})
	}
}

type exhaustedLedger struct {
	providerTestLedger
	userCommitted int64
}

func (l *exhaustedLedger) ReadCounters(context.Context, time.Time, string) (runnerdomain.UsageCounters, runnerdomain.UsageCounters, error) {
	return runnerdomain.UsageCounters{}, runnerdomain.UsageCounters{SettledMicros: l.userCommitted}, nil
}

// Available with a user named refuses a research round the allowance cannot
// finish, before any catalog call; the server-only check stays as it was.
func TestAvailableRefusesAUserWhoCannotAffordAQueryAndOneBatch(t *testing.T) {
	limits := runnerdomain.DefaultLimits()
	model := runnerdomain.DefaultModels()[1]
	registry, err := runnerdomain.NewRegistry(runnerdomain.DefaultModels(), model.Key)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		committed int64
		userID    string
		exhausted bool
	}{
		{"fresh user", 0, "user-1", false},
		{"just enough left", limits.UserDaily - model.MinimumResearchHeadroom(), "user-1", false},
		{"too little left", limits.UserDaily - model.MinimumResearchHeadroom() + 1, "user-1", true},
		{"server-only check ignores the user", limits.UserDaily, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ledger := &exhaustedLedger{userCommitted: tc.committed}
			budget := runnerapp.NewBudgetService(ledger, limits, providerTestTransactor{}, providerTestClock{now: time.Now()}, &providerTestIDs{})
			provider, err := NewProvider(budget, providerTestModel{}, registry, runtimepolicy.NewFinalizer(context.Background(), time.Second))
			if err != nil {
				t.Fatal(err)
			}
			err = provider.Available(context.Background(), tc.userID)
			if tc.exhausted && (fault.CodeOf(err) != fault.QuotaExceeded || fault.Retryable(err)) {
				t.Fatalf("expected a final QUOTA_EXCEEDED, got %v", err)
			}
			if !tc.exhausted && err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
		})
	}
}
