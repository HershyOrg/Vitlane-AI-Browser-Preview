package app

import (
	"context"
	"errors"
	"testing"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type cleanupRepository struct {
	task      agencydomain.OrderSheetSession
	available bool
	retries   int
	completed int
}

func (r *cleanupRepository) ClaimOrderSheetCleanup(
	context.Context, time.Time, time.Time, int,
) ([]agencydomain.OrderSheetSession, error) {
	if !r.available {
		return nil, nil
	}
	r.available = false
	return []agencydomain.OrderSheetSession{r.task}, nil
}

func (r *cleanupRepository) CompleteOrderSheetCleanup(
	context.Context, string, time.Time,
) error {
	r.completed++
	return nil
}

func (r *cleanupRepository) RetryOrderSheetCleanup(
	context.Context, string, string, time.Time, time.Time,
) error {
	r.retries++
	r.available = true
	return nil
}

type cleanupCheckout struct{ calls int }

func (*cleanupCheckout) Explore(context.Context, MerchantRequest) (agencydomain.MerchantCheckout, error) {
	return agencydomain.MerchantCheckout{}, errors.New("unused")
}
func (*cleanupCheckout) SelectDelivery(context.Context, MerchantRequest, map[string]string) (agencydomain.MerchantCheckout, error) {
	return agencydomain.MerchantCheckout{}, errors.New("unused")
}
func (*cleanupCheckout) Preflight(context.Context, MerchantRequest) (agencydomain.MerchantCheckout, error) {
	return agencydomain.MerchantCheckout{}, errors.New("unused")
}
func (*cleanupCheckout) FinalGet(context.Context, MerchantRequest) (agencydomain.MerchantCheckout, error) {
	return agencydomain.MerchantCheckout{}, errors.New("unused")
}
func (c *cleanupCheckout) Cancel(context.Context, MerchantRequest) error {
	c.calls++
	if c.calls == 1 {
		return fault.New(fault.ProviderUnavailable, "SHOPIFY_CANCEL_TEMPORARY", true)
	}
	return nil
}

func TestOrderSheetCleanupResumesDurablyAfterWorkerRestart(t *testing.T) {
	now := time.Date(2026, 8, 15, 3, 0, 0, 0, time.UTC)
	repository := &cleanupRepository{
		available: true,
		task: agencydomain.OrderSheetSession{
			ID: "sheet-1", UserID: "user-1",
			Lines: []agencydomain.ExactLine{{LineID: "line-1"}},
			MerchantCheckouts: []agencydomain.MerchantCheckout{{
				ShopDomain: "shop.example", StorefrontCartSafeRef: "safe-cart",
				BuyerContextSafeRef: "safe-buyer",
			}},
		},
	}
	checkout := &cleanupCheckout{}
	firstWorker := NewOrderSheetCleanupService(repository, checkout, testClock{now})
	if err := firstWorker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repository.retries != 1 || repository.completed != 0 {
		t.Fatalf("first attempt retries=%d completed=%d", repository.retries, repository.completed)
	}

	// A new service instance represents a process restart. The repository owns
	// the pending claim, so the same deterministic provider cleanup resumes.
	secondWorker := NewOrderSheetCleanupService(
		repository, checkout, testClock{now.Add(time.Minute)},
	)
	if err := secondWorker.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if checkout.calls != 2 || repository.retries != 1 || repository.completed != 1 {
		t.Fatalf("calls=%d retries=%d completed=%d", checkout.calls, repository.retries, repository.completed)
	}
}
