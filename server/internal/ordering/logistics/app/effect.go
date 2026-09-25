package app

import (
	"context"
	"errors"
	"time"
)

var ErrPurchaseRegistrationMissing = errors.New("PROCUREMENT_LOGISTICS_NOT_READY")

type purchaseRegistrationRepository interface {
	RequirePurchaseRegistration(context.Context, string, string, []string) error
}

func (s *Service) RequirePurchaseRegistration(ctx context.Context, orderID, moID string, unitIDs []string) error {
	repo, ok := s.repository.(purchaseRegistrationRepository)
	if !ok {
		return ErrPurchaseRegistrationMissing
	}
	return repo.RequirePurchaseRegistration(ctx, orderID, moID, unitIDs)
}

type cancellationEffectRepository interface {
	LockCancellationUnits(context.Context, string, string) (int, error)
	ApplyCancellation(context.Context, string, string, time.Time) error
}

func (s *Service) LockCancellationUnits(ctx context.Context, orderID, moID string) (int, error) {
	repo, ok := s.repository.(cancellationEffectRepository)
	if !ok {
		return 0, ErrPurchaseRegistrationMissing
	}
	return repo.LockCancellationUnits(ctx, orderID, moID)
}
func (s *Service) ApplyCancellation(ctx context.Context, orderID, moID string) error {
	repo, ok := s.repository.(cancellationEffectRepository)
	if !ok {
		return ErrPurchaseRegistrationMissing
	}
	return repo.ApplyCancellation(ctx, orderID, moID, s.clock.Now())
}
