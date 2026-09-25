package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/vitlane/vitlane/server/internal/ordering/policy"
	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

// SUPPORT executor의 owner 사실 조회다(ADR-0070 §4.5) — 카드 payload는 commit된
// 행에서 만들고, 발행 멱등성은 Support 카드 key와 Effect 원장이 진다.

func (r *Repository) GetCancellationSupport(
	ctx context.Context,
	merchantOrderID string,
) (procurementapp.CancellationSupportProjection, bool, error) {
	var item procurementapp.CancellationSupportProjection
	var refundBasis string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT agency_order_id::text, merchant_order_id::text, user_id::text,
		       kind, refund_basis, created_at
		FROM agency_order_cancellations
		WHERE merchant_order_id=$1
	`, merchantOrderID).Scan(
		&item.AgencyOrderID, &item.MerchantOrderID, &item.UserID, &item.Kind,
		&refundBasis, &item.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return procurementapp.CancellationSupportProjection{}, false, nil
		}
		return procurementapp.CancellationSupportProjection{}, false,
			fmt.Errorf("get cancellation projection: %w", err)
	}
	item.RefundBasis = policy.RefundBasis(refundBasis)
	return item, true, nil
}

func (r *Repository) GetDecisionRecord(
	ctx context.Context,
	decisionID string,
) (domain.DecisionRecord, error) {
	record, err := scanDecision(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+decisionColumns+` FROM procurement_decision_records WHERE id=$1
	`, decisionID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DecisionRecord{}, domain.ErrRequestNotFound
	}
	if err != nil {
		return domain.DecisionRecord{}, fmt.Errorf("get decision record: %w", err)
	}
	return record, nil
}

func (r *Repository) GetCustomerRequest(
	ctx context.Context,
	requestID string,
) (domain.CustomerRequest, error) {
	request, err := scanCustomerRequest(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+customerRequestColumns+` FROM procurement_customer_requests WHERE id=$1
	`, requestID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CustomerRequest{}, domain.ErrRequestNotFound
	}
	if err != nil {
		return domain.CustomerRequest{}, fmt.Errorf("get customer request: %w", err)
	}
	return request, nil
}
