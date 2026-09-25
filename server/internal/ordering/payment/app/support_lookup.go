package app

import (
	"context"
	"strings"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

// MOCompensationNotification is the public-safe projection of a completed
// whole-MO release/refund. Customer communication consumes this committed
// fact; it never drives or authorizes the money effect.
type MOCompensationNotification struct {
	Compensation    domain.MOCompensation `json:"compensation"`
	MerchantOrderID string                `json:"merchantOrderId"`
}

// supportLookupRepository는 SUPPORT executor의 owner 사실 조회다(ADR-0070 §4.5).
// 카드 발행은 리듀서가 이벤트에서 결정하고, executor는 참조 id로 commit된
// 사실을 읽어 payload를 만든다 — Payment는 대화 어댑터를 알지 않는다.
type supportLookupRepository interface {
	GetMOCompensationNotification(context.Context, string) (MOCompensationNotification, error)
	GetPayPalDisputeCaseByID(context.Context, string) (domain.PayPalDisputeCase, error)
	GetPayPalDisputeActionByID(context.Context, string) (domain.PayPalDisputeManualAction, error)
}

func (s *Service) GetMOCompensationNotification(
	ctx context.Context,
	compensationID string,
) (MOCompensationNotification, error) {
	lookup, ok := s.repository.(supportLookupRepository)
	if !ok {
		return MOCompensationNotification{}, domain.ErrCompensationNotAvailable
	}
	return lookup.GetMOCompensationNotification(ctx, strings.TrimSpace(compensationID))
}

func (s *Service) GetPayPalDisputeCaseByID(
	ctx context.Context,
	caseID string,
) (domain.PayPalDisputeCase, error) {
	lookup, ok := s.repository.(supportLookupRepository)
	if !ok {
		return domain.PayPalDisputeCase{}, domain.ErrDisputeNotFound
	}
	return lookup.GetPayPalDisputeCaseByID(ctx, strings.TrimSpace(caseID))
}

func (s *Service) GetPayPalDisputeActionByID(
	ctx context.Context,
	actionID string,
) (domain.PayPalDisputeManualAction, error) {
	lookup, ok := s.repository.(supportLookupRepository)
	if !ok {
		return domain.PayPalDisputeManualAction{}, domain.ErrDisputeNotFound
	}
	return lookup.GetPayPalDisputeActionByID(ctx, strings.TrimSpace(actionID))
}
