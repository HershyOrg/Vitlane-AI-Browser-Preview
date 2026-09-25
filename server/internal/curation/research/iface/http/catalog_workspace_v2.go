package http

import (
	"net/http"
	"strings"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type catalogWorkspaceSearchRequestV2 struct {
	Mode                researchapp.CatalogResearchModeV2 `json:"mode"`
	ExpectedPoolVersion *int64                            `json:"expectedPoolVersion"`
	Query               string                            `json:"query"`
	Intent              string                            `json:"intent"`
	Country             string                            `json:"country"`
	Currency            string                            `json:"currency"`
	MinimumMinor        *int64                            `json:"minimumMinor"`
	MaximumMinor        *int64                            `json:"maximumMinor"`
	Limit               int                               `json:"limit"`
}

type catalogResearchHydrationRequest struct {
	Source      researchdomain.Source                       `json:"source,omitempty"`
	Scope       researchapp.CatalogResearchHydrationScopeV2 `json:"scope"`
	TargetID    string                                      `json:"targetId"`
	CandidateID string                                      `json:"candidateId,omitempty"`
}

type catalogWorkspacePoolResponseV2 struct {
	SourceCoverage             []researchapp.SourceCoverage      `json:"sourceCoverage"`
	TargetID                   string                            `json:"targetId"`
	Version                    int64                             `json:"version"`
	ExpandOrdinal              int                               `json:"expandOrdinal"`
	LatestMode                 researchapp.CatalogResearchModeV2 `json:"latestMode,omitempty"`
	LatestDurationMilliseconds int64                             `json:"latestDurationMilliseconds"`
	LatestShopifyCalls         int                               `json:"latestShopifyCalls"`
	LatestRateRemaining        int                               `json:"latestRateRemaining"`
	Products                   []liveCatalogReviewProductV2      `json:"products"`
	HiddenProducts             []liveCatalogReviewProductV2      `json:"hiddenProducts"`
	Messages                   []liveCatalogReviewMessageV2      `json:"messages"`
}

type catalogWorkspaceConfigurationResponseV2 struct {
	CandidateID string                 `json:"candidateId"`
	Variant     liveVariantReviewRowV2 `json:"variant"`
	ObservedAt  string                 `json:"observedAt"`
}

type catalogWorkspaceInteractionResponseV2 struct {
	CandidateID string `json:"candidateId"`
	VariantID   string `json:"variantId"`
	Pinned      bool   `json:"pinned"`
	Sentiment   string `json:"sentiment"`
}

type catalogWorkspaceResponseV2 struct {
	SchemaVersion  string                                    `json:"schemaVersion"`
	Pools          []catalogWorkspacePoolResponseV2          `json:"pools"`
	Messages       []liveCatalogReviewMessageV2              `json:"messages"`
	Configurations []catalogWorkspaceConfigurationResponseV2 `json:"configurations"`
	Interactions   []catalogWorkspaceInteractionResponseV2   `json:"interactions"`
	Metrics        liveCatalogReviewMetricsV2                `json:"metrics"`
}

type catalogConfigurationRequestV2 struct {
	RelationToken   string   `json:"relationToken,omitempty"`
	ExpectedVersion int64    `json:"expectedVersion"`
	CandidateID     string   `json:"candidateId"`
	VariantID       string   `json:"variantId"`
	SelectedOptions []string `json:"selectedOptions"`
	ObservedAt      string   `json:"observedAt"`
}

type catalogInteractionRequestV2 struct {
	RelationToken string                                `json:"relationToken,omitempty"`
	CandidateID   string                                `json:"candidateId"`
	VariantID     string                                `json:"variantId"`
	Pinned        bool                                  `json:"pinned"`
	Sentiment     string                                `json:"sentiment"`
	LikedSnapshot *catalogLikedVariantSnapshotRequestV2 `json:"likedSnapshot,omitempty"`
}

type catalogLikedVariantSnapshotRequestV2 struct {
	ProductTitle string `json:"productTitle"`
	VariantTitle string `json:"variantTitle"`
	ProductURL   string `json:"productUrl"`
	Merchant     string `json:"merchant"`
	PriceMinor   int64  `json:"priceMinor"`
	PriceUnknown bool   `json:"priceUnknown"`
	Currency     string `json:"currency"`
	TargetTitle  string `json:"targetTitle"`
}

func (handler *LiveCatalogReviewHandlerV2) SearchWorkspace(
	w http.ResponseWriter,
	r *http.Request,
) {
	if handler.workspace == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "PHASE8_RESEARCH_UNAVAILABLE", "Research 기능을 사용할 수 없습니다.")
		return
	}
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request catalogWorkspaceSearchRequestV2
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	if request.ExpectedPoolVersion == nil || *request.ExpectedPoolVersion < 0 {
		httpapi.WriteError(
			w, http.StatusBadRequest,
			"PHASE8_CANDIDATE_POOL_VERSION_REQUIRED",
			"최신 Candidate pool 버전으로 다시 시도해 주세요.",
		)
		return
	}
	if request.Mode != researchapp.CatalogResearchAppendV2 {
		httpapi.WriteError(
			w, http.StatusConflict,
			"PHASE8_RESEARCH_REPLACE_REQUIRES_WORKER",
			"재조사는 비동기 Research 작업으로 요청해 주세요.",
		)
		return
	}
	result, err := handler.workspace.SearchWorkspaceV2(
		r.Context(), researchapp.CatalogWorkspaceSearchInputV2{
			UserID: userID, CurationID: r.PathValue("curationId"),
			TargetID: r.PathValue("targetId"), Mode: request.Mode,
			ExpectedPoolVersion: *request.ExpectedPoolVersion,
			IdempotencyKey:      idempotencyKey,
			Search: researchapp.LiveCatalogReviewSearchInputV2{
				Query: request.Query, Intent: request.Intent,
				Country: request.Country, Currency: request.Currency,
				MinimumMinor: request.MinimumMinor, MaximumMinor: request.MaximumMinor,
				Limit: request.Limit,
			},
		},
	)
	if err != nil {
		httpapi.WriteFault(w, r, err, "Shopify 상품 조사를 완료하지 못했습니다.")
		return
	}
	response := mapLiveCatalogReviewResultV2(result.LiveCatalogReviewResultV2)
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": response.SchemaVersion,
		"source":        response.Source, "provider": response.Provider,
		"protocolVersion": response.ProtocolVersion, "outcome": response.Outcome,
		"products": response.Products, "messages": response.Messages,
		"candidateEligibleCount":  response.CandidateEligibleCount,
		"discardedNoLocatorCount": response.DiscardedNoLocatorCount,
		"hasNextPage":             response.HasNextPage,
		"estimatedTotalCount":     response.EstimatedTotalCount,
		"appliedFilterVerified":   response.AppliedFilterVerified,
		"appliedFilterCapability": response.AppliedFilterCapability,
		"metrics":                 response.Metrics,
		"targetId":                result.Pool.TargetID, "poolVersion": result.Pool.Version,
		"expandOrdinal":  result.Pool.ExpandOrdinal,
		"mode":           result.Pool.LatestMode,
		"replay":         result.Replay,
		"sourceCoverage": result.Metrics.SourceCoverage,
	})
}

func (handler *LiveCatalogReviewHandlerV2) HydrateWorkspace(
	w http.ResponseWriter,
	r *http.Request,
) {
	if handler.workspace == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "PHASE8_RESEARCH_UNAVAILABLE", "Research 기능을 사용할 수 없습니다.")
		return
	}
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request catalogResearchHydrationRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	result, err := handler.workspace.HydrateWorkspaceV2(
		r.Context(), researchapp.CatalogResearchHydrationInputV2{
			UserID: userID, CurationID: r.PathValue("curationId"),
			Scope: request.Scope, TargetID: request.TargetID, Source: request.Source,
			CandidateID: request.CandidateID,
		},
	)
	if err != nil {
		httpapi.WriteFault(w, r, err, "저장된 Research 작업공간을 불러오지 못했습니다.")
		return
	}
	response := catalogWorkspaceResponseV2{
		SchemaVersion:  "vitlane.catalog-research-hydration.v3",
		Pools:          []catalogWorkspacePoolResponseV2{},
		Messages:       mapLiveCatalogReviewMessagesV2(result.Messages),
		Configurations: []catalogWorkspaceConfigurationResponseV2{},
		Interactions:   []catalogWorkspaceInteractionResponseV2{},
		Metrics:        mapLiveCatalogMetricsV2(result.Metrics),
	}
	for _, pool := range result.Pools {
		response.Pools = append(response.Pools, catalogWorkspacePoolResponseV2{
			SourceCoverage: pool.Metadata.SourceCoverage, TargetID: pool.Metadata.TargetID, Version: pool.Metadata.Version,
			ExpandOrdinal:              pool.Metadata.ExpandOrdinal,
			LatestMode:                 pool.Metadata.LatestMode,
			LatestDurationMilliseconds: pool.Metadata.LatestDurationMilliseconds,
			LatestShopifyCalls:         pool.Metadata.LatestShopifyCalls,
			LatestRateRemaining:        pool.Metadata.LatestRateRemaining,
			Products:                   mapCatalogWorkspaceProductsV2(pool.Products, pool.Assessments, pool.Hydrations),
			HiddenProducts:             mapCatalogWorkspaceProductsV2(pool.HiddenProducts, pool.Assessments, pool.Hydrations),
			Messages:                   mapLiveCatalogReviewMessagesV2(pool.Messages),
		})
	}
	for _, configuration := range result.Configurations {
		response.Configurations = append(response.Configurations, catalogWorkspaceConfigurationResponseV2{
			CandidateID: configuration.CandidateID,
			Variant:     mapCatalogWorkspaceVariantV2(configuration.Variant),
			ObservedAt:  configuration.ObservedAt,
		})
	}
	for _, interaction := range result.Interactions {
		response.Interactions = append(response.Interactions, catalogWorkspaceInteractionResponseV2{
			CandidateID: interaction.CandidateID, VariantID: interaction.VariantID,
			Pinned: interaction.Pinned, Sentiment: interaction.Sentiment,
		})
	}
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	httpapi.WriteJSON(w, http.StatusOK, response)
}

func (handler *LiveCatalogReviewHandlerV2) writeWorkspaceResponse(
	w http.ResponseWriter,
	result researchapp.CatalogWorkspaceViewV2,
	schemaVersion string,
) {
	response := catalogWorkspaceResponseV2{
		SchemaVersion:  schemaVersion,
		Pools:          []catalogWorkspacePoolResponseV2{},
		Messages:       mapLiveCatalogReviewMessagesV2(result.Messages),
		Configurations: []catalogWorkspaceConfigurationResponseV2{},
		Interactions:   []catalogWorkspaceInteractionResponseV2{},
		Metrics:        mapLiveCatalogMetricsV2(result.Metrics),
	}
	for _, pool := range result.Pools {
		response.Pools = append(response.Pools, catalogWorkspacePoolResponseV2{
			SourceCoverage: pool.Metadata.SourceCoverage, TargetID: pool.Metadata.TargetID, Version: pool.Metadata.Version,
			ExpandOrdinal: pool.Metadata.ExpandOrdinal, LatestMode: pool.Metadata.LatestMode,
			LatestDurationMilliseconds: pool.Metadata.LatestDurationMilliseconds,
			LatestShopifyCalls:         pool.Metadata.LatestShopifyCalls,
			LatestRateRemaining:        pool.Metadata.LatestRateRemaining,
			Products:                   mapCatalogWorkspaceProductsV2(pool.Products, pool.Assessments, nil),
			HiddenProducts:             mapCatalogWorkspaceProductsV2(pool.HiddenProducts, pool.Assessments, nil),
			Messages:                   mapLiveCatalogReviewMessagesV2(pool.Messages),
		})
	}
	for _, configuration := range result.Configurations {
		response.Configurations = append(response.Configurations, catalogWorkspaceConfigurationResponseV2{
			CandidateID: configuration.CandidateID,
			Variant:     mapCatalogWorkspaceVariantV2(configuration.Variant),
			ObservedAt:  configuration.ObservedAt,
		})
	}
	for _, interaction := range result.Interactions {
		response.Interactions = append(response.Interactions, catalogWorkspaceInteractionResponseV2{
			CandidateID: interaction.CandidateID, VariantID: interaction.VariantID,
			Pinned: interaction.Pinned, Sentiment: interaction.Sentiment,
		})
	}
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	httpapi.WriteJSON(w, http.StatusOK, response)
}

func (handler *LiveCatalogReviewHandlerV2) BrowseWorkspaceVariants(
	w http.ResponseWriter,
	r *http.Request,
) {
	if handler.workspace == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "PHASE8_RESEARCH_UNAVAILABLE", "Research 기능을 사용할 수 없습니다.")
		return
	}
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request liveVariantReviewRequestV2
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	candidateID := strings.TrimSpace(r.PathValue("candidateId"))
	if candidateID == "" {
		candidateID = request.CandidateID
	}
	result, err := handler.workspace.BrowseWorkspaceVariantsV2(
		r.Context(), userID, r.PathValue("curationId"), candidateID,
		request.CursorToken,
	)
	if err != nil {
		httpapi.WriteFault(w, r, err, "Shopify Variant를 불러오지 못했습니다.")
		return
	}
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	httpapi.WriteJSON(w, http.StatusOK, mapLiveVariantReviewPageV2(result))
}

func (handler *LiveCatalogReviewHandlerV2) SaveWorkspaceConfiguration(
	w http.ResponseWriter,
	r *http.Request,
) {
	if handler.workspace == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "PHASE8_RESEARCH_UNAVAILABLE", "Research 기능을 사용할 수 없습니다.")
		return
	}
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request catalogConfigurationRequestV2
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	observedAt, err := time.Parse(time.RFC3339, request.ObservedAt)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "PHASE8_CONFIGURATION_INVALID", "선택한 옵션을 확인해 주세요.")
		return
	}
	candidateID := strings.TrimSpace(r.PathValue("candidateId"))
	if candidateID == "" {
		candidateID = request.CandidateID
	}
	err = handler.workspace.SaveWorkspaceConfigurationV2(r.Context(), researchapp.CatalogSaveConfigurationInputV2{
		UserID: userID, CurationID: r.PathValue("curationId"),
		CandidateID: candidateID, VariantID: request.VariantID, RelationToken: request.RelationToken, ExpectedVersion: request.ExpectedVersion,
		SelectedOptions: append([]string(nil), request.SelectedOptions...),
		ObservedAt:      observedAt,
	})
	if err != nil {
		httpapi.WriteFault(w, r, err, "선택한 옵션을 저장하지 못했습니다.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (handler *LiveCatalogReviewHandlerV2) SaveWorkspaceInteraction(
	w http.ResponseWriter,
	r *http.Request,
) {
	if handler.workspace == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "PHASE8_RESEARCH_UNAVAILABLE", "Research 기능을 사용할 수 없습니다.")
		return
	}
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request catalogInteractionRequestV2
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	candidateID := strings.TrimSpace(r.PathValue("candidateId"))
	if candidateID == "" {
		candidateID = request.CandidateID
	}
	variantID := strings.TrimSpace(r.PathValue("variantId"))
	if variantID == "" {
		variantID = request.VariantID
	}
	input := researchapp.CatalogSaveVariantInteractionInputV2{
		RelationToken: request.RelationToken,
		UserID:        userID, CurationID: r.PathValue("curationId"),
		CandidateID: candidateID, VariantID: variantID,
		Pinned: request.Pinned, Sentiment: strings.ToUpper(request.Sentiment),
	}
	if request.LikedSnapshot != nil {
		input.LikedSnapshot = &researchapp.CatalogLikedVariantSnapshotV2{
			ProductTitle: request.LikedSnapshot.ProductTitle,
			VariantTitle: request.LikedSnapshot.VariantTitle,
			ProductURL:   request.LikedSnapshot.ProductURL,
			Merchant:     request.LikedSnapshot.Merchant,
			PriceMinor:   request.LikedSnapshot.PriceMinor,
			PriceUnknown: request.LikedSnapshot.PriceUnknown,
			Currency:     request.LikedSnapshot.Currency,
			TargetTitle:  request.LikedSnapshot.TargetTitle,
		}
	}
	err := handler.workspace.SaveWorkspaceInteractionV2(r.Context(), input)
	if err != nil {
		httpapi.WriteFault(w, r, err, "Variant 반응을 저장하지 못했습니다.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (handler *LiveCatalogReviewHandlerV2) PrepareWorkspaceAgencyOrder(
	w http.ResponseWriter,
	r *http.Request,
) {
	if handler.workspace == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "PHASE8_RESEARCH_UNAVAILABLE", "Research 기능을 사용할 수 없습니다.")
		return
	}
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	result, err := handler.workspace.PrepareWorkspaceAgencyOrderV2(
		r.Context(), userID, r.PathValue("curationId"),
	)
	if err != nil {
		httpapi.WriteFault(w, r, err, "Agency Order 준비 검증을 완료하지 못했습니다.")
		return
	}
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	httpapi.WriteJSON(w, http.StatusOK, mapAgencyOrderPreparationPreviewV2(result))
}

func mapCatalogWorkspaceProductsV2(
	products []researchapp.CatalogProductObservation,
	assessments map[string]researchapp.LiveCandidateAssessmentV2,
	hydrations map[string]researchapp.CatalogCandidateHydrationV2,
) []liveCatalogReviewProductV2 {
	mapped := mapLiveCatalogReviewResultV2(researchapp.LiveCatalogReviewResultV2{
		Search:               researchapp.CatalogProductSearchResult{Products: products},
		CandidateAssessments: assessments,
	})
	for index := range mapped.Products {
		product := &mapped.Products[index]
		hydration, found := hydrations[product.ProviderProductID]
		if !found {
			continue
		}
		product.Hydration = &catalogCandidateHydrationResponseV2{
			Status: hydration.Status, ReasonCode: hydration.ReasonCode,
			Retryable: hydration.Retryable,
		}
	}
	return mapped.Products
}

func mapCatalogWorkspaceVariantV2(row researchapp.LiveVariantReviewRowV2) liveVariantReviewRowV2 {
	options := make([]liveVariantOptionV2, 0, len(row.SelectedOptions))
	for _, option := range row.SelectedOptions {
		options = append(options, liveVariantOptionV2{Name: option.Name, Value: option.Value})
	}
	return liveVariantReviewRowV2{
		VariantID: row.VariantID, Title: row.Title, PriceMinor: row.PriceMinor, PriceUnknown: row.PriceUnknown,
		Currency: row.Currency, Available: row.Available,
		SelectedOptions: options, MediaURL: row.MediaURL, ProductURL: row.ProductURL,
	}
}

func mapLiveVariantReviewPageV2(result researchapp.LiveVariantReviewPageV2) liveVariantReviewResponseV2 {
	rows := make([]liveVariantReviewRowV2, 0, len(result.Rows))
	for _, row := range result.Rows {
		rows = append(rows, mapCatalogWorkspaceVariantV2(row))
	}
	source := "SHOPIFY"
	if result.Source == researchdomain.SourceAmazon {
		source = "AMAZON"
	}
	return liveVariantReviewResponseV2{RelationToken: result.RelationToken, RelationStatus: result.RelationStatus, Truncated: result.Truncated,
		SchemaVersion: "vitlane.phase8-live-variant-page.v3",
		Source:        source, CandidateID: result.CandidateID,
		ProductTitle:   truncateProviderTextV2(result.ProductTitle, 180),
		MerchantDomain: result.MerchantDomain, Rows: rows,
		Pagination: liveVariantPaginationV2{
			PageSize: result.PageSize, HasPrevious: false,
			HasNext: result.HasNext, NextCursor: result.NextCursor,
		},
		ObservedAt: result.ObservedAt, Metrics: mapLiveCatalogMetricsV2(result.Metrics),
	}
}
