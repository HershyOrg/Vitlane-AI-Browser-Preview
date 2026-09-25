package shopifystorefront

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// LiveVariantSourceResolverV2 turns a Candidate's durable locator into a
// verified Storefront mapping. Every call performs a fresh Global Catalog
// lookup and a Storefront identity query. seller.domain from the Candidate is
// never directly used as an API origin; only a fresh lookup-confirmed strict
// *.myshopify.com mapping is probed and then correlated with Shop, primary
// domain, Product and (when present) ProductVariant identity.
type LiveVariantSourceResolverV2 struct {
	lookup   researchapp.CatalogOfferLookupGatewayV2
	identity *ClientV2
	clock    sharedapp.Clock
	ttl      time.Duration
}

var _ researchapp.VariantChoiceSourceResolverV2 = (*LiveVariantSourceResolverV2)(nil)

func NewLiveVariantSourceResolverV2(
	lookup researchapp.CatalogOfferLookupGatewayV2,
	identity *ClientV2,
	clock sharedapp.Clock,
	ttl time.Duration,
) (*LiveVariantSourceResolverV2, error) {
	if lookup == nil || identity == nil || clock == nil || ttl <= 0 || ttl > 30*time.Minute {
		return nil, fault.New(
			fault.InvalidInput, researchapp.VariantChoiceFailureInvalidV2, false,
		)
	}
	return &LiveVariantSourceResolverV2{
		lookup: lookup, identity: identity, clock: clock, ttl: ttl,
	}, nil
}

func (resolver *LiveVariantSourceResolverV2) ResolveVariantChoiceSource(
	ctx context.Context,
	ownerID string,
	source researchapp.VariantChoiceCandidateSourceV2,
	admission researchapp.CatalogProviderCallAdmissionV2,
) (researchapp.VerifiedVariantChoiceSourceV2, error) {
	return resolver.resolveSource(ctx, ownerID, source, admission, false)
}
func (resolver *LiveVariantSourceResolverV2) ResolveVariantChoiceFirstPage(ctx context.Context, ownerID string, source researchapp.VariantChoiceCandidateSourceV2, admission researchapp.CatalogProviderCallAdmissionV2) (researchapp.VerifiedVariantChoiceSourceV2, error) {
	return resolver.resolveSource(ctx, ownerID, source, admission, true)
}
func (resolver *LiveVariantSourceResolverV2) resolveSource(ctx context.Context, ownerID string, source researchapp.VariantChoiceCandidateSourceV2, admission researchapp.CatalogProviderCallAdmissionV2, includeVariants bool) (researchapp.VerifiedVariantChoiceSourceV2, error) {
	ownerID = strings.TrimSpace(ownerID)
	if resolver == nil || resolver.lookup == nil || resolver.identity == nil ||
		resolver.clock == nil || ownerID == "" || source.Validate() != nil || admission == nil {
		return researchapp.VerifiedVariantChoiceSourceV2{}, fault.New(
			fault.InvalidInput, researchapp.VariantChoiceFailureInvalidV2, false,
		)
	}
	identifier := variantLookupIdentifierV2(source.Locator)
	lookup, err := resolver.lookup.LookupOffers(ctx, researchapp.CatalogOfferLookupRequest{
		Inputs: []researchapp.CatalogOfferLookupInput{{
			DraftID: source.CandidateID, Identifier: identifier,
		}},
		Context: researchapp.CatalogBuyerContext{
			Country: string(source.MarketContext.Country), Language: "en",
			Currency: string(source.MarketContext.Currency),
			Intent:   "Resolve exact Shopify product variants",
		},
		ProviderCallAdmission: admission,
	})
	if err != nil {
		return researchapp.VerifiedVariantChoiceSourceV2{}, err
	}
	if lookup.Outcome != researchapp.CatalogOutcomeSuccess || len(lookup.Matches) != 1 ||
		len(lookup.UnresolvedIDs) != 0 || lookup.Matches[0].DraftID != source.CandidateID ||
		lookup.Matches[0].RequestedIdentifier != identifier {
		return researchapp.VerifiedVariantChoiceSourceV2{}, variantSourceCorrelationFaultV2()
	}
	match := lookup.Matches[0]
	handle, variantID, sellerID, allowedDomains, err :=
		correlateFreshVariantLookupV2(source, match)
	if err != nil {
		return researchapp.VerifiedVariantChoiceSourceV2{}, err
	}
	myshopifyDomain := chooseFreshMyshopifyDomainV2(source, match)
	if myshopifyDomain == "" {
		return researchapp.VerifiedVariantChoiceSourceV2{}, fault.New(
			fault.ProviderUnavailable, researchapp.VariantResolutionUnavailableV2, false,
		)
	}
	identity, err := resolver.identity.resolveIdentityV2(ctx, storefrontIdentityInputV2{
		IncludeVariants: includeVariants, Currency: string(source.MarketContext.Currency),
		MyshopifyDomain: myshopifyDomain, ProductHandle: handle,
		VariantID: variantID, Country: string(source.MarketContext.Country),
		ProviderCallAdmission: admission,
	})
	if err != nil {
		return researchapp.VerifiedVariantChoiceSourceV2{}, err
	}
	if _, ok := allowedDomains[identity.PrimaryDomain]; !ok {
		return researchapp.VerifiedVariantChoiceSourceV2{}, variantSourceCorrelationFaultV2()
	}
	if sellerID != "" && identity.ShopID != sellerID {
		return researchapp.VerifiedVariantChoiceSourceV2{}, variantSourceCorrelationFaultV2()
	}
	mappingEvidenceID, err := shareddomain.CanonicalJSONHash(struct {
		SchemaVersion   string
		OwnerID         string
		CandidateID     string
		LocatorHash     string
		LookupProvider  string
		LookupProtocol  string
		RequestedID     string
		MyshopifyDomain string
		ShopID          string
		PrimaryDomain   string
		ProductID       string
		ProductHandle   string
		VariantID       string
	}{
		SchemaVersion: "vitlane.storefront-mapping-evidence.v1",
		OwnerID:       ownerID, CandidateID: source.CandidateID,
		LocatorHash: source.SourceLocatorHash, LookupProvider: lookup.Provider,
		LookupProtocol: lookup.ProtocolVersion, RequestedID: identifier,
		MyshopifyDomain: myshopifyDomain, ShopID: identity.ShopID,
		PrimaryDomain: identity.PrimaryDomain, ProductID: identity.ProductID,
		ProductHandle: identity.ProductHandle, VariantID: identity.VerifiedVariant,
	})
	if err != nil {
		return researchapp.VerifiedVariantChoiceSourceV2{}, fault.Wrap(
			err, fault.InternalFailure, researchapp.VariantChoiceFailureArtifactV2, false,
		)
	}
	now := resolver.clock.Now().UTC()
	return researchapp.VerifiedVariantChoiceSourceV2{
		PrefetchedPage:    identity.FirstPage,
		SourceLocatorHash: source.SourceLocatorHash,
		SeedKind:          source.Locator.Kind,
		SeedAuthority:     variantSeedAuthorityV2(source.Locator),
		SeedIdentifier:    identifier,
		StorefrontShop: researchapp.VerifiedStorefrontShopV2{
			ShopID: identity.ShopID, MyshopifyDomain: myshopifyDomain,
			PrimaryDomain:     identity.PrimaryDomain,
			MappingEvidenceID: mappingEvidenceID,
		},
		ProductID: identity.ProductID, ProductHandle: identity.ProductHandle,
		StorefrontVariantID:        identity.VerifiedVariant,
		SourceResolutionEvidenceID: mappingEvidenceID,
		ResolvedAt:                 now, ExpiresAt: now.Add(resolver.ttl),
	}, nil
}

func correlateFreshVariantLookupV2(
	source researchapp.VariantChoiceCandidateSourceV2,
	match researchapp.CatalogOfferMatch,
) (string, string, string, map[string]struct{}, error) {
	allowedDomains := make(map[string]struct{})
	addCatalogProductDomainsV2(allowedDomains, match.Product)
	variantID := ""
	sellerID := ""
	handle := strings.TrimSpace(match.Product.Handle)
	switch source.Locator.Kind {
	case researchapp.CatalogLocatorProductURL:
		if source.Locator.ProductURL == nil || match.Product.Locator == nil ||
			match.Product.Locator.ProductURL == nil ||
			match.Product.Locator.ProductURL.CanonicalURL != source.Locator.ProductURL.CanonicalURL {
			return "", "", "", nil, variantSourceCorrelationFaultV2()
		}
		sourceHandle := productHandleFromURLV2(source.Locator.ProductURL.CanonicalURL)
		if sourceHandle == "" || (handle != "" && handle != sourceHandle) {
			return "", "", "", nil, variantSourceCorrelationFaultV2()
		}
		handle = sourceHandle
	case researchapp.CatalogLocatorMerchantVariant:
		locator := source.Locator.MerchantVariant
		if locator == nil || match.Variant.ID != locator.VariantID ||
			match.Variant.Seller == nil ||
			!strings.EqualFold(match.Variant.Seller.Domain, locator.SellerDomain) {
			return "", "", "", nil, variantSourceCorrelationFaultV2()
		}
		variantID = locator.VariantID
		sellerID = strings.TrimSpace(match.Variant.Seller.ID)
		if sellerID != "" && !validShopifyNumericGIDV2(sellerID, "Shop") {
			return "", "", "", nil, variantSourceCorrelationFaultV2()
		}
		addDomainV2(allowedDomains, match.Variant.Seller.Domain)
		addURLDomainV2(allowedDomains, match.Variant.Seller.URL)
		addURLDomainV2(allowedDomains, match.Variant.URL)
	default:
		return "", "", "", nil, variantSourceCorrelationFaultV2()
	}
	if handle == "" {
		if match.Product.Locator != nil && match.Product.Locator.ProductURL != nil {
			handle = productHandleFromURLV2(match.Product.Locator.ProductURL.CanonicalURL)
		}
	}
	if handle == "" {
		// Shopify Global lookup commonly omits Product.handle and Product.url
		// for a merchant-variant match while returning the exact fresh Variant
		// URL. Only its /products/{handle} path is used; query parameters remain
		// untrusted display/attribution data and never enter Storefront authority.
		handle = productHandleFromVariantURLV2(match.Variant.URL)
	}
	if !storefrontHandlePatternV2.MatchString(handle) || len(allowedDomains) == 0 {
		return "", "", "", nil, variantSourceCorrelationFaultV2()
	}
	return handle, variantID, sellerID, allowedDomains, nil
}

func chooseFreshMyshopifyDomainV2(
	source researchapp.VariantChoiceCandidateSourceV2,
	match researchapp.CatalogOfferMatch,
) string {
	candidates := make([]string, 0, 5)
	if match.Product.Locator != nil && match.Product.Locator.ProductURL != nil {
		candidates = append(candidates, URLDomainV2(match.Product.Locator.ProductURL.CanonicalURL))
	}
	if match.Variant.Seller != nil {
		candidates = append(candidates, strings.ToLower(strings.TrimSpace(match.Variant.Seller.Domain)))
		candidates = append(candidates, URLDomainV2(match.Variant.Seller.URL))
	}
	// A product URL source is usable only because this fresh lookup returned an
	// exact correlation for the same identifier above.
	if source.Locator.ProductURL != nil {
		candidates = append(candidates, URLDomainV2(source.Locator.ProductURL.CanonicalURL))
	}
	sort.Strings(candidates)
	for _, candidate := range candidates {
		if strictMyshopifyDomainV2(candidate) {
			return candidate
		}
	}
	return ""
}

func addCatalogProductDomainsV2(
	domains map[string]struct{},
	product researchapp.CatalogProductObservation,
) {
	if product.Locator != nil && product.Locator.ProductURL != nil {
		addURLDomainV2(domains, product.Locator.ProductURL.CanonicalURL)
	}
	if product.PreviewVariant != nil {
		addURLDomainV2(domains, product.PreviewVariant.URL)
		if product.PreviewVariant.Seller != nil {
			addDomainV2(domains, product.PreviewVariant.Seller.Domain)
			addURLDomainV2(domains, product.PreviewVariant.Seller.URL)
		}
	}
}

func addURLDomainV2(domains map[string]struct{}, raw string) {
	addDomainV2(domains, URLDomainV2(raw))
}

func addDomainV2(domains map[string]struct{}, domain string) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if domain != "" {
		domains[domain] = struct{}{}
	}
}

func URLDomainV2(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func productHandleFromURLV2(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "products" ||
		!storefrontHandlePatternV2.MatchString(parts[1]) {
		return ""
	}
	return parts[1]
}

func productHandleFromVariantURLV2(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" ||
		parsed.Fragment != "" || parsed.RawPath != "" || parsed.Opaque != "" {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "products" ||
		!storefrontHandlePatternV2.MatchString(parts[1]) {
		return ""
	}
	return parts[1]
}

func strictMyshopifyDomainV2(domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !strings.HasSuffix(domain, ".myshopify.com") || strings.Count(domain, ".") != 2 {
		return false
	}
	label := strings.TrimSuffix(domain, ".myshopify.com")
	if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return false
	}
	for _, character := range label {
		if !((character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '-') {
			return false
		}
	}
	return true
}

func variantLookupIdentifierV2(locator researchapp.CatalogProductLocator) string {
	if locator.ProductURL != nil {
		return strings.TrimSpace(locator.ProductURL.CanonicalURL)
	}
	if locator.MerchantVariant != nil {
		return strings.TrimSpace(locator.MerchantVariant.VariantID)
	}
	return ""
}

func variantSeedAuthorityV2(locator researchapp.CatalogProductLocator) string {
	if locator.ProductURL != nil {
		return URLDomainV2(locator.ProductURL.CanonicalURL)
	}
	if locator.MerchantVariant != nil {
		return strings.ToLower(strings.TrimSpace(locator.MerchantVariant.SellerDomain))
	}
	return ""
}

func variantSourceCorrelationFaultV2() error {
	return fault.New(
		fault.ProviderRejected, researchapp.VariantSourceCorrelationMismatchV2, false,
	)
}
