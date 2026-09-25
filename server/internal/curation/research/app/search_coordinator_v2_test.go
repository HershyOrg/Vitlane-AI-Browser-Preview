package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestCatalogSearchCoordinatorV2StopsAtFirstUsableBaseResult(t *testing.T) {
	t.Parallel()
	plan := mustCatalogSearchPlanV2(t, shareddomain.NoResearchPriceConstraint())
	gateway := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{
		{response: catalogSearchResponseV2(plan, catalogProductWithURLV2("product-1", 1000))},
	}}
	coordinator := mustCatalogSearchCoordinatorV2(t, gateway)
	execution, err := coordinator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Status != CatalogSearchExecutionResultsV2 || len(gateway.calls) != 1 ||
		len(execution.Attempts) != 1 || len(execution.AdmissionInputs) != 1 {
		t.Fatalf("base result should stop fallback: execution=%+v calls=%d", execution, len(gateway.calls))
	}
	if execution.AdmissionInputs[0].Locator.Kind != CatalogLocatorProductURL {
		t.Fatalf("admission input lost required locator: %+v", execution.AdmissionInputs[0])
	}
	proof := execution.AdmissionInputs[0].HardFilters
	if !proof.AppliedFilters.Verified ||
		proof.AppliedFilters.ProviderCapability != CatalogShopifyGlobalCapabilityV2 ||
		proof.AppliedFilters.Filters.Available == nil ||
		!*proof.AppliedFilters.Filters.Available ||
		proof.AppliedFilters.Filters.ShipsToCountry != "US" ||
		len(proof.AppliedFilters.Filters.Categories) != 1 ||
		proof.AppliedFilters.Filters.Categories[0] != "footwear" ||
		len(proof.AppliedFilters.Filters.Conditions) != 1 ||
		proof.AppliedFilters.Filters.Conditions[0] != "new" ||
		len(proof.AppliedFilters.Filters.Attributes) != 3 ||
		!proof.PhysicalEligibility.Verified {
		t.Fatalf("admission did not bind exact hard-filter evidence: %+v", proof)
	}
}

func TestCatalogSearchCoordinatorV2FailsClosedWhenHardFilterProofIsMissingOrDisqualified(
	t *testing.T,
) {
	t.Parallel()
	plan := mustCatalogSearchPlanV2(t, shareddomain.NoResearchPriceConstraint())
	tests := []struct {
		name     string
		response CatalogProductSearchResult
	}{
		{
			name: "missing proof",
			response: func() CatalogProductSearchResult {
				value := catalogSearchResponseV2(
					plan, catalogProductWithURLV2("product-1", 1000),
				)
				value.AppliedFilters.EvidenceRef = "present-but-unverified"
				return value
			}(),
		},
		{
			name: "ignored filter message",
			response: func() CatalogProductSearchResult {
				value := catalogSearchResponseV2(
					plan, catalogProductWithURLV2("product-1", 1000),
				)
				value.Messages = []CatalogProviderMessage{{
					Type: "warning", Code: "filter_ignored",
					Path: "$.filters.attributes", Content: "untrusted provider detail",
				}}
				return value
			}(),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gateway := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{{
				response: test.response,
			}}}
			execution, err := mustCatalogSearchCoordinatorV2(t, gateway).Execute(
				context.Background(), plan,
			)
			classified, ok := fault.As(err)
			if !ok || classified.Code != fault.ProviderRejected ||
				classified.Reason != string(CatalogFailureFilterNotEnforced) ||
				execution.Status != CatalogSearchExecutionFailedV2 ||
				len(gateway.calls) != 1 || len(execution.AdmissionInputs) != 0 {
				t.Fatalf("hard-filter proof failure was not fail-closed: execution=%+v err=%v", execution, err)
			}
			if strings.Contains(err.Error(), "untrusted provider detail") {
				t.Fatalf("provider content leaked through typed error: %v", err)
			}
		})
	}
}

func TestCatalogSearchCoordinatorV2RejectsContradictoryOrNonPhysicalEvidence(t *testing.T) {
	t.Parallel()
	basePlan := mustCatalogSearchPlanV2(t, shareddomain.NoResearchPriceConstraint())
	wrongCategory := catalogProductWithURLV2("wrong-category", 1000)
	wrongCategory.Categories = []CatalogCategory{{Value: "Software"}}
	unavailable := catalogProductWithURLV2("unavailable", 1000)
	available := false
	unavailable.PreviewVariant = &CatalogPreviewVariant{
		Availability: CatalogAvailability{Available: &available},
	}

	noPhysicalInput := catalogCompilerInputV2(t, shareddomain.NoResearchPriceConstraint())
	noPhysicalInput.Intent.VerifiedCategories = nil
	noPhysicalPlan, err := TranslateCatalogSearchPlanV2(noPhysicalInput)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		plan    researchdomain.CatalogSearchPlanV2
		product CatalogProductObservation
	}{
		{name: "category contradiction", plan: basePlan, product: wrongCategory},
		{name: "availability contradiction", plan: basePlan, product: unavailable},
		{
			name: "observed non-physical category contradicts filter eligibility",
			plan: noPhysicalPlan, product: wrongCategory,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gateway := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{{
				response: catalogSearchResponseV2(test.plan, test.product, catalogProductWithURLV2("valid-neighbor", 1000)),
			}}}
			execution, executeErr := mustCatalogSearchCoordinatorV2(t, gateway).Execute(
				context.Background(), test.plan,
			)
			if executeErr != nil || execution.Status != CatalogSearchExecutionResultsV2 || len(execution.AdmissionInputs) != 1 || execution.AdmissionInputs[0].Observation.ProviderProductID != "valid-neighbor" || len(gateway.calls) != 1 {
				t.Fatalf("evidence contradiction was not fail-closed: execution=%+v err=%v", execution, executeErr)
			}
		})
	}
}

func TestCatalogSearchCoordinatorV2AdmitsCategorylessShopifyShippableNewMerchandise(
	t *testing.T,
) {
	t.Parallel()
	input := catalogCompilerInputV2(t, shareddomain.NoResearchPriceConstraint())
	input.Intent.VerifiedCategories = nil
	plan, err := TranslateCatalogSearchPlanV2(input)
	if err != nil {
		t.Fatal(err)
	}
	product := catalogProductWithURLV2("categoryless-product", 1000)
	product.Categories = nil

	gateway := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{{
		response: catalogSearchResponseV2(plan, product),
	}}}
	execution, err := mustCatalogSearchCoordinatorV2(t, gateway).Execute(
		context.Background(), plan,
	)
	if err != nil || execution.Status != CatalogSearchExecutionResultsV2 ||
		len(execution.AdmissionInputs) != 1 {
		t.Fatalf("categoryless Shopify merchandise was not admitted: execution=%+v err=%v", execution, err)
	}
	proof := execution.AdmissionInputs[0].HardFilters.PhysicalEligibility
	if !proof.Verified ||
		proof.PolicyVersion != CatalogPhysicalEligibilityPolicyVersionV2 ||
		proof.EvidenceKind != CatalogPhysicalShippableNewMerchandiseEvidenceV2 ||
		proof.Category != "" || proof.AppliedAvailable == nil ||
		!*proof.AppliedAvailable || proof.AppliedShipsToCountry != "US" ||
		proof.AppliedCondition != "new" || !validCatalogEvidenceHashV2(proof.EvidenceHash) {
		t.Fatalf("exact shippable/new evidence was not bound: %+v", proof)
	}
}

// The coordinator sends the one translated request. Rows without a usable
// locator are not candidates, and an empty page is an honest NO_RESULTS: there
// is no second request with a reshaped query — the next Round asks the plan's
// next phrase.
func TestCatalogSearchCoordinatorV2SendsTheOneRequestAndNeverReshapesIt(t *testing.T) {
	t.Parallel()
	plan := mustCatalogSearchPlanV2(t, shareddomain.NoResearchPriceConstraint())
	withoutLocator := catalogProductWithURLV2("unresolvable-1", 1000)
	withoutLocator.Locator = nil
	gateway := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{
		{response: catalogSearchResponseV2(plan, withoutLocator, catalogProductWithURLV2("product-2", 1000))},
	}}
	execution, err := mustCatalogSearchCoordinatorV2(t, gateway).Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Status != CatalogSearchExecutionResultsV2 || len(gateway.calls) != 1 ||
		len(execution.Attempts) != 1 || len(execution.AdmissionInputs) != 1 ||
		execution.Attempts[0].RawCount != 2 || execution.Attempts[0].UsableCount != 1 ||
		execution.Attempts[0].Outcome != CatalogSearchAttemptSucceededV2 {
		t.Fatalf("one request, one usable row: execution=%+v calls=%d", execution, len(gateway.calls))
	}
	if gateway.calls[0].Query != plan.Requests[0].Query {
		t.Fatalf("the request's phrase was changed: %q", gateway.calls[0].Query)
	}

	onlyUnusable := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{
		{response: catalogSearchResponseV2(plan, withoutLocator)},
	}}
	execution, err = mustCatalogSearchCoordinatorV2(t, onlyUnusable).Execute(context.Background(), plan)
	if err != nil || execution.Status != CatalogSearchExecutionNoResultsV2 || len(onlyUnusable.calls) != 1 {
		t.Fatalf("a page without usable rows ends the search: execution=%+v calls=%d err=%v", execution, len(onlyUnusable.calls), err)
	}
}

func TestCatalogSearchCoordinatorV2ReturnsNoResultsAfterOneSuccessfulEmptyPage(t *testing.T) {
	t.Parallel()
	plan := mustCatalogSearchPlanV2(t, shareddomain.NoResearchPriceConstraint())
	gateway := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{
		{response: catalogSearchResponseV2(plan)},
	}}
	coordinator := mustCatalogSearchCoordinatorV2(t, gateway)
	execution, err := coordinator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Status != CatalogSearchExecutionNoResultsV2 || len(gateway.calls) != 1 ||
		len(execution.Attempts) != 1 || len(execution.AdmissionInputs) != 0 {
		t.Fatalf("unexpected valid empty result: execution=%+v calls=%d", execution, len(gateway.calls))
	}
}

func TestCatalogSearchCoordinatorV2NeverFallsBackAfterProviderFault(t *testing.T) {
	t.Parallel()
	plan := mustCatalogSearchPlanV2(t, shareddomain.NoResearchPriceConstraint())
	tests := []struct {
		name       string
		step       catalogSearchScriptStepV2
		wantCode   fault.Code
		wantReason string
	}{
		{
			name: "transport rate limit",
			step: catalogSearchScriptStepV2{err: fault.New(
				fault.RateLimited, string(CatalogFailureRateLimited), true,
			)},
			wantCode: fault.RateLimited, wantReason: string(CatalogFailureRateLimited),
		},
		{
			name: "schema mismatch",
			step: catalogSearchScriptStepV2{response: CatalogProductSearchResult{
				Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: "unexpected",
				Outcome: CatalogOutcomeSuccess, Products: []CatalogProductObservation{},
			}},
			wantCode: fault.ProviderRejected, wantReason: string(CatalogFailureSchemaMismatch),
		},
		{
			name: "business outcome",
			step: catalogSearchScriptStepV2{response: CatalogProductSearchResult{
				Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: plan.ProviderProtocolVersion,
				Outcome: CatalogOutcomeBusinessError,
				Messages: []CatalogProviderMessage{{
					Type: "error", Code: "unsupported", Content: "untrusted provider text",
				}},
			}},
			wantCode: fault.ProviderRejected, wantReason: string(CatalogFailureBusinessOutcomeV2),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gateway := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{test.step}}
			coordinator := mustCatalogSearchCoordinatorV2(t, gateway)
			execution, err := coordinator.Execute(context.Background(), plan)
			classified, ok := fault.As(err)
			if !ok || classified.Code != test.wantCode || classified.Reason != test.wantReason {
				t.Fatalf("fault=%v want code=%s reason=%s", err, test.wantCode, test.wantReason)
			}
			if execution.Status != CatalogSearchExecutionFailedV2 || len(gateway.calls) != 1 ||
				len(execution.Attempts) != 1 || execution.Attempts[0].Outcome != CatalogSearchAttemptFailedV2 {
				t.Fatalf("fault must stop every fallback: execution=%+v calls=%d", execution, len(gateway.calls))
			}
			if err.Error() == "untrusted provider text" {
				t.Fatalf("provider text leaked into typed failure: %v", err)
			}
		})
	}
}

func TestCatalogSearchCoordinatorV2RejectsAnonymousProviderEnvelope(t *testing.T) {
	t.Parallel()
	plan := mustCatalogSearchPlanV2(t, shareddomain.NoResearchPriceConstraint())
	response := catalogSearchResponseV2(plan, catalogProductWithURLV2("product-1", 1000))
	response.Provider = ""
	gateway := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{{response: response}}}
	coordinator := mustCatalogSearchCoordinatorV2(t, gateway)
	execution, err := coordinator.Execute(context.Background(), plan)
	classified, ok := fault.As(err)
	if !ok || classified.Reason != string(CatalogFailureSchemaMismatch) ||
		execution.Status != CatalogSearchExecutionFailedV2 || len(gateway.calls) != 1 {
		t.Fatalf("anonymous provider response must fail closed: execution=%+v err=%v", execution, err)
	}
}

func TestCatalogSearchCoordinatorV2TreatsInvalidLocatorAsTypedSchemaFault(t *testing.T) {
	t.Parallel()
	plan := mustCatalogSearchPlanV2(t, shareddomain.NoResearchPriceConstraint())
	product := catalogProductWithURLV2("product-1", 1000)
	product.Locator.ProductURL.CanonicalURL = "http://merchant.example/products/product-1"
	gateway := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{
		{response: catalogSearchResponseV2(plan, product, catalogProductWithURLV2("valid-neighbor", 1000))},
	}}
	coordinator := mustCatalogSearchCoordinatorV2(t, gateway)
	execution, err := coordinator.Execute(context.Background(), plan)
	if err != nil || execution.Status != CatalogSearchExecutionResultsV2 || len(execution.AdmissionInputs) != 1 || execution.AdmissionInputs[0].Observation.ProviderProductID != "valid-neighbor" || len(gateway.calls) != 1 {
		t.Fatalf("invalid locator should fail closed without fallback: execution=%+v err=%v", execution, err)
	}
}

func TestCatalogSearchCoordinatorV2AdmitsExplicitZeroPrice(t *testing.T) {
	t.Parallel()
	zero := mustCompilerMoneyV2(t, "0", "USD")
	constraint, err := shareddomain.NewExplicitResearchPriceConstraint(&zero, &zero)
	if err != nil {
		t.Fatalf("constraint: %v", err)
	}
	plan := mustCatalogSearchPlanV2(t, constraint)
	gateway := &scriptedCatalogGatewayV2{steps: []catalogSearchScriptStepV2{
		{response: catalogSearchResponseV2(plan, catalogProductWithURLV2("free-product", 0))},
	}}
	coordinator := mustCatalogSearchCoordinatorV2(t, gateway)
	execution, err := coordinator.Execute(context.Background(), plan)
	if err != nil || execution.Status != CatalogSearchExecutionResultsV2 ||
		len(execution.AdmissionInputs) != 1 {
		t.Fatalf("explicit zero must remain a usable price: execution=%+v err=%v", execution, err)
	}
}

type catalogSearchScriptStepV2 struct {
	response CatalogProductSearchResult
	err      error
}

type scriptedCatalogGatewayV2 struct {
	steps []catalogSearchScriptStepV2
	calls []CatalogProductSearchRequest
}

func (gateway *scriptedCatalogGatewayV2) SearchProducts(
	_ context.Context,
	request CatalogProductSearchRequest,
) (CatalogProductSearchResult, error) {
	gateway.calls = append(gateway.calls, request)
	index := len(gateway.calls) - 1
	if index >= len(gateway.steps) {
		return CatalogProductSearchResult{}, errors.New("unexpected search call")
	}
	response := gateway.steps[index].response
	if gateway.steps[index].err == nil &&
		response.Outcome == CatalogOutcomeSuccess &&
		response.Provider != "" && response.ProtocolVersion != "" &&
		response.AppliedFilters.EvidenceRef == "" {
		proof, err := NewCatalogAppliedFilterProofV2(
			request, response.Provider, response.ProtocolVersion,
			"scripted-catalog-request", "dev.shopify.catalog.global",
			response.ProtocolVersion, response.Messages,
		)
		if err != nil {
			return CatalogProductSearchResult{}, err
		}
		response.AppliedFilters = proof
	}
	return response, gateway.steps[index].err
}

func (gateway *scriptedCatalogGatewayV2) LookupMedia(
	context.Context,
	CatalogMediaLookupRequest,
) (CatalogMediaLookupResult, error) {
	return CatalogMediaLookupResult{}, errors.New("lookup is outside search coordinator")
}

func mustCatalogSearchPlanV2(
	t *testing.T,
	constraint shareddomain.ResearchPriceConstraint,
) researchdomain.CatalogSearchPlanV2 {
	t.Helper()
	plan, err := TranslateCatalogSearchPlanV2(catalogCompilerInputV2(t, constraint))
	if err != nil {
		t.Fatalf("compile plan: %v", err)
	}
	return plan
}

func mustCatalogSearchCoordinatorV2(
	t *testing.T,
	gateway CatalogGatewayV2,
) *CatalogSearchCoordinatorV2 {
	t.Helper()
	coordinator, err := NewCatalogSearchCoordinatorV2(gateway)
	if err != nil {
		t.Fatalf("coordinator: %v", err)
	}
	return coordinator
}

func catalogSearchResponseV2(
	plan researchdomain.CatalogSearchPlanV2,
	products ...CatalogProductObservation,
) CatalogProductSearchResult {
	return CatalogProductSearchResult{
		Provider: CatalogProviderShopifyGlobalV2, ProtocolVersion: plan.ProviderProtocolVersion,
		Outcome: CatalogOutcomeSuccess, Products: products,
	}
}

func catalogProductWithURLV2(
	productID string,
	amountMinor int64,
) CatalogProductObservation {
	return CatalogProductObservation{
		ProviderProductID: productID, Title: "Trail Running Shoes",
		Categories: []CatalogCategory{{Value: "Footwear", Taxonomy: "Shopify"}},
		Locator: &CatalogProductLocator{
			Kind: CatalogLocatorProductURL,
			ProductURL: &CatalogProductURLLocator{
				CanonicalURL: "https://merchant.example/products/" + productID,
			},
		},
		PriceRange: CatalogPriceRange{
			Minimum: CatalogMoney{AmountMinor: amountMinor, Currency: "USD"},
			Maximum: CatalogMoney{AmountMinor: amountMinor, Currency: "USD"},
		},
	}
}
