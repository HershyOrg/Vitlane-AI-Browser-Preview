package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// settlement rail 상태 전이의 process 이벤트 발행이다(ADR-0056 §2). 전이를
// 만든 transaction 안에서 자기 행을 재조회해 발행한다. dedup_key는
// {id}:{state}:{updated_at unix}로 결정적이다 — settlement 상태는 reorg로
// 재방문할 수 있어 version 없는 이 테이블에서는 시각이 전이 식별자다(같은
// 상태의 중복 이벤트는 fold가 같은 값 채택으로 흡수한다).

type settlementQueryer interface {
	procmsg.Queryer
	QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
}

func emitSettlementEvent(
	ctx context.Context,
	q settlementQueryer,
	settlementPaymentID string,
	now time.Time,
) error {
	var agencyOrderID, state, reason string
	var updatedAt time.Time
	if err := q.QueryRowContext(ctx, `
		SELECT COALESCE(agency_order_id::text,''), state,
		       COALESCE(last_reason_code,''), updated_at
		FROM settlement_payments WHERE id=$1
	`, settlementPaymentID).Scan(&agencyOrderID, &state, &reason, &updatedAt); err != nil {
		return fmt.Errorf("read settlement payment for event: %w", err)
	}
	if agencyOrderID == "" {
		// 주문 귀속이 없는 역사적 row는 워크플로 밖이다.
		return nil
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceSettlement,
		Type:          procmsg.EventSettlementStateChanged,
		Payload: procmsg.SettlementStateChangedPayload{
			SettlementPaymentID: settlementPaymentID, State: state, Reason: reason,
		},
		DedupKey: fmt.Sprintf("settlement_payment:%s:%s:%d",
			settlementPaymentID, state, updatedAt.Unix()),
	}, now)
	return err
}

// emitSettlementRefundConflict는 환불 finalize 충돌의 이벤트 표현이다 — 종전
// 이 지점의 process 직접 UPDATE(ATTENTION_REQUIRED)를 결정으로 옮긴다.
func emitSettlementRefundConflict(
	ctx context.Context,
	q settlementQueryer,
	settlementPaymentID, agencyOrderID, reason string,
	now time.Time,
) error {
	if agencyOrderID == "" {
		return nil
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceSettlement,
		Type:          procmsg.EventSettlementRefundConflict,
		Payload: procmsg.SettlementRefundConflictPayload{
			SettlementPaymentID: settlementPaymentID, Reason: reason,
		},
		DedupKey: fmt.Sprintf("settlement_payment:%s:REFUND_CONFLICT:%d",
			settlementPaymentID, now.Unix()),
	}, now)
	return err
}
