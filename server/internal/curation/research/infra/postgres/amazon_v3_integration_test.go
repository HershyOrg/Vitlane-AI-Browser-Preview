package postgres

import (
	"context"
	"encoding/json"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAmazonPurchaseQuotaAndRelationsPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, url)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	const user = "98000000-0000-4000-8000-000000000001"
	const curation = "98000000-0000-4000-8000-000000000003"
	const target = "98000000-0000-4000-8000-000000000004"
	const candidate = "candidate-amazon"
	now := time.Now().UTC()
	for _, sql := range []string{
		`INSERT INTO phase8_research_pools(user_id,curation_id,plan_target_id,version,expand_ordinal,created_at,updated_at) VALUES($1,$2,$3,1,0,now(),now())`,
		`INSERT INTO phase8_research_candidates(user_id,curation_id,plan_target_id,candidate_id,provider_product_id,source_kind,identity_key,locator_kind,product_url,visible,display_order,first_seen_at,last_seen_at) VALUES($1,$2,$3,'candidate-amazon','amazon:US:B012345678','AMAZON','amazon:US:B012345678','PRODUCT_URL','https://www.amazon.com/dp/B012345678',true,0,now(),now())`,
	} {
		if _, err := db.DB.ExecContext(ctx, sql, user, curation, target); err != nil {
			t.Fatal(err)
		}
	}
	config := researchapp.CatalogCandidateConfigurationV2{TargetID: target, CandidateID: candidate, VariantID: "B987654321", SelectedOptions: []string{"Color:Blue"}, ObservedAt: now, UpdatedAt: now}
	if err := repo.SaveAmazonConfiguration(ctx, user, curation, config, 0); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAmazonConfiguration(ctx, user, curation, config, 0); err == nil {
		t.Fatal("stale configuration accepted")
	}
	grant := researchapp.AmazonRelationGrant{TokenHash: "hash", UserID: user, CurationID: curation, CandidateID: candidate, AnchorASIN: "B012345678", Variants: []researchapp.AmazonVariantOption{{ASIN: "B987654321"}}, RelationStatus: "RELATED_REFS", ObservedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := repo.SaveAmazonRelation(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReadAmazonRelation(ctx, user, curation, "wrong", "hash"); err == nil {
		t.Fatal("cross-candidate relation read")
	}
	if g, err := repo.ReadAmazonRelation(ctx, user, curation, candidate, "hash"); err != nil || g.Variants[0].ASIN != "B987654321" {
		t.Fatalf("relation: %v %v", g, err)
	}
	in := researchapp.MarkExternalPurchaseInput{UserID: user, CurationID: curation, CandidateID: candidate, VariantRef: researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B987654321"}, Checked: true, IdempotencyKey: "check-1"}
	first, err := repo.MarkExternalPurchase(ctx, in, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || len(first.Records) != 1 || !first.Records[0].Checked {
		t.Fatalf("first %#v", first)
	}
	replay, err := repo.MarkExternalPurchase(ctx, in, now)
	if err != nil || replay.Version != 1 {
		t.Fatalf("replay %#v %v", replay, err)
	}
	in.Checked = false
	if _, err = repo.MarkExternalPurchase(ctx, in, now); err == nil {
		t.Fatal("idempotency collision accepted")
	}
	in.IdempotencyKey = "undo"
	if _, err = repo.MarkExternalPurchase(ctx, in, now); err == nil {
		t.Fatal("stale version accepted")
	}
	snapshot, _ := json.Marshal(first)
	in.ExpectedVersion = 1
	last, err := repo.MarkExternalPurchase(ctx, in, now)
	if err != nil || last.Version != 2 || last.Records[0].Checked {
		t.Fatalf("undo %#v %v", last, err)
	}
	var original researchapp.PurchaseFeedback
	if err = json.Unmarshal(snapshot, &original); err != nil || !original.Records[0].Checked {
		t.Fatal("immutable snapshot changed")
	}
	// Fresh process reads the exact option, record and feedback version.
	state, err := NewRepository(db).LoadCatalogWorkspaceStateV2(ctx, user, curation)
	if err != nil || state.Configurations[0].VariantID != "B987654321" {
		t.Fatalf("reload %#v %v", state, err)
	}
	in.VariantRef.ASIN = "B000000000"
	in.ExpectedVersion = 0
	in.Checked = true
	in.IdempotencyKey = "forged"
	if _, err = repo.MarkExternalPurchase(ctx, in, now); err == nil {
		t.Fatal("unselected ASIN accepted")
	}
	// A partial source refresh retains the old Amazon candidate after fresh Shopify results.
	command := catalogPoolIntegrationCommandV2(user, curation, target, "partial-source", 1, researchapp.CatalogResearchReplaceV2, 'b')
	preflight, e := repo.PreflightCatalogSearchV2(ctx, command)
	if e != nil {
		t.Fatal(e)
	}
	command = catalogPoolCommandWithReservationV2(command, preflight)
	shop := researchapp.CatalogCandidateReferenceV2{UserID: user, CurationID: curation, PlanTargetID: target, CandidateID: "shop-card", ProviderProductID: "shop-product", SourceKind: "SHOPIFY_LIVE", IdentityKey: "shopify-product:shop-product", Locator: researchapp.CatalogProductLocator{Kind: researchapp.CatalogLocatorProductURL, ProductURL: &researchapp.CatalogProductURLLocator{CanonicalURL: "https://shop.example/products/headset"}}, Assessment: researchapp.LiveCandidateAssessmentV2{Features: []string{}, Specifications: []string{}}}
	_, e = repo.CompleteCatalogSearchV2(ctx, command, []researchapp.CatalogCandidateReferenceV2{shop}, researchapp.LiveCatalogReviewMetricsV2{CompletedAt: now, SourceCoverage: []researchapp.SourceCoverage{{Source: researchdomain.SourceShopify, Status: "SUCCEEDED", CandidateCount: 1}, {Source: researchdomain.SourceAmazon, Status: "FAILED", ReasonCode: "AMAZON_QUOTA_EXHAUSTED"}}})
	if e != nil {
		t.Fatal(e)
	}
	state, e = repo.LoadCatalogWorkspaceStateV2(ctx, user, curation)
	if e != nil || len(state.Candidates) != 2 || !state.Candidates[0].Visible || !state.Candidates[1].Visible || len(state.Pools[0].SourceCoverage) != 2 {
		t.Fatalf("partial source erased saved choices: %#v %v", state, e)
	}
	// The database rejects even a direct write attempting to bypass the Cart application guard.
	_, e = db.DB.ExecContext(ctx, `INSERT INTO phase8_cart_items(user_id,curation_id,cart_item_id,plan_target_id,candidate_id,product_title_snapshot,variant_id,variant_title_snapshot,preview_price_minor,preview_currency,quantity,observed_at,added_at) VALUES($1,$2,'bad-cart',$3,'candidate-amazon','Headset','B987654321','Blue',1,'USD',1,now(),now())`, user, curation, target)
	if e == nil || !strings.Contains(e.Error(), "EXTERNAL_PRODUCT_CART_FORBIDDEN") {
		t.Fatalf("Cart source guard: %v", e)
	}
	// Removing a candidate is not a retraction of its self-reported purchase history.
	if _, e = db.DB.ExecContext(ctx, `DELETE FROM phase8_research_candidates WHERE user_id=$1 AND curation_id=$2 AND candidate_id=$3`, user, curation, candidate); e != nil {
		t.Fatal(e)
	}
	feedback, e := repo.ReadPurchaseFeedback(ctx, user, curation)
	if e != nil || len(feedback.Records) != 1 || feedback.Version != 2 {
		t.Fatalf("deleted purchase history: %#v %v", feedback, e)
	}
	// API quota is shared between repository instances and counts attempted requests.
	quota := researchapp.CatalogAPIQuota{Limit: 2, Remaining: 2, ResetAt: now.Add(time.Hour), ObservedAt: now, IsFree: true}
	if err = repo.SaveAmazonQuota(ctx, quota, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	id, err := repo.ReserveAmazonCall(ctx, "SEARCH", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewRepository(db).ReserveAmazonCall(ctx, "DETAIL", now); err == nil {
		t.Fatal("shared rate guard bypassed")
	}
	if err = repo.CompleteAmazonCall(ctx, id, "AMAZON_SCHEMA_MISMATCH", now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	usage, err := repo.ReadAmazonUsage(ctx)
	if err != nil || usage.EstimatedRemaining == nil || *usage.EstimatedRemaining != 1 || usage.LastFailureCode != "AMAZON_SCHEMA_MISMATCH" {
		t.Fatalf("usage %#v %v", usage, err)
	}
	id, err = repo.ReserveAmazonCall(ctx, "DETAIL", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	_ = repo.CompleteAmazonCall(ctx, id, "SUCCESS", now.Add(3*time.Second))
	if _, err = repo.ReserveAmazonCall(ctx, "DETAIL", now.Add(4*time.Second)); err == nil {
		t.Fatal("quota exceeded")
	}
}
