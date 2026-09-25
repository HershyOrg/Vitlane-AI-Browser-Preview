package app

import (
	"context"
	"errors"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"testing"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

type productReactionFake struct {
	CatalogWorkspaceRepositoryV2
	candidate CatalogCandidateReferenceV2
	state     CatalogWorkspaceStoredStateV2
	writes    int
	saved     researchdomain.ProductReaction
}

func (r *productReactionFake) ResolveCatalogCandidateV2(_ context.Context, user, curation, candidate string) (CatalogCandidateReferenceV2, error) {
	if user != r.candidate.UserID || curation != r.candidate.CurationID || candidate != r.candidate.CandidateID {
		return CatalogCandidateReferenceV2{}, ErrCatalogCandidateNotFoundV2
	}
	return r.candidate, nil
}
func (r *productReactionFake) LoadCatalogWorkspaceStateV2(context.Context, string, string) (CatalogWorkspaceStoredStateV2, error) {
	return r.state, nil
}
func (r *productReactionFake) ListProductReactions(context.Context, string, string) ([]researchdomain.ProductReaction, error) {
	return r.state.ProductInteractions, nil
}
func (r *productReactionFake) SaveProductReaction(_ context.Context, _, _ string, v researchdomain.ProductReaction, expected int64) (researchdomain.ProductReaction, error) {
	if expected != r.saved.Version {
		return v, errors.New("stale")
	}
	v.Version++
	r.saved = v
	r.writes++
	return v, nil
}
func productReactionFixture() (*productReactionFake, *LiveCatalogReviewServiceV2, SaveProductReactionInput) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	ref := researchdomain.SourceProductRef{Source: researchdomain.SourceElevenStreet, Marketplace: "KR", ProductID: "12345"}
	observation := &researchdomain.ExternalProductObservation{SchemaVersion: "vitlane.external-product-observation.v1", ProductRef: ref, ProductURL: "https://www.11st.co.kr/products/12345", Title: "Original product", PriceScope: "PRODUCT", Price: researchdomain.VariantObservedPrice{Kind: "UNKNOWN", ReasonCode: "PRICE_NOT_REPORTED"}, Seller: researchdomain.ObservedSeller{Kind: "UNKNOWN"}, Provenance: researchdomain.ProductProvenance{APIProvider: "fixture", APIProduct: "fixture", DiscoveryChannel: "NAVER_WEB", Country: "KR", QueryLanguage: "ko"}, ObservedAt: now}
	r := &productReactionFake{candidate: CatalogCandidateReferenceV2{UserID: "user", CurationID: "curation", PlanTargetID: "target", CandidateID: "candidate", SourceKind: "ELEVENST", ProviderProductID: ref.IdentityKey(), ExternalObservation: observation}}
	s := &LiveCatalogReviewServiceV2{workspace: r, clock: &catalogPreferenceClockV2{now: now}}
	return r, s, SaveProductReactionInput{UserID: "user", CurationID: "curation", CandidateID: "candidate", ProductRef: ref, Pinned: true, Sentiment: "LIKE"}
}
func TestProductReactionUsesOriginalProductWithoutVariant(t *testing.T) {
	r, s, input := productReactionFixture()
	v, err := s.SaveProductReaction(context.Background(), input)
	if err != nil || v.Version != 1 || r.writes != 1 || v.ProductRef != input.ProductRef || v.LikedSnapshot == nil || v.LikedSnapshot.Price.Kind != "UNKNOWN" {
		t.Fatalf("reaction=%+v err=%v", v, err)
	}
	input.Sentiment = "DISLIKE"
	input.ExpectedVersion = 1
	v, err = s.SaveProductReaction(context.Background(), input)
	if err != nil || !v.Pinned || v.LikedSnapshot != nil || v.Sentiment != "DISLIKE" {
		t.Fatalf("dislike=%+v err=%v", v, err)
	}
	if err = s.SaveWorkspaceInteractionV2(context.Background(), CatalogSaveVariantInteractionInputV2{UserID: input.UserID, CurationID: input.CurationID, CandidateID: input.CandidateID, VariantID: "invented-variant", Sentiment: "NONE"}); err == nil {
		t.Fatal("external product accepted invented Variant")
	}
}
func TestProductReactionRejectsKnownVariantWrongOwnerAndUnconfirmedSource(t *testing.T) {
	tests := []struct {
		name   string
		change func(*productReactionFake, *SaveProductReactionInput)
	}{
		{"configured Variant", func(r *productReactionFake, _ *SaveProductReactionInput) {
			r.state.Configurations = []CatalogCandidateConfigurationV2{{CandidateID: "candidate", VariantID: "exact"}}
		}},
		{"Variant preference", func(r *productReactionFake, _ *SaveProductReactionInput) {
			r.state.Interactions = []CatalogVariantInteractionV2{{CandidateID: "candidate", VariantID: "exact"}}
		}},
		{"Variant locator", func(r *productReactionFake, _ *SaveProductReactionInput) {
			r.candidate.Locator.MerchantVariant = &CatalogMerchantVariantLocator{VariantID: "exact"}
		}},
		{"Shopify without selected option", func(r *productReactionFake, _ *SaveProductReactionInput) {
			r.candidate.SourceKind = "SHOPIFY_LIVE"
			r.candidate.ExternalObservation = nil
		}},
		{"Amazon without selected option", func(r *productReactionFake, _ *SaveProductReactionInput) {
			r.candidate.SourceKind = "AMAZON"
			r.candidate.ExternalObservation = nil
		}},
		{"missing observation", func(r *productReactionFake, _ *SaveProductReactionInput) { r.candidate.ExternalObservation = nil }},
		{"different product", func(_ *productReactionFake, i *SaveProductReactionInput) { i.ProductRef.ProductID = "999" }},
		{"different owner", func(_ *productReactionFake, i *SaveProductReactionInput) { i.UserID = "other" }},
		{"different curation", func(_ *productReactionFake, i *SaveProductReactionInput) { i.CurationID = "other" }},
		{"invalid sentiment", func(_ *productReactionFake, i *SaveProductReactionInput) { i.Sentiment = "INVALID" }},
		{"stale version", func(_ *productReactionFake, i *SaveProductReactionInput) { i.ExpectedVersion = 7 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r, s, input := productReactionFixture()
			test.change(r, &input)
			if _, err := s.SaveProductReaction(context.Background(), input); err == nil || r.writes != 0 {
				t.Fatalf("err=%v writes=%d", err, r.writes)
			}
		})
	}
}
func TestProductReactionFeedbackRemainsTargetScopedAndSeparateFromVariants(t *testing.T) {
	r, s, input := productReactionFixture()
	v, err := s.SaveProductReaction(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	other := v
	other.TargetID = "other"
	other.CandidateID = "other"
	r.state.ProductInteractions = []researchdomain.ProductReaction{other, v}
	feedbackService := &Service{liveCatalog: s}
	snapshot, err := feedbackService.interactionSnapshot(context.Background(), "user", "curation", "target")
	if err != nil || len(snapshot.Variants) != 0 || len(snapshot.Products) != 1 || snapshot.Products[0].ProductRef != input.ProductRef {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}

type analyticsRecorder struct{ events []sharedapp.AnalyticsEvent }

func (r *analyticsRecorder) Record(e sharedapp.AnalyticsEvent) { r.events = append(r.events, e) }
func TestReactionAnalyticsOnlyAfterSuccessfulWrite(t *testing.T) {
	_, s, input := productReactionFixture()
	sink := &analyticsRecorder{}
	ctx := sharedapp.WithAnalyticsSink(context.Background(), sink)
	if _, err := s.SaveProductReaction(ctx, input); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].Name != "candidate_reacted" {
		t.Fatal(sink.events)
	}
	if _, err := s.SaveProductReaction(ctx, input); err == nil {
		t.Fatal("stale write accepted")
	}
	if len(sink.events) != 1 {
		t.Fatal("failed write emitted event")
	}
	_, other, input := productReactionFixture()
	if _, err := other.SaveProductReaction(context.Background(), input); err != nil {
		t.Fatal("refused analytics broke business write", err)
	}
}
