package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

func (r *Repository) GetProjection(
	ctx context.Context,
	userID, agencyOrderID string,
) (agencydomain.Projection, error) {
	order, instruction, err := r.GetOrder(ctx, userID, agencyOrderID)
	if err != nil {
		return agencydomain.Projection{}, err
	}
	projection := agencydomain.Projection{
		AgencyOrder: order, PaymentInstruction: instruction,
		ChainTransactions: []agencydomain.ChainTransaction{},
		MerchantOrders:    []agencydomain.MerchantOrderSummary{},
		Shipments:         []agencydomain.ShipmentSummary{},
	}
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT agency_order_id, state, COALESCE(terminal_reason,''), version,
		       COALESCE(last_reason_code,''), created_at, updated_at
		FROM agency_order_processes
		WHERE agency_order_id=$1
	`, agencyOrderID).Scan(
		&projection.Process.AgencyOrderID, &projection.Process.State,
		&projection.Process.TerminalReason,
		&projection.Process.Version, &projection.Process.LastReasonCode,
		&projection.Process.CreatedAt, &projection.Process.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// 생성은 Manager가 order.issued 소비로 수행한다(ADR-0056 §1) — 발행
		// 직후 ≤1 tick의 공백은 발행 사실에서 결정되는 초기 상태를 합성한다.
		projection.Process = agencydomain.Process{
			AgencyOrderID: order.ID,
			State:         agencydomain.ProcessWaitingCustomerPayment,
			Version:       0,
			CreatedAt:     order.IssuedAt,
			UpdatedAt:     order.IssuedAt,
		}
	} else if err != nil {
		return agencydomain.Projection{}, fmt.Errorf("get AgencyOrder process: %w", err)
	}

	payment, found, err := r.getAgencyOrderPaymentProjection(ctx, agencyOrderID)
	if err != nil {
		return agencydomain.Projection{}, err
	}
	if found {
		projection.Payment = &payment
		projection.ChainTransactions, err = r.listTransactions(ctx, payment.ID, false)
		if err != nil {
			return agencydomain.Projection{}, err
		}
	}
	projection.MerchantOrders, err = r.listMerchantOrderSummaries(ctx, agencyOrderID)
	if err != nil {
		return agencydomain.Projection{}, err
	}
	projection.Shipments, err = r.listShipmentSummaries(ctx, agencyOrderID)
	if err != nil {
		return agencydomain.Projection{}, err
	}
	projection.Notices, err = r.listOrderNotices(ctx, agencyOrderID)
	if err != nil {
		return agencydomain.Projection{}, err
	}
	unitFacts, err := r.listUnitFacts(ctx, agencyOrderID)
	if err != nil {
		return agencydomain.Projection{}, err
	}
	projection.Units = agencydomain.ComposeUnitViews(unitFacts)
	projectMerchantOrderOperations(projection.MerchantOrders, projection.Units)
	projection.RefundRequests, err = r.ListRefundRequests(ctx, userID, agencyOrderID, false, 20)
	if err != nil {
		return agencydomain.Projection{}, err
	}
	receipt, found, err := r.getReceipt(ctx, agencyOrderID)
	if err != nil {
		return agencydomain.Projection{}, err
	}
	if found {
		projection.Receipt = &receipt
	}
	return projection, nil
}

func (r *Repository) ListProjections(
	ctx context.Context,
	userID string,
	query agencyapp.ListQuery,
) (agencyapp.ListPage, error) {
	counts, err := r.countListViews(ctx, userID)
	if err != nil {
		return agencyapp.ListPage{}, err
	}
	where, order, args := agencyOrderListClauses(userID, query)
	args = append(args, query.Limit+1)
	statement := fmt.Sprintf(`
		SELECT orders.id, orders.issued_at, process.updated_at
		FROM agency_orders orders
		LEFT JOIN agency_order_processes live ON live.agency_order_id=orders.id,
		LATERAL (
			-- Manager 생성 전(발행 직후 ≤1 tick)에는 초기 상태를 합성한다.
			SELECT COALESCE(live.state,'WAITING_CUSTOMER_PAYMENT') AS state,
			       live.terminal_reason,
			       COALESCE(live.updated_at, orders.issued_at) AS updated_at
		) process
		WHERE %s
		ORDER BY %s
		LIMIT $%d
	`, where, order, len(args))
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, statement, args...)
	if err != nil {
		return agencyapp.ListPage{}, fmt.Errorf("list AgencyOrders: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0, query.Limit+1)
	cursors := make([]agencyapp.ListCursor, 0, query.Limit+1)
	for rows.Next() {
		var id string
		var issuedAt, updatedAt time.Time
		if err := rows.Scan(&id, &issuedAt, &updatedAt); err != nil {
			return agencyapp.ListPage{}, err
		}
		ids = append(ids, id)
		cursors = append(cursors, agencyapp.ListCursor{
			View: query.View, Sort: query.Sort, UpdatedAt: updatedAt,
			IssuedAt: issuedAt, AgencyOrderID: id,
		})
	}
	if err := rows.Err(); err != nil {
		return agencyapp.ListPage{}, err
	}
	page := agencyapp.ListPage{Counts: counts}
	if len(ids) > query.Limit {
		ids = ids[:query.Limit]
		next := cursors[query.Limit-1]
		page.NextCursor = &next
	}
	result := make([]agencydomain.Projection, 0, len(ids))
	for _, id := range ids {
		projection, err := r.GetProjection(ctx, userID, id)
		if err != nil {
			return agencyapp.ListPage{}, err
		}
		result = append(result, projection)
	}
	page.Items = result
	return page, nil
}

// PAYMENT_REQUIRED는 고객 행동(PAY)이 필요한 유일한 view다(ADR-0057 D1) —
// 진행 중에서 분리한다.
const agencyOrderPaymentRequiredStates = "'WAITING_CUSTOMER_PAYMENT'"
const agencyOrderInProgressStates = "'PROCUREMENT_IN_PROGRESS','LOGISTICS_IN_PROGRESS','RESOLUTION_IN_PROGRESS'"
const agencyOrderAttentionStates = "'PAYMENT_RECONCILIATION','ATTENTION_REQUIRED'"
const agencyOrderFinishedPredicate = "process.state='TERMINAL'"

func agencyOrderListClauses(
	userID string,
	query agencyapp.ListQuery,
) (string, string, []any) {
	args := []any{userID}
	where := []string{"orders.user_id=$1", agencyOrderListViewPredicate(query.View)}
	bind := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if query.Cursor != nil {
		issuedAt := bind(query.Cursor.IssuedAt)
		orderID := bind(query.Cursor.AgencyOrderID)
		switch query.Sort {
		case agencyapp.ListSortCreatedAsc:
			where = append(where, fmt.Sprintf(
				"(orders.issued_at,orders.id) > (%s,%s::uuid)", issuedAt, orderID,
			))
		case agencyapp.ListSortCreatedDesc:
			where = append(where, fmt.Sprintf(
				"(orders.issued_at,orders.id) < (%s,%s::uuid)", issuedAt, orderID,
			))
		default:
			updatedAt := bind(query.Cursor.UpdatedAt)
			where = append(where, fmt.Sprintf(
				"(process.updated_at,orders.issued_at,orders.id) < (%s,%s,%s::uuid)",
				updatedAt, issuedAt, orderID,
			))
		}
	}
	order := "process.updated_at DESC, orders.issued_at DESC, orders.id DESC"
	switch query.Sort {
	case agencyapp.ListSortCreatedDesc:
		order = "orders.issued_at DESC, orders.id DESC"
	case agencyapp.ListSortCreatedAsc:
		order = "orders.issued_at ASC, orders.id ASC"
	}
	return strings.Join(where, " AND "), order, args
}

func agencyOrderListViewPredicate(view agencyapp.ListView) string {
	switch view {
	case agencyapp.ListViewPaymentRequired:
		return "process.state IN (" + agencyOrderPaymentRequiredStates + ")"
	case agencyapp.ListViewNeedsAttention:
		return "process.state IN (" + agencyOrderAttentionStates + ")"
	case agencyapp.ListViewFinished:
		return agencyOrderFinishedPredicate
	case agencyapp.ListViewAll:
		return "TRUE"
	default:
		return "process.state IN (" + agencyOrderInProgressStates + ")"
	}
}

func (r *Repository) countListViews(
	ctx context.Context,
	userID string,
) (agencyapp.ListCounts, error) {
	var counts agencyapp.ListCounts
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
			count(*) FILTER (WHERE process.state IN (`+agencyOrderPaymentRequiredStates+`)),
			count(*) FILTER (WHERE process.state IN (`+agencyOrderInProgressStates+`)),
			count(*) FILTER (WHERE process.state IN (`+agencyOrderAttentionStates+`)),
			count(*) FILTER (WHERE `+agencyOrderFinishedPredicate+`),
			count(*)
		FROM agency_orders orders
		LEFT JOIN agency_order_processes live ON live.agency_order_id=orders.id,
		LATERAL (
			SELECT COALESCE(live.state,'WAITING_CUSTOMER_PAYMENT') AS state,
			       live.terminal_reason
		) process
		WHERE orders.user_id=$1
	`, userID).Scan(
		&counts.PaymentRequired, &counts.InProgress, &counts.NeedsAttention,
		&counts.Finished, &counts.All,
	)
	if err != nil {
		return agencyapp.ListCounts{}, fmt.Errorf("count AgencyOrder list views: %w", err)
	}
	return counts, nil
}

func (r *Repository) getAgencyOrderPaymentProjection(
	ctx context.Context,
	agencyOrderID string,
) (agencydomain.PaymentProjection, bool, error) {
	var value agencydomain.PaymentProjection
	var safeBlock, finalizedBlock sql.NullInt64
	var updatedAt sql.NullTime
	// GIWA도 payment 슬림 코어에 합류했으므로(ADR-0050) 부분환불 terminal과
	// 환불 누계는 중립 CustomerPayment에서 함께 읽는다. whole-MO 보상 환불은
	// Settlement의 전체 refund tx가 아니라 compensation의 refundPartial tx로
	// 관찰되므로, 환불 참조는 payment.refund_tx_hash가 비어 있을 때 최신 SUCCEEDED
	// TVIT_REFUND compensation의 tx hash로 채운다(REFUNDED_ALL 영수증의 terminal
	// 참조 — PayPal projection의 refund id 파생과 동형).
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT payment.id, payment.state, payment.amount_base_units::text,
		       COALESCE(payment.pay_tx_hash,''), COALESCE(payment.complete_tx_hash,''),
		       COALESCE(NULLIF(payment.refund_tx_hash,''), compensation_refund.provider_resource_id, ''),
		       payment.safe_block, payment.finalized_block,
		       COALESCE(payment.last_reason_code,''), payment.updated_at,
		       COALESCE((
		           SELECT SUM(compensation.amount_minor)
		           FROM payment_mo_compensations compensation
		           WHERE compensation.customer_payment_id=pcp.id
		             AND compensation.state='SUCCEEDED'
		       ),0)
		FROM settlement_payments payment
		LEFT JOIN payment_customer_payments pcp
		  ON pcp.agency_order_id=payment.agency_order_id
		 AND pcp.rail='GIWA' AND pcp.state IN ('CAPTURED','CLOSED')
		LEFT JOIN LATERAL (
			SELECT provider_resource_id
			FROM payment_mo_compensations
			WHERE customer_payment_id=pcp.id
			  AND action='TVIT_REFUND' AND state='SUCCEEDED'
			  AND provider_resource_id IS NOT NULL
			ORDER BY updated_at DESC,id DESC LIMIT 1
		) compensation_refund ON TRUE
		WHERE payment.agency_order_id=$1
	`, agencyOrderID).Scan(
		&value.ID, &value.State, &value.AmountBaseUnits,
		&value.PayTxHash, &value.CompleteTxHash, &value.RefundTxHash,
		&safeBlock, &finalizedBlock, &value.LastReasonCode, &updatedAt,
		&value.RefundedTotalCent,
	)
	if err == nil {
		value.Rail = "GIWA"
		if safeBlock.Valid {
			block := uint64(safeBlock.Int64)
			value.SafeBlock = &block
		}
		if finalizedBlock.Valid {
			block := uint64(finalizedBlock.Int64)
			value.FinalizedBlock = &block
		}
		if updatedAt.Valid {
			value.UpdatedAt = &updatedAt.Time
		}
		return value, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return agencydomain.PaymentProjection{}, false, fmt.Errorf("get AgencyOrder payment projection: %w", err)
	}
	return r.getPayPalPaymentProjection(ctx, agencyOrderID)
}

// getPayPalPaymentProjection은 PayPal rail의 compact projection이다. Capture와
// 환불은 MO별로 여러 개일 수 있으므로 대표 reference와 정확한 완료 합계만
// 투영하며, 전체 증거 목록은 운영자 lookup/accounting projection이 소유한다.
func (r *Repository) getPayPalPaymentProjection(
	ctx context.Context,
	agencyOrderID string,
) (agencydomain.PaymentProjection, bool, error) {
	var value agencydomain.PaymentProjection
	var updatedAt sql.NullTime
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT payment.id::text, payment.state, payment.amount_minor::text,
		       COALESCE(payment.last_reason_code,''),
		       payment.updated_at,
		       COALESCE(attempt.paypal_order_id,''),
		       COALESCE(receipt.provider_capture_id,''),
		       COALESCE(compensation.provider_resource_id,''),
		       COALESCE((
		           SELECT SUM(done.amount_minor)
		           FROM payment_mo_compensations done
		           WHERE done.customer_payment_id=payment.id
		             AND done.action='REFUND' AND done.state='SUCCEEDED'
		       ),0)
		FROM payment_customer_payments payment
		LEFT JOIN LATERAL (
			SELECT paypal_order_id
			FROM payment_paypal_attempts
			WHERE customer_payment_id=payment.id
			ORDER BY sequence DESC LIMIT 1
		) attempt ON TRUE
		LEFT JOIN LATERAL (
			SELECT provider_capture_id
			FROM payment_mo_cash_receipts
			WHERE customer_payment_id=payment.id
			ORDER BY occurred_at DESC,id DESC LIMIT 1
		) receipt ON TRUE
		LEFT JOIN LATERAL (
			SELECT provider_resource_id
			FROM payment_mo_compensations
			WHERE customer_payment_id=payment.id
			  AND action='REFUND' AND provider_resource_id IS NOT NULL
			ORDER BY updated_at DESC,id DESC LIMIT 1
		) compensation ON TRUE
		WHERE payment.agency_order_id=$1
		ORDER BY CASE WHEN payment.state IN ('CLOSED','CAPTURED','PARTIALLY_CAPTURED','AUTHORIZED')
		              THEN 0 ELSE 1 END,
		         payment.updated_at DESC
		LIMIT 1
	`, agencyOrderID).Scan(
		&value.ID, &value.State, &value.AmountBaseUnits,
		&value.LastReasonCode, &updatedAt,
		&value.PayPalOrderID, &value.CaptureID, &value.PayPalRefundID,
		&value.RefundedTotalCent,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return agencydomain.PaymentProjection{}, false, nil
	}
	if err != nil {
		return agencydomain.PaymentProjection{}, false, fmt.Errorf("get PayPal payment projection: %w", err)
	}
	value.Rail = "PAYPAL"
	if updatedAt.Valid {
		value.UpdatedAt = &updatedAt.Time
	}
	return value, true, nil
}

// listMerchantOrderSummaries는 Procurement 원본 상태의 read-only 합성이다
// (계약 v7 §12.1 — 고객 사영은 owner 상태를 복제하지 않고 합성만 한다).
func (r *Repository) listMerchantOrderSummaries(
	ctx context.Context,
	agencyOrderID string,
) ([]agencydomain.MerchantOrderSummary, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT mo.id, mo.allocation_id, mo.shop_domain, mo.merchant_id, mo.checkout_ordinal,
		       allocation.customer_gross_minor, allocation.currency,
		       mo.execution_mode, mo.state, COALESCE(mo.failure_code,''),
		       COALESCE(funding.state,''), COALESCE(cancellation.kind,''),
		       COALESCE(refund.state,''), COALESCE(compensation.action,''),
		       COALESCE(compensation.state,''), COALESCE(dispute.state,''),
		       COALESCE(dispute.outcome,''), mo.updated_at,
		       COALESCE(jsonb_agg(jsonb_build_object(
		           'id', unit.id, 'lineId', unit.line_id, 'unitIndex', unit.unit_index,
		           'disposition', unit.disposition
		       ) ORDER BY unit.line_id, unit.unit_index)
		       FILTER (WHERE unit.id IS NOT NULL), '[]'::jsonb),
		       COALESCE(decision.phase,''), COALESCE(decision.intent_kind,''),
		       COALESCE(decision.intent_outcome,''), COALESCE(decision.intent_code,'')
		FROM merchant_orders mo
		JOIN agency_order_mo_allocations allocation ON allocation.id=mo.allocation_id
		LEFT JOIN merchant_order_units unit ON unit.merchant_order_id=mo.id
		LEFT JOIN agency_order_process_merchant_orders decision
		  ON decision.merchant_order_id=mo.id::text
		LEFT JOIN payment_mo_funding_positions funding ON funding.allocation_id=mo.allocation_id
		LEFT JOIN agency_order_cancellations cancellation ON cancellation.allocation_id=mo.allocation_id
		LEFT JOIN LATERAL (
			SELECT request.state FROM agency_order_refund_requests request
			WHERE request.allocation_id=mo.allocation_id
			ORDER BY request.created_at DESC LIMIT 1
		) refund ON TRUE
		LEFT JOIN payment_mo_compensations compensation ON compensation.allocation_id=mo.allocation_id
		LEFT JOIN LATERAL (
			SELECT dispute_case.state, dispute_case.outcome
			FROM payment_paypal_dispute_cases dispute_case
			JOIN payment_mo_cash_receipts receipt
			  ON receipt.id=dispute_case.mo_cash_receipt_id
			WHERE receipt.allocation_id=mo.allocation_id
			ORDER BY dispute_case.updated_at DESC LIMIT 1
		) dispute ON TRUE
		WHERE mo.agency_order_id=$1
		GROUP BY mo.id,allocation.id,funding.id,cancellation.id,refund.state,compensation.id,
		         dispute.state,dispute.outcome,decision.phase,decision.intent_kind,
		         decision.intent_outcome,decision.intent_code
		ORDER BY mo.checkout_ordinal
	`, agencyOrderID)
	if err != nil {
		return nil, fmt.Errorf("list merchant order summaries: %w", err)
	}
	defer rows.Close()
	result := make([]agencydomain.MerchantOrderSummary, 0)
	for rows.Next() {
		var summary agencydomain.MerchantOrderSummary
		var unitsPayload []byte
		var intentKind, intentOutcome, intentCode string
		if err := rows.Scan(
			&summary.ID, &summary.AllocationID, &summary.ShopDomain, &summary.MerchantID,
			&summary.CheckoutOrdinal, &summary.CustomerGrossAmount.AmountMinor,
			&summary.CustomerGrossAmount.Currency, &summary.ExecutionMode, &summary.State,
			&summary.FailureCode, &summary.FundingState, &summary.CancellationState,
			&summary.RefundRequestState, &summary.CompensationAction,
			&summary.CompensationState, &summary.DisputeState, &summary.DisputeOutcome,
			&summary.UpdatedAt, &unitsPayload, &summary.Phase,
			&intentKind, &intentOutcome, &intentCode,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(unitsPayload, &summary.Units); err != nil {
			return nil, err
		}
		if intentKind != "" && intentOutcome != "" {
			summary.CancelIntent = &agencydomain.MerchantOrderCancelIntent{
				Kind: intentKind, Outcome: intentOutcome, Code: intentCode,
			}
		}
		result = append(result, summary)
	}
	return result, rows.Err()
}

func projectMerchantOrderOperations(
	merchantOrders []agencydomain.MerchantOrderSummary,
	units []agencydomain.UnitView,
) {
	for index := range merchantOrders {
		merchantOrder := &merchantOrders[index]
		counts := agencydomain.CountMerchantOrderUnitStages(units, merchantOrder.ID)
		facts := agencydomain.MerchantOrderOperationalFacts{
			MerchantOrderState: merchantOrder.State,
			FundingState:       merchantOrder.FundingState,
			RefundRequestState: merchantOrder.RefundRequestState,
			CancellationState:  merchantOrder.CancellationState,
			CompensationAction: merchantOrder.CompensationAction,
			CompensationState:  merchantOrder.CompensationState,
			DisputeState:       merchantOrder.DisputeState,
			DisputeOutcome:     merchantOrder.DisputeOutcome,
			Units:              counts,
		}
		for _, unit := range units {
			if unit.MerchantOrderID != merchantOrder.ID {
				continue
			}
			if facts.ResolutionCause == "" && unit.ResolutionCause != "" {
				facts.ResolutionCause = unit.ResolutionCause
			}
			if facts.ResolutionDecision == "" && unit.ResolutionDecision != "" {
				facts.ResolutionDecision = unit.ResolutionDecision
			}
			if facts.ReturnState == "" && unit.ReturnState != "" {
				facts.ReturnState = unit.ReturnState
			}
		}
		merchantOrder.Operational = agencydomain.DeriveMerchantOrderOperationalProjection(facts)
	}
}

// listShipmentSummaries는 Logistics 원본 상태의 read-only 합성이다(계약 v7 §9).
// 실물 패키지 단위로 배정 unit의 fulfillment를 disposition 자리에 노출한다.
func (r *Repository) listShipmentSummaries(
	ctx context.Context,
	agencyOrderID string,
) ([]agencydomain.ShipmentSummary, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT shipment.id, shipment.merchant_order_id, shipment.carrier,
		       shipment.tracking_ref, shipment.state, shipment.updated_at,
		       COALESCE(jsonb_agg(jsonb_build_object(
		           'id', merchant_unit.id, 'lineId', expected.line_id,
		           'unitIndex', expected.unit_index, 'disposition', expected.fulfillment
		       ) ORDER BY expected.line_id, expected.unit_index)
		       FILTER (WHERE expected.id IS NOT NULL), '[]'::jsonb)
		FROM logistics_shipments shipment
		LEFT JOIN logistics_shipment_allocations allocation
		  ON allocation.shipment_id=shipment.id AND allocation.active
		LEFT JOIN logistics_expected_units expected
		  ON expected.id=allocation.expected_unit_id
		LEFT JOIN merchant_order_units merchant_unit
		  ON merchant_unit.id=expected.merchant_order_unit_id
		WHERE shipment.agency_order_id=$1
		GROUP BY shipment.id
		ORDER BY shipment.created_at
	`, agencyOrderID)
	if err != nil {
		return nil, fmt.Errorf("list shipment summaries: %w", err)
	}
	defer rows.Close()
	result := make([]agencydomain.ShipmentSummary, 0)
	for rows.Next() {
		var summary agencydomain.ShipmentSummary
		var unitsPayload []byte
		if err := rows.Scan(
			&summary.ID, &summary.MerchantOrderID, &summary.Carrier,
			&summary.TrackingRef, &summary.State, &summary.UpdatedAt, &unitsPayload,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(unitsPayload, &summary.Units); err != nil {
			return nil, err
		}
		result = append(result, summary)
	}
	return result, rows.Err()
}

func (r *Repository) listOrderNotices(
	ctx context.Context,
	agencyOrderID string,
) ([]agencydomain.CustomerNotice, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, agency_order_id, kind, body, created_at, read_at
		FROM agency_order_customer_notices
		WHERE agency_order_id=$1
		ORDER BY created_at DESC
		LIMIT 20
	`, agencyOrderID)
	if err != nil {
		return nil, fmt.Errorf("list order notices: %w", err)
	}
	defer rows.Close()
	notices := make([]agencydomain.CustomerNotice, 0)
	for rows.Next() {
		var notice agencydomain.CustomerNotice
		var readAt sql.NullTime
		if err := rows.Scan(&notice.ID, &notice.AgencyOrderID, &notice.Kind,
			&notice.Body, &notice.CreatedAt, &readAt); err != nil {
			return nil, err
		}
		if readAt.Valid {
			notice.ReadAt = &readAt.Time
		}
		notices = append(notices, notice)
	}
	return notices, rows.Err()
}

// RecordDelayRuleNotice는 send_notice Effect의 실행이다(ADR-0056 — 종전
// EnsureDelayRuleNotices 전수 스캔의 단건 대체). idempotency key는 종전과 같은
// 'delay-rule-30d:{order}'라 컷오버 전후 고지가 이중 발송되지 않는다.
func (r *Repository) RecordDelayRuleNotice(
	ctx context.Context,
	agencyOrderID, idempotencyKey string,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		result, err := r.database.Queryer(tx).ExecContext(tx, `
			INSERT INTO agency_order_customer_notices(
				id, agency_order_id, user_id, kind, body, idempotency_key, created_at
			)
			SELECT gen_random_uuid(), orders.id, orders.user_id, 'SYSTEM',
			       '주문 발행 후 30일이 지나도록 배송이 완료되지 않았습니다. 원하시면 지금 무료로 주문을 취소하고 미수령 상품 전액을 환불받을 수 있습니다(주문 상세의 지연 취소). 계속 기다리셔도 됩니다 — 배송이 확인되면 이 안내는 무시하셔도 됩니다.',
			       $2, $3
			FROM agency_orders orders
			WHERE orders.id=$1
			ON CONFLICT (idempotency_key) DO NOTHING
		`, agencyOrderID, idempotencyKey, now)
		if err != nil {
			return fmt.Errorf("record delay rule notice: %w", err)
		}
		if inserted, _ := result.RowsAffected(); inserted == 0 {
			return nil // 이미 고지된 replay — 최초 기록이 이벤트를 냈다.
		}
		return emitNoticeRecorded(tx, r.database.Queryer(tx),
			agencyOrderID, "DELAY_RULE", idempotencyKey, now)
	})
}

func (r *Repository) GetProjectionForReceipt(ctx context.Context, agencyOrderID string) (agencydomain.Projection, error) {
	var userID string
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT user_id FROM agency_orders WHERE id::text=$1`, agencyOrderID).Scan(&userID); errors.Is(err, sql.ErrNoRows) {
		return agencydomain.Projection{}, agencydomain.ErrNotFound
	} else if err != nil {
		return agencydomain.Projection{}, err
	}
	return r.GetProjection(ctx, userID, agencyOrderID)
}

func (r *Repository) ListFinalizedTransactions(ctx context.Context, paymentID string) ([]agencydomain.ChainTransaction, error) {
	return r.listTransactions(ctx, paymentID, true)
}

func (r *Repository) listTransactions(
	ctx context.Context,
	paymentID string,
	finalizedOnly bool,
) ([]agencydomain.ChainTransaction, error) {
	statePredicate := ""
	if finalizedOnly {
		statePredicate = " AND state='FINALIZED'"
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT purpose,tx_hash,state,COALESCE(block_number,0),updated_at
		FROM chain_transactions
		WHERE settlement_payment_id=$1`+statePredicate+`
		ORDER BY CASE purpose WHEN 'CLAIM' THEN 1 WHEN 'APPROVE' THEN 2 WHEN 'PAY' THEN 3 WHEN 'COMPLETE' THEN 4 WHEN 'REFUND' THEN 5 ELSE 6 END
	`, paymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]agencydomain.ChainTransaction, 0)
	for rows.Next() {
		var item agencydomain.ChainTransaction
		if err := rows.Scan(&item.Purpose, &item.TxHash, &item.State, &item.BlockNumber, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CreateReceipt는 주문당 한 장(UNIQUE)의 영수증을 발급하고, 재종결로 terminal
// 사유가 갱신되면 같은 행을 갱신 발급한다(ADR-0057 2차 D-e — 예: 종결 후 전액
// 환불 → REFUNDED_ALL). 같은 사유 replay는 행을 바꾸지 않는다. migration 100이
// 보존한 미확정 영수증만 한 번 교정하며, 검증된 finality 사실은 별도 dedup으로 보고한다.
func (r *Repository) CreateReceipt(ctx context.Context, receipt agencydomain.Receipt) error {
	finalized := receipt.HasFinalizedTerminalTransaction()
	if receipt.PaymentRail == agencydomain.PaymentProviderGIWA && !finalized {
		return agencydomain.ErrInvalid
	}
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		var storedID string
		err := r.database.Queryer(tx).QueryRowContext(tx, `
				INSERT INTO agency_order_receipts(
					id,agency_order_id,settlement_payment_id,customer_payment_id,kind,
					payment_rail,provider_environment,asset,economic_effect,
					merchant_execution_mode,execution_profile_hash,legal_sale,
					terminal_state,terminal_tx_hash,receipt_hash,payload,created_at
				) VALUES(
					$1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9,$10,$11,
					$12,$13,$14,$15,$16,$17
				)
				ON CONFLICT (agency_order_id) DO UPDATE SET
					settlement_payment_id=EXCLUDED.settlement_payment_id,
					customer_payment_id=EXCLUDED.customer_payment_id,
					kind=EXCLUDED.kind,
					payment_rail=EXCLUDED.payment_rail,
					provider_environment=EXCLUDED.provider_environment,
					asset=EXCLUDED.asset,
					economic_effect=EXCLUDED.economic_effect,
					merchant_execution_mode=EXCLUDED.merchant_execution_mode,
					execution_profile_hash=EXCLUDED.execution_profile_hash,
					legal_sale=EXCLUDED.legal_sale,
					terminal_state=EXCLUDED.terminal_state,
				terminal_tx_hash=EXCLUDED.terminal_tx_hash,
				receipt_hash=EXCLUDED.receipt_hash,
				payload=EXCLUDED.payload,
				created_at=EXCLUDED.created_at
			WHERE agency_order_receipts.terminal_state IS DISTINCT FROM EXCLUDED.terminal_state
			OR (EXCLUDED.payment_rail='GIWA' AND EXISTS (
				SELECT 1 FROM order_process_execution_history h
				WHERE h.source='receipt' AND h.identity=agency_order_receipts.id::text
				AND h.agency_order_id=agency_order_receipts.agency_order_id
				AND h.evidence->>'receipt_hash'=agency_order_receipts.receipt_hash
			))
			RETURNING id
			`, receipt.ID, receipt.AgencyOrderID, receipt.SettlementPaymentID,
			receipt.CustomerPaymentID, receipt.Kind, receipt.PaymentRail,
			receipt.ProviderEnvironment, receipt.Asset, receipt.EconomicEffect,
			receipt.MerchantExecutionMode, receipt.ExecutionProfileHash,
			receipt.LegalSale, receipt.TerminalState, receipt.TerminalTxHash,
			receipt.ReceiptHash, receipt.Payload, receipt.CreatedAt).
			Scan(&storedID)
		if errors.Is(err, sql.ErrNoRows) {
			if !finalized {
				return nil
			}
			stored, found, readErr := r.getReceipt(tx, receipt.AgencyOrderID)
			if readErr != nil {
				return readErr
			}
			if !found || stored.TerminalState != receipt.TerminalState || !strings.EqualFold(stored.TerminalTxHash, receipt.TerminalTxHash) {
				return agencydomain.ErrReceiptNotFound
			}
			storedID = stored.ID
			err = nil
		}
		if err != nil {
			return err
		}
		return emitReceiptIssued(tx, r.database.Queryer(tx),
			receipt.AgencyOrderID, storedID, receipt.TerminalState, finalized, receipt.CreatedAt)
	})
}

func (r *Repository) getReceipt(ctx context.Context, agencyOrderID string) (agencydomain.Receipt, bool, error) {
	var value agencydomain.Receipt
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id,agency_order_id,COALESCE(settlement_payment_id::text,''),
		       COALESCE(customer_payment_id::text,''),kind,payment_rail,
		       provider_environment,asset,economic_effect,merchant_execution_mode,
		       execution_profile_hash,legal_sale,
		       terminal_state,terminal_tx_hash,receipt_hash,payload,created_at
		FROM agency_order_receipts WHERE agency_order_id=$1
	`, agencyOrderID).Scan(&value.ID, &value.AgencyOrderID, &value.SettlementPaymentID,
		&value.CustomerPaymentID, &value.Kind, &value.PaymentRail,
		&value.ProviderEnvironment, &value.Asset, &value.EconomicEffect,
		&value.MerchantExecutionMode, &value.ExecutionProfileHash,
		&value.LegalSale, &value.TerminalState, &value.TerminalTxHash, &value.ReceiptHash,
		&value.Payload, &value.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return agencydomain.Receipt{}, false, nil
	}
	if err == nil && value.PaymentRail == agencydomain.PaymentProviderGIWA && !value.HasFinalizedTerminalTransaction() {
		return agencydomain.Receipt{}, false, nil
	}
	return value, err == nil, err
}

func (r *Repository) GetReceipt(ctx context.Context, userID, agencyOrderID string) (agencydomain.Receipt, error) {
	var owner string
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT user_id FROM agency_orders WHERE id=$1`, agencyOrderID).Scan(&owner); errors.Is(err, sql.ErrNoRows) || owner != userID {
		return agencydomain.Receipt{}, agencydomain.ErrReceiptNotFound
	} else if err != nil {
		return agencydomain.Receipt{}, err
	}
	receipt, found, err := r.getReceipt(ctx, agencyOrderID)
	if !found && err == nil {
		return agencydomain.Receipt{}, agencydomain.ErrReceiptNotFound
	}
	return receipt, err
}
