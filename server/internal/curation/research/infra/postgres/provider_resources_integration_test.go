package postgres

import (
	"context"
	"fmt"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"os"
	"sync"
	"testing"
	"time"
)

func TestActorAccountReservationIsAtomicAcrossActors(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, dsn)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	repo.ConfigureCatalogResources(nil, 25000)
	ids := []string{"APIFY_MUSINSA", "APIFY_29CM", "APIFY_GMARKET"}
	for _, id := range ids {
		_, err := repo.UpdateProviderControl(ctx, id, "98000000-0000-4000-8000-000000000001", true, 1, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan bool, 3)
	errors := make(chan error, 3)
	for i, id := range ids {
		go func(i int, id string) {
			<-start
			_, started, err := repo.BeginActorRun(ctx, researchapp.ActorRun{RunKey: fmt.Sprint("race-", i), UserID: "98000000-0000-4000-8000-000000000001", APIID: id, Source: "GMARKET", StartedAt: time.Now()})
			results <- started
			errors <- err
		}(i, id)
	}
	close(start)
	count := 0
	for range ids {
		if <-results {
			count++
		}
		err := <-errors
		if err != nil {
			f, ok := fault.As(err)
			if !ok || f.Reason != "CATALOG_ACTOR_BUDGET_EXHAUSTED" {
				t.Fatal(err)
			}
		}
	}
	if count != 1 {
		t.Fatalf("admitted %d", count)
	}
	spend, err := repo.ActorSpendMicros(ctx, researchapp.ActorMonthStart(time.Now()))
	if err != nil || spend > 25000 || spend == 0 {
		t.Fatalf("spend=%d err=%v", spend, err)
	}
	for _, id := range ids {
		usage, err := repo.ReadProviderUsage(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, c := range usage.Resources.Constraints {
			if c.ID == "account:apify:usd" {
				found = true
				if c.Used != spend {
					t.Fatal(c)
				}
			}
		}
		if !found {
			t.Fatal("missing shared account")
		}
	}
	if _, err := db.DB.ExecContext(ctx, "DELETE FROM research_actor_runs"); err != nil {
		t.Fatal(err)
	}
	retained, err := repo.ActorSpendMicros(ctx, researchapp.ActorMonthStart(time.Now()))
	if err != nil || retained != spend {
		t.Fatalf("account usage lost after run retention: %d %v", retained, err)
	}

}
func TestConfiguredConcurrencyUsesSameAdmissionAndResourceProjection(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, dsn)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	repo.ConfigureCatalogResources(map[string]researchapp.CatalogLocalLimits{"NAVER_WEBKR": {RequestsPerMinute: 30, DailyLimit: 400, MaxConcurrent: 4}}, 0)
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := []string{}
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := repo.ReserveProviderCall(ctx, "NAVER_WEBKR", "SEARCH", time.Now())
			if err == nil {
				mu.Lock()
				admitted = append(admitted, id)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(admitted) != 4 {
		t.Fatalf("admitted=%d", len(admitted))
	}
	usage, err := repo.ReadProviderUsage(ctx, "NAVER_WEBKR")
	if err != nil {
		t.Fatal(err)
	}
	if usage.MaxConcurrent != 4 || usage.Resources.CanStart || usage.Resources.Pressure != 1 {
		t.Fatal(usage)
	}
	for _, id := range admitted {
		if err := repo.CompleteProviderCall(ctx, id, "SUCCESS", 200, 0, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	usage, err = repo.ReadProviderUsage(ctx, "NAVER_WEBKR")
	if err != nil || !usage.Resources.CanStart || usage.Resources.Pressure >= 1 {
		t.Fatalf("%+v %v", usage.Resources, err)
	}
}

func TestRouteDecisionsRemainScopedAndSummarizeFinalOutcome(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, dsn)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	now := time.Now().UTC()
	user := "98000000-0000-4000-8000-000000000001"
	route := researchapp.ResearchRoute{ID: "NAVER_WEBKR:ELEVENST", APIIDs: []string{"NAVER_WEBKR", "ELEVENST_HTML"}, Weight: 2, Resources: researchapp.ProviderResourceState{Pressure: .3}}
	for _, decision := range []string{"STARTED", "SUCCEEDED"} {
		if err := repo.RecordResearchRoute(ctx, user, "attempt", "GENERAL", route, decision, "", now); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := repo.ReadResearchRoundSummary(ctx, now.Add(-time.Hour), now.Add(time.Minute))
	if err != nil || len(summary.Routes) != 1 {
		t.Fatalf("%+v %v", summary.Routes, err)
	}
	row := summary.Routes[0]
	if row.Count != 1 || row.Decision != "SUCCEEDED" || row.MeanPressure != .3 || row.PolicyVersion != researchapp.ResearchRoutePolicyVersion {
		t.Fatal(row)
	}
}

func TestActorBudgetDenialDoesNotCountAsProviderTraffic(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, dsn)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	repo.ConfigureCatalogResources(nil, 25000)
	now := time.Now().UTC()
	user := "98000000-0000-4000-8000-000000000001"
	if _, err := repo.UpdateProviderControl(ctx, "APIFY_MUSINSA", user, true, 1, now); err != nil {
		t.Fatal(err)
	}
	call, err := repo.ReserveProviderCall(ctx, "APIFY_MUSINSA", "SEARCH", now)
	if err != nil {
		t.Fatal(err)
	}
	// A different Actor can consume the last shared budget after API admission.
	if err := repo.CompleteProviderCall(ctx, call, "CATALOG_ACTOR_BUDGET_EXHAUSTED", 0, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, started, err := repo.BeginActorRun(ctx, researchapp.ActorRun{RunKey: "budget", UserID: user, APIID: "APIFY_MUSINSA", Source: "MUSINSA", StartedAt: now}); err != nil || !started {
		t.Fatalf("started=%v err=%v", started, err)
	}
	if _, err := repo.ReserveProviderCall(ctx, "APIFY_MUSINSA", "SEARCH", now); err == nil {
		t.Fatal("exhausted account admitted")
	}
	usage, err := repo.ReadProviderUsage(ctx, "APIFY_MUSINSA")
	if err != nil || usage.Requests24h != 0 || usage.Resources.Reason != "CATALOG_ACTOR_BUDGET_EXHAUSTED" {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	var calls, billable int
	if err := db.DB.QueryRowContext(ctx, "SELECT count(*),count(*) FILTER(WHERE billable) FROM research_catalog_api_calls WHERE source='APIFY_MUSINSA'").Scan(&calls, &billable); err != nil || calls != 2 || billable != 0 {
		t.Fatalf("calls=%d billable=%d err=%v", calls, billable, err)
	}
	for _, c := range usage.Resources.Constraints {
		if c.Kind == "RATE" && c.Used != 0 {
			t.Fatalf("local denial increased rate: %+v", c)
		}
	}
}
