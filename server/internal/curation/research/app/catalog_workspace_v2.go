package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"sort"
	"strings"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type CatalogResearchModeV2 string

const (
	CatalogResearchReplaceV2 CatalogResearchModeV2 = "REPLACE"
	CatalogResearchAppendV2  CatalogResearchModeV2 = "APPEND"
)

type CatalogCandidateReferenceV2 struct {
	// AmazonObservation is the saved, dated display record for the selected
	// ASIN. Korean candidates keep ExternalObservation for the same purpose.
	AmazonObservation   *researchdomain.AmazonObservation
	ExternalObservation *researchdomain.ExternalProductObservation
	UserID              string
	CurationID          string
	PlanTargetID        string
	CandidateID         string
	ProviderProductID   string
	SourceKind          string
	IdentityKey         string
	Locator             CatalogProductLocator
	Assessment          LiveCandidateAssessmentV2
	Visible             bool
	DisplayOrder        int
	FirstSeenAt         time.Time
	LastSeenAt          time.Time
	// EvaluationRoundID names the Round whose evaluation this candidate still
	// waits for after being published unevaluated (staged publication). It is
	// empty once the assessment is filled or the Round is finalized.
	EvaluationRoundID string
}

func (value CatalogCandidateReferenceV2) LookupIdentifier() string {
	if value.Locator.ProductURL != nil {
		return strings.TrimSpace(value.Locator.ProductURL.CanonicalURL)
	}
	if value.Locator.MerchantVariant != nil {
		return strings.TrimSpace(value.Locator.MerchantVariant.VariantID)
	}
	return ""
}

type CatalogPoolMetadataV2 struct {
	DiscoveryOutcome           *ResearchDiscoveryOutcome
	SourceCoverage             []SourceCoverage
	TargetID                   string
	Version                    int64
	ExpandOrdinal              int
	LatestMode                 CatalogResearchModeV2
	LatestDurationMilliseconds int64
	LatestShopifyCalls         int
	LatestRateRemaining        int
	// AdmittedCandidateIDs is response-scoped projection evidence. On first
	// completion it contains only identities admitted by that command. On a
	// completed idempotency replay it contains the current durable visible
	// Candidate projection, allowing a lost-response retry to converge without
	// persisting Shopify display facts or re-executing the provider command.
	AdmittedCandidateIDs []string
	// CandidateIDBindings maps response-scoped IDs derived before the durable
	// pool transaction to the Candidate IDs actually retained by PostgreSQL.
	// It is needed only while upgrading locator-keyed Phase 8 rows: the stable
	// provider product identity keeps the existing Candidate ID and all of its
	// user-owned configuration/cart references intact.
	CandidateIDBindings []CatalogCandidateIDBindingV2
}

type CatalogCandidateIDBindingV2 struct {
	ProposedCandidateID string
	DurableCandidateID  string
}

type CatalogCandidateConfigurationV2 struct {
	Version         int64
	TargetID        string
	CandidateID     string
	VariantID       string
	SelectedOptions []string
	ObservedAt      time.Time
	UpdatedAt       time.Time
}

type CatalogVariantInteractionV2 struct {
	TargetID    string
	CandidateID string
	VariantID   string
	Pinned      bool
	Sentiment   string
	UpdatedAt   time.Time
}

// CatalogLikedVariantSnapshotV2 is presentation-only account state captured
// when the user likes an exact Variant. It is never catalog, inventory, price,
// or checkout authority.
type CatalogLikedVariantSnapshotV2 struct {
	ProductTitle string
	VariantTitle string
	ProductURL   string
	Merchant     string
	PriceMinor   int64
	PriceUnknown bool
	Currency     string
	TargetTitle  string
}

type CatalogSaveVariantInteractionInputV2 struct {
	RelationToken string
	UserID        string
	CurationID    string
	CandidateID   string
	VariantID     string
	Pinned        bool
	Sentiment     string
	LikedSnapshot *CatalogLikedVariantSnapshotV2
}

type CatalogWorkspaceStoredStateV2 struct {
	ProductInteractions []researchdomain.ProductReaction
	Pools               []CatalogPoolMetadataV2
	Candidates          []CatalogCandidateReferenceV2
	Configurations      []CatalogCandidateConfigurationV2
	Interactions        []CatalogVariantInteractionV2
}

type CatalogWorkspaceRepositoryV2 interface {
	AuthorizeTargetV2(context.Context, string, string, string) error
	CatalogTargetMarketContextV2(context.Context, string, string, string) (CatalogMarketContextV2, error)
	CatalogCurationMarketContextV2(context.Context, string, string) (CatalogMarketContextV2, error)
	LoadCatalogWorkspaceStateV2(context.Context, string, string) (CatalogWorkspaceStoredStateV2, error)
	ResolveCatalogCandidateV2(context.Context, string, string, string) (CatalogCandidateReferenceV2, error)
	SaveCatalogCandidateConfigurationV2(context.Context, string, string, CatalogCandidateConfigurationV2) error
	SaveCatalogVariantPreferenceV2(
		context.Context,
		string,
		string,
		CatalogVariantInteractionV2,
		*researchdomain.LikedVariantV2,
	) error
}

type CatalogMarketContextV2 struct {
	Country  string
	Currency string
}

type CatalogCartDraftSnapshotV2 struct {
	Country  string
	Currency string
	Items    []CartItemInputV2
}

type CatalogCartDraftReaderV2 interface {
	ReadCatalogCartDraftV2(context.Context, string, string) (CatalogCartDraftSnapshotV2, error)
}

type CatalogWorkspaceSearchInputV2 struct {
	ResumeRoundID    string
	SearchLanguage   researchdomain.SearchLanguage
	ExecutionVersion string
	QueryInputHash   string
	QueryProjections []CatalogQueryProjection
	Progress         SourceProgressSet
	ProductVertical  string
	QuerySeeds       []string
	// ExistingExternalProductKeys prevents a changed follow-up plan
	// fingerprint from admitting the same durable merchant product again.
	// ExistingBrowserSourceCounts starts each merchant after the browser rows
	// already retained for this Target.
	ExistingExternalProductKeys map[string]bool
	ExistingBrowserSourceCounts map[researchdomain.Source]int
	Criteria                    *curationdomain.TargetCriteriaSetV1
	Budget                      *ResearchBudgetSnapshot
	UserID                      string
	CurationID                  string
	TargetID                    string
	Mode                        CatalogResearchModeV2
	ExpectedPoolVersion         int64
	IdempotencyKey              string
	Search                      LiveCatalogReviewSearchInputV2
}

type CatalogWorkspaceSearchResultV2 struct {
	LiveCatalogReviewResultV2
	Pool   CatalogPoolMetadataV2
	Replay bool
}

type CatalogWorkspacePoolV2 struct {
	Metadata       CatalogPoolMetadataV2
	Products       []CatalogProductObservation
	HiddenProducts []CatalogProductObservation
	Assessments    map[string]LiveCandidateAssessmentV2
	Hydrations     map[string]CatalogCandidateHydrationV2
	Messages       []CatalogProviderMessage
}

type CatalogCandidateHydrationStatusV2 string

const (
	CatalogCandidateHydrationReadyV2      CatalogCandidateHydrationStatusV2 = "READY"
	CatalogCandidateHydrationUnresolvedV2 CatalogCandidateHydrationStatusV2 = "UNRESOLVED"
)

const CatalogCandidateHydrationUnresolvedReasonV2 = "CATALOG_RESEARCH_CANDIDATE_UNRESOLVED"

// CatalogCandidateHydrationNoLocatorReasonV2: the saved candidate has nothing
// Shopify could be asked with, so it stays unresolved and is not retryable.
const CatalogCandidateHydrationNoLocatorReasonV2 = "CATALOG_RESEARCH_CANDIDATE_LOCATOR_MISSING"

type CatalogCandidateHydrationV2 struct {
	Status     CatalogCandidateHydrationStatusV2
	ReasonCode string
	Retryable  bool
}

type CatalogWorkspaceConfigurationViewV2 struct {
	CandidateID string
	Variant     LiveVariantReviewRowV2
	ObservedAt  string
	Version     int64
}

type CatalogWorkspaceViewV2 struct {
	Pools          []CatalogWorkspacePoolV2
	Messages       []CatalogProviderMessage
	Configurations []CatalogWorkspaceConfigurationViewV2
	Interactions   []CatalogVariantInteractionV2
	Metrics        LiveCatalogReviewMetricsV2
	// External card state travels with the DB-only workspace read so a card
	// grid needs no per-card request (ADR-0075 follow-up). ProductReactions and
	// ReactionAllowedCandidateIDs cover Korean products; PurchaseFeedback is the
	// curation-wide self-report list both Amazon and Korean cards read.
	PurchaseFeedback            PurchaseFeedback
	ProductReactions            []researchdomain.ProductReaction
	ReactionAllowedCandidateIDs []string
}

type CatalogResearchHydrationScopeV2 string

const (
	CatalogResearchHydrationVisibleTargetV2 CatalogResearchHydrationScopeV2 = "VISIBLE_TARGET"
	CatalogResearchHydrationHiddenTargetV2  CatalogResearchHydrationScopeV2 = "HIDDEN_TARGET"
	CatalogResearchHydrationCandidateV2     CatalogResearchHydrationScopeV2 = "CANDIDATE"
)

type CatalogResearchHydrationInputV2 struct {
	UserID      string
	CurationID  string
	Scope       CatalogResearchHydrationScopeV2
	TargetID    string
	CandidateID string
	Source      researchdomain.Source
}

func (service *LiveCatalogReviewServiceV2) EnableWorkspaceRepositoryV2(
	repository CatalogWorkspaceRepositoryV2,
) error {
	if repository == nil {
		return fault.New(fault.InvalidInput, "PHASE8_WORKSPACE_REPOSITORY_INVALID", false)
	}
	service.workspace = repository
	return nil
}

func (service *LiveCatalogReviewServiceV2) EnableCartDraftReaderV2(
	reader CatalogCartDraftReaderV2,
) error {
	if reader == nil {
		return fault.New(fault.InvalidInput, "PHASE8_CART_READER_INVALID", false)
	}
	service.cartReader = reader
	return nil
}

func (service *LiveCatalogReviewServiceV2) PrepareWorkspaceAgencyOrderV2(
	ctx context.Context,
	userID, curationID string,
) (AgencyOrderPreparationPreviewV2, error) {
	if service.cartReader == nil || strings.TrimSpace(userID) == "" ||
		strings.TrimSpace(curationID) == "" {
		return AgencyOrderPreparationPreviewV2{}, fault.New(
			fault.InvalidInput, "PHASE8_CART_PREPARATION_INVALID", false,
		)
	}
	draft, err := service.cartReader.ReadCatalogCartDraftV2(ctx, userID, curationID)
	if err != nil {
		return AgencyOrderPreparationPreviewV2{}, err
	}
	if service.workspace != nil {
		for _, item := range draft.Items {
			ref, e := service.resolveCandidateV2(ctx, userID, curationID, item.CandidateID)
			if e != nil {
				return AgencyOrderPreparationPreviewV2{}, e
			}
			if ref.ProductRef().Source != researchdomain.SourceShopify {
				return AgencyOrderPreparationPreviewV2{}, fault.New(fault.InvalidInput, "EXTERNAL_PRODUCT_CART_FORBIDDEN", false)
			}
		}
	}
	return service.PrepareAgencyOrder(ctx, PrepareAgencyOrderPreviewInputV2{
		Country: draft.Country, Currency: draft.Currency, Items: draft.Items,
	})
}

func (service *LiveCatalogReviewServiceV2) SearchWorkspaceV2(
	ctx context.Context,
	input CatalogWorkspaceSearchInputV2,
) (CatalogWorkspaceSearchResultV2, error) {
	if service.workspace == nil || strings.TrimSpace(input.UserID) == "" ||
		strings.TrimSpace(input.CurationID) == "" || strings.TrimSpace(input.TargetID) == "" ||
		(input.Mode != CatalogResearchReplaceV2 && input.Mode != CatalogResearchAppendV2) {
		return CatalogWorkspaceSearchResultV2{}, fault.New(
			fault.InvalidInput, "PHASE8_WORKSPACE_SEARCH_INVALID", false,
		)
	}
	if input.Mode == CatalogResearchAppendV2 && service.foregroundWork != nil {
		active, err := service.foregroundWork.HasActiveCurationWork(
			ctx, input.UserID, input.CurationID,
		)
		if err != nil {
			return CatalogWorkspaceSearchResultV2{}, err
		}
		if active {
			return CatalogWorkspaceSearchResultV2{}, fault.New(
				fault.Conflict, "CURATION_EXPANSION_IN_PROGRESS", true,
			)
		}
	}
	if err := service.workspace.AuthorizeTargetV2(
		ctx, input.UserID, input.CurationID, input.TargetID,
	); err != nil {
		return CatalogWorkspaceSearchResultV2{}, err
	}
	profileReader, ok := service.workspace.(catalogTargetSearchProfileReaderV2)
	if !ok {
		return CatalogWorkspaceSearchResultV2{}, fault.New(
			fault.InternalFailure, "PHASE8_TARGET_SEARCH_PROFILE_UNAVAILABLE", false,
		)
	}
	profile, err := profileReader.CatalogTargetSearchProfileV2(
		ctx, input.UserID, input.CurationID, input.TargetID,
	)
	if err != nil {
		return CatalogWorkspaceSearchResultV2{}, err
	}
	if input.Mode == CatalogResearchAppendV2 {
		if reader, ok := service.workspace.(interface {
			CatalogExpansionSearchProfileV2(context.Context, string, string, string) (CatalogTargetSearchProfileV2, error)
		}); ok {
			profile, err = reader.CatalogExpansionSearchProfileV2(ctx, input.UserID, input.CurationID, input.TargetID)
			if err != nil {
				return CatalogWorkspaceSearchResultV2{}, err
			}
		}
	}
	input.Budget, err = service.captureAppendBudget(ctx, input, profile)
	if err != nil {
		return CatalogWorkspaceSearchResultV2{}, err
	}
	profile = applyResearchBudget(profile, input.Budget)
	input.Search.Country = profile.Market.Country
	input.Search.Currency = profile.Market.Currency
	exponent := workspaceCurrencyExponentV2(profile.Market.Currency)
	input.Search.MinimumMinor, err = catalogProfileMinorBoundV2(profile.MinimumPrice, exponent)
	if err != nil {
		return CatalogWorkspaceSearchResultV2{}, err
	}
	input.Search.MaximumMinor, err = catalogProfileMinorBoundV2(profile.MaximumPrice, exponent)
	if err != nil {
		return CatalogWorkspaceSearchResultV2{}, err
	}
	command, err := NewCatalogCandidatePoolCommandV2(
		input, input.ExpectedPoolVersion, input.IdempotencyKey,
	)
	if err != nil {
		return CatalogWorkspaceSearchResultV2{}, err
	}
	var result LiveCatalogReviewResultV2
	var references []CatalogCandidateReferenceV2
	execution, err := service.ExecuteWorkspaceCandidatePoolCommandV2(
		ctx, command,
		func() ([]CatalogCandidateReferenceV2, LiveCatalogReviewMetricsV2, error) {
			planned, searchErr := service.searchWorkspacePlanForProfileV2(ctx, input, profile)
			if searchErr != nil {
				return nil, LiveCatalogReviewMetricsV2{}, searchErr
			}
			result, references = catalogCandidateReferencesForSearchResultV2(
				input, planned, service.clock.Now(),
			)
			return references, result.Metrics, nil
		},
	)
	if err != nil {
		return CatalogWorkspaceSearchResultV2{}, err
	}
	if execution.Replay {
		result, err = service.catalogWorkspaceReplayProjectionV2(ctx, input, execution.Pool)
		if err != nil {
			return CatalogWorkspaceSearchResultV2{}, err
		}
	} else {
		result = catalogRebindSearchCandidateIDsV2(
			result, execution.Pool.CandidateIDBindings,
		)
		result = catalogFilterSearchToAdmittedCandidatesV2(
			result, execution.Pool.AdmittedCandidateIDs,
		)
	}
	return CatalogWorkspaceSearchResultV2{
		LiveCatalogReviewResultV2: result,
		Pool:                      execution.Pool,
		Replay:                    execution.Replay,
	}, nil
}

func catalogCandidateReferencesForSearchResultV2(
	input CatalogWorkspaceSearchInputV2,
	result LiveCatalogReviewResultV2,
	now time.Time,
) (LiveCatalogReviewResultV2, []CatalogCandidateReferenceV2) {
	references := make([]CatalogCandidateReferenceV2, 0, len(result.Search.Products))
	candidateIDByProviderProductID := make(map[string]string, len(result.Search.Products))
	for index, product := range result.Search.Products {
		if product.Locator == nil || product.Locator.Validate() != nil {
			continue
		}
		providerProductID := strings.TrimSpace(product.ProviderProductID)
		sourceRef := product.ProductRef()
		identityKey := sourceRef.IdentityKey()
		if identityKey == "" {
			continue
		}
		candidateID := catalogCandidateIDV2(input.TargetID, identityKey)
		candidateIDByProviderProductID[providerProductID] = candidateID
		result.Search.Products[index].SourceProductRef = &sourceRef
		result.Search.Products[index].ProviderProductID = candidateID
		assessment := result.CandidateAssessments[providerProductID]
		if _, found := result.CandidateAssessments[providerProductID]; found {
			delete(result.CandidateAssessments, providerProductID)
			result.CandidateAssessments[candidateID] = assessment
		}
		sourceKind := "SHOPIFY_LIVE"
		if sourceRef.Source.KoreanExternal() {
			sourceKind = string(sourceRef.Source)
		}
		if sourceRef.Source == researchdomain.SourceAmazon {
			sourceKind = "AMAZON"
		}
		references = append(references, CatalogCandidateReferenceV2{
			UserID: input.UserID, CurationID: input.CurationID,
			PlanTargetID: input.TargetID, CandidateID: candidateID,
			ProviderProductID: providerProductID,
			SourceKind:        sourceKind, IdentityKey: identityKey,
			ExternalObservation: product.ExternalObservation,
			Locator:             *product.Locator, Assessment: assessment,
			Visible: true, DisplayOrder: index,
			FirstSeenAt: now, LastSeenAt: now,
		})
	}
	rebindCatalogProviderMessageSubjectsV2(
		result.Search.Messages, candidateIDByProviderProductID,
	)
	return result, references
}

func catalogRebindSearchCandidateIDsV2(
	result LiveCatalogReviewResultV2,
	bindings []CatalogCandidateIDBindingV2,
) LiveCatalogReviewResultV2 {
	byProposedID := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		proposed := strings.TrimSpace(binding.ProposedCandidateID)
		durable := strings.TrimSpace(binding.DurableCandidateID)
		if proposed != "" && durable != "" {
			byProposedID[proposed] = durable
		}
	}
	if len(byProposedID) == 0 {
		return result
	}
	for index := range result.Search.Products {
		proposed := result.Search.Products[index].ProviderProductID
		if durable := byProposedID[proposed]; durable != "" {
			result.Search.Products[index].ProviderProductID = durable
		}
	}
	for index := range result.Search.Messages {
		message := &result.Search.Messages[index]
		if message.SubjectKind == "PRODUCT" {
			if durable := byProposedID[message.SubjectRef]; durable != "" {
				message.SubjectRef = durable
			}
		}
	}
	reboundAssessments := make(map[string]LiveCandidateAssessmentV2, len(result.CandidateAssessments))
	for candidateID, assessment := range result.CandidateAssessments {
		if durable := byProposedID[candidateID]; durable != "" {
			candidateID = durable
		}
		reboundAssessments[candidateID] = assessment
	}
	result.CandidateAssessments = reboundAssessments
	return result
}

// catalogWorkspaceReplayProjectionV2 reconstructs only durable Candidate
// identity, locator and assessment state. Shopify-owned title, price, media
// and mandatory messages remain fresh-response facts and are therefore not
// fabricated or persisted for replay. The UI follows Replay with the canonical
// workspace display read when it needs those fresh facts.
func (service *LiveCatalogReviewServiceV2) catalogWorkspaceReplayProjectionV2(
	ctx context.Context,
	input CatalogWorkspaceSearchInputV2,
	pool CatalogPoolMetadataV2,
) (LiveCatalogReviewResultV2, error) {
	stored, err := service.workspace.LoadCatalogWorkspaceStateV2(
		ctx, input.UserID, input.CurationID,
	)
	if err != nil {
		return LiveCatalogReviewResultV2{}, err
	}
	selected := make(map[string]struct{}, len(pool.AdmittedCandidateIDs))
	for _, candidateID := range pool.AdmittedCandidateIDs {
		if candidateID = strings.TrimSpace(candidateID); candidateID != "" {
			selected[candidateID] = struct{}{}
		}
	}
	selectCurrentVisible := pool.AdmittedCandidateIDs == nil
	products := make([]CatalogProductObservation, 0, len(selected))
	assessments := make(map[string]LiveCandidateAssessmentV2, len(selected))
	for _, candidate := range stored.Candidates {
		if candidate.PlanTargetID != input.TargetID || !candidate.Visible {
			continue
		}
		if !selectCurrentVisible {
			if _, ok := selected[candidate.CandidateID]; !ok {
				continue
			}
		}
		locator := candidate.Locator
		ref := candidate.ProductRef()
		product := CatalogProductObservation{ProviderProductID: candidate.CandidateID, SourceProductRef: &ref, Locator: &locator, ProviderOrder: candidate.DisplayOrder}
		if candidate.ExternalObservation != nil {
			product, err = CatalogProductFromExternalObservation(*candidate.ExternalObservation)
			if err != nil {
				return LiveCatalogReviewResultV2{}, err
			}
			product.ProviderProductID = candidate.CandidateID
			product.ProviderOrder = candidate.DisplayOrder
		}
		products = append(products, product)
		assessments[candidate.CandidateID] = candidate.Assessment
	}
	sort.SliceStable(products, func(left, right int) bool {
		return products[left].ProviderOrder < products[right].ProviderOrder
	})
	now := service.clock.Now()
	return LiveCatalogReviewResultV2{
		Search: CatalogProductSearchResult{
			Provider:        CatalogProviderShopifyGlobalV2,
			ProtocolVersion: catalogProtocolVersionV2,
			Outcome:         CatalogOutcomeSuccess,
			Products:        products,
			// Provider messages are fresh-response scoped and cannot be
			// reproduced by a provider-call-0 replay.
			Messages: []CatalogProviderMessage{},
		},
		CandidateEligibleCount: len(products),
		CandidateAssessments:   assessments,
		Metrics: LiveCatalogReviewMetricsV2{
			PolicyVersion: "phase8-search-replay.v1",
			StartedAt:     now, CompletedAt: now,
			AICallCount: 0, ShopifyCallCount: 0,
			LocalCallsUsed:      0,
			LocalCallsRemaining: pool.LatestRateRemaining,
			LocalRateLimit:      service.config.MaximumCallsPerWindow,
			LocalRateWindow:     service.config.Window,
			ProviderCostStatus:  "NOT_REPORTED_BY_PROVIDER",
			ExternalEffect:      "NONE",
		},
	}, nil
}

func catalogFilterSearchToAdmittedCandidatesV2(
	result LiveCatalogReviewResultV2,
	admittedCandidateIDs []string,
) LiveCatalogReviewResultV2 {
	admitted := make(map[string]struct{}, len(admittedCandidateIDs))
	for _, candidateID := range admittedCandidateIDs {
		if candidateID = strings.TrimSpace(candidateID); candidateID != "" {
			admitted[candidateID] = struct{}{}
		}
	}
	products := make([]CatalogProductObservation, 0, len(result.Search.Products))
	assessments := make(map[string]LiveCandidateAssessmentV2, len(admitted))
	seenProducts := make(map[string]struct{}, len(admitted))
	for _, product := range result.Search.Products {
		if _, ok := admitted[product.ProviderProductID]; !ok {
			continue
		}
		if _, duplicate := seenProducts[product.ProviderProductID]; duplicate {
			continue
		}
		seenProducts[product.ProviderProductID] = struct{}{}
		products = append(products, product)
		if assessment, ok := result.CandidateAssessments[product.ProviderProductID]; ok {
			assessments[product.ProviderProductID] = assessment
		}
	}
	messages := make([]CatalogProviderMessage, 0, len(result.Search.Messages))
	for _, message := range result.Search.Messages {
		if message.SubjectKind == "PRODUCT" {
			if _, ok := admitted[message.SubjectRef]; !ok {
				continue
			}
		}
		messages = append(messages, message)
	}
	result.Search.Products = products
	result.Search.Messages = messages
	result.CandidateAssessments = assessments
	result.CandidateEligibleCount = len(products)
	return result
}

func catalogProfileMinorBoundV2(
	bound *shareddomain.Money,
	exponent uint8,
) (*int64, error) {
	if bound == nil {
		return nil, nil
	}
	value, err := catalogMinorUnitsV2(*bound, exponent)
	if err != nil {
		return nil, fault.Wrap(
			err, fault.InvalidInput, "PHASE8_PRICE_CONSTRAINT_INVALID", false,
		)
	}
	return value, nil
}

func rebindCatalogProviderMessageSubjectsV2(
	messages []CatalogProviderMessage,
	candidateIDByProviderProductID map[string]string,
) {
	for index := range messages {
		message := &messages[index]
		if message.SubjectKind != "PRODUCT" {
			continue
		}
		if candidateID := candidateIDByProviderProductID[message.SubjectRef]; candidateID != "" {
			message.SubjectRef = candidateID
		}
	}
}

// attachExternalCardStateV2 folds the state an external product card used to
// fetch for itself into the one DB-only workspace read: the curation's
// self-reported purchase records, saved Korean product reactions and which
// candidates may still take a product-level reaction. No provider call is made.
func (service *LiveCatalogReviewServiceV2) attachExternalCardStateV2(
	ctx context.Context,
	userID, curationID string,
	stored CatalogWorkspaceStoredStateV2,
	view *CatalogWorkspaceViewV2,
) error {
	if repository, ok := service.workspace.(ExternalPurchaseRepository); ok {
		feedback, err := repository.ReadPurchaseFeedback(ctx, userID, curationID)
		if err != nil {
			return err
		}
		view.PurchaseFeedback = feedback
	}
	if _, ok := service.workspace.(ProductReactionRepository); !ok {
		return nil
	}
	view.ProductReactions = append(view.ProductReactions, stored.ProductInteractions...)
	for _, candidate := range stored.Candidates {
		if candidate.ProductRef().Source.KoreanExternal() && productFallbackAllowed(candidate, stored) {
			view.ReactionAllowedCandidateIDs = append(view.ReactionAllowedCandidateIDs, candidate.CandidateID)
		}
	}
	return nil
}

func (service *LiveCatalogReviewServiceV2) LoadWorkspaceV2(
	ctx context.Context,
	userID, curationID, _ string, _ string, _ bool,
) (CatalogWorkspaceViewV2, error) {
	// The canonical workspace read is intentionally DB-only. Fresh Shopify
	// display facts require an explicit Target-scoped hydration command.
	return service.loadWorkspaceV2(ctx, userID, curationID, false, "", "", "")
}

func (service *LiveCatalogReviewServiceV2) HydrateWorkspaceV2(
	ctx context.Context,
	input CatalogResearchHydrationInputV2,
) (CatalogWorkspaceViewV2, error) {
	if input.Source != "" && input.Source != researchdomain.SourceShopify && input.Source != researchdomain.SourceAmazon && !input.Source.KoreanExternal() {
		return CatalogWorkspaceViewV2{}, fault.New(fault.InvalidInput, "CATALOG_SOURCE_INVALID", false)
	}

	if input.Source.KoreanExternal() {
		if input.Scope != CatalogResearchHydrationCandidateV2 {
			return CatalogWorkspaceViewV2{}, fault.New(fault.InvalidInput, "EXTERNAL_PRODUCT_CANDIDATE_SCOPE_REQUIRED", false)
		}
		return service.hydrateExternalProduct(ctx, input)
	}
	if input.Source == researchdomain.SourceAmazon {
		if input.Scope != CatalogResearchHydrationCandidateV2 {
			return CatalogWorkspaceViewV2{}, fault.New(fault.InvalidInput, "AMAZON_CANDIDATE_SCOPE_REQUIRED", false)
		}
		return service.hydrateAmazonCandidate(ctx, input)
	}

	input.UserID = strings.TrimSpace(input.UserID)
	input.CurationID = strings.TrimSpace(input.CurationID)
	input.TargetID = strings.TrimSpace(input.TargetID)
	input.CandidateID = strings.TrimSpace(input.CandidateID)
	validTargetScope := input.TargetID != "" && input.CandidateID == "" &&
		(input.Scope == CatalogResearchHydrationVisibleTargetV2 ||
			input.Scope == CatalogResearchHydrationHiddenTargetV2)
	validCandidateScope := input.TargetID != "" && input.CandidateID != "" &&
		input.Scope == CatalogResearchHydrationCandidateV2
	if !validTargetScope && !validCandidateScope {
		return CatalogWorkspaceViewV2{}, fault.New(
			fault.InvalidInput, "CATALOG_RESEARCH_HYDRATION_SCOPE_INVALID", false,
		)
	}
	return service.loadWorkspaceV2(
		ctx, input.UserID, input.CurationID, true, input.Scope, input.TargetID,
		input.CandidateID,
	)
}

func (service *LiveCatalogReviewServiceV2) loadWorkspaceV2(
	ctx context.Context,
	userID, curationID string,
	fresh bool,
	hydrationScope CatalogResearchHydrationScopeV2,
	hydrationTargetID string,
	hydrationCandidateID string,
) (CatalogWorkspaceViewV2, error) {
	if service.workspace == nil || strings.TrimSpace(userID) == "" ||
		strings.TrimSpace(curationID) == "" {
		return CatalogWorkspaceViewV2{}, fault.New(
			fault.InvalidInput, "PHASE8_WORKSPACE_READ_INVALID", false,
		)
	}
	stored, err := service.workspace.LoadCatalogWorkspaceStateV2(ctx, userID, curationID)
	if err != nil {
		return CatalogWorkspaceViewV2{}, err
	}
	if fresh {
		var found bool
		stored, found = catalogWorkspaceTargetScopeV2(stored, hydrationTargetID)
		if !found {
			return CatalogWorkspaceViewV2{}, fault.New(
				fault.InvalidInput, "CATALOG_RESEARCH_TARGET_INVALID", false,
			)
		}
		stored, found = catalogWorkspaceHydrationScopeV2(
			stored, hydrationScope, hydrationCandidateID,
		)
		if !found {
			return CatalogWorkspaceViewV2{}, fault.New(
				fault.InvalidInput, "CATALOG_RESEARCH_CANDIDATE_INVALID", false,
			)
		}
	}
	market, err := service.workspace.CatalogCurationMarketContextV2(ctx, userID, curationID)
	if err != nil {
		return CatalogWorkspaceViewV2{}, err
	}
	view := CatalogWorkspaceViewV2{
		Pools: []CatalogWorkspacePoolV2{}, Configurations: []CatalogWorkspaceConfigurationViewV2{},
		Messages:                    []CatalogProviderMessage{},
		Interactions:                append([]CatalogVariantInteractionV2(nil), stored.Interactions...),
		ProductReactions:            []researchdomain.ProductReaction{},
		ReactionAllowedCandidateIDs: []string{},
		PurchaseFeedback:            PurchaseFeedback{SchemaVersion: "vitlane.external-purchase-feedback.v3", Records: []ExternalPurchaseRecord{}},
	}
	if !fresh {
		pools := make(map[string]*CatalogWorkspacePoolV2, len(stored.Pools))
		for _, metadata := range stored.Pools {
			pools[metadata.TargetID] = &CatalogWorkspacePoolV2{
				Metadata: metadata, Assessments: map[string]LiveCandidateAssessmentV2{},
				Messages: []CatalogProviderMessage{},
			}
		}
		for _, candidate := range stored.Candidates {
			pool := pools[candidate.PlanTargetID]
			if pool == nil {
				continue
			}
			locator := candidate.Locator
			sourceRef := candidate.ProductRef()
			product := CatalogProductObservation{
				ProviderProductID: candidate.CandidateID,
				SourceProductRef:  &sourceRef,
				Locator:           &locator,
				ProviderOrder:     candidate.DisplayOrder,
			}
			if candidate.ExternalObservation != nil {
				var err error
				product, err = CatalogProductFromExternalObservation(*candidate.ExternalObservation)
				if err != nil {
					return CatalogWorkspaceViewV2{}, err
				}
				product.ProviderProductID = candidate.CandidateID
				product.ProviderOrder = candidate.DisplayOrder
			}
			if candidate.Visible {
				pool.Products = append(pool.Products, product)
			} else {
				pool.HiddenProducts = append(pool.HiddenProducts, product)
			}
			pool.Assessments[candidate.CandidateID] = candidate.Assessment
		}
		for _, configuration := range stored.Configurations {
			options := make([]LiveVariantOptionV2, 0, len(configuration.SelectedOptions))
			for _, selected := range configuration.SelectedOptions {
				name, value, found := strings.Cut(selected, ":")
				if !found {
					continue
				}
				options = append(options, LiveVariantOptionV2{
					Name: strings.TrimSpace(name), Value: strings.TrimSpace(value),
				})
			}
			view.Configurations = append(view.Configurations, CatalogWorkspaceConfigurationViewV2{
				CandidateID: configuration.CandidateID,
				Variant: LiveVariantReviewRowV2{
					VariantID: configuration.VariantID, SelectedOptions: options,
				},
				ObservedAt: configuration.ObservedAt.UTC().Format(time.RFC3339Nano),
				Version:    configuration.Version,
			})
		}
		for _, metadata := range stored.Pools {
			view.Pools = append(view.Pools, *pools[metadata.TargetID])
		}
		if err := service.attachExternalCardStateV2(ctx, userID, curationID, stored, &view); err != nil {
			return CatalogWorkspaceViewV2{}, err
		}
		return view, nil
	}
	shopifyCandidates := stored.Candidates[:0]
	shopifyIDs := map[string]bool{}
	for _, candidate := range stored.Candidates {
		if candidate.ProductRef().Source == researchdomain.SourceShopify {
			shopifyCandidates = append(shopifyCandidates, candidate)
			shopifyIDs[candidate.CandidateID] = true
		}
	}
	stored.Candidates = shopifyCandidates
	shopifyConfigurations := stored.Configurations[:0]
	for _, configuration := range stored.Configurations {
		if shopifyIDs[configuration.CandidateID] {
			shopifyConfigurations = append(shopifyConfigurations, configuration)
		}
	}
	stored.Configurations = shopifyConfigurations
	if len(stored.Candidates) == 0 && len(stored.Configurations) == 0 {
		for _, metadata := range stored.Pools {
			view.Pools = append(view.Pools, CatalogWorkspacePoolV2{Metadata: metadata})
		}
		return view, nil
	}
	configurationByCandidate := make(map[string]CatalogCandidateConfigurationV2, len(stored.Configurations))
	for _, configuration := range stored.Configurations {
		configurationByCandidate[configuration.CandidateID] = configuration
	}
	// Use one fresh lookup input per selected Candidate. Initial rendering
	// hydrates visible rows only; hidden history stays as a durable skeleton
	// until the user opens that Target's hidden view.
	handoffMatches := []CatalogOfferMatch{}
	lookupInputs := make([]CatalogOfferLookupInput, 0, len(stored.Candidates))
	for _, candidate := range stored.Candidates {
		_, configured := configurationByCandidate[candidate.CandidateID]
		if !configured && hydrationScope == CatalogResearchHydrationVisibleTargetV2 {
			if p, ok := service.responseHandoff.take(candidate, service.clock.Now()); ok {
				handoffMatches = append(handoffMatches, CatalogOfferMatch{DraftID: "candidate:" + candidate.CandidateID, Product: p})
				continue
			}
		}
		identifier := candidate.LookupIdentifier()
		if configuration, configured := configurationByCandidate[candidate.CandidateID]; configured {
			identifier = configuration.VariantID
		}
		if identifier != "" {
			lookupInputs = append(lookupInputs, CatalogOfferLookupInput{
				DraftID:    "candidate:" + candidate.CandidateID,
				Identifier: identifier,
			})
		}
	}
	startedAt := service.clock.Now()
	completedAt := startedAt
	lookup := CatalogOfferLookupResult{
		Outcome: CatalogOutcomeSuccess, Matches: []CatalogOfferMatch{},
		Messages: []CatalogProviderMessage{}, UnresolvedIDs: []string{},
	}
	providerCallCount := 0
	used, remaining := service.providerWindowSnapshotV2(startedAt)
	if len(lookupInputs) > 0 {
		callBudget := providerLookupCallBudgetV2(len(lookupInputs))
		operation, operationErr := service.beginProviderOperationV2(startedAt, callBudget)
		if operationErr != nil {
			return CatalogWorkspaceViewV2{}, operationErr
		}
		defer operation.Close()
		lookup, err = service.gateway.LookupOffers(ctx, CatalogOfferLookupRequest{
			Inputs: lookupInputs,
			Context: CatalogBuyerContext{
				Country: market.Country, Language: "en",
				Currency: market.Currency,
				Intent:   "render saved research workspace",
			},
			ProviderCallAdmission: operation,
		})
		completedAt = service.clock.Now()
		if err != nil && len(handoffMatches) == 0 {
			return CatalogWorkspaceViewV2{}, err
		}
		providerCallCount, used, remaining = operation.Snapshot()
		if err == nil && lookup.ProviderCallCount != providerCallCount {
			return CatalogWorkspaceViewV2{}, fault.New(
				fault.InternalFailure, "CATALOG_RESEARCH_LOOKUP_CALL_COUNT_MISMATCH", false,
			)
		}
	}
	lookup.Matches = append(lookup.Matches, handoffMatches...)
	view.Metrics = LiveCatalogReviewMetricsV2{
		PolicyVersion: LiveCatalogReviewPolicyVersionV2,
		StartedAt:     startedAt, CompletedAt: completedAt,
		Duration: completedAt.Sub(startedAt), AICallCount: 0,
		ShopifyCallCount: providerCallCount,
		LocalCallsUsed:   used, LocalCallsRemaining: remaining,
		LocalRateLimit:            service.config.MaximumCallsPerWindow,
		LocalRateWindow:           service.config.Window,
		ProviderCostStatus:        "NOT_REPORTED_BY_PROVIDER",
		ProviderBillingCredential: false, ExternalEffect: "CATALOG_READ_ONLY",
	}
	matches := make(map[string]CatalogOfferMatch, len(lookup.Matches))
	for _, match := range lookup.Matches {
		matches[match.DraftID] = match
	}
	pools := make(map[string]*CatalogWorkspacePoolV2, len(stored.Pools))
	for _, metadata := range stored.Pools {
		pool := &CatalogWorkspacePoolV2{Metadata: metadata}
		pools[metadata.TargetID] = pool
		view.Pools = append(view.Pools, *pool)
	}
	// Rebuild the slice after mutation below; map pointers above point at copies.
	pools = make(map[string]*CatalogWorkspacePoolV2, len(stored.Pools))
	for _, metadata := range stored.Pools {
		pools[metadata.TargetID] = &CatalogWorkspacePoolV2{
			Metadata: metadata, Assessments: map[string]LiveCandidateAssessmentV2{},
			Hydrations: map[string]CatalogCandidateHydrationV2{},
			Messages:   []CatalogProviderMessage{},
		}
	}
	candidatesByProviderProductID := make(map[string][]CatalogCandidateReferenceV2, len(stored.Candidates))
	for _, candidate := range stored.Candidates {
		locator := candidate.Locator
		product := CatalogProductObservation{
			ProviderProductID: candidate.CandidateID,
			Locator:           &locator, ProviderOrder: candidate.DisplayOrder,
		}
		providerProductID := ""
		hydration := CatalogCandidateHydrationV2{
			Status:     CatalogCandidateHydrationUnresolvedV2,
			ReasonCode: CatalogCandidateHydrationUnresolvedReasonV2,
			Retryable:  true,
		}
		if candidate.LookupIdentifier() == "" {
			// Nothing was sent to Shopify for this candidate: it has no product URL
			// and no variant to look up. Asking again would send nothing again, so the
			// card must not offer a retry that can never succeed.
			if _, configured := configurationByCandidate[candidate.CandidateID]; !configured {
				hydration.ReasonCode = CatalogCandidateHydrationNoLocatorReasonV2
				hydration.Retryable = false
			}
		}
		if match, ok := matches["candidate:"+candidate.CandidateID]; ok {
			product = match.Product
			providerProductID = strings.TrimSpace(product.ProviderProductID)
			product.ProviderProductID = candidate.CandidateID
			product.Locator = &locator
			hydration = CatalogCandidateHydrationV2{
				Status: CatalogCandidateHydrationReadyV2,
			}
		}
		sourceRef := candidate.ProductRef()
		product.SourceProductRef = &sourceRef
		pool := pools[candidate.PlanTargetID]
		if pool == nil {
			continue
		}
		if candidate.Visible {
			pool.Products = append(pool.Products, product)
		} else {
			pool.HiddenProducts = append(pool.HiddenProducts, product)
		}
		pool.Assessments[candidate.CandidateID] = candidate.Assessment
		pool.Hydrations[candidate.CandidateID] = hydration
		if providerProductID != "" {
			candidatesByProviderProductID[providerProductID] = append(
				candidatesByProviderProductID[providerProductID], candidate,
			)
		}
	}
	for _, message := range lookup.Messages {
		if message.SubjectKind != "PRODUCT" {
			view.Messages = append(view.Messages, message)
			continue
		}
		candidates := candidatesByProviderProductID[message.SubjectRef]
		for _, candidate := range candidates {
			pool := pools[candidate.PlanTargetID]
			if pool == nil {
				continue
			}
			bound := message
			bound.SubjectRef = candidate.CandidateID
			pool.Messages = append(pool.Messages, bound)
		}
	}
	view.Pools = view.Pools[:0]
	for _, metadata := range stored.Pools {
		view.Pools = append(view.Pools, *pools[metadata.TargetID])
	}
	for _, configuration := range stored.Configurations {
		match, ok := matches["candidate:"+configuration.CandidateID]
		if !ok {
			continue
		}
		variant := match.Variant
		options := make([]LiveVariantOptionV2, 0, len(configuration.SelectedOptions))
		for _, option := range configuration.SelectedOptions {
			name, value, found := strings.Cut(option, ":")
			if !found {
				name, value = "Option", option
			}
			options = append(options, LiveVariantOptionV2{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)})
		}
		available := variant.Availability.Available != nil && *variant.Availability.Available
		mediaURL := ""
		if len(variant.Media) > 0 {
			mediaURL = variant.Media[0].URL
		}
		view.Configurations = append(view.Configurations, CatalogWorkspaceConfigurationViewV2{
			CandidateID: configuration.CandidateID,
			Variant: LiveVariantReviewRowV2{
				VariantID: variant.ID, Title: catalogConfiguredVariantTitleV2(
					configuration.SelectedOptions, variant.Title,
				),
				PriceMinor: variant.Price.AmountMinor, Currency: variant.Price.Currency,
				Available: available, SelectedOptions: options,
				MediaURL: mediaURL, ProductURL: variant.URL,
			},
			ObservedAt: configuration.ObservedAt.Format("2006-01-02T15:04:05.000Z07:00"),
			Version:    configuration.Version,
		})
	}
	return view, nil
}

func catalogWorkspaceHydrationScopeV2(
	stored CatalogWorkspaceStoredStateV2,
	scope CatalogResearchHydrationScopeV2,
	candidateID string,
) (CatalogWorkspaceStoredStateV2, bool) {
	scoped := CatalogWorkspaceStoredStateV2{
		Pools:          append([]CatalogPoolMetadataV2(nil), stored.Pools...),
		Candidates:     []CatalogCandidateReferenceV2{},
		Configurations: []CatalogCandidateConfigurationV2{},
		Interactions:   []CatalogVariantInteractionV2{},
	}
	found := scope != CatalogResearchHydrationCandidateV2
	for _, candidate := range stored.Candidates {
		selected := false
		switch scope {
		case CatalogResearchHydrationVisibleTargetV2:
			selected = candidate.Visible
		case CatalogResearchHydrationHiddenTargetV2:
			selected = !candidate.Visible
		case CatalogResearchHydrationCandidateV2:
			selected = candidate.CandidateID == candidateID
		}
		if !selected {
			continue
		}
		found = true
		scoped.Candidates = append(scoped.Candidates, candidate)
	}
	selectedCandidateIDs := make(map[string]struct{}, len(scoped.Candidates))
	for _, candidate := range scoped.Candidates {
		selectedCandidateIDs[candidate.CandidateID] = struct{}{}
	}
	for _, configuration := range stored.Configurations {
		if _, selected := selectedCandidateIDs[configuration.CandidateID]; selected {
			scoped.Configurations = append(scoped.Configurations, configuration)
		}
	}
	for _, interaction := range stored.Interactions {
		if _, selected := selectedCandidateIDs[interaction.CandidateID]; selected {
			scoped.Interactions = append(scoped.Interactions, interaction)
		}
	}
	return scoped, found
}

// catalogWorkspaceTargetScopeV2 produces the complete response projection for
// exactly one Target. The repository order is the durable display order; a
// defensive slice keeps malformed legacy rows from ever exceeding Shopify's
// 50-identifier lookup contract without deleting those rows.
func catalogWorkspaceTargetScopeV2(
	stored CatalogWorkspaceStoredStateV2,
	targetID string,
) (CatalogWorkspaceStoredStateV2, bool) {
	scoped := CatalogWorkspaceStoredStateV2{
		Pools: []CatalogPoolMetadataV2{}, Candidates: []CatalogCandidateReferenceV2{},
		Configurations: []CatalogCandidateConfigurationV2{},
		Interactions:   []CatalogVariantInteractionV2{},
	}
	found := false
	for _, pool := range stored.Pools {
		if pool.TargetID == targetID {
			scoped.Pools = append(scoped.Pools, pool)
			found = true
			break
		}
	}
	for _, candidate := range stored.Candidates {
		if candidate.PlanTargetID != targetID ||
			len(scoped.Candidates) >= int(researchdomain.CandidatePoolMaximumCanonicalIdentitiesV2) {
			continue
		}
		scoped.Candidates = append(scoped.Candidates, candidate)
	}
	selectedCandidateIDs := make(map[string]struct{}, len(scoped.Candidates))
	for _, candidate := range scoped.Candidates {
		selectedCandidateIDs[candidate.CandidateID] = struct{}{}
	}
	for _, configuration := range stored.Configurations {
		if configuration.TargetID == targetID {
			if _, selected := selectedCandidateIDs[configuration.CandidateID]; !selected {
				continue
			}
			scoped.Configurations = append(scoped.Configurations, configuration)
		}
	}
	for _, interaction := range stored.Interactions {
		if interaction.TargetID == targetID {
			if _, selected := selectedCandidateIDs[interaction.CandidateID]; !selected {
				continue
			}
			scoped.Interactions = append(scoped.Interactions, interaction)
		}
	}
	return scoped, found
}

func catalogConfiguredVariantTitleV2(selectedOptions []string, fallback string) string {
	values := make([]string, 0, len(selectedOptions))
	for _, selected := range selectedOptions {
		_, value, found := strings.Cut(selected, ":")
		if !found {
			value = selected
		}
		value = strings.TrimSpace(value)
		if value != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return strings.TrimSpace(fallback)
	}
	return strings.Join(values, " / ")
}

type CatalogSaveConfigurationInputV2 struct {
	RelationToken   string
	ExpectedVersion int64
	UserID          string
	CurationID      string
	CandidateID     string
	VariantID       string
	SelectedOptions []string
	ObservedAt      time.Time
}

func (service *LiveCatalogReviewServiceV2) SaveWorkspaceConfigurationV2(
	ctx context.Context,
	input CatalogSaveConfigurationInputV2,
) error {
	if service.workspace == nil || strings.TrimSpace(input.UserID) == "" ||
		strings.TrimSpace(input.CurationID) == "" || strings.TrimSpace(input.CandidateID) == "" ||
		strings.TrimSpace(input.VariantID) == "" || input.ObservedAt.IsZero() {
		return fault.New(fault.InvalidInput, "PHASE8_CONFIGURATION_INVALID", false)
	}
	reference, err := service.resolveCandidateV2(
		ctx, input.UserID, input.CurationID, input.CandidateID,
	)
	if err != nil {
		return err
	}
	if reference.ProductRef().Source.KoreanExternal() {
		return fault.New(fault.InvalidInput, "EXTERNAL_PRODUCT_VARIANT_UNCONFIRMED", false)
	}
	if reference.ProductRef().Source == researchdomain.SourceAmazon {
		return service.saveAmazonConfiguration(ctx, input, reference)
	}
	return service.workspace.SaveCatalogCandidateConfigurationV2(
		ctx, input.UserID, input.CurationID, CatalogCandidateConfigurationV2{
			TargetID: reference.PlanTargetID, CandidateID: reference.CandidateID,
			VariantID:       strings.TrimSpace(input.VariantID),
			SelectedOptions: append([]string(nil), input.SelectedOptions...),
			ObservedAt:      input.ObservedAt, UpdatedAt: service.clock.Now(),
		},
	)
}

func (service *LiveCatalogReviewServiceV2) SaveWorkspaceInteractionV2(
	ctx context.Context,
	input CatalogSaveVariantInteractionInputV2,
) error {
	input.UserID = strings.TrimSpace(input.UserID)
	input.CurationID = strings.TrimSpace(input.CurationID)
	input.CandidateID = strings.TrimSpace(input.CandidateID)
	input.VariantID = strings.TrimSpace(input.VariantID)
	input.Sentiment = strings.ToUpper(strings.TrimSpace(input.Sentiment))
	if service.workspace == nil || input.UserID == "" || input.CurationID == "" ||
		input.CandidateID == "" || input.VariantID == "" ||
		(input.Sentiment != "NONE" && input.Sentiment != "LIKE" && input.Sentiment != "DISLIKE") {
		return fault.New(fault.InvalidInput, "PHASE8_VARIANT_INTERACTION_INVALID", false)
	}
	reference, err := service.resolveCandidateV2(
		ctx, input.UserID, input.CurationID, input.CandidateID,
	)
	if err != nil {
		return err
	}
	if err := service.guardTargetWriteV2(ctx, input.UserID, reference.PlanTargetID); err != nil {
		return err
	}
	updatedAt := service.clock.Now()
	if reference.ProductRef().Source.KoreanExternal() {
		return fault.New(fault.InvalidInput, "EXTERNAL_PRODUCT_VARIANT_UNCONFIRMED", false)
	}
	if ref := reference.ProductRef(); ref.Source == researchdomain.SourceAmazon {
		variant := researchdomain.SourceVariantRef{Source: ref.Source, Marketplace: ref.Marketplace, ASIN: input.VariantID}
		if variant.Validate() != nil {
			return fault.New(fault.InvalidInput, "AMAZON_REFERENCE_INVALID", false)
		}
		stored, loadErr := service.workspace.LoadCatalogWorkspaceStateV2(ctx, input.UserID, input.CurationID)
		if loadErr != nil {
			return loadErr
		}
		known := input.VariantID == ref.AnchorASIN
		for _, config := range stored.Configurations {
			known = known || (config.CandidateID == input.CandidateID && config.VariantID == input.VariantID)
		}
		for _, previous := range stored.Interactions {
			known = known || (previous.CandidateID == input.CandidateID && previous.VariantID == input.VariantID)
		}
		// A reaction is independent of configuration. The same short-lived,
		// owner-scoped relation used by option selection authorizes a newly
		// previewed ASIN without silently saving it or calling the provider.
		if !known && input.RelationToken != "" {
			grant, grantErr := service.amazonGrant(ctx, input.UserID, input.CurationID, input.CandidateID, input.RelationToken)
			if grantErr != nil {
				return grantErr
			}
			if grant.AnchorASIN == ref.AnchorASIN {
				for _, option := range grant.Variants {
					known = known || option.ASIN == input.VariantID
				}
			}
		}
		if !known {
			return fault.New(fault.InvalidInput, "AMAZON_VARIANT_RELATION_INVALID", false)
		}
	}
	interaction := CatalogVariantInteractionV2{
		TargetID: reference.PlanTargetID, CandidateID: reference.CandidateID,
		VariantID: input.VariantID, Pinned: input.Pinned,
		Sentiment: input.Sentiment, UpdatedAt: updatedAt,
	}
	var liked *researchdomain.LikedVariantV2
	if input.Sentiment == "LIKE" {
		if input.LikedSnapshot == nil {
			return fault.New(fault.InvalidInput, "PHASE8_LIKED_VARIANT_SNAPSHOT_REQUIRED", false)
		}
		valid, validationErr := researchdomain.NewLikedVariantV2(
			researchdomain.LikedVariantV2{
				UserID: input.UserID, CurationID: input.CurationID,
				CandidateID: reference.CandidateID, VariantID: input.VariantID,
				ProductTitle: input.LikedSnapshot.ProductTitle,
				VariantTitle: input.LikedSnapshot.VariantTitle,
				ProductURL:   input.LikedSnapshot.ProductURL,
				Merchant:     input.LikedSnapshot.Merchant,
				PriceMinor:   input.LikedSnapshot.PriceMinor,
				PriceUnknown: input.LikedSnapshot.PriceUnknown,
				Currency:     input.LikedSnapshot.Currency,
				TargetTitle:  input.LikedSnapshot.TargetTitle,
				UpdatedAt:    updatedAt,
			},
		)
		if validationErr != nil {
			return validationErr
		}
		liked = &valid
	}
	err = service.workspace.SaveCatalogVariantPreferenceV2(ctx, input.UserID, input.CurationID, interaction, liked)
	if err == nil {
		sharedapp.RecordAnalytics(ctx, sharedapp.AnalyticsEvent{Name: "candidate_reacted", UserID: input.UserID, Key: input.CurationID + ":" + input.CandidateID + ":" + input.VariantID + ":" + updatedAt.Format(time.RFC3339Nano), CurationID: input.CurationID, Source: string(reference.ProductRef().Source), Action: input.Sentiment})
	}
	return err
}

func (service *LiveCatalogReviewServiceV2) BrowseWorkspaceVariantsV2(
	ctx context.Context,
	userID, curationID, candidateID, cursorToken string,
) (LiveVariantReviewPageV2, error) {
	if service.workspace == nil {
		return LiveVariantReviewPageV2{}, fault.New(fault.InvalidInput, "PHASE8_WORKSPACE_DISABLED", false)
	}
	reference, err := service.resolveCandidateV2(ctx, userID, curationID, candidateID)
	if err != nil {
		return LiveVariantReviewPageV2{}, err
	}
	if reference.ProductRef().Source == researchdomain.SourceAmazon {
		return service.browseAmazonVariants(ctx, userID, curationID, reference, cursorToken)
	}
	if reference.ProductRef().Source.KoreanExternal() {
		return LiveVariantReviewPageV2{}, fault.New(fault.InvalidInput, "EXTERNAL_PRODUCT_VARIANT_UNCONFIRMED", false)
	}
	if service.variantChoice == nil {
		return LiveVariantReviewPageV2{}, fault.New(fault.ProviderUnavailable, "PHASE8_WORKSPACE_DISABLED", false)
	}
	market, err := service.workspace.CatalogTargetMarketContextV2(
		ctx, userID, curationID, reference.PlanTargetID,
	)
	if err != nil {
		return LiveVariantReviewPageV2{}, err
	}
	marketContext, err := shareddomain.NewMarketContext(market.Country, market.Currency)
	if err != nil {
		return LiveVariantReviewPageV2{}, fault.New(
			fault.InvalidInput, "LIVE_VARIANT_REVIEW_REQUEST_INVALID", false,
		)
	}
	locatorHash, err := VariantChoiceSourceLocatorHashV2(reference.Locator)
	if err != nil {
		return LiveVariantReviewPageV2{}, fault.New(
			fault.InvalidInput, "LIVE_VARIANT_REVIEW_REQUEST_INVALID", false,
		)
	}
	startedAt := service.clock.Now()
	operation, err := service.beginProviderOperationV2(startedAt, 3)
	if err != nil {
		return LiveVariantReviewPageV2{}, err
	}
	defer operation.Close()
	choice, err := service.variantChoice.LoadPage(ctx, LoadVariantChoicePageV2Input{
		OwnerID: userID,
		Source: VariantChoiceCandidateSourceV2{
			CandidateID:       reference.CandidateID,
			SourceDiscoveryID: "phase8-candidate:" + reference.ProviderProductID,
			SourceLocatorHash: locatorHash, Locator: reference.Locator,
			MarketContext: marketContext,
		},
		CursorToken:           strings.TrimSpace(cursorToken),
		ProviderCallAdmission: operation,
	})
	completedAt := service.clock.Now()
	if err != nil {
		return LiveVariantReviewPageV2{}, err
	}
	providerCallCount, used, remaining := operation.Snapshot()
	return liveVariantReviewPageFromChoiceV2(choice, LiveCatalogReviewMetricsV2{
		PolicyVersion: LiveCatalogReviewPolicyVersionV2,
		StartedAt:     startedAt, CompletedAt: completedAt,
		Duration: completedAt.Sub(startedAt), AICallCount: 0,
		ShopifyCallCount: providerCallCount,
		LocalCallsUsed:   used, LocalCallsRemaining: remaining,
		LocalRateLimit:            service.config.MaximumCallsPerWindow,
		LocalRateWindow:           service.config.Window,
		ProviderCostStatus:        "NOT_REPORTED_BY_PROVIDER",
		ProviderBillingCredential: false, ExternalEffect: "CATALOG_READ_ONLY",
	})
}

func (service *LiveCatalogReviewServiceV2) resolveCandidateV2(
	ctx context.Context,
	userID, curationID, candidateID string,
) (CatalogCandidateReferenceV2, error) {
	return service.workspace.ResolveCatalogCandidateV2(ctx, userID, curationID, candidateID)
}

var ErrCatalogCandidateNotFoundV2 = fmt.Errorf("PHASE8_CANDIDATE_NOT_FOUND")

func catalogCandidateIdentityKeyV2(providerProductID string) string {
	providerProductID = strings.TrimSpace(providerProductID)
	if providerProductID == "" {
		return ""
	}
	// Shopify product IDs are opaque and case-sensitive. The locator is a
	// mutable resolution hint (URL or merchant Variant), so it must never own
	// product-level Candidate identity.
	return "shopify-product:" + providerProductID
}

func catalogCandidateIDV2(targetID, identityKey string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(targetID) + "\x00" + identityKey))
	return "phase8-" + hex.EncodeToString(digest[:])
}

func sortCatalogStoredStateV2(state *CatalogWorkspaceStoredStateV2) {
	sort.SliceStable(state.Pools, func(i, j int) bool { return state.Pools[i].TargetID < state.Pools[j].TargetID })
	sort.SliceStable(state.Candidates, func(i, j int) bool {
		if state.Candidates[i].PlanTargetID == state.Candidates[j].PlanTargetID {
			return state.Candidates[i].DisplayOrder < state.Candidates[j].DisplayOrder
		}
		return state.Candidates[i].PlanTargetID < state.Candidates[j].PlanTargetID
	})
}
