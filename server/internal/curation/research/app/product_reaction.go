package app

import (
	"context"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"strconv"
)

type ProductReactionRepository interface {
	SaveProductReaction(context.Context, string, string, researchdomain.ProductReaction, int64) (researchdomain.ProductReaction, error)
	ListProductReactions(context.Context, string, string) ([]researchdomain.ProductReaction, error)
}

type SaveProductReactionInput struct {
	UserID, CurationID, CandidateID string
	ProductRef                      researchdomain.SourceProductRef
	Pinned                          bool
	Sentiment                       string
	ExpectedVersion                 int64
}

func productFallbackAllowed(candidate CatalogCandidateReferenceV2, stored CatalogWorkspaceStoredStateV2) bool {
	hasVariant := candidate.Locator.MerchantVariant != nil
	for _, configuration := range stored.Configurations {
		hasVariant = hasVariant || configuration.CandidateID == candidate.CandidateID
	}
	for _, interaction := range stored.Interactions {
		hasVariant = hasVariant || interaction.CandidateID == candidate.CandidateID
	}
	return candidate.ExternalObservation != nil && candidate.ProductRef() == candidate.ExternalObservation.ProductRef && researchdomain.ProductReactionFallbackAllowed(candidate.ExternalObservation, hasVariant)
}

func (s *LiveCatalogReviewServiceV2) SaveProductReaction(ctx context.Context, input SaveProductReactionInput) (researchdomain.ProductReaction, error) {
	empty := researchdomain.ProductReaction{}
	if s.workspace == nil || input.UserID == "" || input.CurationID == "" || input.CandidateID == "" {
		return empty, fault.New(fault.InvalidInput, "PRODUCT_REACTION_INVALID", false)
	}
	candidate, err := s.resolveCandidateV2(ctx, input.UserID, input.CurationID, input.CandidateID)
	if err != nil {
		return empty, err
	}
	if err := s.guardTargetWriteV2(ctx, input.UserID, candidate.PlanTargetID); err != nil {
		return empty, err
	}
	stored, err := s.workspace.LoadCatalogWorkspaceStateV2(ctx, input.UserID, input.CurationID)
	if err != nil {
		return empty, err
	}
	if !productFallbackAllowed(candidate, stored) || input.ProductRef != candidate.ProductRef() {
		return empty, fault.New(fault.InvalidInput, "PRODUCT_REACTION_FALLBACK_FORBIDDEN", false)
	}
	reaction := researchdomain.ProductReaction{TargetID: candidate.PlanTargetID, CandidateID: candidate.CandidateID, ProductRef: candidate.ProductRef(), Pinned: input.Pinned, Sentiment: input.Sentiment, Version: input.ExpectedVersion, UpdatedAt: s.clock.Now()}
	if reaction.Sentiment == "LIKE" {
		snapshot := *candidate.ExternalObservation
		reaction.LikedSnapshot = &snapshot
	}
	if reaction.Validate() != nil {
		return empty, fault.New(fault.InvalidInput, "PRODUCT_REACTION_INVALID", false)
	}
	repository, ok := s.workspace.(ProductReactionRepository)
	if !ok {
		return empty, fault.New(fault.ProviderUnavailable, "PRODUCT_REACTION_UNAVAILABLE", false)
	}
	saved, err := repository.SaveProductReaction(ctx, input.UserID, input.CurationID, reaction, input.ExpectedVersion)
	if err == nil {
		sharedapp.RecordAnalytics(ctx, sharedapp.AnalyticsEvent{Name: "candidate_reacted", UserID: input.UserID, Key: input.CurationID + ":" + input.CandidateID + ":" + strconv.FormatInt(saved.Version, 10), CurationID: input.CurationID, Source: string(input.ProductRef.Source), Action: input.Sentiment})
	}
	return saved, err
}

type LikedProductRepository interface {
	ListLikedProducts(context.Context, string, int) ([]researchdomain.LikedProduct, error)
}

func (s *Service) ListLikedProducts(ctx context.Context, user string, limit int) ([]researchdomain.LikedProduct, error) {
	repository, ok := s.repository.(LikedProductRepository)
	if !ok {
		return []researchdomain.LikedProduct{}, nil
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	return repository.ListLikedProducts(ctx, user, limit)
}
