package http

import (
	"context"
	"strings"
	"unicode/utf8"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

type catalogWorkspaceServiceV2 interface {
	SearchWorkspaceV2(
		context.Context,
		researchapp.CatalogWorkspaceSearchInputV2,
	) (researchapp.CatalogWorkspaceSearchResultV2, error)
	HydrateWorkspaceV2(
		context.Context,
		researchapp.CatalogResearchHydrationInputV2,
	) (researchapp.CatalogWorkspaceViewV2, error)
	BrowseWorkspaceVariantsV2(
		context.Context,
		string, string, string, string,
	) (researchapp.LiveVariantReviewPageV2, error)
	SaveWorkspaceConfigurationV2(
		context.Context,
		researchapp.CatalogSaveConfigurationInputV2,
	) error
	SaveWorkspaceInteractionV2(
		context.Context,
		researchapp.CatalogSaveVariantInteractionInputV2,
	) error
	PrepareWorkspaceAgencyOrderV2(
		context.Context,
		string, string,
	) (researchapp.AgencyOrderPreparationPreviewV2, error)
}

type LiveCatalogReviewHandlerV2 struct {
	workspace catalogWorkspaceServiceV2
}

func NewLiveCatalogReviewHandlerV2(
	workspace catalogWorkspaceServiceV2,
) *LiveCatalogReviewHandlerV2 {
	return &LiveCatalogReviewHandlerV2{workspace: workspace}
}

type liveCatalogReviewSearchResponseV2 struct {
	SchemaVersion           string                       `json:"schemaVersion"`
	Source                  string                       `json:"source"`
	Provider                string                       `json:"provider"`
	ProtocolVersion         string                       `json:"protocolVersion"`
	Outcome                 researchapp.CatalogOutcome   `json:"outcome"`
	Products                []liveCatalogReviewProductV2 `json:"products"`
	Messages                []liveCatalogReviewMessageV2 `json:"messages"`
	CandidateEligibleCount  int                          `json:"candidateEligibleCount"`
	DiscardedNoLocatorCount int                          `json:"discardedNoLocatorCount"`
	HasNextPage             bool                         `json:"hasNextPage"`
	EstimatedTotalCount     *int                         `json:"estimatedTotalCount,omitempty"`
	AppliedFilterVerified   bool                         `json:"appliedFilterVerified"`
	AppliedFilterCapability string                       `json:"appliedFilterCapability"`
	Metrics                 liveCatalogReviewMetricsV2   `json:"metrics"`
}

type liveCatalogReviewProductV2 struct {
	ExternalObservation *researchdomain.ExternalProductObservation `json:"externalObservation,omitempty"`
	Source              researchdomain.Source                      `json:"source"`
	SourceProductRef    researchdomain.SourceProductRef            `json:"sourceProductRef"`
	VariantObservation  *researchdomain.VariantObservation         `json:"variantObservation,omitempty"`
	PurchaseRoute       string                                     `json:"purchaseRoute"`
	ProviderProductID   string                                     `json:"candidateId"`
	Title               string                                     `json:"title"`
	Description         string                                     `json:"description"`
	PriceMinimumMinor   *int64                                     `json:"priceMinimumMinor,omitempty"`
	PriceMaximumMinor   *int64                                     `json:"priceMaximumMinor,omitempty"`
	Currency            string                                     `json:"currency"`
	MediaURL            string                                     `json:"mediaUrl,omitempty"`
	MediaAlt            string                                     `json:"mediaAlt,omitempty"`
	Categories          []string                                   `json:"categories"`
	Locator             *liveCatalogReviewLocatorV2                `json:"locator,omitempty"`
	PreviewVariant      *liveCatalogReviewVariantV2                `json:"previewVariant,omitempty"`
	AxisAssessment      *researchdomain.AxisAssessmentV1           `json:"axisAssessment,omitempty"`
	IntentPoint         string                                     `json:"intentPoint,omitempty"`
	Features            []string                                   `json:"features"`
	Specifications      []string                                   `json:"specifications"`
	Hydration           *catalogCandidateHydrationResponseV2       `json:"hydration,omitempty"`
}

type catalogCandidateHydrationResponseV2 struct {
	Status     researchapp.CatalogCandidateHydrationStatusV2 `json:"status"`
	ReasonCode string                                        `json:"reasonCode,omitempty"`
	Retryable  bool                                          `json:"retryable"`
}

type liveCatalogReviewLocatorV2 struct {
	Kind         researchapp.CatalogLocatorKind `json:"kind"`
	ProductURL   string                         `json:"productUrl,omitempty"`
	VariantID    string                         `json:"variantId,omitempty"`
	SellerDomain string                         `json:"sellerDomain,omitempty"`
}

type liveCatalogReviewVariantV2 struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	PriceMinor   int64  `json:"priceMinor"`
	Currency     string `json:"currency"`
	Available    *bool  `json:"available,omitempty"`
	SellerName   string `json:"sellerName,omitempty"`
	SellerDomain string `json:"sellerDomain,omitempty"`
}

type liveCatalogReviewMessageV2 struct {
	Type         string `json:"type"`
	Code         string `json:"code,omitempty"`
	Path         string `json:"path,omitempty"`
	SubjectKind  string `json:"subjectKind,omitempty"`
	SubjectRef   string `json:"subjectRef,omitempty"`
	ContentType  string `json:"contentType,omitempty"`
	Content      string `json:"content"`
	Severity     string `json:"severity,omitempty"`
	Presentation string `json:"presentation,omitempty"`
	ImageURL     string `json:"imageUrl,omitempty"`
	URL          string `json:"url,omitempty"`
}

type liveCatalogReviewMetricsV2 struct {
	PolicyVersion             string `json:"policyVersion"`
	StartedAt                 string `json:"startedAt"`
	CompletedAt               string `json:"completedAt"`
	DurationMilliseconds      int64  `json:"durationMilliseconds"`
	AICallCount               int    `json:"aiCallCount"`
	AICostUSD                 string `json:"aiCostUsd"`
	ShopifyCallCount          int    `json:"shopifyCallCount"`
	ProviderCostStatus        string `json:"providerCostStatus"`
	ProviderBillingCredential bool   `json:"providerBillingCredential"`
	LocalCallsUsed            int    `json:"localCallsUsed"`
	LocalCallsRemaining       int    `json:"localCallsRemaining"`
	LocalRateLimit            int    `json:"localRateLimit"`
	LocalRateWindowSeconds    int64  `json:"localRateWindowSeconds"`
	ExternalEffect            string `json:"externalEffect"`
}

type liveVariantReviewRequestV2 struct {
	CandidateID string `json:"candidateId"`
	CursorToken string `json:"cursorToken,omitempty"`
}

type liveVariantReviewResponseV2 struct {
	RelationToken  string                     `json:"relationToken,omitempty"`
	RelationStatus string                     `json:"relationStatus,omitempty"`
	Truncated      bool                       `json:"truncated"`
	SchemaVersion  string                     `json:"schemaVersion"`
	Source         string                     `json:"source"`
	CandidateID    string                     `json:"candidateId"`
	ProductTitle   string                     `json:"productTitle"`
	MerchantDomain string                     `json:"merchantDomain"`
	Rows           []liveVariantReviewRowV2   `json:"rows"`
	Pagination     liveVariantPaginationV2    `json:"pagination"`
	ObservedAt     string                     `json:"observedAt"`
	Metrics        liveCatalogReviewMetricsV2 `json:"metrics"`
}

type liveVariantReviewRowV2 struct {
	PriceUnknown    bool                  `json:"priceUnknown"`
	VariantID       string                `json:"variantId"`
	Title           string                `json:"title"`
	PriceMinor      int64                 `json:"priceMinor"`
	Currency        string                `json:"currency"`
	Available       bool                  `json:"available"`
	SelectedOptions []liveVariantOptionV2 `json:"selectedOptions"`
	MediaURL        string                `json:"mediaUrl,omitempty"`
	ProductURL      string                `json:"productUrl,omitempty"`
}

type liveVariantOptionV2 struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type liveVariantPaginationV2 struct {
	PageSize    int    `json:"pageSize"`
	HasPrevious bool   `json:"hasPrevious"`
	HasNext     bool   `json:"hasNext"`
	NextCursor  string `json:"nextCursor,omitempty"`
}

type prepareAgencyOrderResponseV2 struct {
	SchemaVersion   string                     `json:"schemaVersion"`
	Source          string                     `json:"source"`
	Status          string                     `json:"status"`
	Lines           []prepareAgencyOrderLineV2 `json:"lines"`
	SubtotalMinor   int64                      `json:"subtotalMinor"`
	Currency        string                     `json:"currency"`
	ObservedAt      string                     `json:"observedAt"`
	Provider        string                     `json:"provider"`
	ProtocolVersion string                     `json:"protocolVersion"`
	Metrics         liveCatalogReviewMetricsV2 `json:"metrics"`
}

type prepareAgencyOrderLineV2 struct {
	CartItemID        string `json:"cartItemId"`
	Status            string `json:"status"`
	ProductTitle      string `json:"productTitle"`
	VariantID         string `json:"variantId"`
	VariantTitle      string `json:"variantTitle"`
	ProductURL        string `json:"productUrl,omitempty"`
	CurrentPriceMinor int64  `json:"currentPriceMinor"`
	Currency          string `json:"currency"`
	PreviewPriceMinor int64  `json:"previewPriceMinor"`
	PreviewCurrency   string `json:"previewCurrency"`
	Quantity          int    `json:"quantity"`
	Available         bool   `json:"available"`
	PriceChanged      bool   `json:"priceChanged"`
	MediaURL          string `json:"mediaUrl,omitempty"`
	SellerName        string `json:"sellerName,omitempty"`
	SafeReasonCode    string `json:"safeReasonCode,omitempty"`
}

func mapAgencyOrderPreparationPreviewV2(
	result researchapp.AgencyOrderPreparationPreviewV2,
) prepareAgencyOrderResponseV2 {
	lines := make([]prepareAgencyOrderLineV2, 0, len(result.Lines))
	for _, line := range result.Lines {
		lines = append(lines, prepareAgencyOrderLineV2{
			CartItemID: line.CartItemID, Status: string(line.Status),
			ProductTitle: truncateProviderTextV2(line.ProductTitle, 180),
			VariantID:    line.VariantID,
			VariantTitle: truncateProviderTextV2(line.VariantTitle, 160),
			ProductURL:   line.ProductURL, CurrentPriceMinor: line.CurrentPriceMinor,
			Currency: line.Currency, PreviewPriceMinor: line.PreviewPriceMinor,
			PreviewCurrency: line.PreviewCurrency, Quantity: line.Quantity,
			Available: line.Available, PriceChanged: line.PriceChanged,
			MediaURL:       line.MediaURL,
			SellerName:     truncateProviderTextV2(line.SellerName, 160),
			SafeReasonCode: line.SafeReasonCode,
		})
	}
	return prepareAgencyOrderResponseV2{
		SchemaVersion: "vitlane.agency-order-preparation-preview.v1",
		Source:        "FRESH_SHOPIFY_LOOKUP", Status: result.Status, Lines: lines,
		SubtotalMinor: result.SubtotalMinor, Currency: result.Currency,
		ObservedAt: result.ObservedAt, Provider: result.Provider,
		ProtocolVersion: result.ProtocolVersion,
		Metrics:         mapLiveCatalogMetricsV2(result.Metrics),
	}
}

func mapLiveCatalogReviewResultV2(
	result researchapp.LiveCatalogReviewResultV2,
) liveCatalogReviewSearchResponseV2 {
	products := make([]liveCatalogReviewProductV2, 0, len(result.Search.Products))
	for _, product := range result.Search.Products {
		route := "VITLANE_CHECKOUT"
		if product.Source() != researchdomain.SourceShopify {
			route = "EXTERNAL"
		}
		mapped := liveCatalogReviewProductV2{
			ExternalObservation: product.ExternalObservation,
			Source:              product.Source(), SourceProductRef: product.ProductRef(), VariantObservation: product.VariantObservation, PurchaseRoute: route,
			ProviderProductID: product.ProviderProductID,
			Title:             truncateProviderTextV2(product.Title, 180),
			Description:       truncateProviderTextV2(catalogDescriptionTextV2(product.Description), 600),
			PriceMinimumMinor: &product.PriceRange.Minimum.AmountMinor,
			PriceMaximumMinor: &product.PriceRange.Maximum.AmountMinor,
			Currency:          product.PriceRange.Minimum.Currency,
			Categories:        []string{}, Features: []string{}, Specifications: []string{},
		}
		if (product.Source() == researchdomain.SourceAmazon && (product.VariantObservation == nil || product.VariantObservation.Price.Kind == "UNKNOWN")) || (product.Source().KoreanExternal() && (product.ExternalObservation == nil || product.ExternalObservation.Price.Kind == "UNKNOWN")) {
			mapped.PriceMinimumMinor = nil
			mapped.PriceMaximumMinor = nil
		}
		// A Shopify product that has not been read from Shopify (the DB-only
		// workspace skeleton, or a lookup that did not resolve it) carries no
		// currency. Its zero is "not read", never a price: leave the numbers out so
		// no consumer can show or reason about $0.00.
		if product.Source() == researchdomain.SourceShopify && product.PriceRange.Minimum.Currency == "" {
			mapped.PriceMinimumMinor = nil
			mapped.PriceMaximumMinor = nil
		}
		if assessment, exists := result.CandidateAssessments[product.ProviderProductID]; exists {
			mapped.AxisAssessment = assessment.AxisAssessment
			mapped.IntentPoint = truncateProviderTextV2(assessment.IntentPoint, 500)
			for _, feature := range assessment.Features {
				mapped.Features = append(mapped.Features, truncateProviderTextV2(feature, 300))
			}
			for _, specification := range assessment.Specifications {
				mapped.Specifications = append(mapped.Specifications, truncateProviderTextV2(specification, 300))
			}
		}
		for _, category := range product.Categories {
			mapped.Categories = append(mapped.Categories, truncateProviderTextV2(category.Value, 120))
		}
		if len(product.Media) > 0 {
			mapped.MediaURL = product.Media[0].URL
			mapped.MediaAlt = truncateProviderTextV2(product.Media[0].AltText, 180)
		}
		if product.Locator != nil {
			mapped.Locator = &liveCatalogReviewLocatorV2{Kind: product.Locator.Kind}
			if product.Locator.ProductURL != nil {
				mapped.Locator.ProductURL = product.Locator.ProductURL.CanonicalURL
			}
			if product.Locator.MerchantVariant != nil {
				mapped.Locator.VariantID = product.Locator.MerchantVariant.VariantID
				mapped.Locator.SellerDomain = product.Locator.MerchantVariant.SellerDomain
			}
		}
		if product.PreviewVariant != nil {
			preview := product.PreviewVariant
			mapped.PreviewVariant = &liveCatalogReviewVariantV2{
				ID: preview.ID, Title: truncateProviderTextV2(preview.Title, 160),
				PriceMinor: preview.Price.AmountMinor, Currency: preview.Price.Currency,
				Available: preview.Availability.Available,
			}
			if preview.Seller != nil {
				mapped.PreviewVariant.SellerName = truncateProviderTextV2(preview.Seller.Name, 160)
				mapped.PreviewVariant.SellerDomain = preview.Seller.Domain
			}
		}
		products = append(products, mapped)
	}
	messages := mapLiveCatalogReviewMessagesV2(result.Search.Messages)
	metrics := result.Metrics
	source := "LIVE_SHOPIFY_GLOBAL_CATALOG"
	provider := result.Search.Provider
	verified := result.Search.AppliedFilters.Verified
	capability := result.Search.AppliedFilters.ProviderCapability
	if len(result.Metrics.SourceCoverage) > 1 {
		source = "MULTI_SOURCE_CATALOG"
		provider = "SOURCE_ADAPTERS"
		verified = false
		capability = "PER_SOURCE"
	}
	return liveCatalogReviewSearchResponseV2{
		SchemaVersion: "vitlane.phase8-live-catalog-review.v3",
		Source:        source, Provider: provider,
		ProtocolVersion: result.Search.ProtocolVersion, Outcome: result.Search.Outcome,
		Products: products, Messages: messages,
		CandidateEligibleCount:  result.CandidateEligibleCount,
		DiscardedNoLocatorCount: result.DiscardedNoLocatorCount,
		HasNextPage:             result.Search.Pagination.HasNextPage,
		EstimatedTotalCount:     result.Search.Pagination.TotalCount,
		AppliedFilterVerified:   verified,
		AppliedFilterCapability: capability,
		Metrics:                 mapLiveCatalogMetricsV2(metrics),
	}
}

func mapLiveCatalogReviewMessagesV2(
	messages []researchapp.CatalogProviderMessage,
) []liveCatalogReviewMessageV2 {
	mapped := make([]liveCatalogReviewMessageV2, 0, len(messages))
	for _, message := range messages {
		mapped = append(mapped, liveCatalogReviewMessageV2{
			Type: message.Type, Code: message.Code, Path: message.Path,
			SubjectKind: message.SubjectKind, SubjectRef: message.SubjectRef,
			ContentType: message.ContentType, Content: message.Content,
			Severity: message.Severity, Presentation: message.Presentation,
			ImageURL: message.ImageURL, URL: message.URL,
		})
	}
	return mapped
}

func mapLiveCatalogMetricsV2(metrics researchapp.LiveCatalogReviewMetricsV2) liveCatalogReviewMetricsV2 {
	return liveCatalogReviewMetricsV2{
		PolicyVersion:        metrics.PolicyVersion,
		StartedAt:            metrics.StartedAt.Format(timeFormatV2),
		CompletedAt:          metrics.CompletedAt.Format(timeFormatV2),
		DurationMilliseconds: metrics.Duration.Milliseconds(),
		AICallCount:          metrics.AICallCount, AICostUSD: "0.00",
		ShopifyCallCount:          metrics.ShopifyCallCount,
		ProviderCostStatus:        metrics.ProviderCostStatus,
		ProviderBillingCredential: metrics.ProviderBillingCredential,
		LocalCallsUsed:            metrics.LocalCallsUsed,
		LocalCallsRemaining:       metrics.LocalCallsRemaining,
		LocalRateLimit:            metrics.LocalRateLimit,
		LocalRateWindowSeconds:    int64(metrics.LocalRateWindow.Seconds()),
		ExternalEffect:            metrics.ExternalEffect,
	}
}

const timeFormatV2 = "2006-01-02T15:04:05.000Z07:00"

func catalogDescriptionTextV2(description researchapp.CatalogDescription) string {
	if strings.TrimSpace(description.Plain) != "" {
		return description.Plain
	}
	return description.Markdown
}

func truncateProviderTextV2(value string, maximumRunes int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= maximumRunes {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maximumRunes])) + "…"
}
