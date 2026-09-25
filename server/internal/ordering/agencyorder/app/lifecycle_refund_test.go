package app

import (
	"context"
	"testing"
	"time"

	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

type refundLifecycleRepository struct {
	lifecycleOwnerRepository
	createdRequest  agencydomain.RefundRequest
	decision        RefundDecision
	resolvedRequest agencydomain.RefundRequest
}

func (r *refundLifecycleRepository) CreateRefundRequest(
	_ context.Context, request agencydomain.RefundRequest, _ RefundAuthority, _ time.Time,
) (agencydomain.RefundRequest, error) {
	r.createdRequest = request
	request.AllocationID = "allocation-1"
	request.RequestedGrossAmount = agencydomain.Money{AmountMinor: 4671, Currency: "USD"}
	return request, nil
}

func (r *refundLifecycleRepository) DecideRefundRequest(
	_ context.Context, _ string, _ string, decision RefundDecision, _ time.Time,
) (agencydomain.RefundRequest, error) {
	r.decision = decision
	return r.resolvedRequest, nil
}

func TestRequestRefundRequiresWholeMerchantOrderAndRationale(t *testing.T) {
	repository := &refundLifecycleRepository{}
	service := NewLifecycleService(repository, nil, testClock{now: time.Now()},
		&testIDs{values: []string{"request-1"}})

	if _, err := service.RequestRefund(context.Background(), "user-1", "order-1", "",
		"ITEM_DAMAGED_DEFECTIVE", "damaged", RefundAuthority{}); err != agencydomain.ErrRefundRequestInvalid {
		t.Fatalf("blank MerchantOrder must fail, got %v", err)
	}
	if _, err := service.RequestRefund(context.Background(), "user-1", "order-1", "mo-1",
		"ITEM_DAMAGED_DEFECTIVE", "   ", RefundAuthority{}); err != agencydomain.ErrRefundRequestInvalid {
		t.Fatalf("blank rationale must fail, got %v", err)
	}

	created, err := service.RequestRefund(context.Background(), "user-1", "order-1", " mo-1 ",
		"ITEM_DAMAGED_DEFECTIVE", "  The delivered item has a cracked case.  ", RefundAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	if created.MerchantOrderID != "mo-1" || created.AllocationID != "allocation-1" ||
		created.RequestedGrossAmount.AmountMinor != 4671 ||
		created.PublicRationale != "The delivered item has a cracked case." {
		t.Fatalf("request not normalized/resolved: %+v", created)
	}
}

func TestDecideRefundIsOneWholeMOOutcome(t *testing.T) {
	repository := &refundLifecycleRepository{}
	service := NewLifecycleService(repository, nil, testClock{now: time.Now()}, &testIDs{})
	if _, err := service.DecideRefund(context.Background(), "request-1", "operator-1",
		RefundDecision{Approve: true}); err != agencydomain.ErrRefundRequestInvalid {
		t.Fatalf("blank rationale must fail, got %v", err)
	}
	_, err := service.DecideRefund(context.Background(), "request-1", "operator-1", RefundDecision{
		Approve: true, PublicRationale: "  Photos confirm damage.  ",
		InternalNote: "  Carrier evidence OPS-17  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !repository.decision.Approve || repository.decision.PublicRationale != "Photos confirm damage." ||
		repository.decision.InternalNote != "Carrier evidence OPS-17" {
		t.Fatalf("decision not normalized: %+v", repository.decision)
	}
}

// 환불 사실은 owner transaction에 commit되고, 고객 카드는 리듀서가 이벤트에서
// 발행한다(ADR-0070 §4.5) — 서비스는 대화 어댑터를 더 이상 알지 않는다.
func TestRefundFactsCommitWithoutInlineConversation(t *testing.T) {
	repository := &refundLifecycleRepository{resolvedRequest: agencydomain.RefundRequest{
		ID: "request-1", AgencyOrderID: "order-1", MerchantOrderID: "mo-1", UserID: "user-1",
		State: agencydomain.RefundRequestResolved, Decision: agencydomain.RefundDecisionApproved,
		DecisionPublicRationale: "The delivery evidence supports a refund.", DecidedBy: "operator-1",
	}}
	service := NewLifecycleService(repository, nil, testClock{now: time.Now()},
		&testIDs{values: []string{"request-1"}})
	created, err := service.RequestRefund(context.Background(), "user-1", "order-1", "mo-1",
		"ITEM_DAMAGED_DEFECTIVE", "The case arrived cracked.", RefundAuthority{})
	if err != nil || created.ID != "request-1" || repository.createdRequest.MerchantOrderID != "mo-1" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	resolved, err := service.DecideRefund(context.Background(), "request-1", "operator-1",
		RefundDecision{Approve: true, PublicRationale: "The evidence supports a refund."})
	if err != nil || resolved.ID != "request-1" || !repository.decision.Approve {
		t.Fatalf("resolved=%+v decision=%+v err=%v", resolved, repository.decision, err)
	}
}
