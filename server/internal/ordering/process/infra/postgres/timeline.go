package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"

	processapp "github.com/vitlane/vitlane/server/internal/ordering/process/app"
)

// LoadTimeline은 운영자 timeline 읽기다(ADR-0070 §4.7). 세 원장을 한 번씩
// 읽는다 — process 행이 없으면 found=false다. 전체 이력을 돌려주며 상한은
// 두지 않는다(주문당 이벤트·결정 수는 유한하고 작다).
func (r *Store) LoadTimeline(
	ctx context.Context,
	agencyOrderID string,
) (processapp.OrderTimeline, bool, error) {
	queryer := r.database.Queryer(ctx)
	timeline := processapp.OrderTimeline{AgencyOrderID: agencyOrderID}
	var terminalReason, lastReasonCode sql.NullString
	var wakeAt sql.NullTime
	err := queryer.QueryRowContext(ctx, `
		SELECT state, terminal_reason, last_reason_code, version, last_applied_seq,
		       wake_at, updated_at
		FROM agency_order_processes WHERE agency_order_id=$1
	`, agencyOrderID).Scan(
		&timeline.Process.State, &terminalReason, &lastReasonCode,
		&timeline.Process.Version, &timeline.Process.LastAppliedSeq, &wakeAt,
		&timeline.Process.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return processapp.OrderTimeline{}, false, nil
	}
	if err != nil {
		return processapp.OrderTimeline{}, false, fmt.Errorf("load process for timeline: %w", err)
	}
	timeline.Process.TerminalReason = terminalReason.String
	timeline.Process.LastReasonCode = lastReasonCode.String
	if wakeAt.Valid {
		timeline.Process.WakeAt = &wakeAt.Time
	}

	rows, err := queryer.QueryContext(ctx, `
		SELECT version, seq_from, seq_to, COALESCE(stage_before,''), stage_after,
		       COALESCE(terminal_reason,''), COALESCE(last_reason_code,''), stage_changed,
		       merchant_order_diff, effects, wake_at, process_state, decided_at
		FROM order_process_decisions WHERE agency_order_id=$1 ORDER BY version
	`, agencyOrderID)
	if err != nil {
		return processapp.OrderTimeline{}, false, fmt.Errorf("load decisions for timeline: %w", err)
	}
	timeline.Decisions = []processapp.TimelineDecision{}
	for rows.Next() {
		var item processapp.TimelineDecision
		var decisionWake sql.NullTime
		if err := rows.Scan(
			&item.Version, &item.SeqFrom, &item.SeqTo, &item.StageBefore, &item.StageAfter,
			&item.TerminalReason, &item.LastReasonCode, &item.StageChanged,
			&item.MerchantOrders, &item.Effects, &decisionWake, &item.ProcessState, &item.DecidedAt,
		); err != nil {
			rows.Close()
			return processapp.OrderTimeline{}, false, err
		}
		if decisionWake.Valid {
			item.WakeAt = &decisionWake.Time
		}
		timeline.Decisions = append(timeline.Decisions, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return processapp.OrderTimeline{}, false, err
	}

	rows, err = queryer.QueryContext(ctx, `
		SELECT id, seq, source, type, payload, occurred_at, recorded_at, applied_version,COALESCE(flow_id::text,''),COALESCE(causation_effect_id::text,''),COALESCE(source_entity_version,0)
		FROM order_process_events WHERE agency_order_id=$1 ORDER BY seq
	`, agencyOrderID)
	if err != nil {
		return processapp.OrderTimeline{}, false, fmt.Errorf("load events for timeline: %w", err)
	}
	timeline.Events = []processapp.TimelineEvent{}
	for rows.Next() {
		var item processapp.TimelineEvent
		var applied sql.NullInt64
		if err := rows.Scan(
			&item.ID, &item.Seq, &item.Source, &item.Type, &item.Payload,
			&item.OccurredAt, &item.RecordedAt, &applied, &item.FlowID, &item.CausationEffectID, &item.SourceEntityVersion,
		); err != nil {
			rows.Close()
			return processapp.OrderTimeline{}, false, err
		}
		if applied.Valid {
			item.AppliedVersion = &applied.Int64
		}
		timeline.Events = append(timeline.Events, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return processapp.OrderTimeline{}, false, err
	}

	rows, err = queryer.QueryContext(ctx, `SELECT id::text,agency_order_id::text,COALESCE(merchant_order_id::text,''),flow_id::text,request_id,target,type,idempotency_key,input_hash,payload,COALESCE(caused_by_event_id,0),created_at,delivery_state,claim_version,attempt_count,next_attempt_at,consumed_at FROM order_process_effects WHERE agency_order_id=$1 ORDER BY created_at,id`, agencyOrderID)
	if err != nil {
		return timeline, false, err
	}
	timeline.Effects = []processapp.TimelineEffect{}
	for rows.Next() {
		var item processapp.TimelineEffect
		var consumed sql.NullTime
		if err := rows.Scan(&item.ID, &item.AgencyOrderID, &item.MerchantOrderID, &item.FlowID, &item.RequestID, &item.Target, &item.Type, &item.IdempotencyKey, &item.InputHash, &item.Payload, &item.CausedByEventID, &item.CreatedAt, &item.DeliveryState, &item.ClaimVersion, &item.AttemptCount, &item.NextAttemptAt, &consumed); err != nil {
			rows.Close()
			return timeline, false, err
		}
		if consumed.Valid {
			item.ConsumedAt = &consumed.Time
		}
		timeline.Effects = append(timeline.Effects, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return timeline, false, err
	}
	rows, err = queryer.QueryContext(ctx, `SELECT receipt FROM order_process_requests WHERE agency_order_id=$1 ORDER BY created_at,request_id`, agencyOrderID)
	if err != nil {
		return timeline, false, err
	}
	defer rows.Close()
	timeline.Requests = []procmsg.RequestReceipt{}
	for rows.Next() {
		var raw []byte
		var item procmsg.RequestReceipt
		if err := rows.Scan(&raw); err != nil {
			return timeline, false, err
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return timeline, false, err
		}
		timeline.Requests = append(timeline.Requests, item)
	}
	return timeline, true, rows.Err()
}
