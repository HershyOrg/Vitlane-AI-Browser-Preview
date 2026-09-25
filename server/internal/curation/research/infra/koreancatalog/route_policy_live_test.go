package koreancatalog

import (
	"context"
	"encoding/json"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/curation/research/infra/apifyactor"
	researchpostgres "github.com/vitlane/vitlane/server/internal/curation/research/infra/postgres"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"
)

// Explicit opt-in: six bounded real searches, including paid Actors, against
// disposable PostgreSQL only. This is adapter evidence, not deployed-app smoke.
func TestLiveResearchRoutePolicy(t *testing.T) {
	if os.Getenv("VITLANE_ROUTE_LIVE_TEST") != "1" {
		t.Skip("VITLANE_ROUTE_LIVE_TEST=1 authorizes paid live calls")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	dsn := os.Getenv("TEST_DATABASE_URL")
	base, err := sharedpostgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "route_live_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err = base.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	parsed, _ := url.Parse(dsn)
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, err := sharedpostgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx, "../../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	const user = "98000000-0000-4000-8000-000000000001"
	if _, err = db.DB.ExecContext(ctx, "INSERT INTO users(id,status,created_at,updated_at)VALUES($1,'ACTIVE',now(),now())", user); err != nil {
		t.Fatal(err)
	}
	repo := researchpostgres.NewRepository(db)
	repo.ConfigureCatalogResources(map[string]researchapp.CatalogLocalLimits{
		"OWN_PRODUCT": {RequestsPerMinute: 20, DailyLimit: 200, MaxConcurrent: 4},
		"NAVER_WEBKR": {RequestsPerMinute: 30, DailyLimit: 400, MaxConcurrent: 4},
		"SERP_GOOGLE": {RequestsPerMinute: 10, DailyLimit: 30, MaxConcurrent: 2},
	}, 250000)
	for _, api := range researchapp.KoreanCatalogAPIs {
		usage, err := repo.ReadProviderUsage(ctx, api.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !usage.Control.Enabled {
			if _, err := repo.UpdateProviderControl(ctx, api.ID, user, true, usage.Control.Version, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	actor, err := apifyactor.New(apifyactor.Config{Token: os.Getenv("APIFY_API_TOKEN"), MonthlyCapMicros: 250000, Client: client, Control: repo, Ledger: repo})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(Config{Enabled: true, OWNKey: os.Getenv("OPEN_WEB_NINJA_API_KEY"), NaverClientID: os.Getenv("NAVER_API_HUB_CLIENT_ID"), NaverClientSecret: os.Getenv("NAVER_API_HUB_CLIENT_SECRET"), SerpKey: os.Getenv("SERP_API_KEY"), Control: repo, Client: client, Actor: actor, DetailLimit: 4, MaxConcurrent: 4, SearchTimeout: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	entries := []map[string]any{}
	allMalls := map[string]bool{}
	for i, scenario := range []struct{ query, vertical string }{{"운동화", "FASHION"}, {"선크림", "BEAUTY"}, {"우유", "FOOD"}, {"수납함", "LIVING"}, {"무선 청소기", "ELECTRONICS"}, {"선물", "GENERAL"}} {
		started := time.Now()
		result, err := g.SearchExternalMalls(ctx, researchapp.KoreanSearchRequest{Query: scenario.query, Country: "KR", Vertical: scenario.vertical, UserID: user, AttemptKey: schema + strconv.Itoa(i)})
		counts := map[string]int{}
		for _, product := range result.Observations {
			if product.Validate() != nil {
				t.Error("invalid original observation")
			}
			source := string(product.ProductRef.Source)
			counts[source]++
			allMalls[source] = true
		}
		entries = append(entries, map[string]any{"query": scenario.query, "assignedVertical": scenario.vertical, "durationMs": time.Since(started).Milliseconds(), "reason": reason(err), "observations": len(result.Observations), "malls": counts, "coverage": result.Coverage})
		t.Logf("vertical=%s observations=%d malls=%v durationMs=%d reason=%s", scenario.vertical, len(result.Observations), counts, time.Since(started).Milliseconds(), reason(err))
	}
	summary, err := repo.ReadResearchRoundSummary(ctx, time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	spend, err := repo.ActorSpendMicros(ctx, researchapp.ActorMonthStart(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	timings := []map[string]any{}
	rows, err := db.DB.QueryContext(ctx, `SELECT source,operation,outcome,count(*),
 COALESCE(avg(extract(epoch FROM(completed_at-started_at))*1000),0)::bigint
 FROM research_catalog_api_calls GROUP BY 1,2,3 ORDER BY 1,2,3`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var api, operation, outcome string
		var count, meanMS int64
		if err := rows.Scan(&api, &operation, &outcome, &count, &meanMS); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		timings = append(timings, map[string]any{"api": api, "operation": operation, "outcome": outcome, "calls": count, "meanDurationMs": meanMS})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	evidence := map[string]any{"scope": "live adapters; disposable PostgreSQL; category assigned by test; not production smoke", "policyVersion": researchapp.ResearchRoutePolicyVersion, "observedAt": time.Now().UTC(), "entries": entries, "apiTimings": timings, "summary": summary, "actorUsedMicros": spend}
	if path := os.Getenv("VITLANE_ROUTE_EVIDENCE_PATH"); path != "" {
		raw, _ := json.MarshalIndent(evidence, "", "  ")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if len(allMalls) < 2 {
		t.Errorf("only %d distinct malls across all live scenarios", len(allMalls))
	}
}
