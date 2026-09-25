package http

import (
	"context"
	"errors"
	conversationapp "github.com/vitlane/vitlane/server/internal/curation/conversation/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	nethttp "net/http"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	agencyorderapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type workspacePlanningReader interface {
	GetByCuration(
		context.Context,
		string,
		string,
	) (curationapp.PlanResult, error)
	ListCurationTimeline(
		context.Context,
		string,
		string,
		int,
	) ([]curationapp.CurationTimelineItem, error)
}

type workspaceResearchReader interface {
	GetPlanResearch(
		context.Context,
		string,
		string,
	) (researchapp.PlanResearchResult, error)
}

type workspaceCartReader interface {
	GetCurationCart(
		context.Context,
		string,
		string,
	) (curationdomain.CartView, error)
}

type workspaceAgencyOrderTraceReader interface {
	ListCurationAgencyOrderTrace(
		context.Context,
		string,
		string,
		int,
	) ([]agencyorderapp.CurationTrace, error)
	ListCurationAgencyOrderedTargetIDs(
		context.Context,
		string,
		string,
	) ([]string, error)
}

// workspaceIntelligenceReader projects the jobs running for this curation.
// ADR-0038 folds progress into this one response so the browser polls a single
// endpoint instead of the three it used to.
type workspaceIntelligenceReader interface {
	ListCurationJobs(
		context.Context,
		string,
		string,
	) ([]intelligenceapp.JobProgress, error)
}

type workspaceCatalogResearchReader interface {
	LoadWorkspaceV2(
		context.Context,
		string, string, string, string, bool,
	) (researchapp.CatalogWorkspaceViewV2, error)
}

// WorkspaceHandler composes canonical product read models. It owns no state
// and never infers commands from transcript text.
type WorkspaceHandler struct {
	conversation interface {
		Read(context.Context, string, string) (conversationapp.Conversation, error)
	}
	plans           workspacePlanningReader
	research        workspaceResearchReader
	cart            workspaceCartReader
	agencyOrders    workspaceAgencyOrderTraceReader
	intelligence    workspaceIntelligenceReader
	catalogResearch workspaceCatalogResearchReader
}

func (h *WorkspaceHandler) EnableConversation(reader interface {
	Read(context.Context, string, string) (conversationapp.Conversation, error)
}) {
	h.conversation = reader
}

func (h *WorkspaceHandler) EnableIntelligence(
	reader workspaceIntelligenceReader,
) {
	h.intelligence = reader
}

// EnableCatalogResearch folds the durable CandidatePool projection into the
// canonical Curation workspace. This read is DB-only: polling Action/Round/Job
// progress can never fan out into Shopify calls.
func (h *WorkspaceHandler) EnableCatalogResearch(reader workspaceCatalogResearchReader) {
	h.catalogResearch = reader
}

func NewWorkspaceHandler(
	plans workspacePlanningReader,
	research workspaceResearchReader,
	cart workspaceCartReader,
	agencyOrders ...workspaceAgencyOrderTraceReader,
) *WorkspaceHandler {
	handler := &WorkspaceHandler{
		plans: plans, research: research, cart: cart,
	}
	if len(agencyOrders) > 0 {
		handler.agencyOrders = agencyOrders[0]
	}
	return handler
}

type curationWorkspaceCoverage string

const (
	curationWorkspaceCoverageNone     curationWorkspaceCoverage = "NONE"
	curationWorkspaceCoveragePartial  curationWorkspaceCoverage = "PARTIAL"
	curationWorkspaceCoverageComplete curationWorkspaceCoverage = "COMPLETE"
)

type curationWorkspaceActiveWork struct {
	WorkTargetID string `json:"workTargetId"`
	Label        string `json:"label"`
	Status       string `json:"status"`
	Detail       string `json:"detail,omitempty"`
}

type curationWorkspaceResponse struct {
	Conversation     *conversationapp.Conversation             `json:"conversation,omitempty"`
	Plan             curationdomain.PlanSnapshot               `json:"plan"`
	Curation         curationdomain.Curation                   `json:"curation"`
	Targets          []curationdomain.PlanTarget               `json:"targets"`
	Research         researchapp.PlanResearchResult            `json:"research"`
	Cart             curationdomain.CartView                   `json:"cart"`
	AvailableActions []curationdomain.CurationActionDescriptor `json:"availableActions"`
	Timeline         []curationapp.CurationTimelineItem        `json:"timeline"`
	LatestArtifact   string                                    `json:"latestArtifact"`
	Coverage         curationWorkspaceCoverage                 `json:"coverage"`
	AgencyOrderTrace []agencyorderapp.CurationTrace            `json:"agencyOrderTrace"`
	ActiveWork       *curationWorkspaceActiveWork              `json:"activeWork,omitempty"`
	Intelligence     []intelligenceapp.JobProgress             `json:"intelligence"`
	CatalogResearch  catalogResearchWorkspaceResponse          `json:"catalogResearch"`
}

type catalogResearchWorkspaceResponse struct {
	SchemaVersion  string                                 `json:"schemaVersion"`
	Pools          []catalogResearchPoolResponse          `json:"pools"`
	Configurations []catalogResearchConfigurationResponse `json:"configurations"`
	Interactions   []catalogResearchInteractionResponse   `json:"interactions"`
	// External product card state, so a card grid needs no per-card request.
	PurchaseFeedback            researchapp.PurchaseFeedback     `json:"purchaseFeedback"`
	ProductReactions            []researchdomain.ProductReaction `json:"productReactions"`
	ReactionAllowedCandidateIDs []string                         `json:"reactionAllowedCandidateIds"`
}

type catalogResearchPoolResponse struct {
	DiscoveryOutcome *researchapp.ResearchDiscoveryOutcome `json:"discoveryOutcome,omitempty"`
	TargetID         string                                `json:"targetId"`
	Version          int64                                 `json:"version"`
	ExpandOrdinal    int                                   `json:"expandOrdinal"`
	LatestMode       researchapp.CatalogResearchModeV2     `json:"latestMode,omitempty"`
	SourceCoverage   []researchapp.SourceCoverage          `json:"sourceCoverage"`
	Products         []catalogResearchCandidate            `json:"products"`
	HiddenProducts   []catalogResearchCandidate            `json:"hiddenProducts"`
}

type catalogResearchCandidate struct {
	AxisAssessment      *researchdomain.AxisAssessmentV1           `json:"axisAssessment,omitempty"`
	ExternalObservation *researchdomain.ExternalProductObservation `json:"externalObservation,omitempty"`
	Source              researchdomain.Source                      `json:"source"`
	SourceProductRef    researchdomain.SourceProductRef            `json:"sourceProductRef"`
	ProviderProductID   string                                     `json:"candidateId"`
	Locator             *catalogResearchLocator                    `json:"locator,omitempty"`
	IntentPoint         string                                     `json:"intentPoint,omitempty"`
	Features            []string                                   `json:"features"`
	Specifications      []string                                   `json:"specifications"`
}

type catalogResearchLocator struct {
	Kind         researchapp.CatalogLocatorKind `json:"kind"`
	ProductURL   string                         `json:"productUrl,omitempty"`
	VariantID    string                         `json:"variantId,omitempty"`
	SellerDomain string                         `json:"sellerDomain,omitempty"`
}

type catalogResearchConfigurationResponse struct {
	CandidateID string                         `json:"candidateId"`
	Version     int64                          `json:"version"`
	Variant     catalogResearchVariantResponse `json:"variant"`
	ObservedAt  string                         `json:"observedAt"`
}

type catalogResearchVariantResponse struct {
	VariantID       string                  `json:"variantId"`
	Title           string                  `json:"title"`
	PriceMinor      int64                   `json:"priceMinor"`
	Currency        string                  `json:"currency"`
	Available       bool                    `json:"available"`
	SelectedOptions []catalogResearchOption `json:"selectedOptions"`
	MediaURL        string                  `json:"mediaUrl,omitempty"`
	ProductURL      string                  `json:"productUrl,omitempty"`
}

type catalogResearchOption struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type catalogResearchInteractionResponse struct {
	CandidateID string `json:"candidateId"`
	VariantID   string `json:"variantId"`
	Pinned      bool   `json:"pinned"`
	Sentiment   string `json:"sentiment"`
}

func (h *WorkspaceHandler) Get(w nethttp.ResponseWriter, r *nethttp.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	curationID := r.PathValue("curationId")
	plan, err := h.plans.GetByCuration(r.Context(), userID, curationID)
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	research, err := h.research.GetPlanResearch(
		r.Context(),
		userID,
		string(plan.Plan.ID),
	)
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	timeline, err := h.plans.ListCurationTimeline(
		r.Context(),
		userID,
		curationID,
		100,
	)
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	cart := curationdomain.CartView{
		CurationID: curationID,
		Selections: []curationdomain.CartSelection{},
		Total: shareddomain.Money{
			Amount:   "0",
			Currency: plan.Plan.TotalBudget.Currency,
		},
		Warnings:  []curationdomain.CartWarning{},
		UpdatedAt: plan.Curation.UpdatedAt,
	}
	latestArtifact := "TARGET_LIST"
	if plan.Curation.Phase == curationdomain.CurationPhaseCurating {
		cart, err = h.cart.GetCurationCart(r.Context(), userID, curationID)
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
		latestArtifact = "CURATION_BOARD"
	}
	trace := []agencyorderapp.CurationTrace{}
	agencyOrderedTargetIDs := []string{}
	if h.agencyOrders != nil {
		agencyOrderedTargetIDs, err =
			h.agencyOrders.ListCurationAgencyOrderedTargetIDs(
				r.Context(),
				userID,
				curationID,
			)
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
		trace, err = h.agencyOrders.ListCurationAgencyOrderTrace(
			r.Context(), userID, curationID, 500,
		)
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
	}
	jobs := []intelligenceapp.JobProgress{}
	if h.intelligence != nil {
		jobs, err = h.intelligence.ListCurationJobs(
			r.Context(), userID, curationID,
		)
		if err != nil {
			writeWorkspaceError(w, err)
			return
		}
	}
	catalogResearch := emptyCatalogResearchWorkspace()
	if h.catalogResearch != nil {
		catalogResearchView, catalogErr := h.catalogResearch.LoadWorkspaceV2(
			r.Context(), userID, curationID, "", "", false,
		)
		if catalogErr != nil {
			writeWorkspaceError(w, catalogErr)
			return
		}
		catalogResearch = mapCatalogResearchWorkspace(catalogResearchView)
	}
	var conversation *conversationapp.Conversation
	if h.conversation != nil {
		value, e := h.conversation.Read(r.Context(), userID, curationID)
		if e != nil {
			writeWorkspaceError(w, e)
			return
		}
		conversation = &value
	}
	httpapi.WriteJSON(w, nethttp.StatusOK, curationWorkspaceResponse{
		Conversation:     conversation,
		Plan:             plan.Plan,
		Curation:         plan.Curation,
		Targets:          plan.Targets,
		Research:         research,
		Cart:             cart,
		AvailableActions: plan.AvailableActions,
		Timeline:         timeline,
		LatestArtifact:   latestArtifact,
		Coverage: projectCurationCoverage(
			plan.Targets,
			agencyOrderedTargetIDs,
		),
		AgencyOrderTrace: trace,
		ActiveWork:       projectCurationActiveWork(jobs),
		Intelligence:     jobs,
		CatalogResearch:  catalogResearch,
	})
}

func emptyCatalogResearchWorkspace() catalogResearchWorkspaceResponse {
	return catalogResearchWorkspaceResponse{
		SchemaVersion:               "vitlane.catalog-research-workspace.v4",
		Pools:                       []catalogResearchPoolResponse{},
		Configurations:              []catalogResearchConfigurationResponse{},
		Interactions:                []catalogResearchInteractionResponse{},
		PurchaseFeedback:            researchapp.PurchaseFeedback{SchemaVersion: "vitlane.external-purchase-feedback.v3", Records: []researchapp.ExternalPurchaseRecord{}},
		ProductReactions:            []researchdomain.ProductReaction{},
		ReactionAllowedCandidateIDs: []string{},
	}
}

func mapCatalogResearchWorkspace(view researchapp.CatalogWorkspaceViewV2) catalogResearchWorkspaceResponse {
	response := emptyCatalogResearchWorkspace()
	if view.PurchaseFeedback.SchemaVersion != "" {
		response.PurchaseFeedback = view.PurchaseFeedback
	}
	if view.PurchaseFeedback.Records == nil {
		response.PurchaseFeedback.Records = []researchapp.ExternalPurchaseRecord{}
	}
	response.ProductReactions = append(response.ProductReactions, view.ProductReactions...)
	response.ReactionAllowedCandidateIDs = append(response.ReactionAllowedCandidateIDs, view.ReactionAllowedCandidateIDs...)
	mapProduct := func(product researchapp.CatalogProductObservation, assessment researchapp.LiveCandidateAssessmentV2) catalogResearchCandidate {
		candidate := catalogResearchCandidate{
			ProviderProductID: product.ProviderProductID, Source: product.Source(), SourceProductRef: product.ProductRef(), ExternalObservation: product.ExternalObservation,
			AxisAssessment: assessment.AxisAssessment, IntentPoint: assessment.IntentPoint,
			Features:       append([]string{}, assessment.Features...),
			Specifications: append([]string{}, assessment.Specifications...),
		}
		if product.Locator != nil {
			candidate.Locator = &catalogResearchLocator{Kind: product.Locator.Kind}
			if product.Locator.ProductURL != nil {
				candidate.Locator.ProductURL = product.Locator.ProductURL.CanonicalURL
			}
			if product.Locator.MerchantVariant != nil {
				candidate.Locator.VariantID = product.Locator.MerchantVariant.VariantID
				candidate.Locator.SellerDomain = product.Locator.MerchantVariant.SellerDomain
			}
		}
		return candidate
	}
	for _, pool := range view.Pools {
		mapped := catalogResearchPoolResponse{
			DiscoveryOutcome: pool.Metadata.DiscoveryOutcome, SourceCoverage: pool.Metadata.SourceCoverage, TargetID: pool.Metadata.TargetID, Version: pool.Metadata.Version,
			ExpandOrdinal: pool.Metadata.ExpandOrdinal, LatestMode: pool.Metadata.LatestMode,
			Products: []catalogResearchCandidate{}, HiddenProducts: []catalogResearchCandidate{},
		}
		for _, product := range pool.Products {
			mapped.Products = append(mapped.Products, mapProduct(product, pool.Assessments[product.ProviderProductID]))
		}
		for _, product := range pool.HiddenProducts {
			mapped.HiddenProducts = append(mapped.HiddenProducts, mapProduct(product, pool.Assessments[product.ProviderProductID]))
		}
		response.Pools = append(response.Pools, mapped)
	}
	for _, configuration := range view.Configurations {
		options := make([]catalogResearchOption, 0, len(configuration.Variant.SelectedOptions))
		for _, option := range configuration.Variant.SelectedOptions {
			options = append(options, catalogResearchOption{Name: option.Name, Value: option.Value})
		}
		response.Configurations = append(response.Configurations, catalogResearchConfigurationResponse{
			CandidateID: configuration.CandidateID,
			Version:     configuration.Version,
			Variant: catalogResearchVariantResponse{
				VariantID: configuration.Variant.VariantID, Title: configuration.Variant.Title,
				PriceMinor: configuration.Variant.PriceMinor, Currency: configuration.Variant.Currency,
				Available: configuration.Variant.Available, SelectedOptions: options,
				MediaURL: configuration.Variant.MediaURL, ProductURL: configuration.Variant.ProductURL,
			},
			ObservedAt: configuration.ObservedAt,
		})
	}
	for _, interaction := range view.Interactions {
		response.Interactions = append(response.Interactions, catalogResearchInteractionResponse{
			CandidateID: interaction.CandidateID, VariantID: interaction.VariantID,
			Pinned: interaction.Pinned, Sentiment: interaction.Sentiment,
		})
	}
	return response
}

func projectCurationCoverage(
	targets []curationdomain.PlanTarget,
	orderedTargetIDs []string,
) curationWorkspaceCoverage {
	if len(targets) == 0 || len(orderedTargetIDs) == 0 {
		return curationWorkspaceCoverageNone
	}
	orderedTargets := make(
		map[string]struct{},
		len(orderedTargetIDs),
	)
	for _, targetID := range orderedTargetIDs {
		orderedTargets[targetID] = struct{}{}
	}
	filled := 0
	for _, target := range targets {
		if _, ok := orderedTargets[string(target.ID)]; ok {
			filled++
		}
	}
	switch {
	case filled == 0:
		return curationWorkspaceCoverageNone
	case filled == len(targets):
		return curationWorkspaceCoverageComplete
	default:
		return curationWorkspaceCoveragePartial
	}
}

func projectCurationActiveWork(
	jobs []intelligenceapp.JobProgress,
) *curationWorkspaceActiveWork {
	var resultConfirmationRequired *curationWorkspaceActiveWork
	for _, job := range jobs {
		if job.Status != string(intelligencedomain.JobPending) &&
			job.Status != string(intelligencedomain.JobRunning) {
			continue
		}
		label := "후보 조사"
		detail := "조사 지능이 후보를 찾고 있습니다."
		if job.TargetKind == string(intelligencedomain.TargetPlanningTask) {
			label = "Target 계획"
			detail = "조사 지능이 Target을 구성하고 있습니다."
		}
		if job.LatestAttemptStatus == intelligencedomain.AttemptEffectUnknown {
			if resultConfirmationRequired == nil {
				resultConfirmationRequired = &curationWorkspaceActiveWork{
					WorkTargetID: job.TargetID,
					Label:        label,
					Status:       "RESULT_CONFIRMATION_REQUIRED",
				}
			}
			continue
		}
		status := "RUNNING"
		if job.Status == string(intelligencedomain.JobPending) {
			status = "QUEUED"
		}
		return &curationWorkspaceActiveWork{
			WorkTargetID: job.TargetID,
			Label:        label, Status: status, Detail: detail,
		}
	}
	return resultConfirmationRequired
}

func writeWorkspaceError(w nethttp.ResponseWriter, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, nil, err, "큐레이션 작업공간을 불러오지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, curationdomain.ErrCurationNotFound),
		errors.Is(err, curationdomain.ErrPlanNotFound),
		errors.Is(err, curationdomain.ErrCurationNotFound):
		httpapi.WriteError(
			w,
			nethttp.StatusNotFound,
			"CURATION_NOT_FOUND",
			"큐레이션을 찾을 수 없습니다.",
		)
	case errors.Is(err, curationdomain.ErrCurationArchived):
		httpapi.WriteError(
			w,
			nethttp.StatusGone,
			err.Error(),
			"보관된 큐레이션입니다.",
		)
	default:
		httpapi.WriteError(
			w,
			nethttp.StatusInternalServerError,
			"INTERNAL_ERROR",
			"큐레이션 작업공간을 불러오지 못했습니다.",
		)
	}
}
