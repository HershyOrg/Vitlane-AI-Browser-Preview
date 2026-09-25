package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

type accountingRecoveryRow struct {
	ID          string    `json:"id"`
	Cause       string    `json:"cause"`
	AmountMinor int64     `json:"amountMinor"`
	OccurredAt  time.Time `json:"occurredAt"`
}

const accountingProjectionSelect = `
	SELECT orders.id::text, payment.id::text, payment.rail,
	       payment.provider_environment, payment.state, payment.currency,
	       orders.issued_at,
	       allocation.id::text, allocation.checkout_ordinal,
	       allocation.shop_domain, allocation.pass_through_minor,
	       allocation.fee_variable_minor, allocation.fee_fixed_minor,
	       allocation.fee_total_minor, allocation.customer_gross_minor,
	       allocation.fee_policy_version, funding.state,
	       COALESCE(merchant_order.id::text,''),
	       COALESCE(merchant_order.state,''),
	       COALESCE(cash.id::text,''),
	       COALESCE(cash.gross_minor,0),
	       COALESCE(cash.economics_reconciled,FALSE),
	       COALESCE(cash.processor_fee_minor,0),
	       COALESCE(cash.net_receivable_minor,0),
	       COALESCE(cash.occurred_at,orders.issued_at),
	       COALESCE(whole_cash.id::text,''),
	       COALESCE(whole_cash.amount_minor,0),
	       COALESCE(whole_cash.occurred_at,orders.issued_at),
	       COALESCE(merchant_payment.id::text,''),
	       COALESCE(merchant_payment.state,''),
	       COALESCE(merchant_payment.amount_minor,0),
	       COALESCE(merchant_payment.updated_at,orders.issued_at),
	       COALESCE(compensation.id::text,''),
	       COALESCE(compensation.action,''),
	       COALESCE(compensation.cause,''),
	       COALESCE(compensation.state,''),
	       COALESCE(compensation.amount_minor,0),
	       COALESCE(compensation.completed_at,compensation.updated_at,orders.issued_at),
	       CASE
	         WHEN compensation.id IS NOT NULL THEN compensation.cause
	         WHEN EXISTS (
	           SELECT 1 FROM agency_order_refund_requests request
	           WHERE request.allocation_id=allocation.id
	             AND (request.state IN ('REQUESTED','REVIEWING')
	               OR (request.state='RESOLVED' AND request.decision='APPROVED'))
	         ) THEN 'CUSTOMER_REFUND_POST_EFFECT'
	         WHEN EXISTS (
	           SELECT 1
	           FROM logistics_delivery_resolutions resolution
	           JOIN logistics_expected_units expected
	             ON expected.id=resolution.expected_unit_id
	           WHERE expected.merchant_order_id=merchant_order.id
	             AND resolution.decision='REFUND'
	         ) THEN 'DELIVERY_EXCEPTION'
	         WHEN EXISTS (
	           SELECT 1 FROM agency_order_cancellations cancellation
	           WHERE cancellation.allocation_id=allocation.id
	             AND cancellation.kind='DELAY_RULE'
	         ) THEN 'DELAY_RULE'
	         WHEN EXISTS (
	           SELECT 1 FROM agency_order_cancellations cancellation
	           WHERE cancellation.allocation_id=allocation.id
	         ) THEN 'CUSTOMER_CANCEL_PRE_EFFECT'
	         WHEN merchant_order.state='FAILED' THEN 'PROCUREMENT_FAILURE'
	         ELSE ''
	       END,
	       COALESCE((
	         SELECT jsonb_agg(jsonb_build_object(
	           'id', recovery.id::text,
	           'cause', recovery.cause,
	           'amountMinor', recovery.received_amount_minor,
	           'occurredAt', recovery.updated_at
	         ) ORDER BY recovery.updated_at, recovery.id)
	         FROM procurement_recovery_entries recovery
	         WHERE recovery.merchant_order_id=merchant_order.id
	           AND recovery.received_amount_minor > 0
	       ), '[]'::jsonb)
	FROM selected_orders selected
	JOIN agency_orders orders ON orders.id=selected.id
	JOIN agency_order_mo_allocations allocation
	  ON allocation.agency_order_id=orders.id
	JOIN payment_mo_funding_positions funding
	  ON funding.allocation_id=allocation.id
	JOIN payment_customer_payments payment
	  ON payment.id=funding.customer_payment_id
	 AND payment.agency_order_id=orders.id
	 AND payment.provider_environment=orders.provider_environment
	LEFT JOIN merchant_orders merchant_order
	  ON merchant_order.allocation_id=allocation.id
	LEFT JOIN merchant_payments merchant_payment
	  ON merchant_payment.merchant_order_id=merchant_order.id
	LEFT JOIN payment_mo_cash_receipts cash
	  ON cash.funding_position_id=funding.id
	LEFT JOIN payment_mo_compensations compensation
	  ON compensation.funding_position_id=funding.id
	LEFT JOIN LATERAL (
	  SELECT receipt.id, receipt.amount_minor, receipt.occurred_at
	  FROM payment_funds_receipts receipt
	  WHERE payment.rail='GIWA'
	    AND receipt.customer_payment_id=payment.id
	    AND receipt.kind='GIWA_FINALIZED_PAY'
	    AND receipt.accepted
	  ORDER BY receipt.created_at DESC
	  LIMIT 1
	) whole_cash ON TRUE
	ORDER BY orders.issued_at DESC, orders.id, allocation.checkout_ordinal`

func scanOrderAccountingRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]domain.OrderAccountingInput, error) {
	orders := make([]domain.OrderAccountingInput, 0)
	orderIndex := make(map[string]int)
	for rows.Next() {
		var (
			order                   domain.OrderAccountingInput
			merchantOrder           domain.MOAccountingInput
			fundingState            string
			cashID                  string
			cashGross               int64
			cashReconciled          bool
			cashFee, cashNet        int64
			cashOccurred            time.Time
			wholeCashID             string
			wholeCashAmount         int64
			wholeCashOccurred       time.Time
			merchantPaymentID       string
			merchantPaymentState    string
			merchantPaymentAmount   int64
			merchantPaymentOccurred time.Time
			compensationID          string
			compensationAction      string
			compensationCause       string
			compensationState       string
			compensationAmount      int64
			compensationOccurred    time.Time
			recoveriesRaw           []byte
		)
		if err := rows.Scan(
			&order.AgencyOrderID, &order.CustomerPaymentID, &order.Rail,
			&order.ProviderEnvironment, &order.PaymentState, &order.Currency,
			&order.CreatedAt,
			&merchantOrder.AllocationID, &merchantOrder.CheckoutOrdinal,
			&merchantOrder.ShopDomain, &merchantOrder.PassThroughMinor,
			&merchantOrder.FeeVariableMinor, &merchantOrder.FeeFixedMinor,
			&merchantOrder.FeeTotalMinor, &merchantOrder.CustomerGrossMinor,
			&merchantOrder.FeePolicyVersion, &fundingState,
			&merchantOrder.MerchantOrderID, &merchantOrder.MerchantOrderState,
			&cashID, &cashGross, &cashReconciled, &cashFee, &cashNet, &cashOccurred,
			&wholeCashID, &wholeCashAmount, &wholeCashOccurred,
			&merchantPaymentID, &merchantPaymentState, &merchantPaymentAmount,
			&merchantPaymentOccurred,
			&compensationID, &compensationAction, &compensationCause,
			&compensationState, &compensationAmount, &compensationOccurred,
			&merchantOrder.ExceptionCause, &recoveriesRaw,
		); err != nil {
			return nil, err
		}
		merchantOrder.FundingState = domain.MOFundingState(fundingState)
		if cashID != "" {
			merchantOrder.Cash = &domain.AccountingCashInput{
				ID: cashID, GrossMinor: cashGross, EconomicsReconciled: cashReconciled,
				ProcessorFeeMinor: cashFee, NetReceivableMinor: cashNet,
				OccurredAt: cashOccurred,
			}
		}
		if merchantPaymentID != "" {
			merchantOrder.MerchantSpend = &domain.AccountingMerchantSpendInput{
				ID: merchantPaymentID, State: merchantPaymentState,
				AmountMinor: merchantPaymentAmount, OccurredAt: merchantPaymentOccurred,
			}
		}
		if compensationID != "" {
			merchantOrder.Compensation = &domain.AccountingCompensationInput{
				ID: compensationID, Action: compensationAction, Cause: compensationCause,
				State: compensationState, AmountMinor: compensationAmount,
				OccurredAt: compensationOccurred,
			}
		}
		var recoveryRows []accountingRecoveryRow
		if err := json.Unmarshal(recoveriesRaw, &recoveryRows); err != nil {
			return nil, fmt.Errorf("decode order accounting recoveries: %w", err)
		}
		merchantOrder.Recoveries = make([]domain.AccountingRecoveryInput, 0, len(recoveryRows))
		for _, recovery := range recoveryRows {
			merchantOrder.Recoveries = append(merchantOrder.Recoveries,
				domain.AccountingRecoveryInput{
					ID: recovery.ID, Cause: recovery.Cause,
					AmountMinor: recovery.AmountMinor, OccurredAt: recovery.OccurredAt,
				},
			)
		}

		index, found := orderIndex[order.AgencyOrderID]
		if !found {
			order.MerchantOrders = make([]domain.MOAccountingInput, 0)
			if wholeCashID != "" {
				order.OrderCash = &domain.AccountingCashInput{
					ID: wholeCashID, GrossMinor: wholeCashAmount,
					EconomicsReconciled: true, NetReceivableMinor: wholeCashAmount,
					OccurredAt: wholeCashOccurred,
				}
			}
			orders = append(orders, order)
			index = len(orders) - 1
			orderIndex[order.AgencyOrderID] = index
		} else if existingOrder := orders[index]; existingOrder.CustomerPaymentID != order.CustomerPaymentID ||
			existingOrder.Rail != order.Rail ||
			existingOrder.ProviderEnvironment != order.ProviderEnvironment ||
			existingOrder.Currency != order.Currency {
			return nil, fmt.Errorf("inconsistent order accounting identity for %s",
				order.AgencyOrderID)
		} else if wholeCashID != "" {
			existing := orders[index].OrderCash
			if existing == nil || existing.ID != wholeCashID ||
				existing.GrossMinor != wholeCashAmount {
				return nil, fmt.Errorf("inconsistent whole-order cash receipt for %s",
					order.AgencyOrderID)
			}
		}
		orders[index].MerchantOrders = append(orders[index].MerchantOrders, merchantOrder)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orders, nil
}

func (r *Repository) listOrderAccountingInputs(
	ctx context.Context,
	filter string,
	value string,
	limit int,
) ([]domain.OrderAccountingInput, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	var selector string
	var args []any
	switch filter {
	case "environment":
		selector = `orders.provider_environment=$1`
		args = []any{strings.ToUpper(strings.TrimSpace(value)), limit}
	case "order":
		selector = `orders.id=$1`
		args = []any{strings.TrimSpace(value), 1}
	default:
		return nil, domain.ErrInvalid
	}
	query := `WITH selected_orders AS (
		SELECT orders.id
		FROM agency_orders orders
		WHERE ` + selector + `
		  AND EXISTS (
		    SELECT 1 FROM agency_order_mo_allocations allocation
		    WHERE allocation.agency_order_id=orders.id
		  )
		ORDER BY orders.issued_at DESC, orders.id
		LIMIT $2
	)` + accountingProjectionSelect
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list order accounting inputs: %w", err)
	}
	defer rows.Close()
	inputs, err := scanOrderAccountingRows(rows)
	if err != nil {
		return nil, fmt.Errorf("scan order accounting inputs: %w", err)
	}
	return inputs, nil
}

func (r *Repository) GetOrderAccountingInput(
	ctx context.Context,
	agencyOrderID string,
) (domain.OrderAccountingInput, bool, error) {
	inputs, err := r.listOrderAccountingInputs(ctx, "order", agencyOrderID, 1)
	if err != nil {
		return domain.OrderAccountingInput{}, false, err
	}
	if len(inputs) == 0 {
		return domain.OrderAccountingInput{}, false, nil
	}
	return inputs[0], true, nil
}

func (r *Repository) ListOrderAccountingInputs(
	ctx context.Context,
	providerEnvironment string,
	limit int,
) ([]domain.OrderAccountingInput, error) {
	inputs, err := r.listOrderAccountingInputs(
		ctx, "environment", providerEnvironment, limit,
	)
	return inputs, err
}
