package postgres

import (
	"context"
	"encoding/json"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/curation/research/infra/catalogstub"
	"github.com/vitlane/vitlane/server/internal/curation/research/infra/openwebninja"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

type amazonPipelineClock struct{}

func (amazonPipelineClock) Now() time.Time { return time.Now().UTC() }

// The real adapter, application, fenced pool repository and purchase repository run together.
// Only the HTTP upstream and pre-existing Shopify fixture are synthetic.
func TestAmazonPipelineSearchSelectRecordAndReload(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, databaseURL)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	const user = "98000000-0000-4000-8000-000000000001"
	const curation = "98000000-0000-4000-8000-000000000003"
	const target = "98000000-0000-4000-8000-000000000004"
	if _, err := db.DB.ExecContext(ctx, `UPDATE plan_targets SET title='wireless headset',normalized_intent='wireless headset',category='electronics' WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		if r.URL.Path == "/usage" {
			data = map[string]any{"plan": map[string]any{"is_free": true}, "quotas": []any{map[string]any{"limit": 100, "used": 0, "remaining": 100, "reset_at": time.Now().Add(time.Hour).Format(time.RFC3339)}}}
		} else {
			calls++
			asin := r.URL.Query().Get("asin")
			if asin == "" {
				asin = "B012345678"
			}
			p := map[string]any{"asin": asin, "country": "US", "product_title": "wireless headset", "product_price": "79.99", "currency": "USD", "product_url": "https://www.amazon.com/dp/" + asin, "product_variations": map[string]any{"color": []any{map[string]any{"asin": "B987654321", "value": "Blue", "is_available": true}}}}
			if r.URL.Path == "/realtime-amazon-data/search" {
				data = map[string]any{"country": "US", "products": []any{p}}
			} else {
				data = p
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "OK", "data": data})
	}))
	defer srv.Close()
	adapter, err := openwebninja.New(openwebninja.Config{APIKey: "test-only", Client: srv.Client(), Usage: repo, Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	shop, err := catalogstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	service, err := researchapp.NewLiveCatalogReviewServiceV2(shop, amazonPipelineClock{}, researchapp.LiveCatalogReviewConfigV2{MaximumCallsPerWindow: 20, Window: time.Minute, MaximumConcurrent: 2})
	if err != nil {
		t.Fatal(err)
	}
	_ = service.EnableWorkspaceRepositoryV2(repo)
	service.EnableAmazon(adapter)
	searched, err := service.SearchWorkspaceV2(ctx, researchapp.CatalogWorkspaceSearchInputV2{UserID: user, CurationID: curation, TargetID: target, Mode: researchapp.CatalogResearchAppendV2, IdempotencyKey: "amazon-pipeline", Search: researchapp.LiveCatalogReviewSearchInputV2{Query: "wireless headset", Limit: 8}})
	if err != nil {
		t.Fatal(err)
	}
	candidate := ""
	for _, p := range searched.Search.Products {
		if p.Source() == researchdomain.SourceAmazon {
			candidate = p.ProviderProductID
		}
	}
	if candidate == "" {
		t.Fatalf("Amazon missing from real finalize: coverage=%#v", searched.Metrics.SourceCoverage)
	}
	time.Sleep(time.Second)
	page, err := service.BrowseWorkspaceVariantsV2(ctx, user, curation, candidate, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SaveWorkspaceConfigurationV2(ctx, researchapp.CatalogSaveConfigurationInputV2{UserID: user, CurationID: curation, CandidateID: candidate, VariantID: "B987654321", ObservedAt: time.Now(), RelationToken: page.RelationToken}); err != nil {
		t.Fatal(err)
	}

	preference := researchapp.CatalogSaveVariantInteractionInputV2{UserID: user, CurationID: curation, CandidateID: candidate, VariantID: "B987654321", Pinned: true, Sentiment: "LIKE", LikedSnapshot: &researchapp.CatalogLikedVariantSnapshotV2{ProductTitle: "wireless headset", VariantTitle: "Blue", ProductURL: "https://www.amazon.com/dp/B987654321", Merchant: "Amazon", PriceUnknown: true, Currency: "USD", TargetTitle: "wireless headset"}}
	if err = service.SaveWorkspaceInteractionV2(ctx, preference); err != nil {
		t.Fatal(err)
	}
	liked, err := repo.ListLikedVariantsV2(ctx, user, 50)
	if err != nil || len(liked) != 1 || !liked[0].PriceUnknown || liked[0].VariantID != "B987654321" {
		t.Fatalf("Amazon account like: %#v %v", liked, err)
	}
	stored, err := repo.LoadCatalogWorkspaceStateV2(ctx, user, curation)
	if err != nil || len(stored.Interactions) != 1 || !stored.Interactions[0].Pinned || stored.Interactions[0].Sentiment != "LIKE" {
		t.Fatalf("Amazon persisted reaction: %#v %v", stored.Interactions, err)
	}
	preference.VariantID = "B111111111"
	if err = service.SaveWorkspaceInteractionV2(ctx, preference); err == nil {
		t.Fatal("unrelated ASIN reaction accepted")
	}
	preference.VariantID = "B987654321"
	preference.Sentiment = "DISLIKE"
	preference.Pinned = false
	if err = service.SaveWorkspaceInteractionV2(ctx, preference); err != nil {
		t.Fatal(err)
	}
	liked, err = repo.ListLikedVariantsV2(ctx, user, 50)
	if err != nil || len(liked) != 0 {
		t.Fatalf("unlike was not removed from account: %#v %v", liked, err)
	}
	stored, err = repo.LoadCatalogWorkspaceStateV2(ctx, user, curation)
	if err != nil || len(stored.Interactions) != 1 || stored.Interactions[0].Pinned || stored.Interactions[0].Sentiment != "DISLIKE" {
		t.Fatalf("Amazon dislike/unpin reload: %#v %v", stored.Interactions, err)
	}
	feedback, err := service.MarkExternalPurchase(ctx, researchapp.MarkExternalPurchaseInput{UserID: user, CurationID: curation, CandidateID: candidate, VariantRef: researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B987654321"}, Checked: true, IdempotencyKey: "purchase-pipeline"})
	if err != nil || len(feedback.Records) != 1 || !feedback.Records[0].Checked {
		t.Fatalf("purchase %#v %v", feedback, err)
	}
	if calls != 2 {
		t.Fatalf("configuration/check called provider: %d", calls)
	}
	time.Sleep(time.Second)
	hydrated, err := service.HydrateWorkspaceV2(ctx, researchapp.CatalogResearchHydrationInputV2{UserID: user, CurationID: curation, TargetID: target, CandidateID: candidate, Scope: researchapp.CatalogResearchHydrationCandidateV2, Source: researchdomain.SourceAmazon})
	if err != nil {
		t.Fatal(err)
	}
	p := hydrated.Pools[0].Products[0]
	if p.ProviderProductID != candidate || p.ProductRef().AnchorASIN != "B012345678" || p.VariantObservation == nil || p.VariantObservation.VariantRef.ASIN != "B987654321" {
		t.Fatalf("reload changed selection/anchor: %#v", p)
	}
	usage, err := repo.ReadAmazonUsage(ctx)
	if err != nil || usage.Requests24h != 4 || usage.Succeeded24h != 4 || usage.EstimatedRemaining == nil || *usage.EstimatedRemaining != 97 {
		t.Fatalf("usage %#v %v", usage, err)
	}
}
