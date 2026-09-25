package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

// The Actor ledger is what keeps a paid run from being bought twice and what
// the monthly cap is measured against, so it is exercised on real PostgreSQL.
func TestActorRunLedgerClaimsSettlesAndTotalsTheMonth(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, dsn)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	const user = "98000000-0000-4000-8000-000000000001"
	now := time.Date(2026, 9, 16, 4, 0, 0, 0, time.UTC)

	run := researchapp.ActorRun{RunKey: "attempt-1:MUSINSA", UserID: user, APIID: "APIFY_MUSINSA", Source: "MUSINSA", StartedAt: now}
	claimed, started, err := repo.BeginActorRun(ctx, run)
	if err != nil || !started || claimed.Status != researchapp.ActorRunRunning {
		t.Fatalf("first claim started=%v run=%+v err=%v", started, claimed, err)
	}
	// An in-flight run already counts against the cap, so a crash between
	// start and settle cannot hide a bill.
	spend, err := repo.ActorSpendMicros(ctx, researchapp.ActorMonthStart(now))
	if err != nil || spend != researchapp.CatalogActorReservationMicros(run.APIID) {
		t.Fatalf("spend before settle=%d err=%v", spend, err)
	}

	again, started, err := repo.BeginActorRun(ctx, run)
	if err != nil || started {
		t.Fatalf("second claim started=%v err=%v", started, err)
	}
	if again.Status != researchapp.ActorRunRunning || again.APIID != "APIFY_MUSINSA" || again.ProviderRunID != "" {
		t.Fatalf("second claim read=%+v", again)
	}

	finished := now.Add(12 * time.Second)
	settled := researchapp.ActorRun{RunKey: run.RunKey, ProviderRunID: "run-1", Status: researchapp.ActorRunSucceeded,
		ItemCount: 5, CostMicros: 20051, FinishedAt: &finished}
	if err := repo.FinishActorRun(ctx, settled); err != nil {
		t.Fatal(err)
	}
	spend, err = repo.ActorSpendMicros(ctx, researchapp.ActorMonthStart(now))
	if err != nil || spend != 20051 {
		t.Fatalf("spend after settle=%d err=%v", spend, err)
	}

	// A settled run keeps its first terminal state and cost.
	late := researchapp.ActorRun{RunKey: run.RunKey, ProviderRunID: "run-1", Status: researchapp.ActorRunFailed,
		ItemCount: 0, CostMicros: 999_999, FinishedAt: &finished}
	if err := repo.FinishActorRun(ctx, late); err != nil {
		t.Fatal(err)
	}
	read, _, err := repo.BeginActorRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if read.Status != researchapp.ActorRunSucceeded || read.CostMicros != 20051 || read.ItemCount != 5 ||
		read.ProviderRunID != "run-1" || read.FinishedAt == nil {
		t.Fatalf("settled run changed: %+v", read)
	}

	// Last month's runs are outside the window the cap measures.
	previous := researchapp.ActorRun{RunKey: "attempt-0:MUSINSA", UserID: user, APIID: "APIFY_MUSINSA",
		Source: "MUSINSA", StartedAt: now.AddDate(0, -1, 0)}
	if _, _, err := repo.BeginActorRun(ctx, previous); err != nil {
		t.Fatal(err)
	}
	lastMonth := previous.StartedAt.Add(time.Minute)
	if err := repo.FinishActorRun(ctx, researchapp.ActorRun{RunKey: previous.RunKey, ProviderRunID: "run-0",
		Status: researchapp.ActorRunSucceeded, ItemCount: 5, CostMicros: 4_000_000, FinishedAt: &lastMonth}); err != nil {
		t.Fatal(err)
	}
	spend, err = repo.ActorSpendMicros(ctx, researchapp.ActorMonthStart(now))
	if err != nil || spend != 20051 {
		t.Fatalf("this month's spend=%d err=%v", spend, err)
	}
	all, err := repo.ActorSpendMicros(ctx, previous.StartedAt.Add(-time.Hour))
	if err != nil || all != 4_020_051 {
		t.Fatalf("two months of spend=%d err=%v", all, err)
	}
}
