package domain_test

import (
	"errors"
	"testing"
	"time"

	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
)

func TestAdmitStopsAtServerAdmissionNotHardCap(t *testing.T) {
	limits := runnerdomain.DefaultLimits()
	// $7.99 already committed leaves room under the $8.00 admission line.
	server := runnerdomain.UsageCounters{SettledMicros: 7_990_000}

	if err := limits.Admit(server, runnerdomain.UsageCounters{}, 10_000); err != nil {
		t.Fatalf("admit under the admission line: %v", err)
	}
	if err := limits.Admit(
		server, runnerdomain.UsageCounters{}, 20_000,
	); !errors.Is(err, runnerdomain.ErrServerDailyLimit) {
		t.Fatalf("error = %v, want ErrServerDailyLimit", err)
	}
	// The $2.00 between admission and the hard cap is reserved for settling
	// work that is already running, not for admitting new calls.
	if limits.ServerAdmission >= limits.ServerHard {
		t.Fatal("admission line must leave headroom below the hard cap")
	}
}

func TestAdmitCountsReservationsNotJustSettledSpend(t *testing.T) {
	limits := runnerdomain.DefaultLimits()
	// Concurrent calls are held, not yet settled. Ignoring reservations here is
	// exactly how two parallel requests would both pass and overshoot the cap.
	user := runnerdomain.UsageCounters{ReservedMicros: 95_000}

	if err := limits.Admit(
		runnerdomain.UsageCounters{}, user, 10_000,
	); !errors.Is(err, runnerdomain.ErrUserDailyLimit) {
		t.Fatalf("error = %v, want ErrUserDailyLimit", err)
	}
}

func TestAdmitChecksServerBeforeUser(t *testing.T) {
	limits := runnerdomain.DefaultLimits()
	server := runnerdomain.UsageCounters{SettledMicros: limits.ServerAdmission}
	user := runnerdomain.UsageCounters{SettledMicros: limits.UserDaily}

	// Both are exhausted; the server reason must win so the user is told the
	// service is out of budget rather than that they personally are.
	if err := limits.Admit(server, user, 1_000); !errors.Is(
		err, runnerdomain.ErrServerDailyLimit,
	) {
		t.Fatalf("error = %v, want ErrServerDailyLimit", err)
	}
}

func TestServerExhaustedGatesNewWork(t *testing.T) {
	limits := runnerdomain.DefaultLimits()

	if limits.ServerExhausted(runnerdomain.UsageCounters{
		SettledMicros: limits.ServerAdmission - 1,
	}) {
		t.Fatal("should not be exhausted below the admission line")
	}
	if !limits.ServerExhausted(runnerdomain.UsageCounters{
		SettledMicros: limits.ServerAdmission,
	}) {
		t.Fatal("should be exhausted at the admission line")
	}
}

func TestCostRoundsUpSoSmallCallsAreNeverFree(t *testing.T) {
	model := runnerdomain.Model{
		Key: "test", ProviderModelID: "test",
		InputMicrosPerMTok: 50_000, OutputMicrosPerMTok: 400_000,
		MaxOutputTokens: 2_000,
	}

	// A handful of tokens is a tiny fraction of a million. Truncating would
	// settle it as zero and let unlimited small calls run free.
	if got := model.Cost(runnerdomain.TokenUsage{
		InputTokens: 1, OutputTokens: 1,
	}); got != 2 {
		t.Fatalf("cost = %d, want 2", got)
	}
	if got := model.Cost(runnerdomain.TokenUsage{}); got != 0 {
		t.Fatalf("zero usage cost = %d, want 0", got)
	}
}

func TestWorstCostAssumesFullOutputBudget(t *testing.T) {
	model := runnerdomain.Model{
		Key: "test", ProviderModelID: "test",
		InputMicrosPerMTok: 1_000_000, OutputMicrosPerMTok: 1_000_000,
		MaxOutputTokens: 4_000,
	}

	worst := model.WorstCost(6_000)
	actual := model.Cost(runnerdomain.TokenUsage{
		InputTokens: 6_000, OutputTokens: 4_000,
	})
	if worst != actual {
		t.Fatalf("worst cost = %d, want %d", worst, actual)
	}
	// The reservation must cover any real response, otherwise settling could
	// push the ledger past the cap after the fact.
	if worst < model.Cost(runnerdomain.TokenUsage{
		InputTokens: 6_000, OutputTokens: model.MaxOutputTokens,
	}) {
		t.Fatal("worst cost must bound the maximum settleable cost")
	}
}

func TestUsageDateIsUTCMidnight(t *testing.T) {
	// 08:30 KST on 2026-08-01 is still 2026-07-31 in UTC. The bucket has to
	// follow UTC or the daily reset drifts with the caller's timezone.
	seoul := time.FixedZone("KST", 9*60*60)
	date := runnerdomain.UsageDate(
		time.Date(2026, 8, 1, 8, 30, 0, 0, seoul),
	)

	if got := date.Format("2006-01-02"); got != "2026-07-31" {
		t.Fatalf("usage date = %s, want 2026-07-31", got)
	}
	if date.Location() != time.UTC {
		t.Fatalf("usage date location = %v, want UTC", date.Location())
	}
}

func TestRegistryRejectsDuplicateAndUnknownDefault(t *testing.T) {
	valid := runnerdomain.Model{
		Key: "a", ProviderModelID: "p", MaxOutputTokens: 1,
	}
	if _, err := runnerdomain.NewRegistry(
		[]runnerdomain.Model{valid, valid}, "a",
	); err == nil {
		t.Fatal("duplicate keys should be rejected")
	}
	if _, err := runnerdomain.NewRegistry(
		[]runnerdomain.Model{valid}, "missing",
	); err == nil {
		t.Fatal("unknown default key should be rejected")
	}
	if _, err := runnerdomain.NewRegistry(nil, ""); !errors.Is(
		err, runnerdomain.ErrModelRegistryEmpty,
	) {
		t.Fatalf("error = %v, want ErrModelRegistryEmpty", err)
	}
}

func TestRegistryLookupFallsBackToDefault(t *testing.T) {
	registry, err := runnerdomain.NewRegistry([]runnerdomain.Model{
		{Key: "fast", ProviderModelID: "p1", MaxOutputTokens: 1},
		{Key: "deep", ProviderModelID: "p2", MaxOutputTokens: 1},
	}, "deep")
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}

	model, err := registry.Lookup("")
	if err != nil {
		t.Fatalf("lookup empty key: %v", err)
	}
	if model.Key != "deep" {
		t.Fatalf("model = %q, want deep", model.Key)
	}
	if _, err := registry.Lookup("nope"); !errors.Is(
		err, runnerdomain.ErrModelUnknown,
	) {
		t.Fatalf("error = %v, want ErrModelUnknown", err)
	}
}
