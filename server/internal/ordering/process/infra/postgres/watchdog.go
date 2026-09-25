package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	processapp "github.com/vitlane/vitlane/server/internal/ordering/process/app"
	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

// Watchdog은 현재 Owner 사실을 bounded hot/cold 조회로 읽어 저장된 사영과
// 비교한다. 누락은 divergence Event로 알리며 부트스트랩·업무 자동 치유는 하지 않는다.

// watchdogFactsSQL은 owner public 상태의 주문 단위 중립 관찰이다 — 종전
// ProcessFacts 폴링 대사와 같은 계약(읽기 전용, 저장 어휘 그대로). MO·Unit
// 관찰은 merchantOrderSnapshotSQL이 identity로 싣는다.
const watchdogFactsSQL = `
	SELECT process.agency_order_id::text,
	       process.state,
	       COALESCE(process.terminal_reason,''),
	       COALESCE(process.last_reason_code,''),
	       process.last_applied_seq,
	       (SELECT count(*) FROM order_process_events pending
	        WHERE pending.agency_order_id=process.agency_order_id
	          AND pending.applied_at IS NULL),
	       (SELECT count(*) FROM (
          SELECT x.value FROM jsonb_each(COALESCE(process.process_state->'effects','{}')) x
          UNION ALL
          SELECT x.value FROM jsonb_each(COALESCE(process.process_state->'merchantOrders','{}')) mo
          CROSS JOIN LATERAL jsonb_each(COALESCE(mo.value->'effects','{}')) x
         ) pending WHERE COALESCE(pending.value->>'outcome','') NOT IN ('SUCCEEDED','REJECTED')),
	       COALESCE((process.process_state->>'modelVersion')::int, 0),
	       COALESCE(settlement.state,''),
	       COALESCE(settlement.last_reason_code,''),
	       EXISTS (
	           SELECT 1 FROM payment_customer_payments pay
	           WHERE pay.agency_order_id=process.agency_order_id
	             AND pay.state IN ('AUTHORIZED','PARTIALLY_CAPTURED','CAPTURED','CLOSED')
	       ),
	       EXISTS (
	           SELECT 1 FROM payment_customer_payments pay
	           WHERE pay.agency_order_id=process.agency_order_id
	             AND pay.state IN ('AUTHORIZED','PARTIALLY_CAPTURED','CAPTURED','CLOSED')
	             AND pay.rail='PAYPAL'
	       ),
	       COALESCE(open_payment.state,''),
	       COALESCE(open_payment.reason,''),
	       orders.issued_at,
	       COALESCE(instruction.rail,''),
	       EXISTS (
	           SELECT 1 FROM agency_order_customer_notices notice
	           WHERE notice.agency_order_id=process.agency_order_id
	             AND notice.idempotency_key='delay-rule-30d:'||process.agency_order_id::text
	       ),
	       EXISTS (
	           SELECT 1 FROM agency_order_receipts receipt
	           WHERE receipt.agency_order_id=process.agency_order_id
	       ),
	       COALESCE((SELECT receipt.terminal_state FROM agency_order_receipts receipt
	                 WHERE receipt.agency_order_id=process.agency_order_id LIMIT 1),'')
	FROM candidates candidate
	JOIN agency_order_processes process
	  ON process.agency_order_id=candidate.agency_order_id
	JOIN agency_orders orders ON orders.id=process.agency_order_id
	LEFT JOIN settlement_payments settlement
	  ON settlement.agency_order_id=process.agency_order_id
	LEFT JOIN LATERAL (
	    SELECT instr.rail FROM agency_order_payment_instructions instr
	    WHERE instr.agency_order_id=process.agency_order_id
	    ORDER BY instr.created_at DESC LIMIT 1
	) instruction ON TRUE
	LEFT JOIN LATERAL (
	    SELECT pay.state, COALESCE(pay.last_reason_code,'') AS reason
	    FROM payment_customer_payments pay
	    WHERE pay.agency_order_id=process.agency_order_id
	      AND pay.state IN ('CREATED','ACTION_REQUIRED','PROCESSING','OUTCOME_UNKNOWN')
	    ORDER BY CASE WHEN pay.state='OUTCOME_UNKNOWN' THEN 0 ELSE 1 END,
	             pay.created_at DESC
	    LIMIT 1
	) open_payment ON TRUE
`

// merchantOrderSnapshotSQL은 한 주문의 MO·Unit 관찰이다(identity 단위) —
// 부트스트랩 기준선과 watchdog 고정점 검사가 같은 질의를 쓴다.
const merchantOrderSnapshotSQL = `
	SELECT mo.id::text, mo.allocation_id::text, mo.shop_domain, mo.state,
	       COALESCE(mo.failure_code,''),
	       (SELECT count(*) FROM merchant_order_units unit WHERE unit.merchant_order_id=mo.id),
	       COALESCE(funding.state,''), COALESCE(lock.state,''),
	       COALESCE(compensation.state,''), COALESCE(compensation.action,''),
	       COALESCE(compensation.cause,''),
	       COALESCE(cancellation.kind,''), COALESCE(refund.state,''),
	       COALESCE(dispute.id::text,''), COALESCE(dispute.state,''),
	       COALESCE(dispute.outcome,''),
	       COALESCE((SELECT jsonb_agg(jsonb_build_object(
	                     'expectedUnitId', expected.id::text,
	                     'fulfillment', expected.fulfillment) ORDER BY expected.id)
	                 FROM logistics_expected_units expected
	                 WHERE expected.merchant_order_id=mo.id), '[]'::jsonb),
	       COALESCE((SELECT jsonb_agg(request.id::text ORDER BY request.id)
	                 FROM procurement_customer_requests request
	                 WHERE request.merchant_order_id=mo.id AND request.state='PENDING'),
	                '[]'::jsonb)
	FROM merchant_orders mo
	LEFT JOIN payment_mo_funding_positions funding ON funding.allocation_id=mo.allocation_id
	LEFT JOIN procurement_effect_locks lock ON lock.merchant_order_id=mo.id
	LEFT JOIN payment_mo_compensations compensation ON compensation.allocation_id=mo.allocation_id
	LEFT JOIN agency_order_cancellations cancellation ON cancellation.merchant_order_id=mo.id
	LEFT JOIN LATERAL (
	    SELECT request.state FROM agency_order_refund_requests request
	    WHERE request.merchant_order_id=mo.id
	    ORDER BY request.created_at DESC LIMIT 1
	) refund ON TRUE
	LEFT JOIN LATERAL (
	    SELECT dispute_case.id, dispute_case.state, dispute_case.outcome
	    FROM payment_paypal_dispute_cases dispute_case
	    JOIN payment_mo_cash_receipts receipt ON receipt.id=dispute_case.mo_cash_receipt_id
	    WHERE receipt.allocation_id=mo.allocation_id
	    ORDER BY dispute_case.updated_at DESC LIMIT 1
	) dispute ON TRUE
	WHERE mo.agency_order_id=$1
	ORDER BY mo.checkout_ordinal
`

// loadMerchantOrderSnapshots는 주문 하나의 MO·Unit 관찰을 owner facts에서
// 읽는다(읽기 전용).
func (r *Store) loadMerchantOrderSnapshots(
	ctx context.Context,
	agencyOrderID string,
) ([]procmsg.MerchantOrderSnapshot, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, merchantOrderSnapshotSQL, agencyOrderID)
	if err != nil {
		return nil, fmt.Errorf("load merchant order snapshots: %w", err)
	}
	defer rows.Close()
	snapshots := make([]procmsg.MerchantOrderSnapshot, 0)
	for rows.Next() {
		var snapshot procmsg.MerchantOrderSnapshot
		var disputeID, disputeState, disputeOutcome string
		var unitsJSON, requestsJSON []byte
		if err := rows.Scan(
			&snapshot.MerchantOrderID, &snapshot.AllocationID, &snapshot.ShopDomain,
			&snapshot.State, &snapshot.FailureCode, &snapshot.UnitCount,
			&snapshot.FundingState, &snapshot.EffectLockState,
			&snapshot.CompensationState, &snapshot.CompensationAction,
			&snapshot.CompensationCause, &snapshot.CancellationKind,
			&snapshot.RefundRequestState, &disputeID, &disputeState, &disputeOutcome,
			&unitsJSON, &requestsJSON,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(unitsJSON, &snapshot.Units); err != nil {
			return nil, fmt.Errorf("decode unit snapshot: %w", err)
		}
		if err := json.Unmarshal(requestsJSON, &snapshot.OpenRequestIDs); err != nil {
			return nil, fmt.Errorf("decode request snapshot: %w", err)
		}
		if disputeID != "" {
			snapshot.Dispute = &procmsg.DisputeSnapshot{
				CaseID: disputeID, State: disputeState, Outcome: disputeOutcome,
			}
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

func (r *Store) scanWatchdogSnapshots(
	ctx context.Context,
	candidates, suffix string,
	args ...any,
) ([]processapp.WatchdogSnapshot, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx,
		candidates+watchdogFactsSQL+suffix, args...)
	if err != nil {
		return nil, fmt.Errorf("list watchdog snapshots: %w", err)
	}
	snapshots := make([]processapp.WatchdogSnapshot, 0)
	for rows.Next() {
		var s processapp.WatchdogSnapshot
		var state, terminalReason string
		var issuedAt time.Time
		if err := rows.Scan(
			&s.AgencyOrderID, &state, &terminalReason, &s.LastReasonCode,
			&s.LastAppliedSeq, &s.UnappliedEvents, &s.PendingEffects, &s.ModelVersion,
			&s.ProcessState.SettlementState, &s.ProcessState.SettlementReason,
			&s.ProcessState.PaymentSucceeded, &s.ProcessState.PayPalSucceeded,
			&s.ProcessState.OpenPaymentState, &s.ProcessState.OpenPaymentReason,
			&issuedAt, &s.ProcessState.Rail, &s.ProcessState.DelayNoticeRecorded,
			&s.ProcessState.ReceiptIssued, &s.ProcessState.ReceiptState,
		); err != nil {
			rows.Close()
			return nil, err
		}
		s.State = processdomain.State(state)
		s.TerminalReason = processdomain.TerminalReason(terminalReason)
		s.ProcessState.IssuedAt = &issuedAt
		snapshots = append(snapshots, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range snapshots {
		merchantOrders, err := r.loadMerchantOrderSnapshots(ctx, snapshots[index].AgencyOrderID)
		if err != nil {
			return nil, err
		}
		snapshots[index].MerchantOrders = merchantOrders
	}
	return snapshots, nil
}

// ListBootstrapCandidates는 이벤트가 전혀 없는 process 행이다 — 컷오버 순간의
// in-flight 주문만 해당하며, migrated 발행 뒤에는 조회가 비어 상수 비용이 된다.
func (r *Store) ListHotWatchdogSnapshots(
	ctx context.Context,
	updatedSince time.Time,
	limit int,
) ([]processapp.WatchdogSnapshot, error) {
	return r.scanWatchdogSnapshots(ctx, `
		WITH candidates AS MATERIALIZED (
		    SELECT process.agency_order_id
		    FROM agency_order_processes process
		    WHERE process.updated_at >= $1
		    ORDER BY process.updated_at DESC, process.agency_order_id
		    LIMIT $2
		)
	`, " ORDER BY process.updated_at DESC, process.agency_order_id", updatedSince, limit)
}

// ListColdWatchdogSnapshots는 hot 여부와 무관하게 전체 corpus를 UUID keyset으로
// 순환한다. 마지막 page 뒤에는 처음으로 wrap하며 cursor는 page 검사가 끝난 뒤
// AdvanceColdWatchdogCursor가 전진시킨다.
func (r *Store) ListColdWatchdogSnapshots(
	ctx context.Context,
	limit int,
) ([]processapp.WatchdogSnapshot, error) {
	queryer := r.database.Queryer(ctx)
	if _, err := queryer.ExecContext(ctx, `
		INSERT INTO order_process_watchdog_cursors(lane, after_agency_order_id, updated_at)
		VALUES('COLD', NULL, now())
		ON CONFLICT (lane) DO NOTHING
	`); err != nil {
		return nil, err
	}
	var after sql.NullString
	if err := queryer.QueryRowContext(ctx, `
		SELECT after_agency_order_id::text
		FROM order_process_watchdog_cursors WHERE lane='COLD'
	`).Scan(&after); err != nil {
		return nil, err
	}
	list := func(cursor any) ([]processapp.WatchdogSnapshot, error) {
		return r.scanWatchdogSnapshots(ctx, `
			WITH candidates AS MATERIALIZED (
			    SELECT process.agency_order_id
			    FROM agency_order_processes process
			    WHERE ($1::uuid IS NULL OR process.agency_order_id > $1::uuid)
			    ORDER BY process.agency_order_id
			    LIMIT $2
			)
		`, " ORDER BY process.agency_order_id", cursor, limit)
	}
	var cursor any
	if after.Valid {
		cursor = after.String
	}
	snapshots, err := list(cursor)
	if err != nil || len(snapshots) > 0 || !after.Valid {
		return snapshots, err
	}
	return list(nil)
}

func (r *Store) AdvanceColdWatchdogCursor(
	ctx context.Context,
	afterAgencyOrderID string,
	now time.Time,
) error {
	var cursor any
	if afterAgencyOrderID != "" {
		cursor = afterAgencyOrderID
	}
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE order_process_watchdog_cursors
		SET after_agency_order_id=$1::uuid, updated_at=$2
		WHERE lane='COLD'
	`, cursor, now)
	return err
}

// AppendMigratedEvent는 부트스트랩 스냅샷 발행이다(주문당 한 번 — dedup).
// 주문 단위 관찰만 싣는다; MO·Unit 관찰은 읽기 전용 merchantOrderSnapshotSQL이 싣는다.
func (r *Store) AppendDivergenceEvent(
	ctx context.Context,
	agencyOrderID, expectedState, expectedReason, detail string,
	now time.Time,
) (bool, error) {
	inserted := false
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		var err error
		inserted, err = procmsg.AppendEvent(txContext, r.database.Queryer(txContext),
			procmsg.ProcessEvent{
				AgencyOrderID: agencyOrderID,
				Source:        procmsg.SourceWatchdog,
				Type:          procmsg.EventWatchdogDivergence,
				Payload: procmsg.WatchdogDivergencePayload{
					ExpectedState: expectedState, ExpectedReason: expectedReason,
					Detail: detail,
				},
				DedupKey: fmt.Sprintf("watchdog:%s:%s:%s",
					agencyOrderID, expectedState, now.Format("2006-01-02")),
			}, now)
		return err
	})
	return inserted, err
}

// A missing continuation becomes visible without releasing business authority.
