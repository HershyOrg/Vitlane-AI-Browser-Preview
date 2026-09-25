package shopifystorefront

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestClientV2LoadsProductHandleVariantPageWithoutBuyerIP(t *testing.T) {
	t.Parallel()
	var captured *http.Request
	var payload graphQLRequestV2
	transport := roundTripFuncV2(func(request *http.Request) (*http.Response, error) {
		captured = request.Clone(request.Context())
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return storefrontResponseV2(http.StatusOK, storefrontSuccessBodyV2()), nil
	})
	client := mustStorefrontClientV2(t, transport, nil)
	result, err := client.LoadVariantPage(context.Background(), storefrontQueryFixtureV2())
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if captured.URL.String() !=
		"https://merchant.myshopify.com/api/2026-07/graphql.json" ||
		captured.Method != http.MethodPost ||
		!strings.Contains(payload.Query, "product(handle: $handle)") ||
		!strings.Contains(payload.Query, "variants(first: 20, after: $after)") ||
		payload.Variables.Handle != "trail-shoe" || payload.Variables.Country != "US" ||
		payload.Variables.After != nil {
		t.Fatalf("unexpected Storefront request: url=%v payload=%+v", captured.URL, payload)
	}
	for _, header := range []string{
		"Shopify-Storefront-Buyer-IP", "X-Shopify-Storefront-Buyer-IP",
		"X-Forwarded-For", "Forwarded", "True-Client-IP", "CF-Connecting-IP",
		"X-Real-IP",
	} {
		if captured.Header.Get(header) != "" {
			t.Fatalf("agent request leaked buyer IP header %s", header)
		}
	}
	if result.ShopID != "gid://shopify/Shop/1" ||
		result.ProductID != "gid://shopify/Product/10" ||
		len(result.Rows) != 1 ||
		result.Rows[0].VariantID != "gid://shopify/ProductVariant/100" ||
		!result.HasNextPage || result.EndCursor != "cursor-1" {
		t.Fatalf("unexpected normalized page: %+v", result)
	}
}

func TestClientV2ClassifiesGraphQL200ErrorsWithoutRawLeak(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		code       string
		wantCode   fault.Code
		wantReason string
		retryable  bool
	}{
		{
			name: "throttled", code: "THROTTLED", wantCode: fault.RateLimited,
			wantReason: string(researchapp.CatalogFailureRateLimited), retryable: true,
		},
		{
			name: "internal", code: "INTERNAL_SERVER_ERROR",
			wantCode:   fault.ProviderUnavailable,
			wantReason: string(researchapp.CatalogFailureUnavailable), retryable: true,
		},
		{
			name: "max complexity", code: "MAX_COMPLEXITY_EXCEEDED",
			wantCode:   fault.ProviderRejected,
			wantReason: string(researchapp.CatalogFailureValidation), retryable: false,
		},
		{
			name: "access denied", code: "ACCESS_DENIED",
			wantCode:   fault.ProviderRejected,
			wantReason: string(researchapp.CatalogFailureProfileOrAuth), retryable: false,
		},
		{
			name: "unknown", code: "FUTURE_SECRET_ERROR",
			wantCode:   fault.ProviderRejected,
			wantReason: string(researchapp.CatalogFailureProtocolRejected), retryable: false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := `{"data":null,"errors":[{"message":"customer alice@example.com token secret","extensions":{"code":"` +
				test.code + `"}}]}`
			transport := roundTripFuncV2(func(*http.Request) (*http.Response, error) {
				return storefrontResponseV2(http.StatusOK, body), nil
			})
			client := mustStorefrontClientV2(t, transport, nil)
			_, err := client.LoadVariantPage(context.Background(), storefrontQueryFixtureV2())
			assertStorefrontFaultV2(
				t, err, test.wantCode, test.wantReason, test.retryable,
			)
			if strings.Contains(err.Error(), "alice@example.com") ||
				strings.Contains(err.Error(), "token secret") {
				t.Fatalf("raw GraphQL error leaked: %v", err)
			}
		})
	}
}

func TestClientV2Classifies430AsSecurityRejection(t *testing.T) {
	t.Parallel()
	transport := roundTripFuncV2(func(*http.Request) (*http.Response, error) {
		return storefrontResponseV2(
			storefrontSecurityStatusV2,
			`{"error":"buyer alice@example.com blocked"}`,
		), nil
	})
	client := mustStorefrontClientV2(t, transport, nil)
	_, err := client.LoadVariantPage(context.Background(), storefrontQueryFixtureV2())
	assertStorefrontFaultV2(
		t, err, fault.ProviderRejected,
		string(researchapp.CatalogFailureSecurityRejected), false,
	)
	if strings.Contains(err.Error(), "alice@example.com") {
		t.Fatalf("raw 430 body leaked: %v", err)
	}
}

func TestClientV2RejectsAuthorizerBuyerIPBeforeTransport(t *testing.T) {
	t.Parallel()
	transportCalls := 0
	transport := roundTripFuncV2(func(*http.Request) (*http.Response, error) {
		transportCalls++
		return storefrontResponseV2(http.StatusOK, storefrontSuccessBodyV2()), nil
	})
	client := mustStorefrontClientV2(t, transport, buyerIPAuthorizerV2{})
	_, err := client.LoadVariantPage(context.Background(), storefrontQueryFixtureV2())
	assertStorefrontFaultV2(
		t, err, fault.ProviderRejected,
		string(researchapp.CatalogFailureSecurityRejected), false,
	)
	if transportCalls != 0 {
		t.Fatalf("unsafe buyer-IP request reached transport: calls=%d", transportCalls)
	}
}

func TestClientV2RejectsAuthorizerBodyMutationBeforeTransport(t *testing.T) {
	t.Parallel()
	transportCalls := 0
	transport := roundTripFuncV2(func(*http.Request) (*http.Response, error) {
		transportCalls++
		return storefrontResponseV2(http.StatusOK, storefrontSuccessBodyV2()), nil
	})
	client := mustStorefrontClientV2(t, transport, bodyMutationAuthorizerV2{})
	_, err := client.LoadVariantPage(context.Background(), storefrontQueryFixtureV2())
	assertStorefrontFaultV2(
		t, err, fault.ProviderRejected,
		string(researchapp.CatalogFailureSecurityRejected), false,
	)
	if transportCalls != 0 {
		t.Fatalf("mutated GraphQL request reached transport: calls=%d", transportCalls)
	}
}

func TestClientV2RejectsAuthorizerHostOverrideBeforeTransport(t *testing.T) {
	t.Parallel()
	transportCalls := 0
	transport := roundTripFuncV2(func(*http.Request) (*http.Response, error) {
		transportCalls++
		return storefrontResponseV2(http.StatusOK, storefrontSuccessBodyV2()), nil
	})
	client := mustStorefrontClientV2(t, transport, hostOverrideAuthorizerV2{})
	_, err := client.LoadVariantPage(context.Background(), storefrontQueryFixtureV2())
	assertStorefrontFaultV2(
		t, err, fault.ProviderRejected,
		string(researchapp.CatalogFailureSecurityRejected), false,
	)
	if transportCalls != 0 {
		t.Fatalf("overridden storefront Host reached transport: calls=%d", transportCalls)
	}
}

func TestNormalizeStorefrontPageV2RejectsMoreThanTenRows(t *testing.T) {
	t.Parallel()
	product := graphQLProductV2{
		ID: "gid://shopify/Product/10", Handle: "trail-shoe",
		OnlineStoreURL: storefrontStringPointerV2(
			"https://merchant.example/products/trail-shoe",
		),
		Variants: graphQLVariantConnectionV2{
			Nodes: make([]graphQLVariantV2, researchapp.VariantChoicePageSizeV2+1),
		},
	}
	shop := graphQLShopV2{
		ID: "gid://shopify/Shop/1",
		PrimaryDomain: graphQLPrimaryDomainV2{
			Host: "merchant.example", URL: "https://merchant.example",
		},
	}
	_, err := normalizeStorefrontPageV2(storefrontQueryFixtureV2(), shop, product)
	assertStorefrontFaultV2(
		t, err, fault.ProviderRejected,
		string(researchapp.CatalogFailureSchemaMismatch), false,
	)
}

func TestNormalizeStorefrontPageV2RejectsVerifiedShopMismatch(t *testing.T) {
	t.Parallel()
	product := graphQLProductV2{
		ID: "gid://shopify/Product/10", Handle: "trail-shoe",
		OnlineStoreURL: storefrontStringPointerV2(
			"https://merchant.example/products/trail-shoe",
		),
	}
	shop := graphQLShopV2{
		ID: "gid://shopify/Shop/999",
		PrimaryDomain: graphQLPrimaryDomainV2{
			Host: "merchant.example", URL: "https://merchant.example",
		},
	}
	_, err := normalizeStorefrontPageV2(storefrontQueryFixtureV2(), shop, product)
	assertStorefrontFaultV2(
		t, err, fault.ProviderRejected,
		researchapp.VariantPageCorrelationMismatchV2, false,
	)
}

func storefrontSuccessBodyV2() string {
	return `{
  "data": {
    "shop": {
      "id": "gid://shopify/Shop/1",
      "primaryDomain": {"host": "merchant.example", "url": "https://merchant.example"}
    },
    "product": {
      "id": "gid://shopify/Product/10",
      "handle": "trail-shoe",
      "title": "Trail Shoe",
      "onlineStoreUrl": "https://merchant.example/products/trail-shoe",
      "options": [{"name": "Title", "optionValues": [{"name": "Default Title"}]}],
      "variants": {
        "nodes": [{
          "id": "gid://shopify/ProductVariant/100",
          "title": "Default Title",
          "availableForSale": true,
          "selectedOptions": [{"name": "Title", "value": "Default Title"}],
          "price": {"amount": "100.00", "currencyCode": "USD"},
          "image": {"url": "https://cdn.example/trail-shoe.jpg", "altText": "Trail shoe"}
        }],
        "pageInfo": {"hasNextPage": true, "endCursor": "cursor-1"}
      }
    }
  }
}`
}

func storefrontQueryFixtureV2() researchapp.StorefrontVariantQueryV2 {
	return researchapp.StorefrontVariantQueryV2{
		MyshopifyDomain:       "merchant.myshopify.com",
		ExpectedShopID:        "gid://shopify/Shop/1",
		ExpectedPrimaryDomain: "merchant.example",
		MappingEvidenceID:     "mapping-evidence-1",
		ExpectedProductID:     "gid://shopify/Product/10",
		ProductHandle:         "trail-shoe", Country: "US", Currency: "USD",
		First:                 researchapp.VariantChoicePageSizeV2,
		ProviderCallAdmission: &storefrontTestCallAdmissionV2{},
	}
}

func TestStorefrontVariantPageRejectsMissingAdmissionBeforeTransport(t *testing.T) {
	transportCalls := 0
	client := mustStorefrontClientV2(t, roundTripFuncV2(func(
		*http.Request,
	) (*http.Response, error) {
		transportCalls++
		return storefrontResponseV2(http.StatusOK, storefrontSuccessBodyV2()), nil
	}), nil)
	query := storefrontQueryFixtureV2()
	query.ProviderCallAdmission = nil
	_, err := client.LoadVariantPage(context.Background(), query)
	assertStorefrontFaultV2(
		t, err, fault.InvalidInput, researchapp.VariantChoiceFailureInvalidV2, false,
	)
	if transportCalls != 0 {
		t.Fatalf("transport calls=%d", transportCalls)
	}
}

type roundTripFuncV2 func(*http.Request) (*http.Response, error)

func (function roundTripFuncV2) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func storefrontResponseV2(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

type buyerIPAuthorizerV2 struct{}

func (buyerIPAuthorizerV2) AuthorizeAgentRequest(
	_ context.Context,
	request *http.Request,
) error {
	request.Header.Set("Shopify-Storefront-Buyer-IP", "203.0.113.10")
	return nil
}

type bodyMutationAuthorizerV2 struct{}

func (bodyMutationAuthorizerV2) AuthorizeAgentRequest(
	_ context.Context,
	request *http.Request,
) error {
	request.Body = io.NopCloser(strings.NewReader(`mutation { customerCreate { id } }`))
	return nil
}

type hostOverrideAuthorizerV2 struct{}

func (hostOverrideAuthorizerV2) AuthorizeAgentRequest(
	_ context.Context,
	request *http.Request,
) error {
	request.Host = "another-store.myshopify.com"
	return nil
}

func mustStorefrontClientV2(
	t *testing.T,
	transport http.RoundTripper,
	authorizer AgentRequestAuthorizerV2,
) *ClientV2 {
	t.Helper()
	client, err := NewClientV2(ClientV2Config{
		HTTPClient:      &http.Client{Transport: transport},
		AgentAuthorizer: authorizer,
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return client
}

func assertStorefrontFaultV2(
	t *testing.T,
	err error,
	wantCode fault.Code,
	wantReason string,
	wantRetryable bool,
) {
	t.Helper()
	classified, ok := fault.As(err)
	if !ok || classified.Code != wantCode || classified.Reason != wantReason ||
		classified.Retryable != wantRetryable {
		t.Fatalf(
			"fault=%v want code=%s reason=%s retryable=%t",
			err, wantCode, wantReason, wantRetryable,
		)
	}
}

func storefrontStringPointerV2(value string) *string { return &value }
