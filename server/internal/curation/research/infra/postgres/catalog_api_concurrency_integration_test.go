package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// Provider APIs admit a second in-flight call so two Targets researching at
// once do not silently lose a source; a public HTML host still gets one call
// at a time. The denial carries a RetryAfter the caller may wait once for.
func TestReserveProviderCallHonoursPerAPIConcurrency(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, url)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	now := time.Now().UTC()
	if err := repo.SaveProviderQuota(ctx, "OWN_PRODUCT", researchapp.CatalogAPIQuota{Limit: 100, Remaining: 100, ResetAt: now.Add(time.Hour), ObservedAt: now}, now); err != nil {
		t.Fatal(err)
	}
	first, err := repo.ReserveProviderCall(ctx, "OWN_PRODUCT", "SEARCH", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReserveProviderCall(ctx, "OWN_PRODUCT", "SEARCH", now); err != nil {
		t.Fatalf("second concurrent OWN Product call must be admitted: %v", err)
	}
	_, err = repo.ReserveProviderCall(ctx, "OWN_PRODUCT", "SEARCH", now)
	f, ok := fault.As(err)
	if !ok || f.Reason != "CATALOG_API_RATE_LIMITED" || f.RetryAfter != 2*time.Second || !f.Retryable {
		t.Fatalf("third concurrent call: %v", err)
	}
	if err = repo.CompleteProviderCall(ctx, first, "SUCCESS", 200, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReserveProviderCall(ctx, "OWN_PRODUCT", "SEARCH", now.Add(time.Second)); err != nil {
		t.Fatalf("slot freed by completion must admit: %v", err)
	}

	if _, err = repo.ReserveProviderCall(ctx, "ELEVENST_HTML", "DETAIL", now); err != nil {
		t.Fatal(err)
	}
	_, err = repo.ReserveProviderCall(ctx, "ELEVENST_HTML", "DETAIL", now)
	if f, ok = fault.As(err); !ok || f.Reason != "CATALOG_API_RATE_LIMITED" || f.RetryAfter != 2*time.Second {
		t.Fatalf("public HTML host must stay at one in-flight call: %v", err)
	}

	// The denials stayed in the ledger as non-billable records for the operator summary.
	summary, err := repo.ReadResearchRoundSummary(ctx, now.Add(-time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	denials := int64(0)
	for _, row := range summary.APICalls {
		if row.Outcome == "CATALOG_API_RATE_LIMITED" && !row.Billable {
			denials += row.Count
		}
	}
	if denials != 2 {
		t.Fatalf("expected 2 recorded denials, got %d in %+v", denials, summary.APICalls)
	}

	// A provider cooldown answers with the exact remaining time.
	held, err := repo.ReserveProviderCall(ctx, "NAVER_WEBKR", "SEARCH", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteProviderCall(ctx, held, "CATALOG_UPSTREAM_RATE_LIMITED", 429, 30, now); err != nil {
		t.Fatal(err)
	}
	_, err = repo.ReserveProviderCall(ctx, "NAVER_WEBKR", "SEARCH", now.Add(10*time.Second))
	if f, ok = fault.As(err); !ok || !strings.Contains(f.Reason, "CATALOG_API_RATE_LIMITED") || f.RetryAfter < 19*time.Second || f.RetryAfter > 21*time.Second {
		t.Fatalf("cooldown RetryAfter: %v (%v)", err, f)
	}
}

// The Shopify catalog is one API product of the operator's ledger (migration
// 127): its calls and failures are counted per 24 hours like every other
// research API's, and the operator's switch stops it before Shopify is called.
func TestShopifyCatalogCallsAreCountedInTheOperatorLedger(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, url)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	now := time.Now().UTC()
	usage, err := repo.ReadProviderUsage(ctx, researchapp.ShopifyCatalogAPIID)
	if err != nil || !usage.Control.Enabled || usage.APIProvider != "Shopify" || usage.Requests24h != 0 || usage.Quota != nil {
		t.Fatalf("the Shopify row starts On and empty: usage=%+v err=%v", usage, err)
	}
	for _, step := range []struct{ operation, outcome string }{
		{"SEARCH", "SUCCESS"}, {"DETAIL", "SUCCESS"}, {"DETAIL", "SUCCESS"}, {"SEARCH", "RATE_LIMITED"},
	} {
		call, err := repo.ReserveProviderCall(ctx, researchapp.ShopifyCatalogAPIID, step.operation, now)
		if err != nil {
			t.Fatalf("reserve %s: %v", step.operation, err)
		}
		if err := repo.CompleteProviderCall(ctx, call, step.outcome, 0, 0, now); err != nil {
			t.Fatal(err)
		}
	}
	usage, err = repo.ReadProviderUsage(ctx, researchapp.ShopifyCatalogAPIID)
	if err != nil || usage.Requests24h != 4 || len(usage.Failures24h) != 1 ||
		usage.Failures24h[0].ReasonCode != "RATE_LIMITED" || usage.Failures24h[0].Count != 1 {
		t.Fatalf("four calls, one of them a named failure: usage=%+v err=%v", usage, err)
	}
	if _, err := repo.UpdateProviderControl(ctx, researchapp.ShopifyCatalogAPIID, "98000000-0000-4000-8000-000000000001", false, usage.Control.Version, now); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if _, err := repo.ReserveProviderCall(ctx, researchapp.ShopifyCatalogAPIID, "DETAIL", now); err == nil {
		t.Fatal("Shopify was admitted while switched off")
	}
	// A call refused by the switch is visible, and is not counted as Shopify traffic.
	usage, err = repo.ReadProviderUsage(ctx, researchapp.ShopifyCatalogAPIID)
	if err != nil || usage.Requests24h != 4 {
		t.Fatalf("a local refusal is not a Shopify call: usage=%+v err=%v", usage, err)
	}
}

// Mall public-page and public-JSON API products get control and quota rows
// from migration 119 so the operator switch, local caps and ledger work for
// them exactly as for the provider APIs.
func TestMallAPIRowsExistAndAdmitCalls(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, url)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	now := time.Now().UTC()
	for _, id := range []string{"KURLY_JSON", "ZIGZAG_HTML", "LOTTEON_HTML", "DAISOMALL_HTML"} {
		usage, err := repo.ReadProviderUsage(ctx, id)
		if err != nil || !usage.Control.Enabled || usage.Control.Version != 1 || usage.Quota != nil {
			t.Fatalf("%s usage=%+v err=%v", id, usage, err)
		}
		call, err := repo.ReserveProviderCall(ctx, id, "SEARCH", now)
		if err != nil {
			t.Fatalf("%s reserve: %v", id, err)
		}
		if err := repo.CompleteProviderCall(ctx, call, "SUCCESS", 200, 0, now); err != nil {
			t.Fatal(err)
		}
	}
	// The paid Actor rows exist but start Off: nothing is bought until the
	// owner turns a mall on in the operator screen.
	for _, id := range []string{"APIFY_MUSINSA", "APIFY_29CM", "APIFY_GMARKET"} {
		usage, err := repo.ReadProviderUsage(ctx, id)
		if err != nil || usage.Control.Enabled {
			t.Fatalf("%s usage=%+v err=%v", id, usage, err)
		}
		if _, err := repo.ReserveProviderCall(ctx, id, "SEARCH", now); err == nil {
			t.Fatalf("%s admitted a call while switched off", id)
		}
		control, err := repo.UpdateProviderControl(ctx, id, "98000000-0000-4000-8000-000000000001", true, usage.Control.Version, now)
		if err != nil || !control.Enabled {
			t.Fatalf("%s enable: control=%+v err=%v", id, control, err)
		}
		call, err := repo.ReserveProviderCall(ctx, id, "SEARCH", now)
		if err != nil {
			t.Fatalf("%s reserve after enabling: %v", id, err)
		}
		if err := repo.CompleteProviderCall(ctx, call, "SUCCESS", 200, 0, now); err != nil {
			t.Fatal(err)
		}
	}
	// Daiso Mall follows its published crawl-delay through the per-minute cap.
	if _, err := repo.ReserveProviderCall(ctx, "DAISOMALL_HTML", "DETAIL", now.Add(time.Second)); err == nil {
		t.Fatal("Daiso Mall crawl delay must wait 30 seconds even below the minute cap")
	}
	call, err := repo.ReserveProviderCall(ctx, "DAISOMALL_HTML", "DETAIL", now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteProviderCall(ctx, call, "SUCCESS", 200, 0, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReserveProviderCall(ctx, "DAISOMALL_HTML", "DETAIL", now.Add(31*time.Second)); err == nil {
		t.Fatal("third call must wait")
	}
	summary, err := repo.ReadResearchRoundSummary(ctx, now.Add(-time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range summary.APICalls {
		if row.Outcome == "SUCCESS" && row.Billable {
			seen[row.APIID] = true
		}
	}
	if !seen["KURLY_JSON"] || !seen["ZIGZAG_HTML"] || !seen["LOTTEON_HTML"] || !seen["DAISOMALL_HTML"] {
		t.Fatalf("mall API calls missing from the summary: %+v", summary.APICalls)
	}
}
