package app

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"unicode"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

const (
	CatalogSearchMaxLimit         = 50
	CatalogLookupDefaultBatchSize = 50
	CatalogLookupMaxBatchSize     = 50
	CatalogLookupMaxInputs        = 50
)

const (
	CatalogAppliedFilterEvidenceV2                   = "PROVIDER_APPLIED_FILTER_REQUEST"
	CatalogPhysicalFilterEvidenceV2                  = "PROVIDER_APPLIED_PHYSICAL_CATEGORY_FILTER"
	CatalogPhysicalProductEvidenceV2                 = "PROVIDER_PRODUCT_PHYSICAL_CATEGORY"
	CatalogPhysicalShippableNewMerchandiseEvidenceV2 = "SHIPPABLE_NEW_MERCHANDISE"
	CatalogPhysicalEligibilityPolicyVersionV2        = "physical-eligibility.v2"
	CatalogProviderShopifyGlobalV2                   = "SHOPIFY_UCP_GLOBAL"
	CatalogShopifyGlobalCapabilityV2                 = "dev.shopify.catalog.global"
)

// CatalogFailureReason is the provider-neutral, safe failure vocabulary used
// by the Phase 8 research process. Provider response text must never be used as
// a failure reason or be copied into logs.
type CatalogFailureReason string

const (
	CatalogFailureRateLimited       CatalogFailureReason = "RATE_LIMITED"
	CatalogFailureSecurityRejected  CatalogFailureReason = "SECURITY_REJECTED"
	CatalogFailureProfileOrAuth     CatalogFailureReason = "PROFILE_OR_AUTH_REJECTED"
	CatalogFailureSchemaMismatch    CatalogFailureReason = "PROVIDER_SCHEMA_MISMATCH"
	CatalogFailureValidation        CatalogFailureReason = "PROVIDER_VALIDATION_ERROR"
	CatalogFailureUnavailable       CatalogFailureReason = "PROVIDER_UNAVAILABLE"
	CatalogFailureCorrelation       CatalogFailureReason = "CORRELATION_MISMATCH"
	CatalogFailureProtocolRejected  CatalogFailureReason = "PROVIDER_PROTOCOL_REJECTED"
	CatalogFailureResponseTooLarge  CatalogFailureReason = "PROVIDER_RESPONSE_TOO_LARGE"
	CatalogFailureRequestInvalid    CatalogFailureReason = "CATALOG_REQUEST_INVALID"
	CatalogFailureFilterNotEnforced CatalogFailureReason = "FILTER_NOT_ENFORCED"
)

type CatalogGatewayV2 interface {
	SearchProducts(context.Context, CatalogProductSearchRequest) (CatalogProductSearchResult, error)
	LookupMedia(context.Context, CatalogMediaLookupRequest) (CatalogMediaLookupResult, error)
}

// CatalogProviderCallAdmissionV2 is the application-owned quota boundary for
// catalog adapters that may turn one logical lookup into multiple provider
// calls. An adapter must acquire immediately before every external attempt and
// release as soon as that attempt finishes. This keeps batching and explicit
// split retries visible to the application rate guard.
type CatalogProviderCallAdmissionV2 interface {
	AcquireCatalogProviderCall(context.Context) (release func(), err error)
}

// CatalogOfferLookupGatewayV2 is deliberately separate from the search/media
// port. A CartView item is allowed to be stale; this port is called only at
// the explicit PrepareAgencyOrder boundary to obtain current variant facts.
type CatalogOfferLookupGatewayV2 interface {
	LookupOffers(context.Context, CatalogOfferLookupRequest) (CatalogOfferLookupResult, error)
}

type CatalogBuyerContext struct {
	Country    string
	Region     string
	PostalCode string
	Language   string
	Currency   string
	Intent     string
}

type CatalogDestination struct {
	Country    string
	Region     string
	PostalCode string
}

type CatalogAttributeFilter struct {
	Name   string
	Values []string
}

// CatalogPriceFilter is optional as a whole. A nil Price on the request means
// that no price constraint is sent to the provider.
type CatalogPriceFilter struct {
	MinimumMinor *int64
	MaximumMinor *int64
}

type CatalogProductSearchFilters struct {
	Available  *bool
	ShipsTo    *CatalogDestination
	Categories []string
	Conditions []string
	Attributes []CatalogAttributeFilter
	Price      *CatalogPriceFilter
}

type CatalogProductSearchRequest struct {
	Query   string
	Context CatalogBuyerContext
	Filters CatalogProductSearchFilters
	Cursor  string
	Limit   int
}

func (request CatalogProductSearchRequest) Validate() error {
	if strings.TrimSpace(request.Query) == "" {
		return errors.New("catalog query is required")
	}
	if request.Limit <= 0 || request.Limit > CatalogSearchMaxLimit {
		return errors.New("catalog search limit is invalid")
	}
	if request.Filters.Price != nil {
		price := request.Filters.Price
		if price.MinimumMinor == nil && price.MaximumMinor == nil {
			return errors.New("catalog price filter is empty")
		}
		if price.MinimumMinor != nil && *price.MinimumMinor < 0 {
			return errors.New("catalog minimum price is negative")
		}
		if price.MaximumMinor != nil && *price.MaximumMinor < 0 {
			return errors.New("catalog maximum price is negative")
		}
		if price.MinimumMinor != nil && price.MaximumMinor != nil &&
			*price.MinimumMinor > *price.MaximumMinor {
			return errors.New("catalog price range is inverted")
		}
		if strings.TrimSpace(request.Context.Currency) == "" {
			return errors.New("catalog price currency context is required")
		}
	}
	for _, attribute := range request.Filters.Attributes {
		if strings.TrimSpace(attribute.Name) == "" || len(attribute.Values) == 0 {
			return errors.New("catalog attribute filter is invalid")
		}
		for _, value := range attribute.Values {
			if strings.TrimSpace(value) == "" {
				return errors.New("catalog attribute filter value is invalid")
			}
		}
	}
	return nil
}

type CatalogOutcome string

const (
	CatalogOutcomeSuccess       CatalogOutcome = "SUCCESS"
	CatalogOutcomeBusinessError CatalogOutcome = "BUSINESS_ERROR"
)

type CatalogProductSearchResult struct {
	Partial         bool
	RejectedCount   int
	RawCount        int
	Provider        string
	ProtocolVersion string
	Outcome         CatalogOutcome
	Products        []CatalogProductObservation
	Messages        []CatalogProviderMessage
	Pagination      CatalogPagination
	AppliedFilters  CatalogAppliedFilterProofV2
}

// CatalogHardFilterSetV2 is the exact normalized set of request-level hard
// filters whose application the provider attested by returning a successful
// response without an ignored/unsupported-filter message. It never invents
// product-level condition, destination, or attribute facts.
type CatalogHardFilterSetV2 struct {
	Available      *bool                    `json:"available,omitempty"`
	ShipsToCountry string                   `json:"shipsToCountry,omitempty"`
	Categories     []string                 `json:"categories"`
	Conditions     []string                 `json:"conditions"`
	Attributes     []CatalogAttributeFilter `json:"attributes"`
}

// CatalogAppliedFilterProofV2 is response-scoped provider evidence. Verified
// means only that the exact request reached a successful provider call and no
// response message said a filter was ignored or unsupported. Its lifetime is
// later bounded by the owning catalog observation's evidence lease.
type CatalogAppliedFilterProofV2 struct {
	Verified                   bool                   `json:"verified"`
	Provider                   string                 `json:"provider"`
	ProtocolVersion            string                 `json:"protocolVersion"`
	EvidenceKind               string                 `json:"evidenceKind"`
	EvidenceRef                string                 `json:"evidenceRef"`
	EvidenceHash               string                 `json:"evidenceHash"`
	RequestHash                string                 `json:"requestHash"`
	ProviderCapability         string                 `json:"providerCapability"`
	ProviderCapabilityVersion  string                 `json:"providerCapabilityVersion"`
	Filters                    CatalogHardFilterSetV2 `json:"filters"`
	Price                      *CatalogPriceFilter    `json:"price,omitempty"`
	PriceCurrency              string                 `json:"priceCurrency,omitempty"`
	DisqualifyingMessageHashes []string               `json:"disqualifyingMessageHashes"`
}

// CatalogPhysicalEligibilityProofV2 is product-specific Candidate evidence.
// It is derived either from a reviewed physical-product category or, when the
// provider omits categories, from an exact Shopify Global attestation that
// available=true, ships_to=<country>, and condition=new were all applied.
// The latter is discovery evidence only: it is not address-level shipping,
// inventory, merchant-authored condition, or checkout authority.
type CatalogPhysicalEligibilityProofV2 struct {
	Verified              bool   `json:"verified"`
	PolicyVersion         string `json:"policyVersion"`
	Category              string `json:"category,omitempty"`
	AppliedAvailable      *bool  `json:"appliedAvailable,omitempty"`
	AppliedShipsToCountry string `json:"appliedShipsToCountry,omitempty"`
	AppliedCondition      string `json:"appliedCondition,omitempty"`
	EvidenceKind          string `json:"evidenceKind"`
	EvidenceRef           string `json:"evidenceRef"`
	EvidenceHash          string `json:"evidenceHash"`
}

type CatalogAdmissionHardFilterProofV2 struct {
	AppliedFilters      CatalogAppliedFilterProofV2       `json:"appliedFilters"`
	PhysicalEligibility CatalogPhysicalEligibilityProofV2 `json:"physicalEligibility"`
}

// NewCatalogAppliedFilterProofV2 creates a provider-applied attestation from
// facts available at the transport boundary. Absence of a disqualifying
// message is meaningful only together with a successful provider envelope;
// callers must not invoke this constructor for failed/business-error calls.
func NewCatalogAppliedFilterProofV2(
	request CatalogProductSearchRequest,
	provider string,
	protocolVersion string,
	evidenceRef string,
	providerCapability string,
	providerCapabilityVersion string,
	messages []CatalogProviderMessage,
) (CatalogAppliedFilterProofV2, error) {
	if err := request.Validate(); err != nil ||
		!validCatalogEvidenceRefV2(evidenceRef) ||
		strings.TrimSpace(providerCapability) == "" ||
		strings.TrimSpace(providerCapabilityVersion) == "" ||
		strings.TrimSpace(provider) == "" || strings.TrimSpace(protocolVersion) == "" {
		return CatalogAppliedFilterProofV2{}, errors.New("catalog filter proof input is invalid")
	}
	filters := normalizeCatalogHardFilterSetV2(request.Filters)
	requestHash, err := shareddomain.CanonicalJSONHash(normalizedCatalogProofRequestV2(request))
	if err != nil {
		return CatalogAppliedFilterProofV2{}, err
	}
	disqualifying := catalogDisqualifyingFilterMessageHashesV2(messages)
	price, priceCurrency := catalogAppliedPriceProofV2(request)
	proof := CatalogAppliedFilterProofV2{
		Verified: len(disqualifying) == 0,
		Provider: strings.TrimSpace(provider), ProtocolVersion: strings.TrimSpace(protocolVersion),
		EvidenceKind: CatalogAppliedFilterEvidenceV2,
		EvidenceRef:  strings.TrimSpace(evidenceRef), RequestHash: requestHash,
		ProviderCapability:        strings.TrimSpace(providerCapability),
		ProviderCapabilityVersion: strings.TrimSpace(providerCapabilityVersion),
		Filters:                   filters, Price: price, PriceCurrency: priceCurrency,
		DisqualifyingMessageHashes: disqualifying,
	}
	proof.EvidenceHash, err = catalogAppliedFilterProofHashV2(proof)
	if err != nil {
		return CatalogAppliedFilterProofV2{}, err
	}
	return proof, nil
}

func (proof CatalogAppliedFilterProofV2) ValidateForRequest(
	request CatalogProductSearchRequest,
	provider string,
	protocolVersion string,
) error {
	if err := request.Validate(); err != nil || !proof.Verified ||
		proof.Provider != strings.TrimSpace(provider) ||
		proof.ProtocolVersion != strings.TrimSpace(protocolVersion) ||
		proof.EvidenceKind != CatalogAppliedFilterEvidenceV2 ||
		strings.TrimSpace(proof.ProviderCapability) == "" ||
		proof.ProviderCapabilityVersion != strings.TrimSpace(protocolVersion) ||
		!validCatalogEvidenceRefV2(proof.EvidenceRef) ||
		!validCatalogEvidenceHashV2(proof.EvidenceHash) ||
		!validCatalogAppliedPriceProofV2(proof.Price, proof.PriceCurrency) ||
		len(proof.DisqualifyingMessageHashes) != 0 ||
		!reflect.DeepEqual(proof.Filters, normalizeCatalogHardFilterSetV2(request.Filters)) {
		return errors.New("catalog applied filter proof is invalid")
	}
	expectedPrice, expectedCurrency := catalogAppliedPriceProofV2(request)
	if !reflect.DeepEqual(proof.Price, expectedPrice) ||
		proof.PriceCurrency != expectedCurrency {
		return errors.New("catalog applied price proof is invalid")
	}
	if proof.Provider == CatalogProviderShopifyGlobalV2 && catalogRequestUsesExtensionFiltersV2(request) &&
		proof.ProviderCapability != CatalogShopifyGlobalCapabilityV2 {
		return errors.New("catalog provider filter capability is invalid")
	}
	requestHash, err := shareddomain.CanonicalJSONHash(normalizedCatalogProofRequestV2(request))
	if err != nil || proof.RequestHash != requestHash {
		return errors.New("catalog filter request hash mismatch")
	}
	expected, err := catalogAppliedFilterProofHashV2(proof)
	if err != nil || proof.EvidenceHash != expected {
		return errors.New("catalog applied filter proof hash mismatch")
	}
	return nil
}

func (proof CatalogAppliedFilterProofV2) validateShape() bool {
	if !proof.Verified || strings.TrimSpace(proof.Provider) == "" ||
		strings.TrimSpace(proof.ProtocolVersion) == "" ||
		proof.EvidenceKind != CatalogAppliedFilterEvidenceV2 ||
		strings.TrimSpace(proof.ProviderCapability) == "" ||
		proof.ProviderCapabilityVersion != proof.ProtocolVersion ||
		!validCatalogEvidenceRefV2(proof.EvidenceRef) ||
		!validCatalogEvidenceHashV2(proof.EvidenceHash) ||
		!validCatalogEvidenceHashV2(proof.RequestHash) ||
		!validCatalogAppliedPriceProofV2(proof.Price, proof.PriceCurrency) ||
		len(proof.DisqualifyingMessageHashes) != 0 {
		return false
	}
	expected, err := catalogAppliedFilterProofHashV2(proof)
	return err == nil && proof.EvidenceHash == expected
}

func catalogAppliedPriceProofV2(
	request CatalogProductSearchRequest,
) (*CatalogPriceFilter, string) {
	if request.Filters.Price == nil {
		return nil, ""
	}
	price := *request.Filters.Price
	price.MinimumMinor = cloneCatalogMinorV2(price.MinimumMinor)
	price.MaximumMinor = cloneCatalogMinorV2(price.MaximumMinor)
	return &price, strings.ToUpper(strings.TrimSpace(request.Context.Currency))
}

func validCatalogAppliedPriceProofV2(
	price *CatalogPriceFilter,
	currency string,
) bool {
	if price == nil {
		return currency == ""
	}
	if len(currency) != 3 || currency != strings.ToUpper(currency) ||
		price.MinimumMinor == nil && price.MaximumMinor == nil ||
		price.MinimumMinor != nil && *price.MinimumMinor < 0 ||
		price.MaximumMinor != nil && *price.MaximumMinor < 0 ||
		price.MinimumMinor != nil && price.MaximumMinor != nil &&
			*price.MinimumMinor > *price.MaximumMinor {
		return false
	}
	return true
}

type CatalogPagination struct {
	Cursor      string
	HasNextPage bool
	TotalCount  *int
}

type CatalogDescription struct {
	Plain    string
	HTML     string
	Markdown string
}

type CatalogMedia struct {
	Type    string
	URL     string
	AltText string
	Width   *int
	Height  *int
}

type CatalogMoney struct {
	AmountMinor int64
	Currency    string
}

type CatalogPriceRange struct {
	Minimum CatalogMoney
	Maximum CatalogMoney
}

type CatalogCategory struct {
	Value    string
	Taxonomy string
}

type CatalogSeller struct {
	ID     string
	Name   string
	Domain string
	URL    string
}

type CatalogAvailability struct {
	Available  *bool
	Status     string
	RunningLow *bool
}

type CatalogPreviewVariant struct {
	ID                     string
	Title                  string
	Description            CatalogDescription
	URL                    string
	Price                  CatalogMoney
	Availability           CatalogAvailability
	Media                  []CatalogMedia
	Seller                 *CatalogSeller
	NativeCheckoutEligible bool
}

type CatalogLocatorKind string

const (
	CatalogLocatorProductURL      CatalogLocatorKind = "PRODUCT_URL"
	CatalogLocatorMerchantVariant CatalogLocatorKind = "MERCHANT_VARIANT"
)

type CatalogProductURLLocator struct {
	CanonicalURL string
}

type CatalogMerchantVariantLocator struct {
	VariantID    string
	SellerDomain string
	SellerID     string
}

// CatalogProductLocator is a tagged union. Exactly one locator payload must be
// set, and it must agree with Kind.
type CatalogProductLocator struct {
	Kind            CatalogLocatorKind
	ProductURL      *CatalogProductURLLocator
	MerchantVariant *CatalogMerchantVariantLocator
}

func (locator CatalogProductLocator) Validate() error {
	switch locator.Kind {
	case CatalogLocatorProductURL:
		if locator.ProductURL == nil || locator.MerchantVariant != nil ||
			!validHTTPSURL(locator.ProductURL.CanonicalURL) {
			return errors.New("catalog product URL locator is invalid")
		}
	case CatalogLocatorMerchantVariant:
		if locator.MerchantVariant == nil || locator.ProductURL != nil ||
			strings.TrimSpace(locator.MerchantVariant.VariantID) == "" ||
			!validSellerDomain(locator.MerchantVariant.SellerDomain) {
			return errors.New("catalog merchant variant locator is invalid")
		}
	default:
		return errors.New("catalog locator kind is invalid")
	}
	return nil
}

func validHTTPSURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" &&
		parsed.User == nil
}

func validSellerDomain(raw string) bool {
	domain := strings.TrimSpace(raw)
	if domain == "" || strings.ContainsAny(domain, "/?#@") {
		return false
	}
	parsed, err := url.Parse("https://" + domain)
	return err == nil && parsed.Hostname() != "" && parsed.Port() == ""
}

type CatalogProductObservation struct {
	SearchPolicy        string
	DiscoveryRoute      string
	AmazonObservation   *researchdomain.AmazonObservation
	ExternalObservation *researchdomain.ExternalProductObservation
	SourceProductRef    *researchdomain.SourceProductRef
	VariantObservation  *researchdomain.VariantObservation
	ProviderProductID   string
	Handle              string
	Title               string
	Description         CatalogDescription
	Locator             *CatalogProductLocator
	PreviewVariant      *CatalogPreviewVariant
	PriceRange          CatalogPriceRange
	Media               []CatalogMedia
	Categories          []CatalogCategory
	ProviderOrder       int
}

// CatalogProviderMessage is response-scoped, untrusted provider content. It
// may be rendered only after the interface layer sanitizes content and URLs;
// it must not be persisted as catalog cache data.
type CatalogProviderMessage struct {
	Type         string
	Code         string
	Path         string
	SubjectKind  string
	SubjectRef   string
	ContentType  string
	Content      string
	Severity     string
	Presentation string
	ImageURL     string
	URL          string
}

type normalizedCatalogProofRequestV2Shape struct {
	Query   string                 `json:"query"`
	Context CatalogBuyerContext    `json:"context"`
	Filters CatalogHardFilterSetV2 `json:"filters"`
	Price   *CatalogPriceFilter    `json:"price,omitempty"`
	Cursor  string                 `json:"cursor,omitempty"`
	Limit   int                    `json:"limit"`
}

func normalizedCatalogProofRequestV2(
	request CatalogProductSearchRequest,
) normalizedCatalogProofRequestV2Shape {
	context := CatalogBuyerContext{
		Country:    strings.ToUpper(strings.TrimSpace(request.Context.Country)),
		Region:     strings.TrimSpace(request.Context.Region),
		PostalCode: strings.TrimSpace(request.Context.PostalCode),
		Language:   strings.ToLower(strings.TrimSpace(request.Context.Language)),
		Currency:   strings.ToUpper(strings.TrimSpace(request.Context.Currency)),
		Intent:     strings.TrimSpace(request.Context.Intent),
	}
	var price *CatalogPriceFilter
	if request.Filters.Price != nil {
		cloned := *request.Filters.Price
		cloned.MinimumMinor = cloneCatalogMinorV2(cloned.MinimumMinor)
		cloned.MaximumMinor = cloneCatalogMinorV2(cloned.MaximumMinor)
		price = &cloned
	}
	return normalizedCatalogProofRequestV2Shape{
		Query: strings.TrimSpace(request.Query), Context: context,
		Filters: normalizeCatalogHardFilterSetV2(request.Filters), Price: price,
		Cursor: strings.TrimSpace(request.Cursor), Limit: request.Limit,
	}
}

func normalizeCatalogHardFilterSetV2(
	filters CatalogProductSearchFilters,
) CatalogHardFilterSetV2 {
	var available *bool
	if filters.Available != nil {
		value := *filters.Available
		available = &value
	}
	country := ""
	if filters.ShipsTo != nil {
		country = strings.ToUpper(strings.TrimSpace(filters.ShipsTo.Country))
	}
	attributes := make([]CatalogAttributeFilter, 0, len(filters.Attributes))
	for _, attribute := range filters.Attributes {
		name := normalizeCatalogFilterTokenV2(attribute.Name)
		values := normalizeCatalogFilterValuesV2(attribute.Values)
		if name != "" && len(values) > 0 {
			attributes = append(attributes, CatalogAttributeFilter{Name: name, Values: values})
		}
	}
	sort.Slice(attributes, func(left, right int) bool {
		if attributes[left].Name != attributes[right].Name {
			return attributes[left].Name < attributes[right].Name
		}
		return strings.Join(attributes[left].Values, "\x00") <
			strings.Join(attributes[right].Values, "\x00")
	})
	if attributes == nil {
		attributes = []CatalogAttributeFilter{}
	}
	return CatalogHardFilterSetV2{
		Available: available, ShipsToCountry: country,
		Categories: normalizeCatalogFilterValuesV2(filters.Categories),
		Conditions: normalizeCatalogFilterValuesV2(filters.Conditions),
		Attributes: attributes,
	}
}

func normalizeCatalogFilterValuesV2(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeCatalogFilterTokenV2(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	if normalized == nil {
		normalized = []string{}
	}
	return normalized
}

func normalizeCatalogFilterTokenV2(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func catalogAppliedFilterProofHashV2(
	proof CatalogAppliedFilterProofV2,
) (string, error) {
	proof.EvidenceHash = ""
	return shareddomain.CanonicalJSONHash(proof)
}

func catalogDisqualifyingFilterMessageHashesV2(
	messages []CatalogProviderMessage,
) []string {
	hashes := make([]string, 0)
	for _, message := range messages {
		if !catalogMessageDisqualifiesFilterProofV2(message) {
			continue
		}
		hash, err := shareddomain.CanonicalJSONHash(struct {
			Type        string `json:"type"`
			Code        string `json:"code"`
			Path        string `json:"path"`
			Severity    string `json:"severity"`
			ContentHash string `json:"contentHash"`
		}{
			Type: strings.TrimSpace(message.Type), Code: strings.TrimSpace(message.Code),
			Path: strings.TrimSpace(message.Path), Severity: strings.TrimSpace(message.Severity),
			ContentHash: catalogProviderContentHashV2(message.Content),
		})
		if err == nil {
			hashes = append(hashes, hash)
		}
	}
	sort.Strings(hashes)
	return hashes
}

func catalogMessageDisqualifiesFilterProofV2(message CatalogProviderMessage) bool {
	typeValue := normalizeCatalogFilterTokenV2(message.Type)
	code := normalizeCatalogFilterTokenV2(message.Code)
	path := normalizeCatalogFilterTokenV2(message.Path)
	content := normalizeCatalogFilterTokenV2(message.Content)
	combined := strings.Join([]string{code, path, content}, " ")
	unsupported := []string{
		"unsupported", "not supported", "ignored", "not applied",
		"unrecognized filter", "unknown filter", "invalid filter",
		"filter not enforced", "filter unavailable",
		"not_supported", "not-applied", "filter_ignored",
		"filter_not_enforced", "unsupported_filter", "invalid_filter",
	}
	for _, signal := range unsupported {
		if strings.Contains(combined, signal) {
			return true
		}
	}
	// Shopify also confirms a filter in words: an info notice whose code says the
	// filter WAS applied ("price_filter_applied": "Price filtering was applied on
	// a USD basis; returned prices are contextualized to the buyer's market and
	// may differ."). That is the opposite of a disqualification. Reading it as one
	// failed every US Round that had a budget, before any product was looked at.
	// The notice stays a provider message the screen may show; the plan's price
	// bounds are checked again on every normalised observation, so a price that
	// differs after contextualisation is still held to the budget.
	if typeValue == "info" && strings.HasSuffix(code, "filter_applied") {
		return false
	}
	return (typeValue == "warning" || typeValue == "info") &&
		strings.Contains(path, "filter")
}

func catalogRequestUsesExtensionFiltersV2(request CatalogProductSearchRequest) bool {
	filters := request.Filters
	return filters.Available != nil || filters.ShipsTo != nil ||
		len(filters.Conditions) > 0 || len(filters.Attributes) > 0
}

func catalogProviderContentHashV2(content string) string {
	hash, err := shareddomain.CanonicalJSONHash(strings.TrimSpace(content))
	if err != nil {
		return ""
	}
	return hash
}

var catalogPhysicalCategoriesV2 = map[string]struct{}{
	"apparel": {}, "baby & toddler": {}, "beauty": {}, "clothing": {},
	"consumer electronics": {}, "electronics": {}, "footwear": {},
	"furniture": {}, "hardware": {}, "health & beauty": {},
	"home & garden": {}, "jewelry": {}, "luggage & bags": {},
	"office supplies": {}, "pet supplies": {}, "shoes": {},
	"sporting goods": {}, "toys & games": {}, "vehicles & parts": {},
}

func newCatalogPhysicalEligibilityProofV2(
	applied CatalogAppliedFilterProofV2,
	product CatalogProductObservation,
) (CatalogPhysicalEligibilityProofV2, bool, error) {
	if !applied.validateShape() || strings.TrimSpace(product.ProviderProductID) == "" {
		return CatalogPhysicalEligibilityProofV2{}, false, errors.New("physical proof input is invalid")
	}
	evidence, ok := catalogPhysicalEligibilityEvidenceV2(applied, product)
	if !ok {
		return CatalogPhysicalEligibilityProofV2{}, false, nil
	}
	productKeyHash, err := shareddomain.CanonicalJSONHash(
		strings.TrimSpace(product.ProviderProductID),
	)
	if err != nil {
		return CatalogPhysicalEligibilityProofV2{}, false, err
	}
	proof := CatalogPhysicalEligibilityProofV2{
		Verified:              true,
		PolicyVersion:         CatalogPhysicalEligibilityPolicyVersionV2,
		Category:              evidence.category,
		AppliedAvailable:      evidence.appliedAvailable,
		AppliedShipsToCountry: evidence.appliedShipsToCountry,
		AppliedCondition:      evidence.appliedCondition,
		EvidenceKind:          evidence.kind,
		EvidenceRef: applied.EvidenceRef + ":physical-eligibility:" +
			strings.TrimPrefix(productKeyHash, "0x")[:16],
	}
	proof.EvidenceHash, err = catalogPhysicalEligibilityProofHashV2(
		proof, applied.EvidenceHash, strings.TrimSpace(product.ProviderProductID),
	)
	if err != nil {
		return CatalogPhysicalEligibilityProofV2{}, false, err
	}
	return proof, true, nil
}

type catalogPhysicalEligibilityEvidenceDataV2 struct {
	kind                  string
	category              string
	appliedAvailable      *bool
	appliedShipsToCountry string
	appliedCondition      string
}

func catalogPhysicalEligibilityEvidenceV2(
	applied CatalogAppliedFilterProofV2,
	product CatalogProductObservation,
) (catalogPhysicalEligibilityEvidenceDataV2, bool) {
	category, evidenceKind := catalogPhysicalCategoryEvidenceV2(applied, product)
	if category != "" {
		return catalogPhysicalEligibilityEvidenceDataV2{
			kind: evidenceKind, category: category,
		}, true
	}
	if !catalogProviderAppliedShippableNewV2(applied, product) {
		return catalogPhysicalEligibilityEvidenceDataV2{}, false
	}
	available := true
	return catalogPhysicalEligibilityEvidenceDataV2{
		kind:                  CatalogPhysicalShippableNewMerchandiseEvidenceV2,
		appliedAvailable:      &available,
		appliedShipsToCountry: applied.Filters.ShipsToCountry,
		appliedCondition:      "new",
	}, true
}

// catalogProviderAppliedShippableNewV2 is deliberately narrower than a
// general provider-filter proof. It accepts only the Shopify Global capability
// and only when the provider omitted category data entirely. A present but
// non-physical category is contradictory evidence and remains fail-closed.
func catalogProviderAppliedShippableNewV2(
	applied CatalogAppliedFilterProofV2,
	product CatalogProductObservation,
) bool {
	if applied.Provider != CatalogProviderShopifyGlobalV2 ||
		applied.ProviderCapability != CatalogShopifyGlobalCapabilityV2 ||
		applied.ProviderCapabilityVersion != applied.ProtocolVersion ||
		len(applied.Filters.Categories) != 0 || len(product.Categories) != 0 ||
		applied.Filters.Available == nil || !*applied.Filters.Available ||
		!validCatalogShippingCountryV2(applied.Filters.ShipsToCountry) ||
		len(applied.Filters.Conditions) != 1 ||
		applied.Filters.Conditions[0] != "new" {
		return false
	}
	return true
}

func validCatalogShippingCountryV2(country string) bool {
	if len(country) != 2 || country != strings.ToUpper(country) {
		return false
	}
	for _, current := range country {
		if current < 'A' || current > 'Z' {
			return false
		}
	}
	return true
}

func catalogPhysicalCategoryEvidenceV2(
	applied CatalogAppliedFilterProofV2,
	product CatalogProductObservation,
) (string, string) {
	if len(applied.Filters.Categories) > 0 {
		allPhysical := true
		for _, category := range applied.Filters.Categories {
			if _, ok := catalogPhysicalCategoriesV2[category]; !ok {
				allPhysical = false
				break
			}
		}
		if allPhysical {
			return applied.Filters.Categories[0], CatalogPhysicalFilterEvidenceV2
		}
	}
	values := make([]string, 0, len(product.Categories))
	for _, category := range product.Categories {
		value := normalizeCatalogFilterTokenV2(category.Value)
		if _, ok := catalogPhysicalCategoriesV2[value]; ok {
			values = append(values, value)
		}
	}
	values = normalizeCatalogFilterValuesV2(values)
	if len(values) == 0 {
		return "", ""
	}
	return values[0], CatalogPhysicalProductEvidenceV2
}

func catalogPhysicalEligibilityProofHashV2(
	proof CatalogPhysicalEligibilityProofV2,
	appliedEvidenceHash string,
	providerProductID string,
) (string, error) {
	proof.EvidenceHash = ""
	return shareddomain.CanonicalJSONHash(struct {
		Proof               CatalogPhysicalEligibilityProofV2 `json:"proof"`
		AppliedEvidenceHash string                            `json:"appliedEvidenceHash"`
		ProviderProductID   string                            `json:"providerProductId"`
	}{proof, appliedEvidenceHash, providerProductID})
}

func validCatalogPhysicalEligibilityProofV2(
	applied CatalogAppliedFilterProofV2,
	product CatalogProductObservation,
	proof CatalogPhysicalEligibilityProofV2,
) bool {
	expected, ok, err := newCatalogPhysicalEligibilityProofV2(applied, product)
	return err == nil && ok && reflect.DeepEqual(expected, proof)
}

func validCatalogEvidenceRefV2(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 512 && !validHTTPSURL(value) &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func validCatalogEvidenceHashV2(value string) bool {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") {
		return false
	}
	for _, current := range value[2:] {
		if !((current >= '0' && current <= '9') ||
			(current >= 'a' && current <= 'f')) {
			return false
		}
	}
	return true
}

func cloneCatalogMinorV2(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type CatalogMediaLookupInput struct {
	CorrelationKey string
	Identifier     string
}

type CatalogMediaLookupRequest struct {
	Inputs                []CatalogMediaLookupInput
	Context               CatalogBuyerContext
	ProviderCallAdmission CatalogProviderCallAdmissionV2 `json:"-"`
}

func (request CatalogMediaLookupRequest) Validate() error {
	if len(request.Inputs) == 0 || len(request.Inputs) > CatalogLookupMaxInputs {
		return errors.New("catalog lookup input count is invalid")
	}
	keys := make(map[string]struct{}, len(request.Inputs))
	for _, input := range request.Inputs {
		key := strings.TrimSpace(input.CorrelationKey)
		if key == "" || strings.TrimSpace(input.Identifier) == "" {
			return errors.New("catalog lookup input is invalid")
		}
		if _, exists := keys[key]; exists {
			return errors.New("catalog lookup correlation key is duplicated")
		}
		keys[key] = struct{}{}
	}
	return nil
}

type CatalogMediaMatch struct {
	CorrelationKey      string
	RequestedIdentifier string
	ProductID           string
	VariantID           string
	Match               string
	Media               []CatalogMedia
}

type CatalogMediaLookupResult struct {
	Provider          string
	ProtocolVersion   string
	ProviderCallCount int
	Outcome           CatalogOutcome
	Matches           []CatalogMediaMatch
	UnresolvedKeys    []string
	Messages          []CatalogProviderMessage
}

type CatalogOfferLookupInput struct {
	DraftID    string
	Identifier string
}

type CatalogOfferLookupRequest struct {
	Inputs                []CatalogOfferLookupInput
	Context               CatalogBuyerContext
	ProviderCallAdmission CatalogProviderCallAdmissionV2 `json:"-"`
}

func (request CatalogOfferLookupRequest) Validate() error {
	if len(request.Inputs) == 0 || len(request.Inputs) > CatalogLookupMaxInputs {
		return errors.New("catalog offer lookup input count is invalid")
	}
	seenDrafts := make(map[string]struct{}, len(request.Inputs))
	for _, input := range request.Inputs {
		draftID := strings.TrimSpace(input.DraftID)
		if draftID == "" || strings.TrimSpace(input.Identifier) == "" {
			return errors.New("catalog offer lookup input is invalid")
		}
		if _, exists := seenDrafts[draftID]; exists {
			return errors.New("catalog offer lookup draft is duplicated")
		}
		seenDrafts[draftID] = struct{}{}
	}
	if strings.TrimSpace(request.Context.Country) == "" ||
		strings.TrimSpace(request.Context.Currency) == "" {
		return errors.New("catalog offer lookup market context is invalid")
	}
	return nil
}

type CatalogOfferMatch struct {
	DraftID             string
	RequestedIdentifier string
	Match               string
	Product             CatalogProductObservation
	Variant             CatalogPreviewVariant
}

type CatalogOfferLookupResult struct {
	Provider          string
	ProtocolVersion   string
	ProviderCallCount int
	Outcome           CatalogOutcome
	Matches           []CatalogOfferMatch
	UnresolvedIDs     []string
	Messages          []CatalogProviderMessage
}
