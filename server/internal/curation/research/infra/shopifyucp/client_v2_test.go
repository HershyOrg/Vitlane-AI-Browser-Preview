package shopifyucp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

func TestSearchProductsAcceptsOfficialSuccessWithoutStatus(t *testing.T) {
	t.Parallel()
	gateway := fixtureGatewayV2(t, "search_success_status_omitted.json", func(t *testing.T, call testCallV2) {
		if call.Method != "tools/call" || call.Params.Name != "search_catalog" {
			t.Errorf("unexpected RPC call: method=%q tool=%q", call.Method, call.Params.Name)
		}
		var catalog map[string]any
		if err := json.Unmarshal(call.Params.Arguments.Catalog, &catalog); err != nil {
			t.Errorf("decode catalog request: %v", err)
			return
		}
		filters, _ := catalog["filters"].(map[string]any)
		if _, exists := filters["price"]; exists {
			t.Error("price must be absent when the request has no explicit price constraint")
		}
	})
	available := true
	result, err := gateway.SearchProducts(context.Background(), researchapp.CatalogProductSearchRequest{
		Query: "trail shoes",
		Context: researchapp.CatalogBuyerContext{
			Country: "US", Currency: "USD", Language: "en",
		},
		Filters: researchapp.CatalogProductSearchFilters{Available: &available},
		Limit:   20,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if result.Outcome != researchapp.CatalogOutcomeSuccess || len(result.Products) != 1 {
		t.Fatalf("unexpected result outcome=%q products=%d", result.Outcome, len(result.Products))
	}
	product := result.Products[0]
	if product.PriceRange.Minimum.AmountMinor != 0 {
		t.Fatalf("zero-priced range minimum was rejected: %+v", product.PriceRange)
	}
	if product.Locator == nil || product.Locator.Kind != researchapp.CatalogLocatorProductURL ||
		product.Locator.ProductURL.CanonicalURL != "https://merchant.example/products/url-only-runner" {
		t.Fatalf("unexpected product URL locator: %+v", product.Locator)
	}
	if product.PreviewVariant != nil {
		t.Fatalf("URL-only product should not invent a preview variant: %+v", product.PreviewVariant)
	}
	if len(result.Messages) != 1 || result.Messages[0].Presentation != "disclosure" ||
		result.Messages[0].ContentType != "markdown" ||
		result.Messages[0].ImageURL != "https://cdn.example/disclosure.png" ||
		result.Messages[0].Path != "$.products[0]" ||
		result.Messages[0].SubjectKind != catalogMessageSubjectProductV2 ||
		result.Messages[0].SubjectRef != product.ProviderProductID {
		t.Fatalf("provider message contract was not preserved: %+v", result.Messages)
	}
	if !result.Pagination.HasNextPage || result.Pagination.Cursor != "next-page" {
		t.Fatalf("pagination was not preserved: %+v", result.Pagination)
	}
}

func TestBindCatalogMessageSubjectsPreservesUnknownAndBindsProductDescendants(t *testing.T) {
	t.Parallel()
	messages := []researchapp.CatalogProviderMessage{
		{Type: "warning", Path: "$.products[1].variants[0]", Content: "variant notice"},
		{Type: "warning", Path: "$.products[01x]", Content: "unknown path"},
		{Type: "warning", Content: "global notice"},
	}
	products := []wireProductV2{{ID: "product-0"}, {ID: "product-1"}}
	bound := bindCatalogMessageSubjectsV2(messages, products)
	if bound[0].SubjectKind != catalogMessageSubjectProductV2 ||
		bound[0].SubjectRef != "product-1" || bound[0].Path != messages[0].Path {
		t.Fatalf("product binding=%+v", bound[0])
	}
	if bound[1].SubjectKind != "" || bound[1].SubjectRef != "" ||
		bound[1].Path != messages[1].Path {
		t.Fatalf("unknown path must remain lossless and unbound: %+v", bound[1])
	}
	if bound[2].SubjectKind != catalogMessageSubjectSearchV2 || bound[2].Content != "global notice" {
		t.Fatalf("global binding=%+v", bound[2])
	}
}

func TestSearchProductsReturnsOneObservationAndFirstPreviewVariant(t *testing.T) {
	t.Parallel()
	gateway := fixtureGatewayV2(t, "search_multi_variant_product.json", nil)
	result, err := gateway.SearchProducts(context.Background(), researchapp.CatalogProductSearchRequest{
		Query: "trail shoes", Limit: 20,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Products) != 1 {
		t.Fatalf("two variants must not fan out into candidates: got %d products", len(result.Products))
	}
	product := result.Products[0]
	if product.PreviewVariant == nil ||
		product.PreviewVariant.ID != "gid://shopify/ProductVariant/first" {
		t.Fatalf("first variant was not kept as preview: %+v", product.PreviewVariant)
	}
	if product.Locator == nil || product.Locator.Kind != researchapp.CatalogLocatorMerchantVariant ||
		product.Locator.MerchantVariant.VariantID != "gid://shopify/ProductVariant/first" ||
		product.Locator.MerchantVariant.SellerDomain != "merchant.example" {
		t.Fatalf("unexpected merchant variant locator: %+v", product.Locator)
	}
}

func TestSearchProductsSendsStructuredFiltersAndExplicitPrice(t *testing.T) {
	t.Parallel()
	gateway := fixtureGatewayV2(t, "search_multi_variant_product.json", func(t *testing.T, call testCallV2) {
		var catalog wireSearchRequestV2
		if err := json.Unmarshal(call.Params.Arguments.Catalog, &catalog); err != nil {
			t.Errorf("decode search request: %v", err)
			return
		}
		if catalog.Context == nil || catalog.Context.Currency != "USD" ||
			catalog.Filters == nil || catalog.Filters.Price == nil ||
			catalog.Filters.Price.Min == nil || *catalog.Filters.Price.Min != 8000 ||
			catalog.Filters.Price.Max == nil || *catalog.Filters.Price.Max != 12000 ||
			catalog.Filters.ShipsTo == nil || catalog.Filters.ShipsTo.Country != "US" ||
			!slices.Equal(catalog.Filters.Condition, []string{"new"}) ||
			len(catalog.Filters.Attributes) != 2 {
			t.Errorf("structured filters were not preserved: %+v", catalog)
		}
	})
	minimum := int64(8000)
	maximum := int64(12000)
	result, err := gateway.SearchProducts(context.Background(), researchapp.CatalogProductSearchRequest{
		Query:   "trail shoes",
		Context: researchapp.CatalogBuyerContext{Country: "US", Currency: "USD"},
		Filters: researchapp.CatalogProductSearchFilters{
			ShipsTo:    &researchapp.CatalogDestination{Country: "US"},
			Conditions: []string{"new"},
			Attributes: []researchapp.CatalogAttributeFilter{
				{Name: "Color", Values: []string{"Black"}},
				{Name: "Size", Values: []string{"10", "10.5"}},
			},
			Price: &researchapp.CatalogPriceFilter{
				MinimumMinor: &minimum, MaximumMinor: &maximum,
			},
		},
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	proof := result.AppliedFilters
	if !proof.Verified ||
		proof.ProviderCapability != researchapp.CatalogShopifyGlobalCapabilityV2 ||
		proof.ProviderCapabilityVersion != "2026-04-08" ||
		proof.Filters.ShipsToCountry != "US" ||
		len(proof.Filters.Conditions) != 1 || proof.Filters.Conditions[0] != "new" ||
		len(proof.Filters.Attributes) != 2 || proof.RequestHash == "" ||
		proof.EvidenceRef == "" || proof.EvidenceHash == "" {
		t.Fatalf("exact applied-filter evidence was not returned: %+v", proof)
	}
}

func TestSearchProductsDoesNotGuessExtensionFilterEnforcement(t *testing.T) {
	t.Parallel()
	available := true
	request := researchapp.CatalogProductSearchRequest{
		Query: "trail shoes", Filters: researchapp.CatalogProductSearchFilters{
			Available: &available,
		}, Limit: 20,
	}

	t.Run("missing Shopify Global capability", func(t *testing.T) {
		t.Parallel()
		gateway := fixtureGatewayV2(t, "search_extension_capability_missing.json", nil)
		_, err := gateway.SearchProducts(context.Background(), request)
		assertCatalogFaultV2(
			t, err, fault.ProviderRejected,
			researchapp.CatalogFailureSchemaMismatch, false,
		)
	})

	t.Run("ignored filter message", func(t *testing.T) {
		t.Parallel()
		gateway := fixtureGatewayV2(t, "search_filter_ignored.json", nil)
		result, err := gateway.SearchProducts(context.Background(), request)
		if err != nil {
			t.Fatalf("ignored filter remains a successful response with unusable proof: %v", err)
		}
		if result.AppliedFilters.Verified ||
			len(result.AppliedFilters.DisqualifyingMessageHashes) != 1 ||
			result.AppliedFilters.EvidenceHash == "" {
			t.Fatalf("ignored filter was guessed as applied: %+v", result.AppliedFilters)
		}
		if strings.Contains(
			strings.Join(result.AppliedFilters.DisqualifyingMessageHashes, " "),
			"requested filter",
		) {
			t.Fatalf("raw provider content leaked into proof: %+v", result.AppliedFilters)
		}
	})
}

// Shopify confirms a price filter in words: an info notice with the code
// price_filter_applied. Reading that notice as "the filter was not enforced"
// failed every US Round that had a budget (observed against the live catalog on
// 2026-09-22). The notice is kept as a provider message; it does not spoil the proof.
func TestSearchProductsTreatsShopifysPriceFilterAppliedNoticeAsApplied(t *testing.T) {
	t.Parallel()
	available, maximum := true, int64(10000)
	request := researchapp.CatalogProductSearchRequest{
		Query:   "trail running shoes",
		Context: researchapp.CatalogBuyerContext{Country: "US", Currency: "USD", Language: "en", Intent: "trail running shoes"},
		Filters: researchapp.CatalogProductSearchFilters{
			Available: &available, Price: &researchapp.CatalogPriceFilter{MaximumMinor: &maximum},
		}, Limit: 20,
	}
	gateway := fixtureGatewayV2(t, "search_price_filter_applied.json", nil)
	result, err := gateway.SearchProducts(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AppliedFilters.Verified || len(result.AppliedFilters.DisqualifyingMessageHashes) != 0 {
		t.Fatalf("an affirmative notice disqualified the proof: %+v", result.AppliedFilters)
	}
	if err := result.AppliedFilters.ValidateForRequest(request, result.Provider, result.ProtocolVersion); err != nil {
		t.Fatalf("the proof must hold for the request that carried the price filter: %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Code != "price_filter_applied" {
		t.Fatalf("the notice stays a provider message: %+v", result.Messages)
	}

	// The same code with words that say the opposite is still a disqualification.
	contradicted := fixtureGatewayV2(t, "search_price_filter_not_applied.json", nil)
	result, err = contradicted.SearchProducts(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.AppliedFilters.Verified || len(result.AppliedFilters.DisqualifyingMessageHashes) != 1 {
		t.Fatalf("a notice that says the filter was not applied must disqualify: %+v", result.AppliedFilters)
	}
}

func TestSearchProductsKeepsBusinessOutcomeSeparateFromEmptySuccess(t *testing.T) {
	t.Parallel()
	gateway := fixtureGatewayV2(t, "search_business_error.json", nil)
	result, err := gateway.SearchProducts(context.Background(), researchapp.CatalogProductSearchRequest{
		Query: "unsupported search", Limit: 20,
	})
	if err != nil {
		t.Fatalf("business outcome must not become a transport error: %v", err)
	}
	if result.Outcome != researchapp.CatalogOutcomeBusinessError || len(result.Products) != 0 ||
		len(result.Messages) != 1 || result.Messages[0].Type != "error" {
		t.Fatalf("business outcome was collapsed into empty success: %+v", result)
	}
}

func TestLookupMediaCorrelatesReorderedPartialResponse(t *testing.T) {
	t.Parallel()
	gateway := fixtureGatewayV2(t, "lookup_reordered_partial.json", func(t *testing.T, call testCallV2) {
		var catalog wireLookupRequestV2
		if err := json.Unmarshal(call.Params.Arguments.Catalog, &catalog); err != nil {
			t.Errorf("decode lookup request: %v", err)
			return
		}
		want := []string{"product-a", "variant-b", "missing-c"}
		if !slices.Equal(catalog.IDs, want) {
			t.Errorf("lookup identifiers should be stable and deduplicated: got %v want %v", catalog.IDs, want)
		}
	})
	result, err := gateway.LookupMedia(context.Background(), researchapp.CatalogMediaLookupRequest{
		Inputs: []researchapp.CatalogMediaLookupInput{
			{CorrelationKey: "candidate-a", Identifier: "product-a"},
			{CorrelationKey: "candidate-b", Identifier: "variant-b"},
			{CorrelationKey: "candidate-c", Identifier: "missing-c"},
			{CorrelationKey: "candidate-a-copy", Identifier: "product-a"},
		},
		Context:               researchapp.CatalogBuyerContext{Country: "US"},
		ProviderCallAdmission: &testCatalogCallAdmissionV2{},
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(result.Matches) != 3 ||
		result.Matches[0].CorrelationKey != "candidate-a" ||
		result.Matches[1].CorrelationKey != "candidate-b" ||
		result.Matches[2].CorrelationKey != "candidate-a-copy" {
		t.Fatalf("matches were not restored to app correlation order: %+v", result.Matches)
	}
	if got := result.Matches[0].Media[0].URL; got != "https://cdn.example/product-a.jpg" {
		t.Fatalf("product media fallback not used: %q", got)
	}
	if got := result.Matches[1].Media[0].URL; got != "https://cdn.example/variant-b.jpg" {
		t.Fatalf("variant media must take precedence: %q", got)
	}
	if !slices.Equal(result.UnresolvedKeys, []string{"candidate-c"}) {
		t.Fatalf("missing provider identifier should be unresolved, not an error: %v", result.UnresolvedKeys)
	}
	if len(result.Messages) != 1 || result.Messages[0].Code != "not_found" {
		t.Fatalf("partial lookup messages were lost: %+v", result.Messages)
	}
}

func TestLookupMediaRejectsConflictingInputCorrelation(t *testing.T) {
	t.Parallel()
	gateway := fixtureGatewayV2(t, "lookup_conflicting_inputs.json", nil)
	result, err := gateway.LookupMedia(context.Background(), researchapp.CatalogMediaLookupRequest{
		Inputs: []researchapp.CatalogMediaLookupInput{
			{CorrelationKey: "candidate", Identifier: "ambiguous"},
		},
		ProviderCallAdmission: &testCatalogCallAdmissionV2{},
	})
	assertCatalogFaultV2(
		t, err, fault.ProviderRejected, researchapp.CatalogFailureCorrelation, false,
	)
	if result.ProviderCallCount != 1 {
		t.Fatalf("provider call count=%d", result.ProviderCallCount)
	}
}

func TestLookupMediaRejectsVariantWithoutInputCorrelation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call, err := readTestCallV2(request)
		if err != nil {
			t.Errorf("decode call: %v", err)
			return
		}
		response := lookupSuccessResponseV2(call.ID, []string{"product-a"})
		result := response["result"].(map[string]any)
		content := result["structuredContent"].(map[string]any)
		products := content["products"].([]any)
		product := products[0].(map[string]any)
		variants := product["variants"].([]any)
		delete(variants[0].(map[string]any), "inputs")
		writeJSONV2(t, writer, response)
	}))
	t.Cleanup(server.Close)
	gateway := mustGatewayV2(t, GatewayV2Config{
		Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
		HTTPClient: server.Client(),
	})
	_, err := gateway.LookupMedia(context.Background(), researchapp.CatalogMediaLookupRequest{
		Inputs: []researchapp.CatalogMediaLookupInput{
			{CorrelationKey: "candidate-a", Identifier: "product-a"},
		},
		ProviderCallAdmission: &testCatalogCallAdmissionV2{},
	})
	assertCatalogFaultV2(
		t, err, fault.ProviderRejected, researchapp.CatalogFailureCorrelation, false,
	)
}

func TestLookupMediaUsesConfiguredSequentialBatches(t *testing.T) {
	t.Parallel()
	var batchSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call, err := readTestCallV2(request)
		if err != nil {
			t.Errorf("decode call: %v", err)
			return
		}
		var catalog wireLookupRequestV2
		if err := json.Unmarshal(call.Params.Arguments.Catalog, &catalog); err != nil {
			t.Errorf("decode lookup: %v", err)
			return
		}
		batchSizes = append(batchSizes, len(catalog.IDs))
		writeJSONV2(t, writer, lookupSuccessResponseV2(call.ID, catalog.IDs))
	}))
	t.Cleanup(server.Close)
	gateway := mustGatewayV2(t, GatewayV2Config{
		Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
		HTTPClient: server.Client(), LookupBatchSize: 2,
	})
	inputs := make([]researchapp.CatalogMediaLookupInput, 0, 5)
	for index := 0; index < 5; index++ {
		inputs = append(inputs, researchapp.CatalogMediaLookupInput{
			CorrelationKey: fmt.Sprintf("candidate-%d", index),
			Identifier:     fmt.Sprintf("product-%d", index),
		})
	}
	result, err := gateway.LookupMedia(context.Background(), researchapp.CatalogMediaLookupRequest{
		Inputs: inputs, ProviderCallAdmission: &testCatalogCallAdmissionV2{},
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !slices.Equal(batchSizes, []int{2, 2, 1}) || len(result.Matches) != 5 ||
		result.ProviderCallCount != 3 {
		t.Fatalf(
			"unexpected sequential batches=%v matches=%d providerCalls=%d",
			batchSizes, len(result.Matches), result.ProviderCallCount,
		)
	}
}

func TestLookupMediaAccountsDefaultSequentialBatches(t *testing.T) {
	for _, test := range []struct {
		name        string
		inputCount  int
		wantBatches []int
	}{
		{name: "eleven", inputCount: 11, wantBatches: []int{11}},
		{name: "twenty", inputCount: 20, wantBatches: []int{20}},
		{name: "more than twenty", inputCount: 21, wantBatches: []int{21}},
		{name: "maximum workspace", inputCount: 50, wantBatches: []int{50}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var batchSizes []int
			server := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				call, err := readTestCallV2(request)
				if err != nil {
					t.Errorf("decode call: %v", err)
					return
				}
				var catalog wireLookupRequestV2
				if err := json.Unmarshal(call.Params.Arguments.Catalog, &catalog); err != nil {
					t.Errorf("decode lookup: %v", err)
					return
				}
				batchSizes = append(batchSizes, len(catalog.IDs))
				writeJSONV2(t, writer, lookupSuccessResponseV2(call.ID, catalog.IDs))
			}))
			t.Cleanup(server.Close)
			gateway := mustGatewayV2(t, GatewayV2Config{
				Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
				HTTPClient: server.Client(),
			})
			admission := &testCatalogCallAdmissionV2{}
			request := lookupRequestV2(test.inputCount)
			request.ProviderCallAdmission = admission
			result, err := gateway.LookupMedia(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := len(test.wantBatches)
			if !slices.Equal(batchSizes, test.wantBatches) ||
				result.ProviderCallCount != wantCalls ||
				int(admission.acquired.Load()) != wantCalls ||
				admission.active.Load() != 0 || admission.maximumActive.Load() != 1 {
				t.Fatalf(
					"batches=%v providerCalls=%d admitted=%d active=%d maxActive=%d",
					batchSizes, result.ProviderCallCount, admission.acquired.Load(),
					admission.active.Load(), admission.maximumActive.Load(),
				)
			}
		})
	}
}

func TestLookupMediaRejectsMissingProviderCallAdmissionBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(server.Close)
	gateway := mustGatewayV2(t, GatewayV2Config{
		Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
		HTTPClient: server.Client(),
	})
	request := lookupRequestV2(1)
	request.ProviderCallAdmission = nil
	result, err := gateway.LookupMedia(context.Background(), request)
	assertCatalogFaultV2(
		t, err, fault.InvalidInput, researchapp.CatalogFailureRequestInvalid, false,
	)
	if calls.Load() != 0 || result.ProviderCallCount != 0 {
		t.Fatalf("calls=%d providerCalls=%d", calls.Load(), result.ProviderCallCount)
	}
}

func TestLookupMediaRedirectIsOneAdmittedProviderAttempt(t *testing.T) {
	for _, status := range []int{
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			var redirectedCalls atomic.Int32
			redirected := httptest.NewServer(http.HandlerFunc(func(
				http.ResponseWriter,
				*http.Request,
			) {
				redirectedCalls.Add(1)
			}))
			t.Cleanup(redirected.Close)

			var endpointCalls atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				endpointCalls.Add(1)
				if request.Method != http.MethodPost {
					t.Errorf("method=%q", request.Method)
				}
				if body, err := io.ReadAll(request.Body); err != nil || len(body) == 0 {
					t.Errorf("initial payload length=%d err=%v", len(body), err)
				}
				writer.Header().Set("Location", redirected.URL+"/unexpected")
				writer.WriteHeader(status)
			}))
			t.Cleanup(endpoint.Close)

			httpClient, err := sharedhttpclient.NewClient(
				endpoint.Client().Transport, time.Second,
			)
			if err != nil {
				t.Fatal(err)
			}
			gateway := mustGatewayV2(t, GatewayV2Config{
				Endpoint: endpoint.URL, ProfileURL: "https://agent.example/profile.json",
				HTTPClient: httpClient,
			})
			admission := &testCatalogCallAdmissionV2{}
			request := lookupRequestV2(1)
			request.ProviderCallAdmission = admission
			result, err := gateway.LookupMedia(context.Background(), request)
			assertCatalogFaultV2(
				t, err, fault.ProviderRejected,
				researchapp.CatalogFailureProtocolRejected, false,
			)
			if endpointCalls.Load() != 1 || redirectedCalls.Load() != 0 ||
				result.ProviderCallCount != 1 || admission.acquired.Load() != 1 ||
				admission.active.Load() != 0 {
				t.Fatalf(
					"endpoint=%d redirected=%d providerCalls=%d admitted=%d active=%d",
					endpointCalls.Load(), redirectedCalls.Load(), result.ProviderCallCount,
					admission.acquired.Load(), admission.active.Load(),
				)
			}
		})
	}
}

func TestLookupMediaSplitsOnlyExplicitBatchLimitErrors(t *testing.T) {
	t.Parallel()
	t.Run("explicit batch error", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			call, err := readTestCallV2(request)
			if err != nil {
				t.Errorf("decode call: %v", err)
				return
			}
			var catalog wireLookupRequestV2
			if err := json.Unmarshal(call.Params.Arguments.Catalog, &catalog); err != nil {
				t.Errorf("decode lookup: %v", err)
				return
			}
			if len(catalog.IDs) > 2 {
				writeJSONV2(t, writer, map[string]any{
					"jsonrpc": "2.0", "id": call.ID,
					"error": map[string]any{
						"code": -32602, "message": "do not expose this provider text",
						"data": map[string]any{
							"code": "BATCH_SIZE_EXCEEDED", "path": "catalog.ids", "limit": 2,
						},
					},
				})
				return
			}
			writeJSONV2(t, writer, lookupSuccessResponseV2(call.ID, catalog.IDs))
		}))
		t.Cleanup(server.Close)
		gateway := mustGatewayV2(t, GatewayV2Config{
			Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
			HTTPClient: server.Client(), LookupBatchSize: 4,
		})
		request := lookupRequestV2(4)
		admission := &testCatalogCallAdmissionV2{}
		request.ProviderCallAdmission = admission
		result, err := gateway.LookupMedia(context.Background(), request)
		if err != nil {
			t.Fatalf("explicit batch error should be split: %v", err)
		}
		if calls.Load() != 3 || len(result.Matches) != 4 ||
			result.ProviderCallCount != 3 || admission.acquired.Load() != 3 {
			t.Fatalf(
				"expected one failed call and two halves: calls=%d matches=%d providerCalls=%d admitted=%d",
				calls.Load(), len(result.Matches), result.ProviderCallCount,
				admission.acquired.Load(),
			)
		}
	})

	t.Run("generic invalid params", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			call, err := readTestCallV2(request)
			if err != nil {
				t.Errorf("decode call: %v", err)
				return
			}
			writeJSONV2(t, writer, map[string]any{
				"jsonrpc": "2.0", "id": call.ID,
				"error": map[string]any{
					"code": -32602, "message": "contains a secret search query",
					"data": map[string]any{"code": "INVALID_PARAMS"},
				},
			})
		}))
		t.Cleanup(server.Close)
		gateway := mustGatewayV2(t, GatewayV2Config{
			Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
			HTTPClient: server.Client(), LookupBatchSize: 4,
		})
		_, err := gateway.LookupMedia(context.Background(), lookupRequestV2(4))
		assertCatalogFaultV2(
			t, err, fault.ProviderRejected, researchapp.CatalogFailureValidation, false,
		)
		if calls.Load() != 1 {
			t.Fatalf("generic -32602 must not be split: calls=%d", calls.Load())
		}
		if strings.Contains(err.Error(), "secret search query") {
			t.Fatalf("raw provider error leaked: %v", err)
		}
	})
}

func TestLookupMediaSplitRetryStopsBeforeRateGuardDeniedCall(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		call, err := readTestCallV2(request)
		if err != nil {
			t.Errorf("decode call: %v", err)
			return
		}
		var catalog wireLookupRequestV2
		if err := json.Unmarshal(call.Params.Arguments.Catalog, &catalog); err != nil {
			t.Errorf("decode lookup: %v", err)
			return
		}
		if len(catalog.IDs) > 2 {
			writeJSONV2(t, writer, map[string]any{
				"jsonrpc": "2.0", "id": call.ID,
				"error": map[string]any{
					"code": -32602, "message": "batch too large",
					"data": map[string]any{
						"code": "BATCH_SIZE_EXCEEDED", "path": "catalog.ids", "limit": 2,
					},
				},
			})
			return
		}
		writeJSONV2(t, writer, lookupSuccessResponseV2(call.ID, catalog.IDs))
	}))
	t.Cleanup(server.Close)
	gateway := mustGatewayV2(t, GatewayV2Config{
		Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
		HTTPClient: server.Client(), LookupBatchSize: 4,
	})
	admission := &testCatalogCallAdmissionV2{limit: 2}
	request := lookupRequestV2(4)
	request.ProviderCallAdmission = admission
	result, err := gateway.LookupMedia(context.Background(), request)
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.RateLimited || failure.Reason != "TEST_RATE_LIMIT" {
		t.Fatalf("failure=%v", err)
	}
	if calls.Load() != 2 || result.ProviderCallCount != 2 ||
		admission.acquired.Load() != 2 || admission.active.Load() != 0 {
		t.Fatalf(
			"calls=%d providerCalls=%d admitted=%d active=%d",
			calls.Load(), result.ProviderCallCount, admission.acquired.Load(),
			admission.active.Load(),
		)
	}
}

func TestLookupOffersSplitRetryAccountsEveryProviderAttempt(t *testing.T) {
	for _, test := range []struct {
		name           string
		admissionLimit int32
		wantError      bool
		wantCalls      int32
	}{
		{name: "three admitted calls", admissionLimit: 3, wantCalls: 3},
		{name: "third call denied", admissionLimit: 2, wantError: true, wantCalls: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				calls.Add(1)
				call, err := readTestCallV2(request)
				if err != nil {
					t.Errorf("decode call: %v", err)
					return
				}
				var catalog wireLookupRequestV2
				if err := json.Unmarshal(call.Params.Arguments.Catalog, &catalog); err != nil {
					t.Errorf("decode lookup: %v", err)
					return
				}
				if len(catalog.IDs) > 5 {
					writeJSONV2(t, writer, map[string]any{
						"jsonrpc": "2.0", "id": call.ID,
						"error": map[string]any{
							"code": -32602, "message": "batch too large",
							"data": map[string]any{
								"code": "BATCH_SIZE_EXCEEDED",
								"path": "catalog.ids", "limit": 5,
							},
						},
					})
					return
				}
				writeJSONV2(t, writer, lookupSuccessResponseV2(call.ID, catalog.IDs))
			}))
			t.Cleanup(server.Close)
			gateway := mustGatewayV2(t, GatewayV2Config{
				Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
				HTTPClient: server.Client(), LookupBatchSize: 10,
			})
			admission := &testCatalogCallAdmissionV2{limit: test.admissionLimit}
			request := offerLookupRequestV2(10)
			request.ProviderCallAdmission = admission
			result, err := gateway.LookupOffers(context.Background(), request)
			if test.wantError {
				failure, ok := fault.As(err)
				if !ok || failure.Code != fault.RateLimited ||
					failure.Reason != "TEST_RATE_LIMIT" {
					t.Fatalf("failure=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != test.wantCalls ||
				int32(result.ProviderCallCount) != test.wantCalls ||
				admission.acquired.Load() != test.wantCalls || admission.active.Load() != 0 {
				t.Fatalf(
					"calls=%d providerCalls=%d admitted=%d active=%d",
					calls.Load(), result.ProviderCallCount, admission.acquired.Load(),
					admission.active.Load(),
				)
			}
			if !test.wantError && len(result.Matches) != 10 {
				t.Fatalf("matches=%d", len(result.Matches))
			}
		})
	}
}

func TestLookupOffersRejectsMissingProviderCallAdmissionBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(server.Close)
	gateway := mustGatewayV2(t, GatewayV2Config{
		Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
		HTTPClient: server.Client(),
	})
	request := offerLookupRequestV2(1)
	request.ProviderCallAdmission = nil
	result, err := gateway.LookupOffers(context.Background(), request)
	assertCatalogFaultV2(
		t, err, fault.InvalidInput, researchapp.CatalogFailureRequestInvalid, false,
	)
	if calls.Load() != 0 || result.ProviderCallCount != 0 {
		t.Fatalf("calls=%d providerCalls=%d", calls.Load(), result.ProviderCallCount)
	}
}

func TestShopifyV2FailureTaxonomy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		code       fault.Code
		reason     researchapp.CatalogFailureReason
		retryable  bool
		retryAfter time.Duration
	}{
		{
			name: "HTTP rate limit",
			err:  classifyHTTPFailureV2(429, "7", nil),
			code: fault.RateLimited, reason: researchapp.CatalogFailureRateLimited,
			retryable: true, retryAfter: 7 * time.Second,
		},
		{
			name: "Storefront security",
			err:  classifyHTTPFailureV2(430, "", nil),
			code: fault.ProviderRejected, reason: researchapp.CatalogFailureSecurityRejected,
		},
		{
			name: "profile auth",
			err:  classifyHTTPFailureV2(401, "", nil),
			code: fault.ProviderRejected, reason: researchapp.CatalogFailureProfileOrAuth,
		},
		{
			name: "unavailable",
			err:  classifyHTTPFailureV2(503, "3", nil),
			code: fault.ProviderUnavailable, reason: researchapp.CatalogFailureUnavailable,
			retryable: true, retryAfter: 3 * time.Second,
		},
		{
			name: "RPC throttled",
			err: classifyRPCFailureV2(&wireRPCErrorV2{
				Code: -32000,
				Data: json.RawMessage(`{"code":"THROTTLED","retry_after":5}`),
			}),
			code: fault.RateLimited, reason: researchapp.CatalogFailureRateLimited,
			retryable: true, retryAfter: 5 * time.Second,
		},
		{
			name: "RPC internal error",
			err:  classifyRPCFailureV2(&wireRPCErrorV2{Code: -32603}),
			code: fault.ProviderUnavailable, reason: researchapp.CatalogFailureUnavailable,
			retryable: true,
		},
		{
			name: "capability mismatch",
			err: classifyRPCFailureV2(&wireRPCErrorV2{
				Code: -32000,
				Data: json.RawMessage(`{"code":"capabilities_incompatible"}`),
			}),
			code: fault.ProviderRejected, reason: researchapp.CatalogFailureSchemaMismatch,
		},
		{
			name: "HTTP capability mismatch",
			err: classifyHTTPFailureV2(
				422,
				"",
				[]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"raw","data":{"code":"capabilities_incompatible"}}}`),
			),
			code: fault.ProviderRejected, reason: researchapp.CatalogFailureSchemaMismatch,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertCatalogFaultV2(t, test.err, test.code, test.reason, test.retryable)
			classified, _ := fault.As(test.err)
			if classified.RetryAfter != test.retryAfter {
				t.Fatalf("retry-after=%v want %v", classified.RetryAfter, test.retryAfter)
			}
		})
	}
}

func TestNewGatewayV2RejectsLookupBatchAboveAppHardMaximum(t *testing.T) {
	t.Parallel()
	_, err := NewGatewayV2(GatewayV2Config{
		Endpoint:        "https://catalog.shopify.com/api/ucp/mcp",
		ProfileURL:      "https://agent.example/profile.json",
		HTTPClient:      http.DefaultClient,
		LookupBatchSize: researchapp.CatalogLookupMaxBatchSize + 1,
	})
	classified, ok := fault.As(err)
	if !ok || classified.Code != fault.InvalidInput {
		t.Fatalf("expected invalid batch configuration: %v", err)
	}
}

func TestGatewayUsesAgentTokenAndRefreshesOnceAfterUnauthorized(t *testing.T) {
	fixture, err := os.ReadFile("testdata/search_success_status_omitted.json")
	if err != nil {
		t.Fatal(err)
	}
	tokens := &testCatalogTokenSourceV2{current: "stale-token", refreshed: "fresh-token"}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		switch request.Header.Get("Authorization") {
		case "Bearer stale-token":
			writer.WriteHeader(http.StatusUnauthorized)
		case "Bearer fresh-token":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(fixture)
		default:
			t.Errorf("authorization header was not token-tier: %q", request.Header.Get("Authorization"))
			writer.WriteHeader(http.StatusForbidden)
		}
	}))
	t.Cleanup(server.Close)
	gateway := mustGatewayV2(t, GatewayV2Config{
		Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
		HTTPClient: server.Client(), Tokens: tokens,
	})
	_, err = gateway.SearchProducts(context.Background(), researchapp.CatalogProductSearchRequest{
		Query: "trail shoes", Limit: 1,
	})
	if err != nil || requests != 2 || tokens.invalidations != 1 {
		t.Fatalf("search err=%v requests=%d invalidations=%d", err, requests, tokens.invalidations)
	}
}

func TestGatewaySharesProviderRateLimitCooldown(t *testing.T) {
	fixture, err := os.ReadFile("testdata/search_success_status_omitted.json")
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if requests == 1 {
			writer.Header().Set("Retry-After", "3")
			writer.WriteHeader(http.StatusTooManyRequests)
			return
		}
		call, decodeErr := readTestCallV2(request)
		if decodeErr != nil {
			t.Errorf("decode call: %v", decodeErr)
			return
		}
		var response map[string]any
		if json.Unmarshal(fixture, &response) != nil {
			t.Error("decode fixture")
			return
		}
		response["id"] = call.ID
		writeJSONV2(t, writer, response)
	}))
	t.Cleanup(server.Close)
	gateway := mustGatewayV2(t, GatewayV2Config{
		Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
		HTTPClient: server.Client(),
	})
	now := time.Date(2026, 9, 1, 1, 2, 3, 0, time.UTC)
	gateway.now = func() time.Time { return now }
	request := researchapp.CatalogProductSearchRequest{Query: "trail shoes", Limit: 1}
	_, firstErr := gateway.SearchProducts(context.Background(), request)
	_, cooldownErr := gateway.SearchProducts(context.Background(), request)
	if requests != 1 {
		t.Fatalf("provider cooldown made %d requests, want 1", requests)
	}
	first, firstOK := fault.As(firstErr)
	cooldown, cooldownOK := fault.As(cooldownErr)
	if !firstOK || !cooldownOK || first.Code != fault.RateLimited ||
		cooldown.Code != fault.RateLimited || cooldown.RetryAfter != 3*time.Second {
		t.Fatalf("first=%v cooldown=%v", firstErr, cooldownErr)
	}
	now = now.Add(3 * time.Second)
	if _, err := gateway.SearchProducts(context.Background(), request); err != nil || requests != 2 {
		t.Fatalf("post-cooldown search err=%v requests=%d", err, requests)
	}
}

type testCallV2 struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  struct {
		Name      string `json:"name"`
		Arguments struct {
			Meta    map[string]wireAgentProfileV2 `json:"meta"`
			Catalog json.RawMessage               `json:"catalog"`
		} `json:"arguments"`
	} `json:"params"`
}

type testCatalogTokenSourceV2 struct {
	current       string
	refreshed     string
	invalidations int
}

func (source *testCatalogTokenSourceV2) AccessToken(context.Context) (string, error) {
	return source.current, nil
}

func (source *testCatalogTokenSourceV2) InvalidateAccessToken(accessToken string) {
	if accessToken == source.current {
		source.invalidations++
		source.current = source.refreshed
	}
}

func fixtureGatewayV2(
	t *testing.T,
	fixtureName string,
	inspect func(*testing.T, testCallV2),
) *GatewayV2 {
	t.Helper()
	fixture, err := os.ReadFile("testdata/" + fixtureName)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call, err := readTestCallV2(request)
		if err != nil {
			t.Errorf("decode call: %v", err)
			return
		}
		if inspect != nil {
			inspect(t, call)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(fixture)
	}))
	t.Cleanup(server.Close)
	return mustGatewayV2(t, GatewayV2Config{
		Endpoint: server.URL, ProfileURL: "https://agent.example/profile.json",
		HTTPClient: server.Client(),
	})
}

func mustGatewayV2(t *testing.T, config GatewayV2Config) *GatewayV2 {
	t.Helper()
	gateway, err := NewGatewayV2(config)
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	return gateway
}

func readTestCallV2(request *http.Request) (testCallV2, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return testCallV2{}, err
	}
	var call testCallV2
	if err := json.Unmarshal(body, &call); err != nil {
		return testCallV2{}, err
	}
	return call, nil
}

func writeJSONV2(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func lookupSuccessResponseV2(requestID int64, identifiers []string) map[string]any {
	products := make([]any, 0, len(identifiers))
	for index := len(identifiers) - 1; index >= 0; index-- {
		identifier := identifiers[index]
		products = append(products, map[string]any{
			"id":          "resolved-" + identifier,
			"title":       "Resolved " + identifier,
			"description": map[string]any{"plain": "Lookup fixture"},
			"price_range": map[string]any{
				"min": map[string]any{"amount": 1000, "currency": "USD"},
				"max": map[string]any{"amount": 1000, "currency": "USD"},
			},
			"media": []any{map[string]any{
				"type": "image", "url": "https://cdn.example/" + identifier + ".jpg",
			}},
			"variants": []any{map[string]any{
				"id":     "variant-" + identifier,
				"title":  "Variant " + identifier,
				"price":  map[string]any{"amount": 1000, "currency": "USD"},
				"inputs": []any{map[string]any{"id": identifier, "match": "featured"}},
			}},
		})
	}
	return map[string]any{
		"jsonrpc": "2.0", "id": requestID,
		"result": map[string]any{
			"structuredContent": map[string]any{
				"ucp": map[string]any{
					"version": "2026-04-08",
					"capabilities": map[string]any{
						"dev.ucp.shopping.catalog.lookup": []any{map[string]any{"version": "2026-04-08"}},
					},
				},
				"products": products,
			},
		},
	}
}

func lookupRequestV2(count int) researchapp.CatalogMediaLookupRequest {
	request := researchapp.CatalogMediaLookupRequest{
		Inputs:                make([]researchapp.CatalogMediaLookupInput, 0, count),
		ProviderCallAdmission: &testCatalogCallAdmissionV2{},
	}
	for index := 0; index < count; index++ {
		request.Inputs = append(request.Inputs, researchapp.CatalogMediaLookupInput{
			CorrelationKey: fmt.Sprintf("candidate-%d", index),
			Identifier:     fmt.Sprintf("product-%d", index),
		})
	}
	return request
}

func offerLookupRequestV2(count int) researchapp.CatalogOfferLookupRequest {
	request := researchapp.CatalogOfferLookupRequest{
		Inputs:                make([]researchapp.CatalogOfferLookupInput, 0, count),
		Context:               researchapp.CatalogBuyerContext{Country: "US", Currency: "USD"},
		ProviderCallAdmission: &testCatalogCallAdmissionV2{},
	}
	for index := 0; index < count; index++ {
		request.Inputs = append(request.Inputs, researchapp.CatalogOfferLookupInput{
			DraftID:    fmt.Sprintf("draft-%d", index),
			Identifier: fmt.Sprintf("product-%d", index),
		})
	}
	return request
}

type testCatalogCallAdmissionV2 struct {
	limit         int32
	acquired      atomic.Int32
	active        atomic.Int32
	maximumActive atomic.Int32
}

func (admission *testCatalogCallAdmissionV2) AcquireCatalogProviderCall(
	context.Context,
) (func(), error) {
	acquired := admission.acquired.Add(1)
	if admission.limit > 0 && acquired > admission.limit {
		admission.acquired.Add(-1)
		return nil, fault.New(fault.RateLimited, "TEST_RATE_LIMIT", true)
	}
	active := admission.active.Add(1)
	for {
		maximum := admission.maximumActive.Load()
		if active <= maximum || admission.maximumActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	return func() {
		admission.active.Add(-1)
	}, nil
}

func assertCatalogFaultV2(
	t *testing.T,
	err error,
	wantCode fault.Code,
	wantReason researchapp.CatalogFailureReason,
	wantRetryable bool,
) {
	t.Helper()
	if err == nil {
		t.Fatal("expected classified error")
	}
	classified, ok := fault.As(err)
	if !ok {
		t.Fatalf("expected fault classification: %T %v", err, err)
	}
	if classified.Code != wantCode || classified.Reason != string(wantReason) ||
		classified.Retryable != wantRetryable {
		t.Fatalf(
			"fault=(%s,%s,retry=%t) want=(%s,%s,retry=%t)",
			classified.Code, classified.Reason, classified.Retryable,
			wantCode, wantReason, wantRetryable,
		)
	}
}

func TestSearchNormalizationKeepsUsablePreviewAndUnknownPrice(t *testing.T) {
	raw, err := os.ReadFile("testdata/search_multi_variant_product.json")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Result struct {
			Content struct {
				Products []wireProductV2 `json:"products"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	p := body.Result.Content.Products[0]
	(*p.Variants)[0].ID = ""
	got, err := normalizeProductObservationV2(p, 0)
	if err != nil || got.PreviewVariant == nil || got.PreviewVariant.ID != "gid://shopify/ProductVariant/second" {
		t.Fatalf("bad first preview hid second: %+v %v", got, err)
	}
	p.PriceRange = nil
	got, err = normalizeProductObservationV2(p, 0)
	if err != nil || got.PriceRange.Minimum.Currency != "" || got.Locator == nil {
		t.Fatalf("missing price lost valid product: %+v %v", got, err)
	}
}

func TestMalformedProductJSONDoesNotEraseNeighbor(t *testing.T) {
	var rows []wireProductV2
	raw := `[{"id":"bad","title":"Broken","price_range":{"min":{"amount":"oops"}}},{"id":"good","title":"Shoe","url":"https://merchant.example/products/good","variants":[]}]`
	if err := json.Unmarshal([]byte(raw), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("one malformed row broke page: %v", err)
	}
	if _, err := normalizeProductObservationV2(rows[0], 0); err == nil {
		t.Fatal("malformed row admitted")
	}
	if p, err := normalizeProductObservationV2(rows[1], 1); err != nil || p.ProviderProductID != "good" {
		t.Fatalf("valid neighbor lost: %+v %v", p, err)
	}
}
