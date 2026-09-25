package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

// SUPPORT executor의 owner 사실 조회다(ADR-0070 §4.5). marker 열 없이 참조
// id로 읽는다 — 발행 멱등성은 Support 카드 key와 Effect 원장이 진다.

func (r *Repository) GetMOCompensationNotification(
	ctx context.Context,
	compensationID string,
) (paymentapp.MOCompensationNotification, error) {
	var item paymentapp.MOCompensationNotification
	var completedAt sql.NullTime
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+moCompensationColumns+`,merchant_order.id::text
		FROM payment_mo_compensations compensation
		JOIN merchant_orders merchant_order
		  ON merchant_order.allocation_id=compensation.allocation_id
		WHERE compensation.id=$1
	`, compensationID).Scan(
		&item.Compensation.ID, &item.Compensation.AllocationID,
		&item.Compensation.FundingPositionID, &item.Compensation.AgencyOrderID,
		&item.Compensation.CustomerPaymentID, &item.Compensation.Rail,
		&item.Compensation.ProviderEnvironment, &item.Compensation.Action,
		&item.Compensation.Cause, &item.Compensation.State,
		&item.Compensation.AmountMinor, &item.Compensation.Currency,
		&item.Compensation.ExecutionProfileHash,
		&item.Compensation.ProviderResourceID,
		&item.Compensation.IdempotencyKey, &item.Compensation.Version,
		&item.Compensation.ApprovedAt, &completedAt,
		&item.Compensation.CreatedAt, &item.Compensation.UpdatedAt,
		&item.MerchantOrderID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return paymentapp.MOCompensationNotification{}, domain.ErrCompensationNotAvailable
	}
	if err != nil {
		return paymentapp.MOCompensationNotification{}, fmt.Errorf("get MO compensation notification: %w", err)
	}
	if completedAt.Valid {
		item.Compensation.CompletedAt = &completedAt.Time
	}
	return item, nil
}

func (r *Repository) GetPayPalDisputeCaseByID(
	ctx context.Context,
	caseID string,
) (domain.PayPalDisputeCase, error) {
	item, err := scanDisputeCase(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+disputeCaseColumns+`
		FROM payment_paypal_dispute_cases dispute
		WHERE dispute.id=$1
	`, caseID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PayPalDisputeCase{}, domain.ErrDisputeNotFound
	}
	if err != nil {
		return domain.PayPalDisputeCase{}, fmt.Errorf("get PayPal dispute case: %w", err)
	}
	return item, nil
}

func (r *Repository) GetPayPalDisputeActionByID(
	ctx context.Context,
	actionID string,
) (domain.PayPalDisputeManualAction, error) {
	item, err := scanDisputeAction(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT `+disputeActionColumns+`
		FROM payment_paypal_dispute_manual_actions action
		WHERE action.id=$1
	`, actionID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PayPalDisputeManualAction{}, domain.ErrDisputeNotFound
	}
	if err != nil {
		return domain.PayPalDisputeManualAction{}, fmt.Errorf("get PayPal dispute action: %w", err)
	}
	return item, nil
}
