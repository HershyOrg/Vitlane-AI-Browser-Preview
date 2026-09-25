package app

import (
	"context"
	"strings"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const (
	orderSheetCleanupBatch = 20
	orderSheetCleanupLease = 30 * time.Second
	orderSheetCleanupRetry = 30 * time.Second
)

type OrderSheetCleanupRepository interface {
	ClaimOrderSheetCleanup(context.Context, time.Time, time.Time, int) ([]agencydomain.OrderSheetSession, error)
	CompleteOrderSheetCleanup(context.Context, string, time.Time) error
	RetryOrderSheetCleanup(context.Context, string, string, time.Time, time.Time) error
}

// OrderSheetCleanupService closes provider Cart/Checkout resources after an
// AgencyOrder consumes its sheet or an abandoned sheet expires. Claims and
// leases are durable; provider cancel keys are deterministic, so a process
// crash resumes the same cleanup instead of creating a new checkout.
type OrderSheetCleanupService struct {
	repository OrderSheetCleanupRepository
	checkout   MerchantCheckoutPreflightPort
	clock      sharedapp.Clock
}

func NewOrderSheetCleanupService(
	repository OrderSheetCleanupRepository,
	checkout MerchantCheckoutPreflightPort,
	clock sharedapp.Clock,
) *OrderSheetCleanupService {
	return &OrderSheetCleanupService{repository: repository, checkout: checkout, clock: clock}
}

func (s *OrderSheetCleanupService) Tick(ctx context.Context) error {
	now := s.clock.Now()
	tasks, err := s.repository.ClaimOrderSheetCleanup(
		ctx, now, now.Add(orderSheetCleanupLease), orderSheetCleanupBatch,
	)
	if err != nil {
		return err
	}
	for _, session := range tasks {
		if err := s.cleanupSession(ctx, session); err != nil {
			retryAt := s.clock.Now().Add(orderSheetCleanupRetry)
			reason := string(fault.CodeOf(err))
			if failure, ok := fault.As(err); ok {
				if strings.TrimSpace(failure.Reason) != "" {
					reason += ":" + failure.Reason
				}
				if failure.RetryAfter > 0 {
					retryAt = s.clock.Now().Add(failure.RetryAfter)
				}
			}
			if retryErr := s.repository.RetryOrderSheetCleanup(
				ctx, session.ID, reason, retryAt, s.clock.Now(),
			); retryErr != nil {
				return retryErr
			}
			continue
		}
		if err := s.repository.CompleteOrderSheetCleanup(
			ctx, session.ID, s.clock.Now(),
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *OrderSheetCleanupService) cleanupSession(
	ctx context.Context,
	session agencydomain.OrderSheetSession,
) error {
	for index := range session.MerchantCheckouts {
		checkout := session.MerchantCheckouts[index]
		if err := s.checkout.Cancel(ctx, MerchantRequest{
			OrderSheetSessionID: session.ID,
			UserID:              session.UserID,
			ShopDomain:          checkout.ShopDomain,
			Lines:               session.Lines,
			Existing:            &checkout,
		}); err != nil {
			return err
		}
	}
	return nil
}
