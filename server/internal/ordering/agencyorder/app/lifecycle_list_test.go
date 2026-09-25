package app

import (
	"context"
	"testing"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

type lifecycleOwnerRepository struct{}

func (lifecycleOwnerRepository) GetProjection(_ context.Context, userID, _ string) (agencydomain.Projection, error) {
	if userID != "owner-1" {
		return agencydomain.Projection{}, agencydomain.ErrNotFound
	}
	return agencydomain.Projection{AgencyOrder: agencydomain.AgencyOrder{
		ShippingAddress: agencydomain.ShippingSnapshot{SnapshotRef: "snapshot-1"},
	}}, nil
}
func (lifecycleOwnerRepository) ListProjections(context.Context, string, ListQuery) (ListPage, error) {
	return ListPage{}, nil
}
func (lifecycleOwnerRepository) RecordDelayRuleNotice(context.Context, string, string, time.Time) error {
	return nil
}
func (lifecycleOwnerRepository) ResolveOrderOwner(context.Context, string) (string, error) {
	return "owner-1", nil
}
func (lifecycleOwnerRepository) GetProjectionForReceipt(context.Context, string) (agencydomain.Projection, error) {
	return agencydomain.Projection{}, nil
}
func (lifecycleOwnerRepository) ListFinalizedTransactions(context.Context, string) ([]agencydomain.ChainTransaction, error) {
	return nil, nil
}
func (lifecycleOwnerRepository) CreateReceipt(context.Context, agencydomain.Receipt) error {
	return nil
}
func (lifecycleOwnerRepository) GetReceipt(context.Context, string, string) (agencydomain.Receipt, error) {
	return agencydomain.Receipt{}, nil
}

func (lifecycleOwnerRepository) CreateRefundRequest(context.Context, agencydomain.RefundRequest, RefundAuthority, time.Time) (agencydomain.RefundRequest, error) {
	return agencydomain.RefundRequest{}, nil
}

func (lifecycleOwnerRepository) ListRefundRequests(context.Context, string, string, bool, int) ([]agencydomain.RefundRequest, error) {
	return nil, nil
}

func (lifecycleOwnerRepository) ListResolvedRefundRequests(context.Context, int) ([]agencydomain.RefundRequest, error) {
	return nil, nil
}

func (lifecycleOwnerRepository) CountOpenRefundRequests(context.Context) (int, error) {
	return 0, nil
}

func (lifecycleOwnerRepository) DecideRefundRequest(context.Context, string, string, RefundDecision, time.Time) (agencydomain.RefundRequest, error) {
	return agencydomain.RefundRequest{}, nil
}

type lifecycleOwnerShipping struct{ calls int }

func (s *lifecycleOwnerShipping) RevealShippingSnapshot(context.Context, string) (OperatorShippingAddress, error) {
	s.calls++
	return OperatorShippingAddress{Country: "US"}, nil
}

func TestAgencyOrderListQueryRequiresMatchingCursorScope(t *testing.T) {
	query := ListQuery{
		View: ListViewFinished, Sort: ListSortCreatedDesc, Limit: 30,
		Cursor: &ListCursor{
			View: ListViewFinished, Sort: ListSortCreatedDesc,
			IssuedAt:      time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC),
			AgencyOrderID: "c5e88ca9-ade4-477f-a6d7-f82103629e30",
		},
	}
	if !validListQuery(query) {
		t.Fatal("matching AgencyOrder cursor must be valid")
	}
	query.Cursor.View = ListViewInProgress
	if validListQuery(query) {
		t.Fatal("cursor from a different saved view must be rejected")
	}
}

func TestAgencyOrderUpdatedSortRequiresUpdatedCursorTime(t *testing.T) {
	query := ListQuery{
		View: ListViewAll, Sort: ListSortUpdatedDesc, Limit: 30,
		Cursor: &ListCursor{
			View: ListViewAll, Sort: ListSortUpdatedDesc,
			IssuedAt:      time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC),
			AgencyOrderID: "c5e88ca9-ade4-477f-a6d7-f82103629e30",
		},
	}
	if validListQuery(query) {
		t.Fatal("UPDATED_DESC cursor without updatedAt must be rejected")
	}
}

func TestRevealOwnerShippingChecksAgencyOrderOwnershipBeforePIIRead(t *testing.T) {
	shipping := &lifecycleOwnerShipping{}
	service := NewLifecycleService(lifecycleOwnerRepository{}, shipping, nil, nil)
	if _, err := service.RevealOwnerShipping(context.Background(), "other-user", "order-1"); err == nil {
		t.Fatal("cross-owner reveal must fail")
	}
	if shipping.calls != 0 {
		t.Fatal("shipping snapshot must not be read before ownership succeeds")
	}
	address, err := service.RevealOwnerShipping(context.Background(), "owner-1", "order-1")
	if err != nil || address.Country != "US" || shipping.calls != 1 {
		t.Fatalf("address=%+v calls=%d err=%v", address, shipping.calls, err)
	}
}
