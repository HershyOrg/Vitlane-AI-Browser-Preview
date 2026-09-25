package app

import (
	"context"
	"testing"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

type workspaceExternalStateRepository struct {
	*liveReviewWorkspaceRepositoryV2
	feedback  PurchaseFeedback
	reactions []researchdomain.ProductReaction
	reads     int
}

func (r *workspaceExternalStateRepository) ReadPurchaseFeedback(context.Context, string, string) (PurchaseFeedback, error) {
	r.reads++
	return r.feedback, nil
}
func (r *workspaceExternalStateRepository) MarkExternalPurchase(context.Context, MarkExternalPurchaseInput, time.Time) (PurchaseFeedback, error) {
	return r.feedback, nil
}
func (r *workspaceExternalStateRepository) PurchaseFeedbackForPlan(context.Context, string, string) (PurchaseFeedback, error) {
	return r.feedback, nil
}
func (r *workspaceExternalStateRepository) SaveProductReaction(context.Context, string, string, researchdomain.ProductReaction, int64) (researchdomain.ProductReaction, error) {
	return researchdomain.ProductReaction{}, nil
}
func (r *workspaceExternalStateRepository) ListProductReactions(context.Context, string, string) ([]researchdomain.ProductReaction, error) {
	return r.reactions, nil
}

// The DB-only workspace read carries what an external product card used to ask
// for on its own: the curation's purchase records, saved Korean reactions,
// which candidates may still react, and the saved option version.
func TestWorkspaceReadCarriesExternalCardState(t *testing.T) {
	now := time.Now().UTC()
	clock := &liveReviewClockV2{now: now}
	service, _ := NewLiveCatalogReviewServiceV2(&liveReviewGatewayV2{clock: clock}, clock, LiveCatalogReviewConfigV2{MaximumCallsPerWindow: 10, Window: time.Minute, MaximumConcurrent: 1})
	amazonRef := researchdomain.SourceProductRef{Source: researchdomain.SourceAmazon, Marketplace: "US", AnchorASIN: "B012345678"}
	koreanRef := researchdomain.SourceProductRef{Source: researchdomain.SourceCoupang, Marketplace: "KR", ProductID: "8825648110"}
	amount := int64(64000)
	observation := &researchdomain.ExternalProductObservation{
		SchemaVersion: "vitlane.external-product-observation.v1", ProductRef: koreanRef,
		ProductURL: "https://www.coupang.com/vp/products/8825648110", Title: "라미 사파리 만년필",
		Price: researchdomain.VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &amount, Currency: "KRW"}, PriceScope: "PRODUCT",
		Seller:     researchdomain.ObservedSeller{Kind: "UNKNOWN"},
		Provenance: researchdomain.ProductProvenance{APIProvider: "OpenWebNinja", APIProduct: "Real-Time Product Search v2", DiscoveryChannel: "GOOGLE_SHOPPING", Country: "KR", QueryLanguage: "ko"},
		ObservedAt: now,
	}
	reaction := researchdomain.ProductReaction{TargetID: "target", CandidateID: "coupang-card", ProductRef: koreanRef, Pinned: true, Sentiment: "LIKE", Version: 2, UpdatedAt: now}
	feedback := PurchaseFeedback{SchemaVersion: "vitlane.external-purchase-feedback.v4", Version: 3, Records: []ExternalPurchaseRecord{{
		CandidateID: "anchor-card", VariantRef: researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B987654321"},
		Checked: true, Version: 1, RecordedAt: now, Evidence: "SELF_REPORTED",
	}}}
	state := CatalogWorkspaceStoredStateV2{
		Pools: []CatalogPoolMetadataV2{{TargetID: "target", Version: 1}},
		Candidates: []CatalogCandidateReferenceV2{
			{UserID: "owner", CurationID: "curation", CandidateID: "anchor-card", PlanTargetID: "target", ProviderProductID: amazonRef.IdentityKey(), SourceKind: "AMAZON", IdentityKey: amazonRef.IdentityKey(), Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL, ProductURL: &CatalogProductURLLocator{CanonicalURL: "https://www.amazon.com/dp/B012345678"}}, Visible: true},
			{UserID: "owner", CurationID: "curation", CandidateID: "coupang-card", PlanTargetID: "target", ProviderProductID: koreanRef.IdentityKey(), SourceKind: "COUPANG", IdentityKey: koreanRef.IdentityKey(), ExternalObservation: observation, Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL, ProductURL: &CatalogProductURLLocator{CanonicalURL: observation.ProductURL}}, Visible: true},
		},
		Configurations:      []CatalogCandidateConfigurationV2{{TargetID: "target", CandidateID: "anchor-card", VariantID: "B987654321", SelectedOptions: []string{"Color:Blue"}, ObservedAt: now, UpdatedAt: now, Version: 4}},
		ProductInteractions: []researchdomain.ProductReaction{reaction},
	}
	repository := &workspaceExternalStateRepository{liveReviewWorkspaceRepositoryV2: &liveReviewWorkspaceRepositoryV2{state: state}, feedback: feedback, reactions: []researchdomain.ProductReaction{reaction}}
	service.workspace = repository

	view, err := service.LoadWorkspaceV2(context.Background(), "owner", "curation", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if repository.reads != 1 {
		t.Fatalf("one feedback read per workspace read: %d", repository.reads)
	}
	if view.PurchaseFeedback.Version != 3 || len(view.PurchaseFeedback.Records) != 1 || !view.PurchaseFeedback.Records[0].Checked {
		t.Fatalf("purchase feedback: %#v", view.PurchaseFeedback)
	}
	if len(view.ProductReactions) != 1 || view.ProductReactions[0].CandidateID != "coupang-card" || view.ProductReactions[0].Version != 2 {
		t.Fatalf("product reactions: %#v", view.ProductReactions)
	}
	if len(view.ReactionAllowedCandidateIDs) != 1 || view.ReactionAllowedCandidateIDs[0] != "coupang-card" {
		t.Fatalf("only the Korean product may react: %#v", view.ReactionAllowedCandidateIDs)
	}
	if len(view.Configurations) != 1 || view.Configurations[0].Version != 4 || view.Configurations[0].Variant.VariantID != "B987654321" {
		t.Fatalf("saved option version: %#v", view.Configurations)
	}
	// A candidate that already resolved a Variant cannot take a product reaction.
	state.Configurations = append(state.Configurations, CatalogCandidateConfigurationV2{TargetID: "target", CandidateID: "coupang-card", VariantID: "variant", ObservedAt: now, UpdatedAt: now, Version: 1})
	repository.liveReviewWorkspaceRepositoryV2 = &liveReviewWorkspaceRepositoryV2{state: state}
	view, err = service.LoadWorkspaceV2(context.Background(), "owner", "curation", "", "", false)
	if err != nil || len(view.ReactionAllowedCandidateIDs) != 0 {
		t.Fatalf("resolved variant still allowed a product reaction: %#v %v", view.ReactionAllowedCandidateIDs, err)
	}
}
