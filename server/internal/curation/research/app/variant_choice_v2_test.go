package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestVariantChoiceServiceV2IssuesExactSingletonChoice(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	source := variantChoiceSourceFixtureV2(t)
	resolution := variantChoiceResolutionFixtureV2(t, source, now)
	gateway := &variantChoiceGatewayStubV2{
		page: variantChoiceProviderPageFixtureV2(t),
	}
	authority := &variantChoiceAuthorityStubV2{}
	service := mustVariantChoiceServiceV2(t, gateway, authority, now)

	page, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{
		OwnerID: "user-1", Source: source,
	})
	if err != nil {
		t.Fatalf("load first page: %v", err)
	}
	if len(gateway.requests) != 1 || gateway.requests[0].First != VariantChoicePageSizeV2 ||
		gateway.requests[0].AfterCursor != "" ||
		gateway.requests[0].MyshopifyDomain != "merchant.myshopify.com" ||
		gateway.requests[0].MappingEvidenceID != "mapping-evidence-1" ||
		gateway.requests[0].ProductHandle != "trail-shoe" {
		t.Fatalf("unexpected product(handle) request: %+v", gateway.requests)
	}
	if len(page.Rows) != 1 ||
		page.Rows[0].VariantRef.MerchantID != "gid://shopify/Shop/1" ||
		page.Rows[0].VariantRef.ProductID != "gid://shopify/Product/10" ||
		page.Rows[0].VariantRef.VariantID != "gid://shopify/ProductVariant/100" ||
		page.NextCursor != "" || !page.CanSkipRemoteLoading ||
		page.SnapshotHash == "" || !page.ExpiresAt.Equal(resolution.ExpiresAt) {
		t.Fatalf("singleton result did not bind exact lineage: %+v", page)
	}
	if len(authority.issuedContinuations) != 0 {
		t.Fatalf("singleton must not issue a page cursor: %+v", authority.issuedContinuations)
	}
}

func TestVariantChoiceServiceV2ValidatesFreshResolutionAfterProviderRead(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	resolvedAt := startedAt.Add(2 * time.Second)
	source := variantChoiceMerchantVariantSourceFixtureV2(t)
	resolution := variantChoiceResolutionFixtureV2(t, source, resolvedAt)
	resolution.ResolvedAt = resolvedAt
	resolution.ExpiresAt = resolvedAt.Add(time.Minute)
	clock := &variantChoiceSequenceClockV2{values: []time.Time{startedAt, resolvedAt}}
	service, err := NewVariantChoiceServiceV2(
		&variantChoiceSourceResolverStubV2{resolution: resolution},
		&variantChoiceGatewayStubV2{page: variantChoiceProviderPageFixtureV2(t)},
		&variantChoiceAuthorityStubV2{}, clock, 5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}

	page, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{
		OwnerID: "user-1", Source: source,
	})
	if err != nil || len(page.Rows) != 1 || !page.ObservedAt.Equal(resolvedAt) {
		t.Fatalf("fresh post-read evidence was rejected: page=%+v err=%v", page, err)
	}
}

func TestVariantChoiceServiceV2UsesOpaqueContinuationAndDeduplicates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	source := variantChoiceSourceFixtureV2(t)
	resolution := variantChoiceResolutionFixtureV2(t, source, now)
	page := variantChoiceProviderPageFixtureV2(t)
	page.ProductOptions = []StorefrontProductOptionV2{
		{Name: "Color", Values: []string{"Black", "Red"}},
	}
	page.Rows = []StorefrontVariantRowV2{
		variantChoiceRowFixtureV2(t, "gid://shopify/ProductVariant/100", "Black"),
		variantChoiceRowFixtureV2(t, "gid://shopify/ProductVariant/101", "Red"),
	}
	page.HasNextPage = true
	page.EndCursor = "provider-cursor-2"
	gateway := &variantChoiceGatewayStubV2{page: page}
	authority := &variantChoiceAuthorityStubV2{continuation: VariantChoiceContinuationV2{
		OwnerID: "user-1", CandidateID: source.CandidateID,
		SourceDiscoveryID:          source.SourceDiscoveryID,
		SourceLocatorHash:          source.SourceLocatorHash,
		ShopID:                     resolution.StorefrontShop.ShopID,
		MyshopifyDomain:            resolution.StorefrontShop.MyshopifyDomain,
		PrimaryDomain:              resolution.StorefrontShop.PrimaryDomain,
		MappingEvidenceID:          resolution.StorefrontShop.MappingEvidenceID,
		SourceResolutionEvidenceID: resolution.SourceResolutionEvidenceID,
		ProductID:                  resolution.ProductID,
		ProductHandle:              resolution.ProductHandle,
		StorefrontVariantID:        resolution.StorefrontVariantID,
		ContextCountry:             "US",
		ProviderCursor:             "provider-cursor-1",
		SeenVariantIDs:             []string{"gid://shopify/ProductVariant/100"},
		PreviousPageHash:           "0xprevious", ExpiresAt: now.Add(time.Minute),
	}}
	service := mustVariantChoiceServiceV2(t, gateway, authority, now)

	result, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{
		OwnerID: "user-1", Source: source, CursorToken: "opaque-page-token-1",
	})
	if err != nil {
		t.Fatalf("load continuation: %v", err)
	}
	if authority.verifyToken != "opaque-page-token-1" || len(gateway.requests) != 1 ||
		gateway.requests[0].AfterCursor != "provider-cursor-1" || len(result.Rows) != 1 ||
		result.Rows[0].VariantRef.VariantID != "gid://shopify/ProductVariant/101" ||
		result.NextCursor != "opaque-next-token" ||
		strings.Contains(result.NextCursor, "provider-cursor") || result.CanSkipRemoteLoading {
		t.Fatalf("continuation was not opaque/deduplicated: result=%+v request=%+v", result, gateway.requests)
	}
	next := authority.issuedContinuations[0]
	if next.ProviderCursor != "provider-cursor-2" ||
		len(next.SeenVariantIDs) != 2 || next.SeenVariantIDs[1] !=
		"gid://shopify/ProductVariant/101" {
		t.Fatalf("next continuation lost cursor/seen lineage: %+v", next)
	}
}

func TestVariantChoiceServiceV2FailsClosedOnLineageMismatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	source := variantChoiceSourceFixtureV2(t)
	providerPage := variantChoiceProviderPageFixtureV2(t)
	providerPage.ShopID = "gid://shopify/Shop/999"
	gateway := &variantChoiceGatewayStubV2{page: providerPage}
	authority := &variantChoiceAuthorityStubV2{}
	service := mustVariantChoiceServiceV2(t, gateway, authority, now)

	_, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{
		OwnerID: "user-1", Source: source,
	})
	assertVariantChoiceFaultV2(
		t, err, fault.ProviderRejected, VariantPageLineageMismatchV2,
	)
	if len(authority.issuedContinuations) != 0 {
		t.Fatalf("mismatched Shop must not issue cursor: %+v", authority.issuedContinuations)
	}
}

func TestVariantChoiceServiceV2RejectsStaleContinuationBeforeProviderCall(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	source := variantChoiceSourceFixtureV2(t)
	resolution := variantChoiceResolutionFixtureV2(t, source, now)
	gateway := &variantChoiceGatewayStubV2{page: variantChoiceProviderPageFixtureV2(t)}
	authority := &variantChoiceAuthorityStubV2{continuation: VariantChoiceContinuationV2{
		OwnerID: "user-1", CandidateID: source.CandidateID,
		SourceDiscoveryID:          source.SourceDiscoveryID,
		SourceLocatorHash:          source.SourceLocatorHash,
		ShopID:                     resolution.StorefrontShop.ShopID,
		MyshopifyDomain:            resolution.StorefrontShop.MyshopifyDomain,
		PrimaryDomain:              resolution.StorefrontShop.PrimaryDomain,
		MappingEvidenceID:          "superseded-mapping-evidence",
		SourceResolutionEvidenceID: resolution.SourceResolutionEvidenceID,
		ProductID:                  resolution.ProductID,
		ProductHandle:              resolution.ProductHandle,
		StorefrontVariantID:        resolution.StorefrontVariantID,
		ContextCountry:             "US",
		ProviderCursor:             "provider-cursor-1",
		SeenVariantIDs:             []string{"gid://shopify/ProductVariant/100"},
		PreviousPageHash:           "0xprevious", ExpiresAt: now.Add(time.Minute),
	}}
	service := mustVariantChoiceServiceV2(t, gateway, authority, now)
	_, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{
		OwnerID: "user-1", Source: source, CursorToken: "opaque-page-token-1",
	})
	assertVariantChoiceFaultV2(t, err, fault.Conflict, VariantChoiceFailureStalePageV2)
	if len(gateway.requests) != 0 {
		t.Fatalf("stale cursor reached provider: %+v", gateway.requests)
	}
}

func TestVariantChoiceServiceV2NeverExposesRawProviderCursor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	providerPage := variantChoiceProviderPageFixtureV2(t)
	providerPage.HasNextPage = true
	providerPage.EndCursor = "provider-cursor-raw"
	gateway := &variantChoiceGatewayStubV2{page: providerPage}
	authority := &variantChoiceAuthorityStubV2{
		nextCursorToken: "provider-cursor-raw",
	}
	service := mustVariantChoiceServiceV2(t, gateway, authority, now)
	_, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{
		OwnerID: "user-1", Source: variantChoiceSourceFixtureV2(t),
	})
	assertVariantChoiceFaultV2(
		t, err, fault.InternalFailure, VariantChoiceFailureArtifactV2,
	)
}

func TestVariantChoiceServiceV2OpensMerchantVariantOnlyCandidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	source := variantChoiceMerchantVariantSourceFixtureV2(t)
	resolution := variantChoiceResolutionFixtureV2(t, source, now)
	resolver := &variantChoiceSourceResolverStubV2{resolution: resolution}
	gateway := &variantChoiceGatewayStubV2{
		page: variantChoiceProviderPageFixtureV2(t),
	}
	authority := &variantChoiceAuthorityStubV2{}
	service := mustVariantChoiceServiceWithResolverV2(
		t, resolver, gateway, authority, now,
	)

	page, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{
		OwnerID: "user-1", Source: source,
	})
	if err != nil {
		t.Fatalf("open merchant-variant candidate: %v", err)
	}
	if source.Locator.ProductURL != nil || len(resolver.calls) != 1 ||
		resolver.calls[0].source.SourceLocatorHash != source.SourceLocatorHash ||
		len(gateway.requests) != 1 ||
		gateway.requests[0].MyshopifyDomain != "merchant.myshopify.com" ||
		gateway.requests[0].MyshopifyDomain ==
			source.Locator.MerchantVariant.SellerDomain ||
		gateway.requests[0].ExpectedProductID != resolution.ProductID ||
		gateway.requests[0].ProductHandle != resolution.ProductHandle {
		t.Fatalf(
			"merchant seed bypassed verified resolution: calls=%+v requests=%+v",
			resolver.calls, gateway.requests,
		)
	}
	if len(page.Rows) != 1 ||
		page.Rows[0].VariantRef.ProductID != resolution.ProductID ||
		page.Rows[0].VariantRef.VariantID != resolution.StorefrontVariantID ||
		len(authority.issuedContinuations) != 0 {
		t.Fatalf("merchant-only candidate did not reach choice page: %+v", page)
	}
}

func TestVariantChoiceServiceV2RejectsSellerDomainAsUnverifiedAPIOrigin(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	source := variantChoiceMerchantVariantSourceFixtureV2(t)
	resolution := variantChoiceResolutionFixtureV2(t, source, now)
	resolution.StorefrontShop.MyshopifyDomain =
		source.Locator.MerchantVariant.SellerDomain
	resolver := &variantChoiceSourceResolverStubV2{resolution: resolution}
	gateway := &variantChoiceGatewayStubV2{
		page: variantChoiceProviderPageFixtureV2(t),
	}
	service := mustVariantChoiceServiceWithResolverV2(
		t, resolver, gateway, &variantChoiceAuthorityStubV2{}, now,
	)

	_, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{
		OwnerID: "user-1", Source: source,
	})
	assertVariantChoiceFaultV2(
		t, err, fault.ProviderRejected, VariantResolvedSourceLineageMismatchV2,
	)
	if len(gateway.requests) != 0 {
		t.Fatalf("unverified seller domain reached Storefront: %+v", gateway.requests)
	}
}

func TestVariantChoiceServiceV2RejectsExpiredSourceResolutionBeforeStorefront(
	t *testing.T,
) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	source := variantChoiceMerchantVariantSourceFixtureV2(t)
	resolution := variantChoiceResolutionFixtureV2(t, source, now)
	resolution.ExpiresAt = now
	resolver := &variantChoiceSourceResolverStubV2{resolution: resolution}
	gateway := &variantChoiceGatewayStubV2{
		page: variantChoiceProviderPageFixtureV2(t),
	}
	service := mustVariantChoiceServiceWithResolverV2(
		t, resolver, gateway, &variantChoiceAuthorityStubV2{}, now,
	)

	_, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{
		OwnerID: "user-1", Source: source,
	})
	assertVariantChoiceFaultV2(
		t, err, fault.ProviderRejected, VariantResolvedSourceLineageMismatchV2,
	)
	if len(gateway.requests) != 0 {
		t.Fatalf("expired resolution reached Storefront: %+v", gateway.requests)
	}
}

func TestVariantChoiceServiceV2DoesNotExposeRawResolverFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	source := variantChoiceMerchantVariantSourceFixtureV2(t)
	resolver := &variantChoiceSourceResolverStubV2{
		err: fault.Wrap(
			errors.New("seller alice@example.com lookup token secret"),
			fault.ProviderRejected, "alice@example.com token secret", false,
		),
	}
	gateway := &variantChoiceGatewayStubV2{
		page: variantChoiceProviderPageFixtureV2(t),
	}
	service := mustVariantChoiceServiceWithResolverV2(
		t, resolver, gateway, &variantChoiceAuthorityStubV2{}, now,
	)

	_, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{
		OwnerID: "user-1", Source: source,
	})
	assertVariantChoiceFaultV2(
		t, err, fault.ProviderRejected, VariantChoiceFailureSourceResolutionV2,
	)
	if strings.Contains(err.Error(), "alice@example.com") ||
		strings.Contains(err.Error(), "token secret") {
		t.Fatalf("raw resolver failure leaked: %v", err)
	}
	if len(gateway.requests) != 0 {
		t.Fatalf("failed resolution reached Storefront: %+v", gateway.requests)
	}
}

func variantChoiceSourceFixtureV2(t *testing.T) VariantChoiceCandidateSourceV2 {
	t.Helper()
	market, err := shareddomain.NewMarketContext("US", "USD")
	if err != nil {
		t.Fatalf("market: %v", err)
	}
	locator := CatalogProductLocator{
		Kind: CatalogLocatorProductURL,
		ProductURL: &CatalogProductURLLocator{
			CanonicalURL: "https://merchant.example/products/trail-shoe",
		},
	}
	hash, err := VariantChoiceSourceLocatorHashV2(locator)
	if err != nil {
		t.Fatalf("locator hash: %v", err)
	}
	return VariantChoiceCandidateSourceV2{
		CandidateID: "candidate-1", SourceDiscoveryID: "discovery-1",
		SourceLocatorHash: hash, Locator: locator,
		MarketContext: market,
	}
}

func variantChoiceMerchantVariantSourceFixtureV2(
	t *testing.T,
) VariantChoiceCandidateSourceV2 {
	t.Helper()
	market, err := shareddomain.NewMarketContext("US", "USD")
	if err != nil {
		t.Fatalf("market: %v", err)
	}
	locator := CatalogProductLocator{
		Kind: CatalogLocatorMerchantVariant,
		MerchantVariant: &CatalogMerchantVariantLocator{
			VariantID:    "ucp-variant-opaque-100",
			SellerDomain: "seller-market.example",
			SellerID:     "seller-7",
		},
	}
	hash, err := VariantChoiceSourceLocatorHashV2(locator)
	if err != nil {
		t.Fatalf("merchant locator hash: %v", err)
	}
	return VariantChoiceCandidateSourceV2{
		CandidateID:       "candidate-merchant-variant-1",
		SourceDiscoveryID: "discovery-merchant-variant-1",
		SourceLocatorHash: hash,
		Locator:           locator,
		MarketContext:     market,
	}
}

func variantChoiceResolutionFixtureV2(
	t *testing.T,
	source VariantChoiceCandidateSourceV2,
	now time.Time,
) VerifiedVariantChoiceSourceV2 {
	t.Helper()
	resolution := VerifiedVariantChoiceSourceV2{
		SourceLocatorHash: source.SourceLocatorHash,
		SeedKind:          source.Locator.Kind,
		StorefrontShop: VerifiedStorefrontShopV2{
			ShopID:            "gid://shopify/Shop/1",
			MyshopifyDomain:   "merchant.myshopify.com",
			PrimaryDomain:     "merchant.example",
			MappingEvidenceID: "mapping-evidence-1",
		},
		ProductID:                  "gid://shopify/Product/10",
		ProductHandle:              "trail-shoe",
		SourceResolutionEvidenceID: "source-resolution-evidence-1",
		ResolvedAt:                 now.Add(-time.Minute),
		ExpiresAt:                  now.Add(time.Minute),
	}
	switch source.Locator.Kind {
	case CatalogLocatorProductURL:
		resolution.SeedAuthority = "merchant.example"
		resolution.SeedIdentifier = source.Locator.ProductURL.CanonicalURL
	case CatalogLocatorMerchantVariant:
		resolution.SeedAuthority = source.Locator.MerchantVariant.SellerDomain
		resolution.SeedIdentifier = source.Locator.MerchantVariant.VariantID
		resolution.StorefrontVariantID = "gid://shopify/ProductVariant/100"
	default:
		t.Fatalf("unsupported fixture locator kind: %s", source.Locator.Kind)
	}
	return resolution
}

func variantChoiceProviderPageFixtureV2(t *testing.T) StorefrontVariantPageV2 {
	t.Helper()
	return StorefrontVariantPageV2{
		ShopID: "gid://shopify/Shop/1", PrimaryDomain: "merchant.example",
		ProductID: "gid://shopify/Product/10", ProductHandle: "trail-shoe",
		ProductURL: "https://merchant.example/products/trail-shoe",
		ProductOptions: []StorefrontProductOptionV2{
			{Name: "Title", Values: []string{"Default Title"}},
		},
		Rows: []StorefrontVariantRowV2{
			variantChoiceRowFixtureV2(
				t, "gid://shopify/ProductVariant/100", "Default Title",
			),
		},
	}
}

func variantChoiceRowFixtureV2(
	t *testing.T,
	variantID string,
	optionValue string,
) StorefrontVariantRowV2 {
	t.Helper()
	price, err := shareddomain.NewMoney("100.00", "USD")
	if err != nil {
		t.Fatalf("price: %v", err)
	}
	optionName := "Color"
	if optionValue == "Default Title" {
		optionName = "Title"
	}
	return StorefrontVariantRowV2{
		VariantID: variantID, Title: optionValue,
		SelectedOptions: []researchdomain.VariantOptionSelectionV2{
			{Name: optionName, Value: optionValue},
		},
		Price: price, AvailableForSale: true,
	}
}

type variantChoiceGatewayStubV2 struct {
	page     StorefrontVariantPageV2
	err      error
	requests []StorefrontVariantQueryV2
}

func (stub *variantChoiceGatewayStubV2) LoadVariantPage(
	_ context.Context,
	request StorefrontVariantQueryV2,
) (StorefrontVariantPageV2, error) {
	stub.requests = append(stub.requests, request)
	return stub.page, stub.err
}

type variantChoiceSourceResolverCallV2 struct {
	ownerID string
	source  VariantChoiceCandidateSourceV2
}

type variantChoiceSourceResolverStubV2 struct {
	resolution VerifiedVariantChoiceSourceV2
	err        error
	calls      []variantChoiceSourceResolverCallV2
}

func (stub *variantChoiceSourceResolverStubV2) ResolveVariantChoiceSource(
	_ context.Context,
	ownerID string,
	source VariantChoiceCandidateSourceV2,
	_ CatalogProviderCallAdmissionV2,
) (VerifiedVariantChoiceSourceV2, error) {
	stub.calls = append(stub.calls, variantChoiceSourceResolverCallV2{
		ownerID: ownerID,
		source:  source,
	})
	return stub.resolution, stub.err
}

type variantChoiceAuthorityStubV2 struct {
	continuation        VariantChoiceContinuationV2
	verifyErr           error
	issueErr            error
	verifyToken         string
	issuedContinuations []VariantChoiceContinuationV2
	nextCursorToken     string
}

func (stub *variantChoiceAuthorityStubV2) VerifyContinuation(
	_ context.Context,
	_ string,
	token string,
) (VariantChoiceContinuationV2, error) {
	stub.verifyToken = token
	return stub.continuation, stub.verifyErr
}

func (stub *variantChoiceAuthorityStubV2) IssueContinuation(
	_ context.Context,
	continuation VariantChoiceContinuationV2,
) (string, error) {
	stub.issuedContinuations = append(stub.issuedContinuations, continuation)
	if stub.issueErr != nil {
		return "", stub.issueErr
	}
	if stub.nextCursorToken == "" {
		return "opaque-next-token", nil
	}
	return stub.nextCursorToken, nil
}

type variantChoiceClockV2 struct{ now time.Time }

func (clock variantChoiceClockV2) Now() time.Time { return clock.now }

type variantChoiceSequenceClockV2 struct {
	values []time.Time
	index  int
}

func (clock *variantChoiceSequenceClockV2) Now() time.Time {
	if len(clock.values) == 0 {
		return time.Time{}
	}
	index := min(clock.index, len(clock.values)-1)
	value := clock.values[index]
	clock.index++
	return value
}

func mustVariantChoiceServiceV2(
	t *testing.T,
	gateway StorefrontVariantGatewayV2,
	authority VariantPageCursorAuthorityV2,
	now time.Time,
) *VariantChoiceServiceV2 {
	t.Helper()
	source := variantChoiceSourceFixtureV2(t)
	resolver := &variantChoiceSourceResolverStubV2{
		resolution: variantChoiceResolutionFixtureV2(t, source, now),
	}
	return mustVariantChoiceServiceWithResolverV2(
		t, resolver, gateway, authority, now,
	)
}

func mustVariantChoiceServiceWithResolverV2(
	t *testing.T,
	resolver VariantChoiceSourceResolverV2,
	gateway StorefrontVariantGatewayV2,
	authority VariantPageCursorAuthorityV2,
	now time.Time,
) *VariantChoiceServiceV2 {
	t.Helper()
	service, err := NewVariantChoiceServiceV2(
		resolver, gateway, authority, variantChoiceClockV2{now: now}, 5*time.Minute,
	)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return service
}

func assertVariantChoiceFaultV2(
	t *testing.T,
	err error,
	wantCode fault.Code,
	wantReason string,
) {
	t.Helper()
	if err == nil {
		t.Fatal("expected typed failure")
	}
	classified, ok := fault.As(err)
	if !ok || classified.Code != wantCode || classified.Reason != wantReason {
		t.Fatalf("fault=%v want code=%s reason=%s", err, wantCode, wantReason)
	}
}

func TestVariantContinuationReusesResolutionWithoutExtendingExpiry(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	source := variantChoiceSourceFixtureV2(t)
	resolution := variantChoiceResolutionFixtureV2(t, source, now)
	resolver := &variantChoiceSourceResolverStubV2{resolution: resolution}
	gateway := &variantChoiceGatewayStubV2{page: variantChoiceProviderPageFixtureV2(t)}
	gateway.page.HasNextPage = true
	gateway.page.EndCursor = "cursor-1"
	authority := &variantChoiceAuthorityStubV2{}
	clock := &liveReviewClockV2{now: now}
	service, err := NewVariantChoiceServiceV2(resolver, gateway, authority, clock, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{OwnerID: "user-1", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	original := authority.issuedContinuations[0]
	for name, mutate := range map[string]func(*VariantChoiceContinuationV2){
		"owner":     func(c *VariantChoiceContinuationV2) { c.OwnerID = "other" },
		"candidate": func(c *VariantChoiceContinuationV2) { c.CandidateID = "other" },
		"locator":   func(c *VariantChoiceContinuationV2) { c.SourceLocatorHash = "other" },
		"currency":  func(c *VariantChoiceContinuationV2) { c.ContextCurrency = "KRW" },
		"expired":   func(c *VariantChoiceContinuationV2) { c.ExpiresAt = now },
	} {
		t.Run(name, func(t *testing.T) {
			authority.continuation = original
			mutate(&authority.continuation)
			_, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{OwnerID: "user-1", Source: source, CursorToken: first.NextCursor})
			if err == nil || len(resolver.calls) != 1 || len(gateway.requests) != 1 {
				t.Fatal("invalid continuation made provider call")
			}
		})
	}
	authority.continuation = original
	clock.now = now.Add(30 * time.Second)
	gateway.page.EndCursor = "cursor-2"
	next, err := service.LoadPage(context.Background(), LoadVariantChoicePageV2Input{OwnerID: "user-1", Source: source, CursorToken: first.NextCursor})
	if err != nil || len(resolver.calls) != 1 || len(gateway.requests) != 2 || !next.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("continuation re-resolved or renewed expiry: %v %+v", err, next)
	}
}
