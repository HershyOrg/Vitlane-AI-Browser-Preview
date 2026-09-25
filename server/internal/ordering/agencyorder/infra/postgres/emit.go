package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// agencyorder 소유 사실의 process 이벤트 발행이다(ADR-0056 §2) — 발행·환불
// 심사·Receipt·고지. 각 emit은 그 사실을 만든 transaction 안에서 호출된다.

type agencyQueryer interface {
	procmsg.Queryer
	QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	QueryContext(context.Context, string, ...any) (sharedpostgres.Rows, error)
}

func emitOrderIssued(
	ctx context.Context,
	q agencyQueryer,
	agencyOrderID, userID string,
	issuedAt, now time.Time,
) error {
	var rail string
	var authorization procmsg.OrderAuthorization
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(a.authorization_kind,''),COALESCE(a.authorization_hash,''),o.execution_profile_hash,o.merchant_execution_mode FROM agency_orders o LEFT JOIN agency_order_procurement_authorizations a ON a.agency_order_id=o.id AND a.execution_profile_hash=o.execution_profile_hash WHERE o.id=$1`, agencyOrderID).Scan(&authorization.Kind, &authorization.Hash, &authorization.ExecutionProfileHash, &authorization.ExecutionMode); err != nil {
		return err
	}
	if err := q.QueryRowContext(ctx, `
		SELECT rail FROM agency_order_payment_instructions
		WHERE agency_order_id=$1
		ORDER BY created_at DESC LIMIT 1
	`, agencyOrderID).Scan(&rail); err != nil {
		return fmt.Errorf("read instruction rail for event: %w", err)
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceAgencyOrder,
		Type:          procmsg.EventOrderIssued,
		Payload: procmsg.OrderIssuedPayload{
			UserID: userID, Rail: rail, IssuedAt: issuedAt, Authorization: authorization,
		},
		DedupKey:   procmsg.EventDedupKey("agency_order", agencyOrderID, "ISSUED"),
		OccurredAt: issuedAt,
	}, now)
	return err
}

// emitRefundReviewDecided publishes the whole-MO review fact. The request ID
// resolves the immutable allocation; no unit/slice IDs are read or emitted.
func emitRefundReviewDecided(
	ctx context.Context,
	q agencyQueryer,
	requestID string,
	now time.Time,
) error {
	var agencyOrderID, merchantOrderID, allocationID, reasonCode, decision string
	if err := q.QueryRowContext(ctx, `
		SELECT agency_order_id::text, merchant_order_id::text,
		       allocation_id::text, COALESCE(reason_code,''), decision
		FROM agency_order_refund_requests WHERE id=$1
	`, requestID).Scan(
		&agencyOrderID, &merchantOrderID, &allocationID, &reasonCode, &decision,
	); err != nil {
		return fmt.Errorf("read refund request for event: %w", err)
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceAgencyOrder,
		Type:          procmsg.EventRefundReviewDecided,
		Payload: procmsg.RefundReviewDecidedPayload{
			RequestID:       requestID,
			MerchantOrderID: merchantOrderID,
			AllocationID:    allocationID,
			Decision:        decision,
			ReasonCode:      reasonCode,
		},
		DedupKey: procmsg.EventDedupKey("refund_request", requestID, "DECIDED"),
	}, now)
	return err
}

func emitReceiptIssued(
	ctx context.Context,
	q agencyQueryer,
	agencyOrderID, receiptID, terminalState string,
	terminalTxFinalized bool,
	now time.Time,
) error {
	keyState := terminalState
	if terminalTxFinalized {
		keyState += ":finalized"
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceAgencyOrder,
		Type:          procmsg.EventReceiptIssued,
		Payload: procmsg.ReceiptIssuedPayload{
			ReceiptID: receiptID, TerminalState: terminalState, TerminalTxFinalized: terminalTxFinalized,
		},
		// 사유 갱신 재발급(D-e)이 같은 receipt 행 id로 새 이벤트를 내므로
		// dedup은 사유와 finality 축까지 포함한다 — 같은 증거 replay만 차단된다.
		DedupKey: procmsg.EventDedupKey("agency_order_receipt", receiptID,
			"ISSUED:"+keyState),
	}, now)
	return err
}

func emitNoticeRecorded(
	ctx context.Context,
	q agencyQueryer,
	agencyOrderID, kind, idempotencyKey string,
	now time.Time,
) error {
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceAgencyOrder,
		Type:          procmsg.EventNoticeRecorded,
		Payload: procmsg.NoticeRecordedPayload{
			Kind: kind, IdempotencyKey: idempotencyKey,
		},
		DedupKey: procmsg.EventDedupKey("agency_order_customer_notice", idempotencyKey, "RECORDED"),
	}, now)
	return err
}

// emitRefundRequested publishes the customer refund intent (whole-MO). The
// review decision is a separate event (refund_review.decided).
func emitRefundRequested(
	ctx context.Context,
	q agencyQueryer,
	requestID string,
	now time.Time,
) error {
	var agencyOrderID, merchantOrderID, allocationID, reasonCode string
	if err := q.QueryRowContext(ctx, `
		SELECT agency_order_id::text, merchant_order_id::text,
		       allocation_id::text, COALESCE(reason_code,'')
		FROM agency_order_refund_requests WHERE id=$1
	`, requestID).Scan(&agencyOrderID, &merchantOrderID, &allocationID, &reasonCode); err != nil {
		return fmt.Errorf("read refund request for event: %w", err)
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceAgencyOrder,
		Type:          procmsg.EventRefundRequested,
		Payload: procmsg.RefundRequestedPayload{
			RequestID: requestID, MerchantOrderID: merchantOrderID,
			AllocationID: allocationID, ReasonCode: reasonCode,
		},
		DedupKey: procmsg.EventDedupKey("refund_request", requestID, "REQUESTED"),
	}, now)
	return err
}
