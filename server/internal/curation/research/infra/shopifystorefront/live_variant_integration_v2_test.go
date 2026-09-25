package shopifystorefront

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestLiveVariantSourceResolverFreshlyMapsProductURLToStorefrontIdentity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	source := storefrontProductSourceFixtureV2(t)
	lookup := &offerLookupGatewayStubV2{result: researchapp.CatalogOfferLookupResult{
		Provider: "SHOPIFY_UCP", ProtocolVersion: "2026-04-08",
		Outcome: researchapp.CatalogOutcomeSuccess,
		Matches: []researchapp.CatalogOfferMatch{{
			DraftID:             source.CandidateID,
			RequestedIdentifier: source.Locator.ProductURL.CanonicalURL,
			Product: researchapp.CatalogProductObservation{
				Handle: "trail-shoe", Locator: &researchapp.CatalogProductLocator{
					Kind: researchapp.CatalogLocatorProductURL,
					ProductURL: &researchapp.CatalogProductURLLocator{
						CanonicalURL: source.Locator.ProductURL.CanonicalURL,
					},
				},
			},
			Variant: researchapp.CatalogPreviewVariant{Seller: &researchapp.CatalogSeller{
				ID: "gid://shopify/Shop/1", Domain: "merchant.myshopify.com",
			}},
		}},
	}}
	identityCalls := 0
	client := mustStorefrontClientV2(t, roundTripFuncV2(func(request *http.Request) (*http.Response, error) {
		identityCalls++
		if request.URL.Hostname() != "merchant.myshopify.com" ||
			request.Header.Get("Cookie") != "" ||
			request.Header.Get("Shopify-Storefront-Buyer-IP") != "" {
			t.Fatalf("unsafe identity request: %s headers=%v", request.URL, request.Header)
		}
		var payload identityGraphQLRequestV2
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.Query, "product(handle: $handle)") ||
			payload.Variables.Handle != "trail-shoe" || payload.Variables.VariantID != "" {
			t.Fatalf("identity payload=%+v", payload)
		}
		return storefrontResponseV2(http.StatusOK, storefrontIdentityBodyV2(false)), nil
	}), nil)
	resolver, err := NewLiveVariantSourceResolverV2(
		lookup, client, fixedStorefrontClockV2{now: now}, 5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	admission := &storefrontTestCallAdmissionV2{}
	result, err := resolver.ResolveVariantChoiceSource(
		context.Background(), "user-1", source, admission,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.calls != 1 || identityCalls != 1 || admission.calls != 2 ||
		result.StorefrontShop.MyshopifyDomain != "merchant.myshopify.com" ||
		result.StorefrontShop.PrimaryDomain != "merchant.example" ||
		result.StorefrontShop.ShopID != "gid://shopify/Shop/1" ||
		result.ProductID != "gid://shopify/Product/10" ||
		result.ProductHandle != "trail-shoe" || result.StorefrontVariantID != "" ||
		result.StorefrontShop.MappingEvidenceID == "" || !result.ExpiresAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("resolution=%+v lookupCalls=%d identityCalls=%d", result, lookup.calls, identityCalls)
	}
}

func TestLiveVariantSourceResolverFreshlyMapsMerchantVariantAndChecksParent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	source := storefrontMerchantVariantSourceFixtureV2(t)
	lookup := &offerLookupGatewayStubV2{result: researchapp.CatalogOfferLookupResult{
		Provider: "SHOPIFY_UCP", ProtocolVersion: "2026-04-08",
		Outcome: researchapp.CatalogOutcomeSuccess,
		Matches: []researchapp.CatalogOfferMatch{{
			DraftID:             source.CandidateID,
			RequestedIdentifier: source.Locator.MerchantVariant.VariantID,
			Product:             researchapp.CatalogProductObservation{},
			Variant: researchapp.CatalogPreviewVariant{
				ID:  source.Locator.MerchantVariant.VariantID,
				URL: "https://merchant.example/products/trail-shoe?variant=100&_gsid=fresh",
				Seller: &researchapp.CatalogSeller{
					ID: "gid://shopify/Shop/1", Domain: "merchant.example",
					URL: "https://merchant.myshopify.com",
				},
			},
		}},
	}}
	client := mustStorefrontClientV2(t, roundTripFuncV2(func(request *http.Request) (*http.Response, error) {
		var payload identityGraphQLRequestV2
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.Query, "node(id: $variantId)") ||
			payload.Variables.VariantID != source.Locator.MerchantVariant.VariantID {
			t.Fatalf("variant identity payload=%+v", payload)
		}
		return storefrontResponseV2(http.StatusOK, storefrontIdentityBodyV2(true)), nil
	}), nil)
	resolver, err := NewLiveVariantSourceResolverV2(
		lookup, client, fixedStorefrontClockV2{now: now}, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolver.ResolveVariantChoiceSource(
		context.Background(), "user-1", source, &storefrontTestCallAdmissionV2{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.StorefrontVariantID != source.Locator.MerchantVariant.VariantID ||
		result.SeedAuthority != "merchant.example" ||
		result.ProductID != "gid://shopify/Product/10" {
		t.Fatalf("merchant resolution=%+v", result)
	}
}

func TestLiveVariantSourceResolverDoesNotUseUnverifiedSellerDomainAsOrigin(t *testing.T) {
	t.Parallel()
	source := storefrontMerchantVariantSourceFixtureV2(t)
	lookup := &offerLookupGatewayStubV2{result: researchapp.CatalogOfferLookupResult{
		Provider: "SHOPIFY_UCP", ProtocolVersion: "2026-04-08",
		Outcome: researchapp.CatalogOutcomeSuccess,
		Matches: []researchapp.CatalogOfferMatch{{
			DraftID:             source.CandidateID,
			RequestedIdentifier: source.Locator.MerchantVariant.VariantID,
			Product:             researchapp.CatalogProductObservation{Handle: "trail-shoe"},
			Variant: researchapp.CatalogPreviewVariant{
				ID: source.Locator.MerchantVariant.VariantID,
				Seller: &researchapp.CatalogSeller{
					ID: "gid://shopify/Shop/1", Domain: "merchant.example",
				},
			},
		}},
	}}
	transportCalls := 0
	client := mustStorefrontClientV2(t, roundTripFuncV2(func(*http.Request) (*http.Response, error) {
		transportCalls++
		return nil, nil
	}), nil)
	resolver, err := NewLiveVariantSourceResolverV2(
		lookup, client, fixedStorefrontClockV2{now: time.Now().UTC()}, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.ResolveVariantChoiceSource(
		context.Background(), "user-1", source, &storefrontTestCallAdmissionV2{},
	)
	assertStorefrontFaultV2(
		t, err, fault.ProviderUnavailable,
		researchapp.VariantResolutionUnavailableV2, false,
	)
	if transportCalls != 0 {
		t.Fatalf("unverified seller domain reached transport: calls=%d", transportCalls)
	}
}

func TestLiveVariantChoiceAccountsLookupIdentityAndPageAttempts(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	source := storefrontProductSourceFixtureV2(t)
	lookup := &offerLookupGatewayStubV2{result: researchapp.CatalogOfferLookupResult{
		Provider: "SHOPIFY_UCP", ProtocolVersion: "2026-04-08",
		Outcome: researchapp.CatalogOutcomeSuccess,
		Matches: []researchapp.CatalogOfferMatch{{
			DraftID:             source.CandidateID,
			RequestedIdentifier: source.Locator.ProductURL.CanonicalURL,
			Product: researchapp.CatalogProductObservation{
				Handle: "trail-shoe", Locator: &researchapp.CatalogProductLocator{
					Kind: researchapp.CatalogLocatorProductURL,
					ProductURL: &researchapp.CatalogProductURLLocator{
						CanonicalURL: source.Locator.ProductURL.CanonicalURL,
					},
				},
			},
			Variant: researchapp.CatalogPreviewVariant{Seller: &researchapp.CatalogSeller{
				ID: "gid://shopify/Shop/1", Domain: "merchant.myshopify.com",
			}},
		}},
	}}
	storefrontCalls := 0
	client := mustStorefrontClientV2(t, roundTripFuncV2(func(
		request *http.Request,
	) (*http.Response, error) {
		storefrontCalls++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.Contains(string(body), "CandidateIdentity"):
			if !strings.Contains(string(body), "variants(first: 20)") {
				t.Fatal("identity request omitted first variant page")
			}
			return storefrontResponseV2(http.StatusOK, storefrontSuccessBodyV2()), nil
		case strings.Contains(string(body), "CandidateVariants"):
			return storefrontResponseV2(http.StatusOK, strings.ReplaceAll(storefrontSuccessBodyV2(), "cursor-1", "cursor-2")), nil
		default:
			t.Fatalf("unexpected Storefront query: %s", body)
			return nil, nil
		}
	}), nil)
	resolver, err := NewLiveVariantSourceResolverV2(
		lookup, client, fixedStorefrontClockV2{now: now}, 5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := NewMemoryVariantPageCursorAuthorityV2(
		fixedStorefrontClockV2{now: now},
	)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := researchapp.NewVariantChoiceServiceV2(
		resolver, client, authority, fixedStorefrontClockV2{now: now}, 5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	admission := &storefrontTestCallAdmissionV2{}
	page, err := choice.LoadPage(context.Background(), researchapp.LoadVariantChoicePageV2Input{
		OwnerID: source.CandidateID, Source: source,
		ProviderCallAdmission: admission,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lookup.calls != 1 || storefrontCalls != 1 || admission.calls != 2 ||
		admission.active != 0 || len(page.Rows) != 1 {
		t.Fatalf(
			"lookup=%d storefront=%d admitted=%d active=%d rows=%d",
			lookup.calls, storefrontCalls, admission.calls, admission.active,
			len(page.Rows),
		)
	}

	if page.NextCursor == "" {
		t.Fatal("missing continuation")
	}
	_, err = choice.LoadPage(context.Background(), researchapp.LoadVariantChoicePageV2Input{
		OwnerID: source.CandidateID, Source: source, CursorToken: page.NextCursor, ProviderCallAdmission: admission,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lookup.calls != 1 || storefrontCalls != 2 || admission.calls != 3 {
		t.Fatalf("continuation re-resolved source: lookup=%d storefront=%d admitted=%d", lookup.calls, storefrontCalls, admission.calls)
	}
}

func TestMemoryVariantCursorAuthorityKeepsProviderCursorOpaqueAndOwnerBound(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	clock := &mutableStorefrontClockV2{now: now}
	authority, err := NewMemoryVariantPageCursorAuthorityV2(clock)
	if err != nil {
		t.Fatal(err)
	}
	continuation := researchapp.VariantChoiceContinuationV2{
		OwnerID: "user-1", ProviderCursor: "raw-provider-cursor-secret",
		SeenVariantIDs: []string{"gid://shopify/ProductVariant/100"},
		ExpiresAt:      now.Add(time.Minute),
	}
	token, err := authority.IssueContinuation(context.Background(), continuation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "vpc_") || strings.Contains(token, continuation.ProviderCursor) {
		t.Fatalf("cursor token is not opaque: %q", token)
	}
	verified, err := authority.VerifyContinuation(context.Background(), "user-1", token)
	if err != nil || verified.ProviderCursor != continuation.ProviderCursor {
		t.Fatalf("verify=%+v err=%v", verified, err)
	}
	verified.SeenVariantIDs[0] = "tampered"
	again, err := authority.VerifyContinuation(context.Background(), "user-1", token)
	if err != nil || again.SeenVariantIDs[0] != "gid://shopify/ProductVariant/100" {
		t.Fatalf("authority returned mutable state: %+v err=%v", again, err)
	}
	if _, err := authority.VerifyContinuation(context.Background(), "user-2", token); err == nil {
		t.Fatal("cross-owner cursor verification succeeded")
	}
	clock.now = now.Add(2 * time.Minute)
	if _, err := authority.VerifyContinuation(context.Background(), "user-1", token); err == nil {
		t.Fatal("expired cursor verification succeeded")
	}
}

func storefrontIdentityBodyV2(withVariant bool) string {
	node := ""
	if withVariant {
		node = `,"node":{"id":"gid://shopify/ProductVariant/100","product":{"id":"gid://shopify/Product/10","handle":"trail-shoe"}}`
	}
	return `{"data":{"shop":{"id":"gid://shopify/Shop/1","primaryDomain":{"host":"merchant.example","url":"https://merchant.example"}},"product":{"id":"gid://shopify/Product/10","handle":"trail-shoe","title":"Trail Shoe","onlineStoreUrl":"https://merchant.example/products/trail-shoe"}` + node + `}}`
}

func storefrontProductSourceFixtureV2(t *testing.T) researchapp.VariantChoiceCandidateSourceV2 {
	t.Helper()
	market, err := shareddomain.NewMarketContext("US", "USD")
	if err != nil {
		t.Fatal(err)
	}
	locator := researchapp.CatalogProductLocator{
		Kind: researchapp.CatalogLocatorProductURL,
		ProductURL: &researchapp.CatalogProductURLLocator{
			CanonicalURL: "https://merchant.example/products/trail-shoe",
		},
	}
	hash, err := researchapp.VariantChoiceSourceLocatorHashV2(locator)
	if err != nil {
		t.Fatal(err)
	}
	return researchapp.VariantChoiceCandidateSourceV2{
		CandidateID: "candidate-1", SourceDiscoveryID: "discovery-1",
		SourceLocatorHash: hash, Locator: locator, MarketContext: market,
	}
}

func storefrontMerchantVariantSourceFixtureV2(t *testing.T) researchapp.VariantChoiceCandidateSourceV2 {
	t.Helper()
	market, err := shareddomain.NewMarketContext("US", "USD")
	if err != nil {
		t.Fatal(err)
	}
	locator := researchapp.CatalogProductLocator{
		Kind: researchapp.CatalogLocatorMerchantVariant,
		MerchantVariant: &researchapp.CatalogMerchantVariantLocator{
			VariantID:    "gid://shopify/ProductVariant/100",
			SellerDomain: "merchant.example", SellerID: "gid://shopify/Shop/1",
		},
	}
	hash, err := researchapp.VariantChoiceSourceLocatorHashV2(locator)
	if err != nil {
		t.Fatal(err)
	}
	return researchapp.VariantChoiceCandidateSourceV2{
		CandidateID: "candidate-merchant-1", SourceDiscoveryID: "discovery-merchant-1",
		SourceLocatorHash: hash, Locator: locator, MarketContext: market,
	}
}

type offerLookupGatewayStubV2 struct {
	result researchapp.CatalogOfferLookupResult
	err    error
	calls  int
}

func (gateway *offerLookupGatewayStubV2) LookupOffers(
	ctx context.Context,
	request researchapp.CatalogOfferLookupRequest,
) (researchapp.CatalogOfferLookupResult, error) {
	if request.ProviderCallAdmission == nil {
		return researchapp.CatalogOfferLookupResult{}, fault.New(
			fault.InvalidInput, "TEST_OFFER_ADMISSION_MISSING", false,
		)
	}
	release, err := request.ProviderCallAdmission.AcquireCatalogProviderCall(ctx)
	if err != nil {
		return researchapp.CatalogOfferLookupResult{}, err
	}
	gateway.calls++
	result := gateway.result
	result.ProviderCallCount = 1
	release()
	return result, gateway.err
}

type storefrontTestCallAdmissionV2 struct {
	calls  int
	active int
}

func (admission *storefrontTestCallAdmissionV2) AcquireCatalogProviderCall(
	context.Context,
) (func(), error) {
	if admission.active != 0 {
		return nil, fault.New(fault.RateLimited, "TEST_STOREFRONT_BUSY", true)
	}
	admission.calls++
	admission.active++
	return func() { admission.active-- }, nil
}

type fixedStorefrontClockV2 struct{ now time.Time }

func (clock fixedStorefrontClockV2) Now() time.Time { return clock.now }

type mutableStorefrontClockV2 struct{ now time.Time }

func (clock *mutableStorefrontClockV2) Now() time.Time { return clock.now }
