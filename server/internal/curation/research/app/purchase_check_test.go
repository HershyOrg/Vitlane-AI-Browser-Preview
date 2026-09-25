package app

import (
	"context"
	"strings"
	"testing"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type purchaseCheckWorkspace struct {
	*liveReviewWorkspaceRepositoryV2
	inputs []MarkExternalPurchaseInput
}

func (w *purchaseCheckWorkspace) ResolveCatalogCandidateV2(_ context.Context, user, curation, candidate string) (CatalogCandidateReferenceV2, error) {
	for _, v := range w.state.Candidates {
		if v.UserID == user && v.CurationID == curation && v.CandidateID == candidate {
			return v, nil
		}
	}
	return CatalogCandidateReferenceV2{}, ErrCatalogCandidateNotFoundV2
}
func (w *purchaseCheckWorkspace) ReadPurchaseFeedback(context.Context, string, string) (PurchaseFeedback, error) {
	return PurchaseFeedback{}, nil
}
func (w *purchaseCheckWorkspace) PurchaseFeedbackForPlan(context.Context, string, string) (PurchaseFeedback, error) {
	return PurchaseFeedback{}, nil
}
func (w *purchaseCheckWorkspace) MarkExternalPurchase(_ context.Context, in MarkExternalPurchaseInput, _ time.Time) (PurchaseFeedback, error) {
	w.inputs = append(w.inputs, in)
	return PurchaseFeedback{Version: int64(len(w.inputs))}, nil
}

func TestMarkExternalPurchaseValidatesSnapshotBySubject(t *testing.T) {
	now := time.Now().UTC()
	clock := &liveReviewClockV2{now: now}
	service, _ := NewLiveCatalogReviewServiceV2(&liveReviewGatewayV2{clock: clock}, clock, LiveCatalogReviewConfigV2{MaximumCallsPerWindow: 10, Window: time.Minute, MaximumConcurrent: 1})
	amazonRef := researchdomain.SourceProductRef{Source: researchdomain.SourceAmazon, Marketplace: "US", AnchorASIN: "B012345678"}
	koreanRef := researchdomain.SourceProductRef{Source: researchdomain.SourceCoupang, Marketplace: "KR", ProductID: "8825648110"}
	amount := int64(64000)
	observation := &researchdomain.ExternalProductObservation{SchemaVersion: "vitlane.external-product-observation.v1", ProductRef: koreanRef, ProductURL: "https://www.coupang.com/vp/products/8825648110", Title: "라미 사파리 만년필", Price: researchdomain.VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &amount, Currency: "KRW"}, PriceScope: "PRODUCT", Seller: researchdomain.ObservedSeller{Kind: "UNKNOWN"}, Provenance: researchdomain.ProductProvenance{APIProvider: "OpenWebNinja", APIProduct: "Real-Time Product Search v2", DiscoveryChannel: "GOOGLE_SHOPPING", Country: "KR", QueryLanguage: "ko"}, ObservedAt: now}
	workspace := &purchaseCheckWorkspace{liveReviewWorkspaceRepositoryV2: &liveReviewWorkspaceRepositoryV2{state: CatalogWorkspaceStoredStateV2{Candidates: []CatalogCandidateReferenceV2{
		{UserID: "owner", CurationID: "curation", CandidateID: "anchor-card", PlanTargetID: "target", ProviderProductID: amazonRef.IdentityKey(), SourceKind: "AMAZON", IdentityKey: amazonRef.IdentityKey(), Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL, ProductURL: &CatalogProductURLLocator{CanonicalURL: "https://www.amazon.com/dp/B012345678"}}, Visible: true},
		{UserID: "owner", CurationID: "curation", CandidateID: "coupang-card", PlanTargetID: "target", ProviderProductID: koreanRef.IdentityKey(), SourceKind: "COUPANG", IdentityKey: koreanRef.IdentityKey(), ExternalObservation: observation, Locator: CatalogProductLocator{Kind: CatalogLocatorProductURL, ProductURL: &CatalogProductURLLocator{CanonicalURL: observation.ProductURL}}, Visible: true},
	}}}}
	service.workspace = workspace
	amazon := MarkExternalPurchaseInput{UserID: "owner", CurationID: "curation", CandidateID: "anchor-card", VariantRef: researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B012345678"}, Checked: true, IdempotencyKey: "amazon-1"}
	amazon.Snapshot = &researchdomain.PurchaseCheckSnapshot{ProductTitle: " Wireless Headset ", VariantTitle: "Black", Merchant: " Amazon ", PriceMinor: 12999, Currency: "usd"}
	if _, err := service.MarkExternalPurchase(context.Background(), amazon); err != nil {
		t.Fatal(err)
	}
	if len(workspace.inputs) != 1 || workspace.inputs[0].Snapshot == nil || workspace.inputs[0].Snapshot.ProductTitle != "Wireless Headset" || workspace.inputs[0].Snapshot.Merchant != "Amazon" || workspace.inputs[0].Snapshot.Currency != "USD" || workspace.inputs[0].Snapshot.PriceMinor != 12999 {
		t.Fatalf("normalized Amazon snapshot not forwarded: %#v", workspace.inputs)
	}
	for name, snapshot := range map[string]researchdomain.PurchaseCheckSnapshot{
		"empty title":   {PriceUnknown: true},
		"long variant":  {ProductTitle: "Headset", VariantTitle: strings.Repeat("x", 241), PriceUnknown: true},
		"no currency":   {ProductTitle: "Headset", PriceMinor: 1},
		"bad currency":  {ProductTitle: "Headset", PriceMinor: 1, Currency: "dollar"},
		"negative cost": {ProductTitle: "Headset", PriceMinor: -5, Currency: "USD"},
	} {
		invalid := amazon
		invalid.IdempotencyKey = "invalid-" + name
		value := snapshot
		invalid.Snapshot = &value
		_, err := service.MarkExternalPurchase(context.Background(), invalid)
		if err == nil || fault.CodeOf(err) != fault.InvalidInput {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	if len(workspace.inputs) != 1 {
		t.Fatalf("invalid snapshots reached the repository: %d", len(workspace.inputs))
	}
	// Without a snapshot the Amazon command stays valid (older clients keep the previous snapshot).
	plain := amazon
	plain.Snapshot, plain.IdempotencyKey = nil, "amazon-2"
	if _, err := service.MarkExternalPurchase(context.Background(), plain); err != nil || len(workspace.inputs) != 2 || workspace.inputs[1].Snapshot != nil {
		t.Fatalf("snapshot-less Amazon command: %v %#v", err, workspace.inputs)
	}
	// Korean products never take a client snapshot: the server derives it from the observation.
	korean := MarkExternalPurchaseInput{UserID: "owner", CurationID: "curation", CandidateID: "coupang-card", ProductRef: &koreanRef, Checked: true, IdempotencyKey: "korean-1"}
	rejected := korean
	rejected.Snapshot = &researchdomain.PurchaseCheckSnapshot{ProductTitle: "client title", PriceUnknown: true}
	if _, err := service.MarkExternalPurchase(context.Background(), rejected); err == nil || fault.CodeOf(err) != fault.InvalidInput {
		t.Fatalf("Korean client snapshot accepted: %v", err)
	}
	if _, err := service.MarkExternalPurchase(context.Background(), korean); err != nil || len(workspace.inputs) != 3 || workspace.inputs[2].Snapshot != nil || workspace.inputs[2].ProductRef == nil {
		t.Fatalf("Korean command: %v %#v", err, workspace.inputs)
	}
}

type purchaseCheckListRepository struct {
	Repository
	rows  []researchdomain.PurchaseCheck
	user  string
	limit int
}

func (r *purchaseCheckListRepository) ListPurchaseChecks(_ context.Context, user string, limit int) ([]researchdomain.PurchaseCheck, error) {
	r.user, r.limit = user, limit
	return r.rows, nil
}

func TestServiceListPurchaseChecks(t *testing.T) {
	unsupported := &Service{repository: &memoryResearchRepository{}}
	if values, err := unsupported.ListPurchaseChecks(context.Background(), "user-1", 50); err != nil || values == nil || len(values) != 0 {
		t.Fatalf("repository without the projection must list nothing: %#v %v", values, err)
	}
	repository := &purchaseCheckListRepository{rows: []researchdomain.PurchaseCheck{{CurationID: "c1", CandidateID: "cand", Checked: true, Version: 1, Evidence: "SELF_REPORTED"}}}
	service := &Service{repository: repository}
	for _, limit := range []int{0, -1, 101} {
		values, err := service.ListPurchaseChecks(context.Background(), "user-1", limit)
		if err != nil || len(values) != 1 || repository.limit != 50 || repository.user != "user-1" {
			t.Fatalf("limit %d not clamped: %d %v", limit, repository.limit, err)
		}
	}
	if _, err := service.ListPurchaseChecks(context.Background(), "user-1", 7); err != nil || repository.limit != 7 {
		t.Fatalf("explicit limit lost: %d %v", repository.limit, err)
	}
}
