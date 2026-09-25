package app

import (
	"context"
	"encoding/json"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"testing"
	"time"
)

type amazonTestGateway struct {
	calls   int
	product CatalogProductObservation
}

func (g *amazonTestGateway) SearchAmazon(context.Context, AmazonSearchRequest) (AmazonSearchResult, error) {
	return AmazonSearchResult{Products: []CatalogProductObservation{g.product}}, nil
}
func (g *amazonTestGateway) LookupAmazon(_ context.Context, ref researchdomain.SourceVariantRef) (AmazonProductDetail, error) {
	g.calls++
	p := g.product
	o := *p.VariantObservation
	o.VariantRef = ref
	o.ProductURL, _ = ref.ExternalURL()
	p.VariantObservation = &o
	return AmazonProductDetail{Product: p, Variants: []AmazonVariantOption{{ASIN: "B987654321", Labels: []LiveVariantOptionV2{{Name: "Color", Value: "Blue"}}}}, RelationStatus: "RELATED_REFS"}, nil
}
func (g *amazonTestGateway) RefreshAmazonUsage(context.Context) (CatalogAPIUsage, error) {
	return CatalogAPIUsage{}, nil
}

type amazonTestWorkspace struct {
	*liveReviewWorkspaceRepositoryV2
	grants map[string]AmazonRelationGrant
	saved  CatalogCandidateConfigurationV2
}

func (r *amazonTestWorkspace) ResolveCatalogCandidateV2(_ context.Context, user, curation, candidate string) (CatalogCandidateReferenceV2, error) {
	for _, v := range r.state.Candidates {
		if v.UserID == user && v.CurationID == curation && v.CandidateID == candidate {
			return v, nil
		}
	}
	return CatalogCandidateReferenceV2{}, ErrCatalogCandidateNotFoundV2
}
func (r *amazonTestWorkspace) SaveAmazonRelation(_ context.Context, g AmazonRelationGrant) error {
	r.grants[g.TokenHash] = g
	return nil
}
func (r *amazonTestWorkspace) ReadAmazonRelation(_ context.Context, u, c, id, hash string) (AmazonRelationGrant, error) {
	g, ok := r.grants[hash]
	if !ok || g.UserID != u || g.CurationID != c || g.CandidateID != id {
		return g, ErrCatalogCandidateNotFoundV2
	}
	return g, nil
}
func (r *amazonTestWorkspace) SaveAmazonConfiguration(_ context.Context, _, _ string, v CatalogCandidateConfigurationV2, _ int64) error {
	r.saved = v
	return nil
}
func (r *amazonTestWorkspace) SaveCatalogVariantPreferenceV2(_ context.Context, _, _ string, value CatalogVariantInteractionV2, _ *researchdomain.LikedVariantV2) error {
	r.state.Interactions = append(r.state.Interactions, value)
	return nil
}
func TestAmazonVariantGrantAndStableCandidateIdentity(t *testing.T) {
	now := time.Now().UTC()
	clock := &liveReviewClockV2{now: now}
	shop := &liveReviewGatewayV2{clock: clock}
	s, _ := NewLiveCatalogReviewServiceV2(shop, clock, LiveCatalogReviewConfigV2{MaximumCallsPerWindow: 10, Window: time.Minute, MaximumConcurrent: 1})
	ref := researchdomain.SourceProductRef{Source: researchdomain.SourceAmazon, Marketplace: "US", AnchorASIN: "B012345678"}
	variant := researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: ref.AnchorASIN}
	url, _ := variant.ExternalURL()
	amount := int64(12999)
	p := CatalogProductObservation{ProviderProductID: ref.IdentityKey(), SourceProductRef: &ref, Title: "Headset", VariantObservation: &researchdomain.VariantObservation{VariantRef: variant, Price: researchdomain.VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &amount, Currency: "USD"}, ProductURL: url}, Locator: &CatalogProductLocator{Kind: CatalogLocatorProductURL, ProductURL: &CatalogProductURLLocator{CanonicalURL: url}}}
	gateway := &amazonTestGateway{product: p}
	s.EnableAmazon(gateway)
	repository := &amazonTestWorkspace{liveReviewWorkspaceRepositoryV2: &liveReviewWorkspaceRepositoryV2{state: CatalogWorkspaceStoredStateV2{Candidates: []CatalogCandidateReferenceV2{{UserID: "owner", CurationID: "curation", CandidateID: "anchor-card", PlanTargetID: "target", ProviderProductID: ref.IdentityKey(), SourceKind: "AMAZON", IdentityKey: ref.IdentityKey(), Locator: *p.Locator, Visible: true}}}}, grants: map[string]AmazonRelationGrant{}}
	s.workspace = repository
	page, err := s.BrowseWorkspaceVariantsV2(context.Background(), "owner", "curation", "anchor-card", "")
	if err != nil || len(page.Rows) != 2 || gateway.calls != 1 {
		t.Fatalf("one detail + direct refs: %#v %v", page, err)
	}
	exact := researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B987654321"}
	resolved, err := s.ResolveAmazonVariant(context.Background(), "owner", "curation", "anchor-card", page.RelationToken, exact)
	if err != nil || resolved.ProviderProductID != "anchor-card" || resolved.ProductRef().AnchorASIN != ref.AnchorASIN || resolved.VariantObservation.VariantRef.ASIN != exact.ASIN {
		t.Fatalf("anchor changed: %#v %v", resolved, err)
	}
	calls := gateway.calls
	for _, user := range []string{"other", "owner"} {
		forged := exact
		forged.ASIN = "B000000000"
		if _, err = s.ResolveAmazonVariant(context.Background(), user, "curation", "anchor-card", page.RelationToken, forged); err == nil {
			t.Fatal("forged relation accepted")
		}
	}
	if gateway.calls != calls {
		t.Fatal("forged selection called provider")
	}
	reaction := CatalogSaveVariantInteractionInputV2{UserID: "owner", CurationID: "curation", CandidateID: "anchor-card", VariantID: exact.ASIN, Pinned: true, Sentiment: "NONE"}
	if err = s.SaveWorkspaceInteractionV2(context.Background(), reaction); err == nil {
		t.Fatal("unsaved variant reaction accepted without relation proof")
	}
	reaction.RelationToken = page.RelationToken
	clock.now = now.Add(16 * time.Minute)
	if err = s.SaveWorkspaceInteractionV2(context.Background(), reaction); err == nil {
		t.Fatal("expired relation accepted for a new variant reaction")
	}
	clock.now = now
	forgedReaction := reaction
	forgedReaction.VariantID = "B000000000"
	if err = s.SaveWorkspaceInteractionV2(context.Background(), forgedReaction); err == nil {
		t.Fatal("unrelated ASIN accepted for reaction")
	}
	forgedReaction = reaction
	forgedReaction.UserID = "other"
	if err = s.SaveWorkspaceInteractionV2(context.Background(), forgedReaction); err == nil {
		t.Fatal("another owner reused reaction grant")
	}
	if err = s.SaveWorkspaceInteractionV2(context.Background(), reaction); err != nil {
		t.Fatalf("verified draft variant reaction failed: %v", err)
	}
	if repository.saved.VariantID != "" || gateway.calls != calls {
		t.Fatal("reaction must not save configuration or call provider")
	}
	in := CatalogSaveConfigurationInputV2{UserID: "owner", CurationID: "curation", CandidateID: "anchor-card", VariantID: exact.ASIN, RelationToken: page.RelationToken, ObservedAt: now, SelectedOptions: []string{"forged"}}
	if err = s.SaveWorkspaceConfigurationV2(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if repository.saved.SelectedOptions[0] != "Color:Blue" {
		t.Fatal("trusted client option label")
	}
	clock.now = now.Add(16 * time.Minute)
	if err = s.SaveWorkspaceConfigurationV2(context.Background(), in); err == nil {
		t.Fatal("expired relationship accepted")
	}
	repository.state.Configurations = []CatalogCandidateConfigurationV2{repository.saved}
	if link, err := s.ResolveExternalLink(context.Background(), "owner", "curation", "anchor-card"); err != nil || link != "https://www.amazon.com/dp/B987654321" || gateway.calls != calls {
		t.Fatalf("external link called provider: %s %v", link, err)
	}
}

type purchaseSnapshotRepo struct {
	Repository
	ExternalPurchaseRepository
	feedback PurchaseFeedback
}

func (r *purchaseSnapshotRepo) PurchaseFeedbackForPlan(context.Context, string, string) (PurchaseFeedback, error) {
	return r.feedback, nil
}
func TestPurchaseFeedbackOnlyChangesFutureContextSnapshot(t *testing.T) {
	repo := &purchaseSnapshotRepo{feedback: PurchaseFeedback{Version: 1, Records: []ExternalPurchaseRecord{{Checked: true, Version: 1, VariantRef: researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B012345678"}}}}}
	s := &Service{repository: repo}
	first, err := s.attachPurchaseFeedback(context.Background(), "owner", "plan", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	repo.feedback.Version = 2
	repo.feedback.Records[0].Checked = false
	second, err := s.attachPurchaseFeedback(context.Background(), "owner", "plan", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var a, b ResearchContext
	_ = json.Unmarshal(first, &a)
	_ = json.Unmarshal(second, &b)
	if a.PurchaseFeedbackVersion != 1 || len(a.AlreadyPurchased) != 1 || b.PurchaseFeedbackVersion != 2 || len(b.AlreadyPurchased) != 0 {
		t.Fatalf("snapshots: %s %s", first, second)
	}
}
