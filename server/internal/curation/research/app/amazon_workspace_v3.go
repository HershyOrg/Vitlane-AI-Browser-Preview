package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"strconv"
	"strings"
	"time"
)

type AmazonRelationGrant struct {
	TokenHash      string
	UserID         string
	CurationID     string
	CandidateID    string
	AnchorASIN     string
	Variants       []AmazonVariantOption
	RelationStatus string
	Truncated      bool
	ObservedAt     time.Time
	ExpiresAt      time.Time
}
type AmazonRelationRepository interface {
	SaveAmazonRelation(context.Context, AmazonRelationGrant) error
	ReadAmazonRelation(context.Context, string, string, string, string) (AmazonRelationGrant, error)
}

func relationTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (service *LiveCatalogReviewServiceV2) amazonGrant(ctx context.Context, user, curation, candidate, token string) (AmazonRelationGrant, error) {
	repo, ok := service.workspace.(AmazonRelationRepository)
	if !ok || len(token) != 64 {
		return AmazonRelationGrant{}, fault.New(fault.InvalidInput, "AMAZON_VARIANT_PAGE_EXPIRED", true)
	}
	grant, err := repo.ReadAmazonRelation(ctx, user, curation, candidate, relationTokenHash(token))
	if err != nil {
		return grant, err
	}
	if !grant.ExpiresAt.After(service.clock.Now()) {
		return grant, fault.New(fault.InvalidInput, "AMAZON_VARIANT_PAGE_EXPIRED", true)
	}
	return grant, nil
}
func (service *LiveCatalogReviewServiceV2) browseAmazonVariants(ctx context.Context, user, curation string, candidate CatalogCandidateReferenceV2, cursor string) (LiveVariantReviewPageV2, error) {
	if service.amazon == nil {
		return LiveVariantReviewPageV2{}, fault.New(fault.ProviderUnavailable, "AMAZON_SOURCE_DISABLED", false)
	}
	repo, ok := service.workspace.(AmazonRelationRepository)
	if !ok {
		return LiveVariantReviewPageV2{}, fault.New(fault.InternalFailure, "AMAZON_RELATION_STORE_UNAVAILABLE", false)
	}
	var grant AmazonRelationGrant
	var token string
	var title string
	offset := 0
	if cursor != "" {
		var rawOffset string
		var found bool
		token, rawOffset, found = strings.Cut(cursor, ".")
		var err error
		offset, err = strconv.Atoi(rawOffset)
		if !found || err != nil || offset < 0 || offset%20 != 0 || offset > 40 {
			return LiveVariantReviewPageV2{}, fault.New(fault.InvalidInput, "AMAZON_VARIANT_PAGE_EXPIRED", true)
		}
		grant, err = service.amazonGrant(ctx, user, curation, candidate.CandidateID, token)
		if err != nil {
			return LiveVariantReviewPageV2{}, err
		}
	} else {
		ref := candidate.ProductRef()
		detail, err := service.amazon.LookupAmazon(ctx, researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: ref.Marketplace, ASIN: ref.AnchorASIN})
		if err != nil {
			return LiveVariantReviewPageV2{}, err
		}
		title = detail.Product.Title
		variants := detail.Variants
		found := false
		for _, v := range variants {
			if v.ASIN == ref.AnchorASIN {
				found = true
			}
		}
		if !found {
			variants = append([]AmazonVariantOption{{ASIN: ref.AnchorASIN, Labels: []LiveVariantOptionV2{}}}, variants...)
		}
		if len(variants) > 50 {
			variants = variants[:50]
			detail.Truncated = true
		}
		bytes := make([]byte, 32)
		if _, err = rand.Read(bytes); err != nil {
			return LiveVariantReviewPageV2{}, err
		}
		token = hex.EncodeToString(bytes)
		grant = AmazonRelationGrant{TokenHash: relationTokenHash(token), UserID: user, CurationID: curation, CandidateID: candidate.CandidateID, AnchorASIN: ref.AnchorASIN, Variants: variants, RelationStatus: detail.RelationStatus, Truncated: detail.Truncated, ObservedAt: service.clock.Now(), ExpiresAt: service.clock.Now().Add(15 * time.Minute)}
		if err = repo.SaveAmazonRelation(ctx, grant); err != nil {
			return LiveVariantReviewPageV2{}, err
		}
	}
	end := min(offset+20, len(grant.Variants))
	if offset >= len(grant.Variants) {
		return LiveVariantReviewPageV2{}, fault.New(fault.InvalidInput, "AMAZON_VARIANT_PAGE_EXPIRED", true)
	}
	rows := []LiveVariantReviewRowV2{}
	for _, option := range grant.Variants[offset:end] {
		labels := []string{}
		for _, label := range option.Labels {
			labels = append(labels, label.Value)
		}
		name := strings.Join(labels, " · ")
		if name == "" {
			name = option.ASIN
		}
		rows = append(rows, LiveVariantReviewRowV2{VariantID: option.ASIN, Title: name, SelectedOptions: option.Labels, Available: option.Available == nil || *option.Available, PriceUnknown: true})
	}
	next := ""
	if end < len(grant.Variants) {
		next = token + "." + strconv.Itoa(end)
	}
	return LiveVariantReviewPageV2{Source: researchdomain.SourceAmazon, CandidateID: candidate.CandidateID, ProductTitle: title, Rows: rows, PageSize: 20, HasNext: next != "", NextCursor: next, ObservedAt: grant.ObservedAt.Format(time.RFC3339Nano), RelationToken: token, RelationStatus: grant.RelationStatus, Truncated: grant.Truncated}, nil
}
func (service *LiveCatalogReviewServiceV2) ResolveAmazonVariant(ctx context.Context, user, curation, candidateID, token string, ref researchdomain.SourceVariantRef) (CatalogProductObservation, error) {
	if ref.Validate() != nil || ref.Source != researchdomain.SourceAmazon {
		return CatalogProductObservation{}, fault.New(fault.InvalidInput, "AMAZON_REFERENCE_INVALID", false)
	}
	candidate, err := service.resolveCandidateV2(ctx, user, curation, candidateID)
	if err != nil {
		return CatalogProductObservation{}, err
	}
	if candidate.ProductRef().Source != researchdomain.SourceAmazon {
		return CatalogProductObservation{}, fault.New(fault.InvalidInput, "AMAZON_REFERENCE_INVALID", false)
	}
	grant, err := service.amazonGrant(ctx, user, curation, candidateID, token)
	if err != nil {
		return CatalogProductObservation{}, err
	}
	allowed := false
	for _, option := range grant.Variants {
		if option.ASIN == ref.ASIN {
			allowed = true
		}
	}
	if !allowed || service.amazon == nil {
		return CatalogProductObservation{}, fault.New(fault.InvalidInput, "AMAZON_VARIANT_RELATION_INVALID", false)
	}
	detail, err := service.amazon.LookupAmazon(ctx, ref)
	if err != nil {
		return CatalogProductObservation{}, err
	}
	sourceRef := candidate.ProductRef()
	detail.Product.SourceProductRef = &sourceRef
	detail.Product.ProviderProductID = candidate.CandidateID
	return detail.Product, nil
}
func (service *LiveCatalogReviewServiceV2) ResolveExternalLink(ctx context.Context, user, curation, candidateID string) (string, error) {
	candidate, err := service.resolveCandidateV2(ctx, user, curation, candidateID)
	if err != nil {
		return "", err
	}
	if candidate.ProductRef().Source != researchdomain.SourceAmazon {
		return "", fault.New(fault.InvalidInput, "EXTERNAL_SOURCE_REQUIRED", false)
	}
	asin := candidate.ProductRef().AnchorASIN
	stored, err := service.workspace.LoadCatalogWorkspaceStateV2(ctx, user, curation)
	if err != nil {
		return "", err
	}
	for _, config := range stored.Configurations {
		if config.CandidateID == candidateID {
			asin = config.VariantID
			break
		}
	}
	return (researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: asin}).ExternalURL()
}

// AmazonObservationRepository stores the saved, dated display record of the
// selected ASIN so a card can render from the database when the provider is off
// or out of quota, and so the server can fill an account list snapshot without a
// client value (ADR-0077).
type AmazonObservationRepository interface {
	SaveAmazonObservation(context.Context, string, string, string, researchdomain.AmazonObservation) error
}

// CatalogProductFromAmazonObservation renders the saved record. It never claims
// a fresh price: the observation time travels with it.
func CatalogProductFromAmazonObservation(candidate CatalogCandidateReferenceV2) (CatalogProductObservation, bool) {
	observation := candidate.AmazonObservation
	if observation == nil || observation.Validate() != nil {
		return CatalogProductObservation{}, false
	}
	ref := candidate.ProductRef()
	locator := candidate.Locator
	product := CatalogProductObservation{
		ProviderProductID: candidate.CandidateID, SourceProductRef: &ref, Locator: &locator,
		AmazonObservation: observation, Title: observation.Title,
		ProviderOrder: candidate.DisplayOrder,
		VariantObservation: &researchdomain.VariantObservation{
			ObservationID: "amazon-saved:" + observation.VariantRef.ASIN,
			VariantRef:    observation.VariantRef, Price: observation.Price,
			Seller: researchdomain.ObservedSeller{Kind: "UNKNOWN"}, Availability: "UNKNOWN",
			DeliveryEligibility: "UNCONFIRMED", PurchaseRoute: "EXTERNAL",
			ProductURL: observation.ProductURL, ObservedAt: observation.ObservedAt,
			RefreshAfter: observation.ObservedAt.Add(15 * time.Minute),
		},
	}
	if observation.Price.Kind == "OBSERVED" && observation.Price.AmountMinor != nil {
		product.PriceRange = CatalogPriceRange{
			Minimum: CatalogMoney{AmountMinor: *observation.Price.AmountMinor, Currency: observation.Price.Currency},
			Maximum: CatalogMoney{AmountMinor: *observation.Price.AmountMinor, Currency: observation.Price.Currency},
		}
	}
	return product, true
}

// saveAmazonObservation keeps the minimal record of what the provider just
// showed. A failure never fails the read: the card already has its answer.
func (service *LiveCatalogReviewServiceV2) saveAmazonObservation(ctx context.Context, user, curation, candidate string, product CatalogProductObservation) {
	repository, ok := service.workspace.(AmazonObservationRepository)
	if !ok || product.VariantObservation == nil {
		return
	}
	observation, err := researchdomain.NewAmazonObservation(*product.VariantObservation, product.Title, service.clock.Now())
	if err != nil {
		return
	}
	_ = repository.SaveAmazonObservation(ctx, user, curation, candidate, observation)
}

func (service *LiveCatalogReviewServiceV2) hydrateAmazonCandidate(ctx context.Context, input CatalogResearchHydrationInputV2) (CatalogWorkspaceViewV2, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	candidate, err := service.resolveCandidateV2(ctx, input.UserID, input.CurationID, input.CandidateID)
	if err != nil {
		return CatalogWorkspaceViewV2{}, err
	}
	if candidate.PlanTargetID != input.TargetID || candidate.ProductRef().Source != researchdomain.SourceAmazon {
		return CatalogWorkspaceViewV2{}, fault.New(fault.InvalidInput, "AMAZON_REFERENCE_INVALID", false)
	}
	stored, err := service.workspace.LoadCatalogWorkspaceStateV2(ctx, input.UserID, input.CurationID)
	if err != nil {
		return CatalogWorkspaceViewV2{}, err
	}
	ref := candidate.ProductRef()
	asin := ref.AnchorASIN
	for _, config := range stored.Configurations {
		if config.CandidateID == candidate.CandidateID {
			asin = config.VariantID
		}
	}
	product := CatalogProductObservation{ProviderProductID: candidate.CandidateID, SourceProductRef: &ref, Locator: &candidate.Locator}
	hydration := CatalogCandidateHydrationV2{Status: CatalogCandidateHydrationUnresolvedV2, ReasonCode: "AMAZON_SOURCE_DISABLED"}
	// The saved record answers first, so a card shows its title and the time it
	// was seen even when the provider is off or out of quota.
	if saved, ok := CatalogProductFromAmazonObservation(candidate); ok && saved.AmazonObservation.VariantRef.ASIN == asin {
		product = saved
		hydration = CatalogCandidateHydrationV2{Status: CatalogCandidateHydrationReadyV2}
	}
	if service.amazon != nil {
		detail, e := service.amazon.LookupAmazon(ctx, researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: asin})
		if e == nil {
			product = detail.Product
			product.ProviderProductID = candidate.CandidateID
			product.SourceProductRef = &ref
			hydration = CatalogCandidateHydrationV2{Status: CatalogCandidateHydrationReadyV2}
			service.saveAmazonObservation(ctx, input.UserID, input.CurationID, candidate.CandidateID, detail.Product)
		} else if product.AmazonObservation == nil {
			hydration.ReasonCode = amazonFailureReason(e)
			hydration.Retryable = !AmazonAdmissionUnavailable(hydration.ReasonCode)
		}
	}
	metadata := CatalogPoolMetadataV2{TargetID: input.TargetID}
	for _, pool := range stored.Pools {
		if pool.TargetID == input.TargetID {
			metadata = pool
		}
	}
	pool := CatalogWorkspacePoolV2{Metadata: metadata, Products: []CatalogProductObservation{}, HiddenProducts: []CatalogProductObservation{}, Assessments: map[string]LiveCandidateAssessmentV2{candidate.CandidateID: candidate.Assessment}, Hydrations: map[string]CatalogCandidateHydrationV2{candidate.CandidateID: hydration}, Messages: []CatalogProviderMessage{}}
	if candidate.Visible {
		pool.Products = append(pool.Products, product)
	} else {
		pool.HiddenProducts = append(pool.HiddenProducts, product)
	}
	return CatalogWorkspaceViewV2{Pools: []CatalogWorkspacePoolV2{pool}, Messages: []CatalogProviderMessage{}, Configurations: []CatalogWorkspaceConfigurationViewV2{}, Interactions: []CatalogVariantInteractionV2{}}, nil
}
