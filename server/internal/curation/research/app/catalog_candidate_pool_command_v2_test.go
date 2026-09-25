package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestCatalogCandidatePoolCommandHashesExpectedVersionAndExplicitZero(t *testing.T) {
	zero := int64(0)
	input := CatalogWorkspaceSearchInputV2{
		UserID: "user-1", CurationID: "curation-1", TargetID: "target-1",
		Mode: CatalogResearchAppendV2,
		Search: LiveCatalogReviewSearchInputV2{
			Query: "carry on bag", Intent: "lightweight travel bag",
			Country: "US", Currency: "USD", MinimumMinor: &zero, Limit: 10,
		},
	}
	first, err := NewCatalogCandidatePoolCommandV2(input, 4, "command-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCatalogCandidatePoolCommandV2(input, 4, "command-1")
	if err != nil || second.RequestHash != first.RequestHash {
		t.Fatalf("deterministic command first=%#v second=%#v err=%v", first, second, err)
	}
	withoutPrice := input
	withoutPrice.Search.MinimumMinor = nil
	changedPrice, err := NewCatalogCandidatePoolCommandV2(withoutPrice, 4, "command-1")
	if err != nil || changedPrice.RequestHash == first.RequestHash {
		t.Fatalf("explicit zero was not bound: first=%s changed=%s err=%v", first.RequestHash, changedPrice.RequestHash, err)
	}
	changedVersion, err := NewCatalogCandidatePoolCommandV2(input, 5, "command-1")
	if err != nil || changedVersion.RequestHash == first.RequestHash {
		t.Fatalf("expected version was not bound: first=%s changed=%s err=%v", first.RequestHash, changedVersion.RequestHash, err)
	}
	ignoredBrowserCopy := input
	ignoredBrowserCopy.Search.Query = "browser must not reshape deterministic expand"
	ignoredBrowserCopy.Search.Intent = "another ignored browser intent"
	ignored, err := NewCatalogCandidatePoolCommandV2(ignoredBrowserCopy, 4, "command-1")
	if err != nil || ignored.RequestHash != first.RequestHash {
		t.Fatalf("ignored APPEND copy changed semantic hash: first=%s changed=%s err=%v", first.RequestHash, ignored.RequestHash, err)
	}
}

func TestCatalogCandidatePoolReplaceCommandBindsManagedHardTerms(t *testing.T) {
	input := CatalogWorkspaceSearchInputV2{
		UserID: "user-1", CurationID: "curation-1", TargetID: "target-1",
		Mode: CatalogResearchReplaceV2,
		Search: LiveCatalogReviewSearchInputV2{
			Query: "trail shoes", Country: "US", Currency: "USD", Limit: 8,
			HardLexicalTerms: []string{"water-resistant"},
			Exclusions:       []string{"used"},
		},
	}
	command, err := NewCatalogCandidatePoolCommandV2(input, 2, "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	changed := input
	changed.Search.HardLexicalTerms = []string{"reflective"}
	changedCommand, err := NewCatalogCandidatePoolCommandV2(changed, 2, "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	if command.RequestHash == changedCommand.RequestHash {
		t.Fatal("managed hard search terms were not bound to the worker command")
	}
}

func TestCatalogCandidatePoolExecutionStopsBeforeProviderAndAbortsProviderFault(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)}
	service, err := NewLiveCatalogReviewServiceV2(
		&liveReviewGatewayV2{clock: clock}, clock,
		LiveCatalogReviewConfigV2{
			MaximumCallsPerWindow: 3, Window: time.Minute, MaximumConcurrent: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	repository := &catalogPoolCommandRepositoryFakeV2{}
	if err := service.EnableWorkspaceRepositoryV2(repository); err != nil {
		t.Fatal(err)
	}
	command := CatalogCandidatePoolCommandV2{
		UserID: "user-1", CurationID: "curation-1", TargetID: "target-1",
		Mode: CatalogResearchAppendV2, ExpectedPoolVersion: 50,
		IdempotencyKey: "command-1", RequestHash: "0xhash",
	}

	providerCalls := 0
	repository.preflightErr = fault.New(
		fault.Conflict, CatalogCandidatePoolCapacityReachedV2, false,
	)
	_, err = service.ExecuteWorkspaceCandidatePoolCommandV2(
		context.Background(), command,
		func() ([]CatalogCandidateReferenceV2, LiveCatalogReviewMetricsV2, error) {
			providerCalls++
			return nil, LiveCatalogReviewMetricsV2{}, nil
		},
	)
	failure, ok := fault.As(err)
	if !ok || failure.Reason != CatalogCandidatePoolCapacityReachedV2 || providerCalls != 0 || repository.abortCalls != 0 {
		t.Fatalf("capacity failure=%v providerCalls=%d abortCalls=%d", err, providerCalls, repository.abortCalls)
	}

	repository.preflightErr = nil
	repository.preflight = CatalogCandidatePoolPreflightV2{
		FencingToken:   "opaque-reservation-1",
		LeaseExpiresAt: clock.now.Add(time.Minute),
	}
	providerFailure := fault.New(fault.ProviderUnavailable, "SHOPIFY_UNAVAILABLE", true)
	_, err = service.ExecuteWorkspaceCandidatePoolCommandV2(
		context.Background(), command,
		func() ([]CatalogCandidateReferenceV2, LiveCatalogReviewMetricsV2, error) {
			providerCalls++
			return nil, LiveCatalogReviewMetricsV2{}, providerFailure
		},
	)
	if !errors.Is(err, providerFailure) || repository.abortCalls != 1 || repository.completeCalls != 0 || repository.mutations != 0 {
		t.Fatalf("provider failure=%v state=%#v providerCalls=%d", err, repository, providerCalls)
	}
	if repository.lastAbortToken != "opaque-reservation-1" {
		t.Fatalf("provider failure abort token=%q", repository.lastAbortToken)
	}

	repository.preflight = CatalogCandidatePoolPreflightV2{
		FencingToken:   "opaque-reservation-2",
		LeaseExpiresAt: clock.now.Add(time.Minute),
	}
	completed, err := service.ExecuteWorkspaceCandidatePoolCommandV2(
		context.Background(), command,
		func() ([]CatalogCandidateReferenceV2, LiveCatalogReviewMetricsV2, error) {
			providerCalls++
			return nil, LiveCatalogReviewMetricsV2{}, nil
		},
	)
	if err != nil || completed.Pool.Version != 51 ||
		repository.lastCompleteToken != "opaque-reservation-2" {
		t.Fatalf("completed=%#v err=%v complete token=%q", completed, err, repository.lastCompleteToken)
	}

	repository.preflight = CatalogCandidatePoolPreflightV2{
		Replay: true,
		Pool:   CatalogPoolMetadataV2{TargetID: "target-1", Version: 51},
	}
	replayed, err := service.ExecuteWorkspaceCandidatePoolCommandV2(
		context.Background(), command,
		func() ([]CatalogCandidateReferenceV2, LiveCatalogReviewMetricsV2, error) {
			providerCalls++
			return nil, LiveCatalogReviewMetricsV2{}, nil
		},
	)
	if err != nil || !replayed.Replay || replayed.Pool.Version != 51 || providerCalls != 2 {
		t.Fatalf("replayed=%#v err=%v providerCalls=%d", replayed, err, providerCalls)
	}
}

func TestCatalogSearchResponseContainsOnlyDurablyAdmittedCandidates(t *testing.T) {
	result := LiveCatalogReviewResultV2{
		Search: CatalogProductSearchResult{
			Products: []CatalogProductObservation{
				{ProviderProductID: "candidate-admitted"},
				{ProviderProductID: "candidate-admitted"},
				{ProviderProductID: "candidate-capacity-rejected"},
			},
			Messages: []CatalogProviderMessage{
				{Type: "warning", SubjectKind: "PRODUCT", SubjectRef: "candidate-admitted"},
				{Type: "warning", SubjectKind: "PRODUCT", SubjectRef: "candidate-capacity-rejected"},
				{Type: "warning", SubjectKind: "GLOBAL"},
			},
		},
		CandidateAssessments: map[string]LiveCandidateAssessmentV2{
			"candidate-admitted":          {IntentPoint: "admitted"},
			"candidate-capacity-rejected": {IntentPoint: "must not escape"},
		},
		CandidateEligibleCount: 3,
	}

	filtered := catalogFilterSearchToAdmittedCandidatesV2(
		result, []string{"candidate-admitted"},
	)
	if len(filtered.Search.Products) != 1 ||
		filtered.Search.Products[0].ProviderProductID != "candidate-admitted" ||
		filtered.CandidateEligibleCount != 1 ||
		len(filtered.CandidateAssessments) != 1 ||
		len(filtered.Search.Messages) != 2 {
		t.Fatalf("unadmitted Candidate escaped response: %#v", filtered)
	}
}

func TestCatalogProviderProductIdentitySurvivesLocatorAndDurableIDChanges(t *testing.T) {
	const providerProductID = "gid://shopify/Product/stable-1"
	if got := catalogCandidateIdentityKeyV2(providerProductID); got !=
		"shopify-product:"+providerProductID {
		t.Fatalf("provider product identity=%q", got)
	}
	if catalogCandidateIdentityKeyV2("   ") != "" {
		t.Fatal("blank provider product identity was admitted")
	}

	result := LiveCatalogReviewResultV2{
		Search: CatalogProductSearchResult{
			Products: []CatalogProductObservation{{ProviderProductID: "proposed-a"}},
			Messages: []CatalogProviderMessage{{
				Type: "warning", SubjectKind: "PRODUCT", SubjectRef: "proposed-a",
			}},
		},
		CandidateAssessments: map[string]LiveCandidateAssessmentV2{
			"proposed-a": {IntentPoint: "stable product"},
		},
	}
	rebound := catalogRebindSearchCandidateIDsV2(result, []CatalogCandidateIDBindingV2{{
		ProposedCandidateID: "proposed-a", DurableCandidateID: "durable-a",
	}})
	if rebound.Search.Products[0].ProviderProductID != "durable-a" ||
		rebound.Search.Messages[0].SubjectRef != "durable-a" ||
		rebound.CandidateAssessments["durable-a"].IntentPoint != "stable product" {
		t.Fatalf("durable Candidate identity was not rebound consistently: %#v", rebound)
	}
}

func TestCatalogCompletedCommandReplayAfterLostResponseReturnsDurableProjectionWithoutProvider(t *testing.T) {
	clock := &liveReviewClockV2{now: time.Date(2026, 8, 13, 2, 3, 4, 0, time.UTC)}
	gateway := &liveReviewGatewayV2{clock: clock}
	service, err := NewLiveCatalogReviewServiceV2(
		gateway, clock,
		LiveCatalogReviewConfigV2{
			MaximumCallsPerWindow: 30, Window: time.Minute, MaximumConcurrent: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	locator := CatalogProductLocator{
		Kind: CatalogLocatorProductURL,
		ProductURL: &CatalogProductURLLocator{
			CanonicalURL: "https://shop.example/products/durable",
		},
	}
	repository := &catalogPoolCommandRepositoryFakeV2{
		preflight: CatalogCandidatePoolPreflightV2{
			Replay: true,
			Pool: CatalogPoolMetadataV2{
				TargetID: "target-1", Version: 9, ExpandOrdinal: 4,
				LatestMode:         CatalogResearchAppendV2,
				LatestShopifyCalls: 1, LatestRateRemaining: 21,
				AdmittedCandidateIDs: []string{"candidate-visible"},
			},
		},
		state: CatalogWorkspaceStoredStateV2{
			Candidates: []CatalogCandidateReferenceV2{
				{
					UserID: "user-1", CurationID: "curation-1", PlanTargetID: "target-1",
					CandidateID: "candidate-visible", ProviderProductID: "provider-visible",
					Locator: locator, Visible: true, DisplayOrder: 2,
					Assessment: LiveCandidateAssessmentV2{
						IntentPoint: "Durably matched.", Features: []string{"Feature"},
					},
				},
				{
					UserID: "user-1", CurationID: "curation-1", PlanTargetID: "target-1",
					CandidateID: "candidate-hidden", ProviderProductID: "provider-hidden",
					Locator: locator, Visible: false, DisplayOrder: 1,
				},
			},
		},
	}
	if err := service.EnableWorkspaceRepositoryV2(repository); err != nil {
		t.Fatal(err)
	}

	// This retry models: CompleteCatalogSearch committed, the HTTP response was
	// lost, and the browser repeated the exact key/hash request.
	result, err := service.SearchWorkspaceV2(context.Background(), CatalogWorkspaceSearchInputV2{
		UserID: "user-1", CurationID: "curation-1", TargetID: "target-1",
		Mode: CatalogResearchAppendV2, ExpectedPoolVersion: 8,
		IdempotencyKey: "lost-response-command",
		Search:         LiveCatalogReviewSearchInputV2{Limit: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replay || result.Pool.Version != 9 || len(result.Search.Products) != 1 ||
		result.Search.Products[0].ProviderProductID != "candidate-visible" ||
		result.Search.Products[0].Locator == nil ||
		result.CandidateAssessments["candidate-visible"].IntentPoint != "Durably matched." {
		t.Fatalf("replay projection=%#v", result)
	}
	if result.Search.Products[0].Title != "" || len(result.Search.Messages) != 0 {
		t.Fatalf("replay fabricated non-durable Shopify facts: %#v", result.Search)
	}
	if result.Metrics.ShopifyCallCount != 0 || result.Metrics.AICallCount != 0 ||
		result.Metrics.ExternalEffect != "NONE" || result.Metrics.LocalCallsUsed != 0 ||
		gateway.calls != 0 || gateway.lookupCalls != 0 || repository.completeCalls != 0 ||
		repository.abortCalls != 0 || repository.mutations != 0 {
		t.Fatalf("replay performed effects: result=%#v gateway=%#v repository=%#v",
			result.Metrics, gateway, repository)
	}
}

type catalogPoolCommandRepositoryFakeV2 struct {
	CatalogWorkspaceRepositoryV2
	preflight         CatalogCandidatePoolPreflightV2
	preflightErr      error
	abortCalls        int
	completeCalls     int
	mutations         int
	lastAbortToken    string
	lastCompleteToken string
	state             CatalogWorkspaceStoredStateV2
}

func (*catalogPoolCommandRepositoryFakeV2) AuthorizeTargetV2(
	context.Context, string, string, string,
) error {
	return nil
}

func (*catalogPoolCommandRepositoryFakeV2) CatalogTargetSearchProfileV2(
	context.Context, string, string, string,
) (CatalogTargetSearchProfileV2, error) {
	return CatalogTargetSearchProfileV2{
		TargetID: "target-1", NormalizedIntent: "durable product", Category: "product",
		Market: CatalogMarketContextV2{Country: "US", Currency: "USD"},
	}, nil
}

func (repository *catalogPoolCommandRepositoryFakeV2) LoadCatalogWorkspaceStateV2(
	context.Context, string, string,
) (CatalogWorkspaceStoredStateV2, error) {
	return repository.state, nil
}

func (repository *catalogPoolCommandRepositoryFakeV2) PreflightCatalogSearchV2(
	context.Context,
	CatalogCandidatePoolCommandV2,
) (CatalogCandidatePoolPreflightV2, error) {
	return repository.preflight, repository.preflightErr
}

func (repository *catalogPoolCommandRepositoryFakeV2) CompleteCatalogSearchV2(
	_ context.Context,
	command CatalogCandidatePoolCommandV2,
	_ []CatalogCandidateReferenceV2,
	_ LiveCatalogReviewMetricsV2,
) (CatalogPoolMetadataV2, error) {
	repository.completeCalls++
	repository.lastCompleteToken = command.FencingToken
	repository.mutations++
	return CatalogPoolMetadataV2{
		TargetID: command.TargetID, Version: command.ExpectedPoolVersion + 1,
	}, nil
}

func (repository *catalogPoolCommandRepositoryFakeV2) AbortCatalogSearchV2(
	_ context.Context,
	command CatalogCandidatePoolCommandV2,
) error {
	repository.abortCalls++
	repository.lastAbortToken = command.FencingToken
	return nil
}

// The staged stages are not exercised by these execution-boundary tests; the
// fake only satisfies the widened repository port.
func (repository *catalogPoolCommandRepositoryFakeV2) PublishObservedCandidatesV2(
	ctx context.Context,
	command CatalogCandidatePoolCommandV2,
	candidates []CatalogCandidateReferenceV2,
	metrics LiveCatalogReviewMetricsV2,
) (CatalogPoolMetadataV2, error) {
	return repository.CompleteCatalogSearchV2(ctx, command, candidates, metrics)
}

func (repository *catalogPoolCommandRepositoryFakeV2) FillCandidateAssessmentsV2(
	context.Context, CatalogCandidatePoolCommandV2, int64, map[string]LiveCandidateAssessmentV2, time.Time,
) (int64, int, error) {
	return 0, 0, nil
}

func (repository *catalogPoolCommandRepositoryFakeV2) FinalizeStagedSearchV2(
	_ context.Context, _ CatalogCandidatePoolCommandV2, _ int64, _ string, published CatalogPoolMetadataV2, _ LiveCatalogReviewMetricsV2,
) (CatalogPoolMetadataV2, error) {
	return published, nil
}
