package app

import (
	"context"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

type LikedVariantRepositoryV2 interface {
	ListLikedVariantsV2(context.Context, string, int) ([]researchdomain.LikedVariantV2, error)
}

func (s *Service) ListLikedVariantsV2(
	ctx context.Context,
	userID string,
	limit int,
) ([]researchdomain.LikedVariantV2, error) {
	repository, ok := s.repository.(LikedVariantRepositoryV2)
	if !ok {
		return []researchdomain.LikedVariantV2{}, nil
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	return repository.ListLikedVariantsV2(ctx, userID, limit)
}
