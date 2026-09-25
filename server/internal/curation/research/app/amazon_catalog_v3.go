package app

import (
	"context"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"time"
)

type AmazonSearchRequest struct {
	Page        int
	Query       string
	Marketplace string
}
type AmazonVariantOption struct {
	ASIN      string                `json:"asin"`
	Labels    []LiveVariantOptionV2 `json:"labels"`
	Available *bool                 `json:"available,omitempty"`
}
type AmazonProductDetail struct {
	Product        CatalogProductObservation
	Variants       []AmazonVariantOption
	RelationStatus string
	Truncated      bool
}
type AmazonSearchResult struct {
	RawCount        int
	HasNextPage     bool
	PaginationKnown bool
	Products        []CatalogProductObservation
	Partial         bool
}
type AmazonCatalogGateway interface {
	SearchAmazon(context.Context, AmazonSearchRequest) (AmazonSearchResult, error)
	LookupAmazon(context.Context, researchdomain.SourceVariantRef) (AmazonProductDetail, error)
	RefreshAmazonUsage(context.Context) (CatalogAPIUsage, error)
}
type CatalogAPIQuota struct {
	Limit      int64     `json:"limit"`
	Used       int64     `json:"used"`
	Remaining  int64     `json:"remaining"`
	ResetAt    time.Time `json:"resetAt"`
	ObservedAt time.Time `json:"observedAt"`
	IsFree     bool      `json:"isFree"`
}
type CatalogAPIFailureCount struct {
	ReasonCode string `json:"reasonCode"`
	Count      int64  `json:"count"`
}
type CatalogAPIUsage struct {
	Operations24h            []CatalogOperationUsage  `json:"operations24h,omitempty"`
	Configured               bool                     `json:"configured"`
	Control                  AmazonSourceControl      `json:"control"`
	Mode                     string                   `json:"mode,omitempty"`
	SchemaVersion            string                   `json:"schemaVersion"`
	Source                   researchdomain.Source    `json:"source"`
	Enabled                  bool                     `json:"enabled"`
	Quota                    *CatalogAPIQuota         `json:"quota"`
	EstimatedRemaining       *int64                   `json:"estimatedRemaining"`
	AttemptsSinceObservation int64                    `json:"attemptsSinceObservation"`
	Requests24h              int64                    `json:"requests24h"`
	Succeeded24h             int64                    `json:"succeeded24h"`
	Failures24h              []CatalogAPIFailureCount `json:"failures24h"`
	LastFailureCode          string                   `json:"lastFailureCode,omitempty"`
	LastFailureAt            *time.Time               `json:"lastFailureAt,omitempty"`
	QuotaRefreshFailure      string                   `json:"quotaRefreshFailure,omitempty"`
}

// CatalogAPIUsageRepository stores counters and safe failures, never query, key or product content.
type CatalogAPIUsageRepository interface {
	ReserveAmazonCall(context.Context, string, time.Time) (string, error)
	CompleteAmazonCall(context.Context, string, string, time.Time) error
	SaveAmazonQuota(context.Context, CatalogAPIQuota, time.Time) error
	RecordAmazonQuotaFailure(context.Context, string) error
	ReadAmazonUsage(context.Context) (CatalogAPIUsage, error)
}
