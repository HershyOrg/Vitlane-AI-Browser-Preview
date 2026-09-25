package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// procurement 소유 상태 변경의 process 이벤트 발행이다(ADR-0056 §2, ADR-0070 §4.2).
// 이벤트는 owner 자기 행 값만 싣는다 — 주문 단위 집계(총수·미종결 수)는
// 리듀서가 identity fold에서 파생하므로 발행 transaction이 COUNT하거나 주문
// advisory lock을 앞당겨 잡지 않는다(AppendEvent가 채번 직전에만 잡는다).

type procurementQueryer interface {
	procmsg.Queryer
	QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
}

func emitMerchantOrderEvent(
	ctx context.Context,
	q procurementQueryer,
	merchantOrderID string,
	now time.Time,
) error {
	var agencyOrderID, allocationID, shopDomain, state, failureCode string
	var version int64
	var unitCount int
	var taskID string
	var unitJSON []byte
	if err := q.QueryRowContext(ctx, `
		SELECT agency_order_id::text, allocation_id::text, shop_domain, state,
		       COALESCE(failure_code,''), version,
		       (SELECT count(*) FROM merchant_order_units unit
		        WHERE unit.merchant_order_id=merchant_orders.id),
		       COALESCE((SELECT id::text FROM merchant_order_execution_tasks WHERE merchant_order_id=merchant_orders.id ORDER BY created_at,id LIMIT 1),''),
		       COALESCE((SELECT jsonb_agg(jsonb_build_object('id',id::text,'lineId',line_id::text,'unitIndex',unit_index) ORDER BY id) FROM merchant_order_units WHERE merchant_order_id=merchant_orders.id),'[]'::jsonb)
		FROM merchant_orders WHERE id=$1
	`, merchantOrderID).Scan(&agencyOrderID, &allocationID, &shopDomain, &state,
		&failureCode, &version, &unitCount, &taskID, &unitJSON); err != nil {
		return fmt.Errorf("read merchant order for event: %w", err)
	}
	var units []procmsg.UnitManifestEntry
	if err := json.Unmarshal(unitJSON, &units); err != nil {
		return err
	}
	unitIDs := make([]string, len(units))
	for i, unit := range units {
		unitIDs[i] = unit.ID
	}
	compensationCause := ""
	switch state {
	case "FAILED":
		compensationCause = procmsg.CompensationCauseProcurementFailure
	case "CANCELLED":
		var cancellationKind string
		err := q.QueryRowContext(ctx, `
			SELECT kind FROM agency_order_cancellations
			WHERE merchant_order_id=$1
		`, merchantOrderID).Scan(&cancellationKind)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read merchant order cancellation cause: %w", err)
		}
		if cancellationKind == "DELAY_RULE" {
			compensationCause = procmsg.CompensationCauseDelayRule
		} else if cancellationKind == "PRE_EFFECT" {
			compensationCause = procmsg.CompensationCauseCustomerCancelPreEffect
		} else {
			return fmt.Errorf("cancelled merchant order has no typed cancellation cause")
		}
	}
	var flowID, effectID string
	err := q.QueryRowContext(ctx, `SELECT COALESCE(process_flow_id::text,''),COALESCE(process_effect_id::text,'') FROM procurement_effect_locks WHERE merchant_order_id=$1`, merchantOrderID).Scan(&flowID, &effectID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	_, err = procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		FlowID: flowID, CausationEffectID: effectID, SourceEntityVersion: version,
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceProcurement,
		Type:          procmsg.EventMerchantOrderStateChanged,
		Payload: procmsg.MerchantOrderStateChangedPayload{
			TaskID: taskID, UnitIDs: unitIDs, Units: units,
			MerchantOrderID: merchantOrderID, AllocationID: allocationID,
			ShopDomain: shopDomain,
			State:      state, FailureCode: failureCode,
			CompensationCause: compensationCause, UnitCount: unitCount,
		},
		DedupKey: procmsg.EventDedupKey("merchant_order", merchantOrderID,
			fmt.Sprintf("v%d", version)),
	}, now)
	return err
}

// emitEffectLockEvent는 캡처↔구매 펜스(effect lock) 전이의 보고다(ADR-0070
// §4.2). dedup은 lock 행 version에 붙는다 — 같은 상태의 재방문도 별개 전이다.
func emitEffectLockEvent(
	ctx context.Context,
	q procurementQueryer,
	merchantOrderID string,
	now time.Time,
) error {
	var agencyOrderID, allocationID, taskID, operatorUserID, state, fundingState string
	var version int64
	if err := q.QueryRowContext(ctx, `
		SELECT mo.agency_order_id::text, mo.allocation_id::text, lock.task_id::text,
		       COALESCE(lock.operator_user_id::text,''), lock.state,
		       COALESCE(lock.funding_state,''), lock.version
		FROM procurement_effect_locks lock
		JOIN merchant_orders mo ON mo.id=lock.merchant_order_id
		WHERE lock.merchant_order_id=$1
	`, merchantOrderID).Scan(
		&agencyOrderID, &allocationID, &taskID, &operatorUserID, &state,
		&fundingState, &version,
	); err != nil {
		return fmt.Errorf("read effect lock for event: %w", err)
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceProcurement,
		Type:          procmsg.EventEffectLockStateChanged,
		Payload: procmsg.EffectLockStateChangedPayload{
			MerchantOrderID: merchantOrderID, AllocationID: allocationID,
			TaskID: taskID, State: state, FundingState: fundingState,
			OperatorUserID: operatorUserID,
		},
		DedupKey: procmsg.EventDedupKey("procurement_effect_lock", merchantOrderID,
			fmt.Sprintf("v%d", version)),
	}, now)
	return err
}

// emitCustomerRequestEvent는 고객 정보·동의 요청의 생성·응답·종결 보고다.
func emitCustomerRequestEvent(
	ctx context.Context,
	q procurementQueryer,
	requestID string,
	now time.Time,
) error {
	var agencyOrderID, merchantOrderID, customerUserID, kind, state,
		sourceDecisionID, actorUserID string
	var version int64
	if err := q.QueryRowContext(ctx, `
		SELECT agency_order_id::text, merchant_order_id::text, user_id::text,
		       kind, state, COALESCE(source_decision_id::text,''),
		       COALESCE(resolved_by_user_id::text, requested_by_user_id::text, ''),
		       version
		FROM procurement_customer_requests WHERE id=$1
	`, requestID).Scan(
		&agencyOrderID, &merchantOrderID, &customerUserID, &kind, &state,
		&sourceDecisionID, &actorUserID, &version,
	); err != nil {
		return fmt.Errorf("read customer request for event: %w", err)
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceProcurement,
		Type:          procmsg.EventCustomerRequestStateChanged,
		Payload: procmsg.CustomerRequestStateChangedPayload{
			RequestID: requestID, MerchantOrderID: merchantOrderID, Kind: kind,
			State: state, SourceDecisionID: sourceDecisionID, ActorUserID: actorUserID,
			CustomerUserID: customerUserID, Version: version,
		},
		DedupKey: procmsg.EventDedupKey("procurement_customer_request", requestID,
			fmt.Sprintf("v%d", version)),
	}, now)
	return err
}

// emitDecisionRecordedEvent는 운영자 수동 판단 기록의 보고다(UNABLE만 고객
// 카드 대상 — 판별은 리듀서).
func emitDecisionRecordedEvent(
	ctx context.Context,
	q procurementQueryer,
	decisionID string,
	now time.Time,
) error {
	var agencyOrderID, merchantOrderID, decision, operatorUserID string
	if err := q.QueryRowContext(ctx, `
		SELECT agency_order_id::text, merchant_order_id::text, decision,
		       COALESCE(decided_by_user_id::text,'')
		FROM procurement_decision_records WHERE id=$1
	`, decisionID).Scan(&agencyOrderID, &merchantOrderID, &decision, &operatorUserID); err != nil {
		return fmt.Errorf("read decision record for event: %w", err)
	}
	_, err := procmsg.AppendEvent(ctx, q, procmsg.ProcessEvent{
		AgencyOrderID: agencyOrderID,
		Source:        procmsg.SourceProcurement,
		Type:          procmsg.EventProcurementDecisionRecorded,
		Payload: procmsg.ProcurementDecisionRecordedPayload{
			DecisionRecordID: decisionID, MerchantOrderID: merchantOrderID,
			Decision: decision, OperatorUserID: operatorUserID,
		},
		DedupKey: procmsg.EventDedupKey("procurement_decision_record", decisionID, "RECORDED"),
	}, now)
	return err
}
