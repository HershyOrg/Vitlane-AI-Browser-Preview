package postgres

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

func TestProductReactionPostgresCASOwnershipLikeAndReload(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, url)
	seedCatalogPreferenceFixture(t, ctx, db)
	repo := NewRepository(db)
	ref := researchdomain.SourceProductRef{Source: researchdomain.SourceElevenStreet, Marketplace: "KR", ProductID: "12345"}
	observation := researchdomain.ExternalProductObservation{SchemaVersion: "vitlane.external-product-observation.v1", ProductRef: ref, ProductURL: "https://www.11st.co.kr/products/12345", Title: "Fixture product", PriceScope: "PRODUCT", Price: researchdomain.VariantObservedPrice{Kind: "UNKNOWN", ReasonCode: "PRICE_NOT_REPORTED"}, Seller: researchdomain.ObservedSeller{Kind: "UNKNOWN"}, Provenance: researchdomain.ProductProvenance{APIProvider: "fixture", APIProduct: "fixture", DiscoveryChannel: "NAVER_WEB", Country: "KR", QueryLanguage: "ko"}, ObservedAt: time.Now().UTC()}
	raw, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.DB.ExecContext(ctx, `UPDATE phase8_research_candidates SET source_kind='ELEVENST',provider_product_id=$1,identity_key=$1,product_url=$2,external_observation=$3 WHERE user_id=$4 AND curation_id=$5 AND candidate_id=$6`, ref.IdentityKey(), observation.ProductURL, raw, catalogPreferenceUserID, catalogPreferenceCurationID, catalogPreferenceCandidateID)
	if err != nil {
		t.Fatal(err)
	}
	v := researchdomain.ProductReaction{TargetID: catalogPreferenceTargetID, CandidateID: catalogPreferenceCandidateID, ProductRef: ref, Pinned: true, Sentiment: "LIKE", UpdatedAt: time.Now().UTC(), LikedSnapshot: &observation}
	v, err = repo.SaveProductReaction(ctx, catalogPreferenceUserID, catalogPreferenceCurationID, v, 0)
	if err != nil || v.Version != 1 {
		t.Fatalf("save err=%v version=%d", err, v.Version)
	}
	state, err := repo.LoadCatalogWorkspaceStateV2(ctx, catalogPreferenceUserID, catalogPreferenceCurationID)
	if err != nil || len(state.ProductInteractions) != 1 || len(state.Interactions) != 0 || len(state.Configurations) != 0 {
		t.Fatalf("reload=%+v err=%v", state, err)
	}
	retained, err := readCatalogRetainedPinnedCandidateIDsV2(ctx, db.Queryer(ctx), catalogPreferenceUserID, catalogPreferenceCurationID, catalogPreferenceTargetID, map[string]struct{}{}, []string{})
	if err != nil || len(retained) != 1 || retained[0] != catalogPreferenceCandidateID {
		t.Fatalf("product pin was not retained: %v %v", retained, err)
	}
	liked, err := repo.ListLikedProducts(ctx, catalogPreferenceUserID, 50)
	if err != nil || len(liked) != 1 || liked[0].Observation.Price.Kind != "UNKNOWN" {
		t.Fatalf("likes=%+v err=%v", liked, err)
	}
	wrong := v
	wrong.ProductRef.ProductID = "99999"
	wrong.Sentiment = "NONE"
	wrong.LikedSnapshot = nil
	if _, err = repo.SaveProductReaction(ctx, catalogPreferenceUserID, catalogPreferenceCurationID, wrong, 1); err == nil {
		t.Fatal("wrong original product accepted")
	}
	if _, err = repo.SaveProductReaction(ctx, "00000000-0000-4000-8000-000000000099", catalogPreferenceCurationID, v, 1); err == nil {
		t.Fatal("wrong owner accepted")
	}
	if other, err := repo.ListLikedProducts(ctx, "00000000-0000-4000-8000-000000000099", 50); err != nil || len(other) != 0 {
		t.Fatal("likes escaped owner scope")
	}
	// Two devices writing from the same observed version cannot overwrite one another.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := v
			next.Sentiment = "DISLIKE"
			next.LikedSnapshot = nil
			_, err := repo.SaveProductReaction(ctx, catalogPreferenceUserID, catalogPreferenceCurationID, next, 1)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("CAS winners=%d", winners)
	}
	liked, err = repo.ListLikedProducts(ctx, catalogPreferenceUserID, 50)
	if err != nil || len(liked) != 0 {
		t.Fatalf("dislike retained account like: %v %v", liked, err)
	}
	state, err = repo.LoadCatalogWorkspaceStateV2(ctx, catalogPreferenceUserID, catalogPreferenceCurationID)
	if err != nil || !state.ProductInteractions[0].Pinned || state.ProductInteractions[0].Version != 2 {
		t.Fatalf("pin/version lost: %+v %v", state, err)
	}
	// A subsequently known exact Variant disables the product fallback at SQL write time too.
	_, err = db.DB.ExecContext(ctx, `INSERT INTO phase8_variant_interactions(user_id,curation_id,plan_target_id,candidate_id,variant_id,pinned,sentiment,updated_at) VALUES ($1,$2,$3,$4,'known-variant',false,'NONE',now())`, catalogPreferenceUserID, catalogPreferenceCurationID, catalogPreferenceTargetID, catalogPreferenceCandidateID)
	if err != nil {
		t.Fatal(err)
	}
	v.Sentiment = "NONE"
	v.LikedSnapshot = nil
	if _, err = repo.SaveProductReaction(ctx, catalogPreferenceUserID, catalogPreferenceCurationID, v, 2); err == nil {
		t.Fatal("known Variant accepted product fallback")
	}
}
