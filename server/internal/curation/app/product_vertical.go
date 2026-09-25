package app

import (
	"context"
	"github.com/vitlane/vitlane/server/internal/curation/domain"
)

func (s *Service) SetTargetProductVertical(ctx context.Context, user, target, vertical string) error {
	if repo, ok := s.repository.(interface {
		SetTargetProductVertical(context.Context, string, string, string) error
	}); ok {
		return repo.SetTargetProductVertical(ctx, user, target, domain.NormalizeProductVertical(vertical))
	}
	return nil
}
