package app

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const (
	VariantChoicePageSizeV2                = 20
	VariantChoiceSnapshotSchemaV2          = "vitlane.variant-choice-snapshot.v2"
	VariantChoiceFailureInvalidV2          = "VARIANT_CHOICE_REQUEST_INVALID"
	VariantChoiceFailureStalePageV2        = "STALE_VARIANT_PAGE"
	VariantChoiceFailureSourceResolutionV2 = "VARIANT_SOURCE_RESOLUTION_FAILED"
	VariantChoiceFailureGatewayV2          = "VARIANT_CHOICE_GATEWAY_FAILED"
	VariantChoiceFailureArtifactV2         = "VARIANT_CHOICE_ARTIFACT_FAILED"
	VariantResolutionUnavailableV2         = "VARIANT_RESOLUTION_UNAVAILABLE"
	VariantSourceCorrelationMismatchV2     = "VARIANT_SOURCE_CORRELATION_MISMATCH"
	VariantIdentityCorrelationMismatchV2   = "VARIANT_IDENTITY_CORRELATION_MISMATCH"
	VariantResolvedSourceLineageMismatchV2 = "VARIANT_RESOLVED_SOURCE_LINEAGE_MISMATCH"
	VariantPageCorrelationMismatchV2       = "VARIANT_PAGE_CORRELATION_MISMATCH"
	VariantPageLineageMismatchV2           = "VARIANT_PAGE_LINEAGE_MISMATCH"
)

var (
	storefrontShopGIDV2    = regexp.MustCompile(`^gid://shopify/Shop/[0-9]+$`)
	storefrontProductGIDV2 = regexp.MustCompile(`^gid://shopify/Product/[0-9]+$`)
	storefrontVariantGIDV2 = regexp.MustCompile(`^gid://shopify/ProductVariant/[0-9]+$`)
	storefrontDomainV2     = regexp.MustCompile(
		`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`,
	)
	storefrontMyshopifyDomainV2 = regexp.MustCompile(
		`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.myshopify\.com$`,
	)
	storefrontProductHandleV2 = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
)

// VerifiedStorefrontShopV2 is evidence produced by a separate trusted
// Shop-to-myshopify mapping process. Seller-domain text alone is never enough
// to construct a Storefront endpoint.
type VerifiedStorefrontShopV2 struct {
	ShopID            string `json:"shopId"`
	MyshopifyDomain   string `json:"myshopifyDomain"`
	PrimaryDomain     string `json:"primaryDomain"`
	MappingEvidenceID string `json:"mappingEvidenceId"`
}

func (shop VerifiedStorefrontShopV2) Validate() error {
	if !storefrontShopGIDV2.MatchString(shop.ShopID) ||
		shop.MyshopifyDomain != strings.ToLower(strings.TrimSpace(shop.MyshopifyDomain)) ||
		!storefrontMyshopifyDomainV2.MatchString(shop.MyshopifyDomain) ||
		shop.PrimaryDomain != strings.ToLower(strings.TrimSpace(shop.PrimaryDomain)) ||
		!storefrontDomainV2.MatchString(shop.PrimaryDomain) ||
		strings.TrimSpace(shop.MappingEvidenceID) == "" ||
		shop.MappingEvidenceID != strings.TrimSpace(shop.MappingEvidenceID) {
		return errors.New(VariantChoiceFailureInvalidV2)
	}
	return nil
}

// VariantChoiceCandidateSourceV2 binds a fresh Variant read to the exact
// Candidate discovery and trusted locator observation that opened the modal.
type VariantChoiceCandidateSourceV2 struct {
	CandidateID       string                     `json:"candidateId"`
	SourceDiscoveryID string                     `json:"sourceDiscoveryId"`
	SourceLocatorHash string                     `json:"sourceLocatorHash"`
	Locator           CatalogProductLocator      `json:"locator"`
	MarketContext     shareddomain.MarketContext `json:"marketContext"`
}

func (source VariantChoiceCandidateSourceV2) Validate() error {
	if strings.TrimSpace(source.CandidateID) == "" ||
		strings.TrimSpace(source.SourceDiscoveryID) == "" ||
		strings.TrimSpace(source.SourceLocatorHash) == "" ||
		source.CandidateID != strings.TrimSpace(source.CandidateID) ||
		source.SourceDiscoveryID != strings.TrimSpace(source.SourceDiscoveryID) ||
		source.SourceLocatorHash != strings.TrimSpace(source.SourceLocatorHash) ||
		source.Locator.Validate() != nil {
		return errors.New(VariantChoiceFailureInvalidV2)
	}
	if err := source.MarketContext.Validate(); err != nil {
		return errors.New(VariantChoiceFailureInvalidV2)
	}
	hash, err := VariantChoiceSourceLocatorHashV2(source.Locator)
	if err != nil || hash != source.SourceLocatorHash {
		return errors.New(VariantChoiceFailureInvalidV2)
	}
	switch source.Locator.Kind {
	case CatalogLocatorProductURL:
		_, _, err = productSeedFromLocatorV2(source.Locator)
		return err
	case CatalogLocatorMerchantVariant:
		locator := source.Locator.MerchantVariant
		if locator == nil || locator.SellerDomain != strings.ToLower(
			strings.TrimSpace(locator.SellerDomain),
		) || locator.VariantID != strings.TrimSpace(locator.VariantID) ||
			locator.SellerID != strings.TrimSpace(locator.SellerID) ||
			!storefrontDomainV2.MatchString(locator.SellerDomain) {
			return errors.New(VariantChoiceFailureInvalidV2)
		}
		return nil
	default:
		return errors.New(VariantChoiceFailureInvalidV2)
	}
}

func VariantChoiceSourceLocatorHashV2(locator CatalogProductLocator) (string, error) {
	if locator.Validate() != nil {
		return "", errors.New(VariantChoiceFailureInvalidV2)
	}
	switch locator.Kind {
	case CatalogLocatorProductURL:
		return shareddomain.CanonicalJSONHash(struct {
			Version      string `json:"version"`
			Kind         string `json:"kind"`
			CanonicalURL string `json:"canonicalUrl"`
		}{
			Version:      "vitlane.variant-source-locator.v1",
			Kind:         string(locator.Kind),
			CanonicalURL: locator.ProductURL.CanonicalURL,
		})
	case CatalogLocatorMerchantVariant:
		return shareddomain.CanonicalJSONHash(struct {
			Version      string `json:"version"`
			Kind         string `json:"kind"`
			VariantID    string `json:"variantId"`
			SellerDomain string `json:"sellerDomain"`
			SellerID     string `json:"sellerId,omitempty"`
		}{
			Version:   "vitlane.variant-source-locator.v1",
			Kind:      string(locator.Kind),
			VariantID: strings.TrimSpace(locator.MerchantVariant.VariantID),
			SellerDomain: strings.ToLower(strings.TrimSpace(
				locator.MerchantVariant.SellerDomain,
			)),
			SellerID: strings.TrimSpace(locator.MerchantVariant.SellerID),
		})
	default:
		return "", errors.New(VariantChoiceFailureInvalidV2)
	}
}

// VerifiedVariantChoiceSourceV2 is the result of a server-controlled, fresh
// mapping/lookup stage. SeedAuthority is the observed product host or seller
// domain that was mapped; it is never itself used as the Storefront API origin.
type VerifiedVariantChoiceSourceV2 struct {
	PrefetchedPage             *StorefrontVariantPageV2 `json:"-"`
	SourceLocatorHash          string
	SeedKind                   CatalogLocatorKind
	SeedAuthority              string
	SeedIdentifier             string
	StorefrontShop             VerifiedStorefrontShopV2
	ProductID                  string
	ProductHandle              string
	StorefrontVariantID        string
	SourceResolutionEvidenceID string
	ResolvedAt                 time.Time
	ExpiresAt                  time.Time
}

// VariantChoiceSourceResolverV2 owns trusted Shop-to-myshopify mapping and,
// for merchant-variant seeds, variant-to-parent-product lookup. Implementations
// must perform a fresh read or return still-valid reviewed evidence; they must
// never turn seller.domain directly into an API origin.
type VariantChoiceSourceResolverV2 interface {
	ResolveVariantChoiceSource(
		context.Context,
		string,
		VariantChoiceCandidateSourceV2,
		CatalogProviderCallAdmissionV2,
	) (VerifiedVariantChoiceSourceV2, error)
}

type StorefrontVariantQueryV2 struct {
	MyshopifyDomain       string
	ExpectedShopID        string
	ExpectedPrimaryDomain string
	MappingEvidenceID     string
	ExpectedProductID     string
	ProductHandle         string
	Country               string
	Currency              string
	AfterCursor           string
	First                 int
	ProviderCallAdmission CatalogProviderCallAdmissionV2 `json:"-"`
}

func (request StorefrontVariantQueryV2) Validate() error {
	if !storefrontMyshopifyDomainV2.MatchString(request.MyshopifyDomain) ||
		request.MyshopifyDomain != strings.ToLower(strings.TrimSpace(request.MyshopifyDomain)) ||
		!storefrontShopGIDV2.MatchString(request.ExpectedShopID) ||
		!storefrontDomainV2.MatchString(request.ExpectedPrimaryDomain) ||
		request.ExpectedPrimaryDomain != strings.ToLower(
			strings.TrimSpace(request.ExpectedPrimaryDomain),
		) || strings.TrimSpace(request.MappingEvidenceID) == "" ||
		request.MappingEvidenceID != strings.TrimSpace(request.MappingEvidenceID) ||
		!storefrontProductHandleV2.MatchString(request.ProductHandle) ||
		request.First != VariantChoicePageSizeV2 {
		return errors.New(VariantChoiceFailureInvalidV2)
	}
	market, err := shareddomain.NewMarketContext(request.Country, request.Currency)
	if err != nil || string(market.Country) != request.Country ||
		string(market.Currency) != request.Currency {
		return errors.New(VariantChoiceFailureInvalidV2)
	}
	if request.ExpectedProductID != "" &&
		!storefrontProductGIDV2.MatchString(request.ExpectedProductID) {
		return errors.New(VariantChoiceFailureInvalidV2)
	}
	return nil
}

type StorefrontProductOptionV2 struct {
	Name   string
	Values []string
}

type StorefrontVariantMediaV2 struct {
	URL     string
	AltText string
}

type StorefrontVariantRowV2 struct {
	VariantID        string
	Title            string
	SelectedOptions  []researchdomain.VariantOptionSelectionV2
	Price            shareddomain.Money
	AvailableForSale bool
	Media            *StorefrontVariantMediaV2
}

type StorefrontVariantPageV2 struct {
	ShopID         string
	PrimaryDomain  string
	ProductID      string
	ProductHandle  string
	ProductTitle   string
	ProductURL     string
	ProductOptions []StorefrontProductOptionV2
	Rows           []StorefrontVariantRowV2
	HasNextPage    bool
	EndCursor      string
}

type StorefrontVariantGatewayV2 interface {
	LoadVariantPage(
		context.Context,
		StorefrontVariantQueryV2,
	) (StorefrontVariantPageV2, error)
}

// VariantChoiceContinuationV2 is returned only by a trusted opaque-token
// verifier. ProviderCursor is never returned from VariantChoicePageV2.
type VariantChoiceContinuationV2 struct {
	ResolutionObservedAt       time.Time
	ResolutionExpiresAt        time.Time
	ContextCurrency            string
	OwnerID                    string
	CandidateID                string
	SourceDiscoveryID          string
	SourceLocatorHash          string
	ShopID                     string
	MyshopifyDomain            string
	PrimaryDomain              string
	MappingEvidenceID          string
	SourceResolutionEvidenceID string
	ProductID                  string
	ProductHandle              string
	StorefrontVariantID        string
	ContextCountry             string
	ProviderCursor             string
	SeenVariantIDs             []string
	PreviousPageHash           string
	ExpiresAt                  time.Time
}

// VariantPageCursorAuthorityV2 signs and verifies only pagination cursors.
// Selecting a row needs no authority token: its Variant reference is copied
// into a fallible CartView item and freshly resolved at PrepareAgencyOrder.
type VariantPageCursorAuthorityV2 interface {
	VerifyContinuation(
		context.Context,
		string,
		string,
	) (VariantChoiceContinuationV2, error)
	IssueContinuation(
		context.Context,
		VariantChoiceContinuationV2,
	) (string, error)
}

type LoadVariantChoicePageV2Input struct {
	OwnerID               string
	Source                VariantChoiceCandidateSourceV2
	CursorToken           string
	ProviderCallAdmission CatalogProviderCallAdmissionV2 `json:"-"`
}

type VariantChoiceRowV2 struct {
	VariantRef       researchdomain.VariantRefV2
	Title            string
	SelectedOptions  []researchdomain.VariantOptionSelectionV2
	ObservedPrice    shareddomain.Money
	AvailableForSale bool
	Media            *StorefrontVariantMediaV2
}

type VariantChoicePageV2 struct {
	CandidateID          string
	SourceDiscoveryID    string
	SourceLocatorHash    string
	ProductTitle         string
	ProductURL           string
	MerchantDomain       string
	Rows                 []VariantChoiceRowV2
	NextCursor           string
	ContextCountry       string
	ObservedAt           time.Time
	ExpiresAt            time.Time
	SnapshotHash         string
	CanSkipRemoteLoading bool
}

type VariantChoiceServiceV2 struct {
	resolver  VariantChoiceSourceResolverV2
	gateway   StorefrontVariantGatewayV2
	authority VariantPageCursorAuthorityV2
	clock     sharedapp.Clock
	ttl       time.Duration
}

func NewVariantChoiceServiceV2(
	resolver VariantChoiceSourceResolverV2,
	gateway StorefrontVariantGatewayV2,
	authority VariantPageCursorAuthorityV2,
	clock sharedapp.Clock,
	ttl time.Duration,
) (*VariantChoiceServiceV2, error) {
	if resolver == nil || gateway == nil || authority == nil || clock == nil || ttl <= 0 ||
		ttl > 30*time.Minute {
		return nil, fault.New(fault.InvalidInput, VariantChoiceFailureInvalidV2, false)
	}
	return &VariantChoiceServiceV2{
		resolver: resolver, gateway: gateway, authority: authority, clock: clock, ttl: ttl,
	}, nil
}

func (service *VariantChoiceServiceV2) LoadPage(
	ctx context.Context,
	input LoadVariantChoicePageV2Input,
) (VariantChoicePageV2, error) {
	input.OwnerID = strings.TrimSpace(input.OwnerID)
	input.CursorToken = strings.TrimSpace(input.CursorToken)
	if service == nil || service.resolver == nil || service.gateway == nil ||
		service.authority == nil ||
		service.clock == nil || input.OwnerID == "" || input.Source.Validate() != nil {
		return VariantChoicePageV2{}, fault.New(
			fault.InvalidInput, VariantChoiceFailureInvalidV2, false,
		)
	}
	startedAt := service.clock.Now().UTC()
	if startedAt.IsZero() {
		return VariantChoicePageV2{}, fault.New(
			fault.InternalFailure, VariantChoiceFailureArtifactV2, false,
		)
	}
	var verifiedContinuation *VariantChoiceContinuationV2
	if input.CursorToken != "" {
		continuation, err := service.authority.VerifyContinuation(ctx, input.OwnerID, input.CursorToken)
		if err != nil {
			return VariantChoicePageV2{}, typedVariantArtifactFailureV2(err)
		}
		if continuation.OwnerID != input.OwnerID || continuation.CandidateID != input.Source.CandidateID || continuation.SourceDiscoveryID != input.Source.SourceDiscoveryID || continuation.SourceLocatorHash != input.Source.SourceLocatorHash || continuation.ContextCountry != string(input.Source.MarketContext.Country) || !continuation.ExpiresAt.After(startedAt) {
			return VariantChoicePageV2{}, fault.New(fault.Conflict, VariantChoiceFailureStalePageV2, false)
		}
		verifiedContinuation = &continuation
	}
	var resolution VerifiedVariantChoiceSourceV2
	var err error
	if verifiedContinuation != nil && !verifiedContinuation.ResolutionObservedAt.IsZero() {
		if verifiedContinuation.ContextCurrency != string(input.Source.MarketContext.Currency) {
			return VariantChoicePageV2{}, fault.New(fault.Conflict, VariantChoiceFailureStalePageV2, false)
		}
		resolution = variantResolutionFromContinuation(*verifiedContinuation, input.Source)
	} else if combined, ok := service.resolver.(interface {
		ResolveVariantChoiceFirstPage(context.Context, string, VariantChoiceCandidateSourceV2, CatalogProviderCallAdmissionV2) (VerifiedVariantChoiceSourceV2, error)
	}); ok && input.CursorToken == "" {
		resolution, err = combined.ResolveVariantChoiceFirstPage(ctx, input.OwnerID, input.Source, input.ProviderCallAdmission)
	} else {
		resolution, err = service.resolver.ResolveVariantChoiceSource(ctx, input.OwnerID, input.Source, input.ProviderCallAdmission)
	}
	if err != nil {
		return VariantChoicePageV2{}, typedVariantSourceResolutionFailureV2(err)
	}
	now := service.clock.Now().UTC()
	if now.IsZero() || now.Before(startedAt) {
		return VariantChoicePageV2{}, fault.New(
			fault.InternalFailure, VariantChoiceFailureArtifactV2, false,
		)
	}
	if !validVerifiedVariantChoiceSourceV2(resolution, input.Source, now) {
		return VariantChoicePageV2{}, fault.New(
			fault.ProviderRejected, VariantResolvedSourceLineageMismatchV2, false,
		)
	}

	after := ""
	seen := []string{}
	if verifiedContinuation != nil {
		continuation := *verifiedContinuation
		if !validVariantContinuationV2(continuation, input.OwnerID, input.Source, resolution, now) {
			return VariantChoicePageV2{}, fault.New(fault.Conflict, VariantChoiceFailureStalePageV2, false)
		}
		after = continuation.ProviderCursor
		seen = slices.Clone(continuation.SeenVariantIDs)
	}

	request := StorefrontVariantQueryV2{
		MyshopifyDomain:       resolution.StorefrontShop.MyshopifyDomain,
		ExpectedShopID:        resolution.StorefrontShop.ShopID,
		ExpectedPrimaryDomain: resolution.StorefrontShop.PrimaryDomain,
		MappingEvidenceID:     resolution.StorefrontShop.MappingEvidenceID,
		ExpectedProductID:     resolution.ProductID,
		ProductHandle:         resolution.ProductHandle,
		Country:               string(input.Source.MarketContext.Country),
		Currency:              string(input.Source.MarketContext.Currency),
		AfterCursor:           after,
		First:                 VariantChoicePageSizeV2,
		ProviderCallAdmission: input.ProviderCallAdmission,
	}
	var providerPage StorefrontVariantPageV2
	if resolution.PrefetchedPage != nil && input.CursorToken == "" {
		providerPage = *resolution.PrefetchedPage
	} else {
		providerPage, err = service.gateway.LoadVariantPage(ctx, request)
	}
	if err != nil {
		return VariantChoicePageV2{}, typedVariantGatewayFailureV2(err)
	}
	rows, cumulativeSeen, err := validateAndBindVariantPageV2(
		input.Source, resolution, request, providerPage, seen,
	)
	if err != nil {
		return VariantChoicePageV2{}, err
	}
	observedAt := now
	expiresAt := now.Add(service.ttl)
	if resolution.ExpiresAt.Before(expiresAt) {
		expiresAt = resolution.ExpiresAt
	}
	snapshotHash, err := variantChoiceSnapshotHashV2(
		input.OwnerID, input.Source, resolution, providerPage, rows, cumulativeSeen,
		observedAt, expiresAt,
	)
	if err != nil {
		return VariantChoicePageV2{}, fault.Wrap(
			err, fault.InternalFailure, VariantChoiceFailureArtifactV2, false,
		)
	}

	nextCursor := ""
	if providerPage.HasNextPage {
		continuation := VariantChoiceContinuationV2{
			ResolutionObservedAt: resolution.ResolvedAt, ResolutionExpiresAt: resolution.ExpiresAt, ContextCurrency: string(input.Source.MarketContext.Currency),
			OwnerID: input.OwnerID, CandidateID: input.Source.CandidateID,
			SourceDiscoveryID:          input.Source.SourceDiscoveryID,
			SourceLocatorHash:          input.Source.SourceLocatorHash,
			ShopID:                     providerPage.ShopID,
			MyshopifyDomain:            resolution.StorefrontShop.MyshopifyDomain,
			PrimaryDomain:              resolution.StorefrontShop.PrimaryDomain,
			MappingEvidenceID:          resolution.StorefrontShop.MappingEvidenceID,
			SourceResolutionEvidenceID: resolution.SourceResolutionEvidenceID,
			ProductID:                  providerPage.ProductID,
			ProductHandle:              providerPage.ProductHandle,
			StorefrontVariantID:        resolution.StorefrontVariantID,
			ContextCountry:             string(input.Source.MarketContext.Country),
			ProviderCursor:             providerPage.EndCursor,
			SeenVariantIDs:             slices.Clone(cumulativeSeen),
			PreviousPageHash:           snapshotHash,
			ExpiresAt:                  expiresAt,
		}
		issuedCursor, issueErr := service.authority.IssueContinuation(ctx, continuation)
		if issueErr != nil {
			return VariantChoicePageV2{}, typedVariantArtifactFailureV2(issueErr)
		}
		nextCursor = strings.TrimSpace(issuedCursor)
		if nextCursor == "" || nextCursor != issuedCursor ||
			nextCursor == providerPage.EndCursor {
			return VariantChoicePageV2{}, fault.New(
				fault.InternalFailure, VariantChoiceFailureArtifactV2, false,
			)
		}
	}

	return VariantChoicePageV2{
		CandidateID:       input.Source.CandidateID,
		SourceDiscoveryID: input.Source.SourceDiscoveryID,
		SourceLocatorHash: input.Source.SourceLocatorHash,
		ProductTitle:      providerPage.ProductTitle,
		ProductURL:        providerPage.ProductURL,
		MerchantDomain:    providerPage.PrimaryDomain,
		Rows:              rows, NextCursor: nextCursor,
		ContextCountry: string(input.Source.MarketContext.Country),
		ObservedAt:     observedAt, ExpiresAt: expiresAt,
		SnapshotHash: snapshotHash,
		CanSkipRemoteLoading: input.CursorToken == "" &&
			len(rows) == 1 && !providerPage.HasNextPage &&
			!hasMerchantSelectableProductOptionsV2(providerPage.ProductOptions),
	}, nil
}

func validateAndBindVariantPageV2(
	source VariantChoiceCandidateSourceV2,
	resolution VerifiedVariantChoiceSourceV2,
	request StorefrontVariantQueryV2,
	page StorefrontVariantPageV2,
	seen []string,
) ([]VariantChoiceRowV2, []string, error) {
	if page.ShopID != resolution.StorefrontShop.ShopID ||
		page.PrimaryDomain != resolution.StorefrontShop.PrimaryDomain ||
		!storefrontProductGIDV2.MatchString(page.ProductID) ||
		page.ProductID != resolution.ProductID ||
		page.ProductID != request.ExpectedProductID ||
		page.ProductHandle != request.ProductHandle ||
		len(page.Rows) > VariantChoicePageSizeV2 ||
		(page.HasNextPage && (strings.TrimSpace(page.EndCursor) == "" ||
			page.EndCursor == request.AfterCursor)) ||
		!productURLMatchesResolutionV2(page.ProductURL, resolution) ||
		!validProductOptionsV2(page.ProductOptions) {
		return nil, nil, variantLineageFaultV2()
	}

	known := make(map[string]struct{}, len(seen)+len(page.Rows))
	for _, variantID := range seen {
		if !storefrontVariantGIDV2.MatchString(variantID) {
			return nil, nil, fault.New(
				fault.Conflict, VariantChoiceFailureStalePageV2, false,
			)
		}
		known[variantID] = struct{}{}
	}
	cumulative := slices.Clone(seen)
	rows := make([]VariantChoiceRowV2, 0, len(page.Rows))
	pageIDs := make(map[string]struct{}, len(page.Rows))
	for _, row := range page.Rows {
		canonicalPrice, priceErr := shareddomain.NewMoney(
			row.Price.Amount, string(row.Price.Currency),
		)
		if !storefrontVariantGIDV2.MatchString(row.VariantID) ||
			strings.TrimSpace(row.Title) == "" ||
			priceErr != nil || canonicalPrice != row.Price || row.Price.Sign() < 0 ||
			row.Price.Currency != source.MarketContext.Currency ||
			!validVariantMediaV2(row.Media) ||
			!validSelectedOptionsV2(row.SelectedOptions, page.ProductOptions) {
			return nil, nil, variantLineageFaultV2()
		}
		if _, duplicate := pageIDs[row.VariantID]; duplicate {
			return nil, nil, variantLineageFaultV2()
		}
		pageIDs[row.VariantID] = struct{}{}
		if _, duplicate := known[row.VariantID]; duplicate {
			continue
		}
		known[row.VariantID] = struct{}{}
		cumulative = append(cumulative, row.VariantID)
		rows = append(rows, VariantChoiceRowV2{
			VariantRef: researchdomain.VariantRefV2{
				Provider:   "SHOPIFY_STOREFRONT",
				MerchantID: page.ShopID,
				ProductID:  page.ProductID,
				VariantID:  row.VariantID,
			},
			Title:            row.Title,
			SelectedOptions:  slices.Clone(row.SelectedOptions),
			ObservedPrice:    row.Price,
			AvailableForSale: row.AvailableForSale,
			Media:            cloneStorefrontVariantMediaV2(row.Media),
		})
	}
	return rows, cumulative, nil
}

func validProductOptionsV2(options []StorefrontProductOptionV2) bool {
	seen := make(map[string]struct{}, len(options))
	for _, option := range options {
		name := strings.TrimSpace(option.Name)
		if name == "" || len(option.Values) == 0 {
			return false
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
		values := make(map[string]struct{}, len(option.Values))
		for _, value := range option.Values {
			value = strings.TrimSpace(value)
			if value == "" {
				return false
			}
			valueKey := strings.ToLower(value)
			if _, exists := values[valueKey]; exists {
				return false
			}
			values[valueKey] = struct{}{}
		}
	}
	return true
}

func validSelectedOptionsV2(
	selections []researchdomain.VariantOptionSelectionV2,
	options []StorefrontProductOptionV2,
) bool {
	if len(options) == 0 {
		return len(selections) == 0
	}
	if len(selections) != len(options) {
		return false
	}
	allowed := make(map[string]map[string]struct{}, len(options))
	for _, option := range options {
		values := make(map[string]struct{}, len(option.Values))
		for _, value := range option.Values {
			values[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
		}
		allowed[strings.ToLower(strings.TrimSpace(option.Name))] = values
	}
	seen := make(map[string]struct{}, len(selections))
	for _, selection := range selections {
		name := strings.ToLower(strings.TrimSpace(selection.Name))
		value := strings.ToLower(strings.TrimSpace(selection.Value))
		values, exists := allowed[name]
		if !exists || value == "" {
			return false
		}
		if _, exists := values[value]; !exists {
			return false
		}
		if _, exists := seen[name]; exists {
			return false
		}
		seen[name] = struct{}{}
	}
	return true
}

func hasMerchantSelectableProductOptionsV2(options []StorefrontProductOptionV2) bool {
	if len(options) == 0 {
		return false
	}
	return !(len(options) == 1 && strings.EqualFold(options[0].Name, "Title") &&
		len(options[0].Values) == 1 &&
		strings.EqualFold(options[0].Values[0], "Default Title"))
}

func validVariantContinuationV2(
	continuation VariantChoiceContinuationV2,
	ownerID string,
	source VariantChoiceCandidateSourceV2,
	resolution VerifiedVariantChoiceSourceV2,
	now time.Time,
) bool {
	if continuation.OwnerID != ownerID ||
		continuation.CandidateID != source.CandidateID ||
		continuation.SourceDiscoveryID != source.SourceDiscoveryID ||
		continuation.SourceLocatorHash != source.SourceLocatorHash ||
		continuation.ShopID != resolution.StorefrontShop.ShopID ||
		continuation.MyshopifyDomain != resolution.StorefrontShop.MyshopifyDomain ||
		continuation.PrimaryDomain != resolution.StorefrontShop.PrimaryDomain ||
		continuation.MappingEvidenceID != resolution.StorefrontShop.MappingEvidenceID ||
		strings.TrimSpace(continuation.SourceResolutionEvidenceID) == "" ||
		continuation.SourceResolutionEvidenceID != strings.TrimSpace(
			continuation.SourceResolutionEvidenceID,
		) ||
		continuation.ProductID != resolution.ProductID ||
		continuation.ProductHandle != resolution.ProductHandle ||
		continuation.StorefrontVariantID != resolution.StorefrontVariantID ||
		continuation.ContextCountry != string(source.MarketContext.Country) ||
		strings.TrimSpace(continuation.ProviderCursor) == "" ||
		continuation.ProviderCursor != strings.TrimSpace(continuation.ProviderCursor) ||
		strings.TrimSpace(continuation.PreviousPageHash) == "" ||
		continuation.PreviousPageHash != strings.TrimSpace(continuation.PreviousPageHash) ||
		!continuation.ExpiresAt.After(now) {
		return false
	}
	seen := make(map[string]struct{}, len(continuation.SeenVariantIDs))
	for _, variantID := range continuation.SeenVariantIDs {
		if !storefrontVariantGIDV2.MatchString(variantID) {
			return false
		}
		if _, exists := seen[variantID]; exists {
			return false
		}
		seen[variantID] = struct{}{}
	}
	return true
}

func variantChoiceSnapshotHashV2(
	ownerID string,
	source VariantChoiceCandidateSourceV2,
	resolution VerifiedVariantChoiceSourceV2,
	page StorefrontVariantPageV2,
	rows []VariantChoiceRowV2,
	seen []string,
	observedAt time.Time,
	expiresAt time.Time,
) (string, error) {
	return shareddomain.CanonicalJSONHash(struct {
		SchemaVersion              string
		OwnerID                    string
		CandidateID                string
		SourceDiscoveryID          string
		SourceLocatorHash          string
		SeedKind                   CatalogLocatorKind
		SeedAuthority              string
		SeedIdentifier             string
		ShopID                     string
		MyshopifyDomain            string
		PrimaryDomain              string
		MappingEvidenceID          string
		SourceResolutionEvidenceID string
		ProductID                  string
		ProductHandle              string
		StorefrontVariantID        string
		ContextCountry             string
		Rows                       []VariantChoiceRowV2
		SeenVariantIDs             []string
		NextProviderCursor         string
		ObservedAt                 time.Time
		ExpiresAt                  time.Time
	}{
		SchemaVersion: VariantChoiceSnapshotSchemaV2,
		OwnerID:       ownerID, CandidateID: source.CandidateID,
		SourceDiscoveryID:          source.SourceDiscoveryID,
		SourceLocatorHash:          source.SourceLocatorHash,
		SeedKind:                   resolution.SeedKind,
		SeedAuthority:              resolution.SeedAuthority,
		SeedIdentifier:             resolution.SeedIdentifier,
		ShopID:                     page.ShopID,
		MyshopifyDomain:            resolution.StorefrontShop.MyshopifyDomain,
		PrimaryDomain:              resolution.StorefrontShop.PrimaryDomain,
		MappingEvidenceID:          resolution.StorefrontShop.MappingEvidenceID,
		SourceResolutionEvidenceID: resolution.SourceResolutionEvidenceID,
		ProductID:                  page.ProductID,
		ProductHandle:              page.ProductHandle,
		StorefrontVariantID:        resolution.StorefrontVariantID,
		ContextCountry:             string(source.MarketContext.Country),
		Rows:                       rows, SeenVariantIDs: seen,
		NextProviderCursor: page.EndCursor,
		ObservedAt:         observedAt, ExpiresAt: expiresAt,
	})
}

func validVerifiedVariantChoiceSourceV2(
	resolution VerifiedVariantChoiceSourceV2,
	source VariantChoiceCandidateSourceV2,
	now time.Time,
) bool {
	if resolution.SourceLocatorHash != source.SourceLocatorHash ||
		resolution.SeedKind != source.Locator.Kind ||
		resolution.StorefrontShop.Validate() != nil ||
		!storefrontProductGIDV2.MatchString(resolution.ProductID) ||
		!storefrontProductHandleV2.MatchString(resolution.ProductHandle) ||
		strings.TrimSpace(resolution.SourceResolutionEvidenceID) == "" ||
		resolution.SourceResolutionEvidenceID != strings.TrimSpace(
			resolution.SourceResolutionEvidenceID,
		) || resolution.ResolvedAt.IsZero() || resolution.ResolvedAt.After(now) ||
		!resolution.ExpiresAt.After(now) ||
		!resolution.ExpiresAt.After(resolution.ResolvedAt) {
		return false
	}

	switch source.Locator.Kind {
	case CatalogLocatorProductURL:
		authority, handle, err := productSeedFromLocatorV2(source.Locator)
		return err == nil && resolution.SeedAuthority == authority &&
			resolution.SeedIdentifier == source.Locator.ProductURL.CanonicalURL &&
			resolution.ProductHandle == handle && resolution.StorefrontVariantID == ""
	case CatalogLocatorMerchantVariant:
		locator := source.Locator.MerchantVariant
		return locator != nil &&
			resolution.SeedAuthority == locator.SellerDomain &&
			resolution.SeedIdentifier == locator.VariantID &&
			storefrontVariantGIDV2.MatchString(resolution.StorefrontVariantID)
	default:
		return false
	}
}

func productSeedFromLocatorV2(
	locator CatalogProductLocator,
) (string, string, error) {
	if locator.Kind != CatalogLocatorProductURL || locator.ProductURL == nil ||
		locator.MerchantVariant != nil {
		return "", "", errors.New(VariantChoiceFailureInvalidV2)
	}
	raw := locator.ProductURL.CanonicalURL
	parsed, err := url.Parse(raw)
	if err != nil || raw != strings.TrimSpace(raw) || parsed.Scheme != "https" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Port() != "" || parsed.RawPath != "" || parsed.Opaque != "" {
		return "", "", errors.New(VariantChoiceFailureInvalidV2)
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Hostname() != host || !storefrontDomainV2.MatchString(host) {
		return "", "", errors.New(VariantChoiceFailureInvalidV2)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "products" ||
		!storefrontProductHandleV2.MatchString(parts[1]) {
		return "", "", errors.New(VariantChoiceFailureInvalidV2)
	}
	return host, parts[1], nil
}

func productURLMatchesResolutionV2(
	raw string,
	resolution VerifiedVariantChoiceSourceV2,
) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" ||
		parsed.RawPath != "" || parsed.Opaque != "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != resolution.StorefrontShop.PrimaryDomain &&
		host != resolution.StorefrontShop.MyshopifyDomain {
		return false
	}
	return strings.Trim(parsed.Path, "/") == "products/"+resolution.ProductHandle
}

func cloneStorefrontVariantMediaV2(
	media *StorefrontVariantMediaV2,
) *StorefrontVariantMediaV2 {
	if media == nil {
		return nil
	}
	cloned := *media
	return &cloned
}

func validVariantMediaV2(media *StorefrontVariantMediaV2) bool {
	if media == nil {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(media.URL))
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" &&
		parsed.User == nil
}

func variantLineageFaultV2() error {
	return fault.New(
		fault.ProviderRejected, VariantPageLineageMismatchV2, false,
	)
}

func typedVariantSourceResolutionFailureV2(err error) error {
	if classified, ok := fault.As(err); ok {
		reason := VariantChoiceFailureSourceResolutionV2
		if safeVariantSourceResolutionReasonV2(classified.Reason) {
			reason = classified.Reason
		}
		sanitized := fault.Wrap(
			err, classified.Code, reason, classified.Retryable,
		)
		sanitized.RetryAfter = classified.RetryAfter
		return sanitized
	}
	return fault.Wrap(
		err, fault.InternalFailure, VariantChoiceFailureSourceResolutionV2, false,
	)
}

func safeVariantSourceResolutionReasonV2(reason string) bool {
	switch reason {
	case string(CatalogFailureRateLimited),
		string(CatalogFailureSecurityRejected),
		string(CatalogFailureProfileOrAuth),
		string(CatalogFailureSchemaMismatch),
		string(CatalogFailureValidation),
		string(CatalogFailureUnavailable),
		string(CatalogFailureCorrelation),
		string(CatalogFailureProtocolRejected),
		string(CatalogFailureResponseTooLarge),
		string(CatalogFailureRequestInvalid),
		VariantResolutionUnavailableV2,
		VariantSourceCorrelationMismatchV2,
		VariantIdentityCorrelationMismatchV2,
		VariantChoiceFailureSourceResolutionV2:
		return true
	default:
		return false
	}
}

func typedVariantGatewayFailureV2(err error) error {
	if _, ok := fault.As(err); ok {
		return err
	}
	return fault.Wrap(
		err, fault.InternalFailure, VariantChoiceFailureGatewayV2, false,
	)
}

func typedVariantArtifactFailureV2(err error) error {
	if classified, ok := fault.As(err); ok {
		if classified.Code == fault.Conflict ||
			classified.Code == fault.InvalidInput {
			return fault.Wrap(
				err, fault.Conflict, VariantChoiceFailureStalePageV2, false,
			)
		}
		return err
	}
	return fault.Wrap(
		err, fault.InternalFailure, VariantChoiceFailureArtifactV2, false,
	)
}

// Only a signature-verified, owner/candidate/context-bound cursor reaches this
// reconstruction. Its original resolution expiry is never renewed by paging.
func variantResolutionFromContinuation(c VariantChoiceContinuationV2, source VariantChoiceCandidateSourceV2) VerifiedVariantChoiceSourceV2 {
	authority, identifier := "", ""
	if source.Locator.Kind == CatalogLocatorProductURL {
		authority, _, _ = productSeedFromLocatorV2(source.Locator)
		identifier = source.Locator.ProductURL.CanonicalURL
	} else if source.Locator.MerchantVariant != nil {
		authority = source.Locator.MerchantVariant.SellerDomain
		identifier = source.Locator.MerchantVariant.VariantID
	}
	expires := c.ResolutionExpiresAt
	if c.ExpiresAt.Before(expires) {
		expires = c.ExpiresAt
	}
	return VerifiedVariantChoiceSourceV2{SourceLocatorHash: c.SourceLocatorHash, SeedKind: source.Locator.Kind, SeedAuthority: authority, SeedIdentifier: identifier, StorefrontShop: VerifiedStorefrontShopV2{ShopID: c.ShopID, MyshopifyDomain: c.MyshopifyDomain, PrimaryDomain: c.PrimaryDomain, MappingEvidenceID: c.MappingEvidenceID}, ProductID: c.ProductID, ProductHandle: c.ProductHandle, StorefrontVariantID: c.StorefrontVariantID, SourceResolutionEvidenceID: c.SourceResolutionEvidenceID, ResolvedAt: c.ResolutionObservedAt, ExpiresAt: expires}
}
