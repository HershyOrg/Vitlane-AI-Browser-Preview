// Package postgres는 agency_order_processes의 유일한 writer다(ADR-0056 §1).
// 이 패키지 밖에서 이 테이블을 쓰는 SQL이 0임을 아키텍처 계약 테스트가
// 강제한다. 이벤트의 applied_* 컬럼과 Effect의 발행 행도 여기서만 쓴다
// (Effect 실행 컬럼은 target executor 소유 — ADR-0056 §3). ADR-0070의 MO 결정
// 사영(agency_order_process_merchant_orders)과 결정 기록(order_process_decisions)
// 역시 이 패키지의 결정 transaction만 쓴다.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	processapp "github.com/vitlane/vitlane/server/internal/ordering/process/app"
	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// ErrDecisionConflict는 version CAS 패배다 — transaction을 버리고 다음 tick이
// 재관찰한다(단일 스케줄러에서는 발생하지 않아야 하며, 발생 자체가 신호다).
var ErrDecisionConflict = errors.New("ORDER_PROCESS_DECISION_CONFLICT")

type Store struct {
	database *sharedpostgres.Database
}

func NewStore(database *sharedpostgres.Database) *Store {
	return &Store{database: database}
}

// ListOrdersWithUnappliedEvents는 미소비 이벤트가 있는 주문만 돌려준다 —
// 전수 스캔의 자리를 부분 인덱스 조회가 대신한다(ADR-0056).
func (r *Store) ListOrdersWithUnappliedEvents(
	ctx context.Context,
	limit int,
) ([]string, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT DISTINCT agency_order_id::text
		FROM order_process_events
		WHERE applied_at IS NULL
		ORDER BY agency_order_id
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list orders with unapplied events: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListDueTimers discovers persisted deadlines without deciding their effect.
func (r *Store) ListDueTimers(ctx context.Context, now time.Time, limit int) ([]processapp.DueTimer, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT agency_order_id::text,wake_at FROM agency_order_processes WHERE wake_at IS NOT NULL AND wake_at<=$1 ORDER BY wake_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	timers := []processapp.DueTimer{}
	for rows.Next() {
		var timer processapp.DueTimer
		if err := rows.Scan(&timer.AgencyOrderID, &timer.At); err != nil {
			return nil, err
		}
		timers = append(timers, timer)
	}
	return timers, rows.Err()
}

func (r *Store) LoadDecision(ctx context.Context, agencyOrderID string) (input processapp.DecisionInput, err error) {
	txContext := ctx
	err = func() error {
		q := r.database.Queryer(txContext)
		p := processdomain.Process{AgencyOrderID: agencyOrderID}
		var stateRaw []byte
		var terminalReason, lastReason sql.NullString
		var wakeAt sql.NullTime
		var state string
		row := q.QueryRowContext(txContext, `
			SELECT state, COALESCE(terminal_reason,''), COALESCE(last_reason_code,''),
			       version, last_applied_seq, wake_at, process_state, created_at, updated_at
			FROM agency_order_processes
			WHERE agency_order_id=$1
			FOR UPDATE
		`, agencyOrderID)
		if scanErr := row.Scan(&state, &terminalReason, &lastReason,
			&p.Version, &p.LastAppliedSeq, &wakeAt, &stateRaw,
			&p.CreatedAt, &p.UpdatedAt); scanErr != nil {
			if !errors.Is(scanErr, sql.ErrNoRows) {
				return fmt.Errorf("load process: %w", scanErr)
			}
		} else {
			p.State = processdomain.State(state)
			p.TerminalReason = processdomain.TerminalReason(terminalReason.String)
			p.LastReasonCode = lastReason.String
			if wakeAt.Valid {
				at := wakeAt.Time
				p.WakeAt = &at
			}
			if len(stateRaw) > 0 {
				if err := json.Unmarshal(stateRaw, &p.ProcessState); err != nil {
					return fmt.Errorf("decode process process_state: %w", err)
				}
			}
		}

		event, err := r.nextUnappliedEvent(txContext, agencyOrderID, p.LastAppliedSeq)
		if err != nil {
			return err
		}
		input = processapp.DecisionInput{Process: p, Event: event}
		return nil

	}()
	return
}

// SaveDecision persists an already-computed decision and its consumed cursor.
// The Process has inserted its effects in the same transaction.
func (r *Store) SaveDecision(ctx context.Context, input processapp.DecisionInput, decision processdomain.Decision, effects []processapp.EffectWrite, now time.Time) (outcome processapp.DecisionOutcome, err error) {
	txContext := ctx
	p, event := input.Process, input.Event
	agencyOrderID := p.AgencyOrderID
	err = func() error {
		q := r.database.Queryer(txContext)
		stateJSON, err := json.Marshal(decision.ProcessState)
		if err != nil {
			return fmt.Errorf("encode process process_state: %w", err)
		}
		lastSeq := event.Seq
		newVersion := p.Version + 1
		if p.Version == 0 {
			if _, err := q.ExecContext(txContext, `
				INSERT INTO agency_order_processes(
					agency_order_id, state, terminal_reason, last_reason_code,
					version, last_applied_seq, wake_at, process_state,
					created_at, updated_at
				) VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),1,$5,$6,$7,$8,$8)
			`, agencyOrderID, string(decision.State), string(decision.TerminalReason),
				decision.LastReasonCode, lastSeq, decision.WakeAt, stateJSON, now,
			); err != nil {
				return fmt.Errorf("create process: %w", err)
			}
			newVersion = 1
		} else {
			result, err := q.ExecContext(txContext, `
				UPDATE agency_order_processes
				SET state=$2, terminal_reason=NULLIF($3,''),
				    last_reason_code=NULLIF($4,''), version=version+1,
				    last_applied_seq=$5, wake_at=$6, process_state=$7, updated_at=$8
				WHERE agency_order_id=$1 AND version=$9
			`, agencyOrderID, string(decision.State), string(decision.TerminalReason),
				decision.LastReasonCode, lastSeq, decision.WakeAt, stateJSON, now,
				p.Version)
			if err != nil {
				return fmt.Errorf("apply process decision: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return ErrDecisionConflict
			}
		}

		effectRecords := make([]map[string]any, 0, len(effects))
		for _, effect := range effects {
			effectRecords = append(effectRecords, map[string]any{"target": effect.Target, "type": effect.Type, "idempotencyKey": effect.IdempotencyKey, "causedByEventId": effect.CausedByEventID})
		}
		changedMerchantOrders := make([]processdomain.MerchantOrderDecision, 0)
		for _, merchantOrder := range decision.MerchantOrders {
			if !merchantOrder.Changed {
				continue
			}
			changedMerchantOrders = append(changedMerchantOrders, merchantOrder)
			if err := r.upsertMerchantOrderDecision(
				txContext, agencyOrderID, merchantOrder, newVersion, now,
			); err != nil {
				return err
			}
		}
		if err := r.recordDecision(txContext, agencyOrderID, newVersion, p, decision,
			event.Seq, event.Seq, changedMerchantOrders, effectRecords, stateJSON, now,
		); err != nil {
			return err
		}

		if _, err := q.ExecContext(txContext, `
			UPDATE order_process_events
			SET applied_at=$2, applied_version=$3
			WHERE id=$1
		`, event.ID, now, newVersion); err != nil {
			return fmt.Errorf("mark event applied: %w", err)
		}

		outcome = processapp.DecisionOutcome{
			Applied:               true,
			EventSeq:              event.Seq,
			StageChanged:          decision.StageChanged,
			State:                 decision.State,
			TerminalReason:        decision.TerminalReason,
			LastReasonCode:        decision.LastReasonCode,
			EffectsIssued:         len(decision.Effects),
			MerchantOrdersChanged: len(changedMerchantOrders),
		}
		return nil
	}()
	return
}

// upsertMerchantOrderDecision은 MO 결정 사영 행이다 — 고객·운영자 사영이
// phase·intent·attention을 JOIN으로 읽는다(결정=Process, 고지=Projection).
func (r *Store) upsertMerchantOrderDecision(
	ctx context.Context,
	agencyOrderID string,
	merchantOrder processdomain.MerchantOrderDecision,
	version int64,
	now time.Time,
) error {
	var attentionCode, attentionEffectID, intentKind, intentOutcome, intentCode string
	if merchantOrder.Attention != nil {
		attentionCode = merchantOrder.Attention.Code
		attentionEffectID = merchantOrder.Attention.EffectID
	}
	if merchantOrder.Intent != nil {
		intentKind = merchantOrder.Intent.Kind
		intentOutcome = merchantOrder.Intent.Outcome
		intentCode = merchantOrder.Intent.Code
	}
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO agency_order_process_merchant_orders(
			merchant_order_id, agency_order_id, allocation_id, phase, owner_state,
			funding_state, lock_state, compensation_state, compensation_action,
			compensation_cause, failure_code, attention_code, attention_effect_id,
			intent_kind, intent_outcome, intent_code, decided_version, updated_at
		) VALUES ($1,$2,NULLIF($3,''),$4,$5,NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),
		          NULLIF($9,''),NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),
		          NULLIF($13,''),NULLIF($14,''),NULLIF($15,''),NULLIF($16,''),$17,$18)
		ON CONFLICT (merchant_order_id) DO UPDATE SET
			allocation_id=EXCLUDED.allocation_id, phase=EXCLUDED.phase,
			owner_state=EXCLUDED.owner_state, funding_state=EXCLUDED.funding_state,
			lock_state=EXCLUDED.lock_state, compensation_state=EXCLUDED.compensation_state,
			compensation_action=EXCLUDED.compensation_action,
			compensation_cause=EXCLUDED.compensation_cause, failure_code=EXCLUDED.failure_code,
			attention_code=EXCLUDED.attention_code,
			attention_effect_id=EXCLUDED.attention_effect_id,
			intent_kind=EXCLUDED.intent_kind, intent_outcome=EXCLUDED.intent_outcome,
			intent_code=EXCLUDED.intent_code, decided_version=EXCLUDED.decided_version,
			updated_at=EXCLUDED.updated_at
	`, merchantOrder.MerchantOrderID, agencyOrderID, merchantOrder.AllocationID,
		string(merchantOrder.Phase), merchantOrder.OwnerState, merchantOrder.FundingState,
		merchantOrder.LockState, merchantOrder.Compensation.State,
		merchantOrder.Compensation.Action, merchantOrder.Compensation.Cause,
		merchantOrder.FailureCode, attentionCode, attentionEffectID,
		intentKind, intentOutcome, intentCode, version, now)
	if err != nil {
		return fmt.Errorf("upsert merchant order decision: %w", err)
	}
	return nil
}

// recordDecision은 결정 감사 기록이다(append-only, 권위 아님 — AGENTS §7).
func (r *Store) recordDecision(
	ctx context.Context,
	agencyOrderID string,
	version int64,
	before processdomain.Process,
	decision processdomain.Decision,
	seqFrom, seqTo int64,
	merchantOrders []processdomain.MerchantOrderDecision,
	effects []map[string]any,
	stateJSON []byte,
	now time.Time,
) error {
	diffJSON, err := json.Marshal(merchantOrders)
	if err != nil {
		return fmt.Errorf("encode merchant order diff: %w", err)
	}
	effectsJSON, err := json.Marshal(effects)
	if err != nil {
		return fmt.Errorf("encode decision effects: %w", err)
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO order_process_decisions(
			agency_order_id, version, seq_from, seq_to, stage_before, stage_after,
			terminal_reason, last_reason_code, stage_changed, merchant_order_diff,
			effects, wake_at, process_state, decided_at
		) VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,NULLIF($7,''),NULLIF($8,''),$9,$10,$11,$12,$13,$14)
		ON CONFLICT (agency_order_id, version) DO NOTHING
	`, agencyOrderID, version, seqFrom, seqTo, string(before.State), string(decision.State),
		string(decision.TerminalReason), decision.LastReasonCode, decision.StageChanged,
		diffJSON, effectsJSON, decision.WakeAt, stateJSON, now)
	if err != nil {
		return fmt.Errorf("record process decision: %w", err)
	}
	return nil
}

func (r *Store) nextUnappliedEvent(ctx context.Context, agencyOrderID string, afterSeq int64) (*processdomain.Event, error) {
	var event processdomain.Event
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, seq, source, type, payload, occurred_at,regexp_replace(dedup_key,':v[0-9]+$',''),COALESCE(source_entity_version,0)
		FROM order_process_events
		WHERE agency_order_id=$1 AND applied_at IS NULL AND seq > $2
		ORDER BY seq
		LIMIT 1
	`, agencyOrderID, afterSeq).Scan(&event.ID, &event.Seq, &event.Source, &event.Type,
		&event.Payload, &event.OccurredAt, &event.EntityKey, &event.SourceEntityVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read next unapplied event: %w", err)
	}
	return &event, nil
}

// merchantOrderIDFromPayload는 MO scope Effect payload의 대상 MO다(없으면 "").
func merchantOrderIDFromPayload(payload []byte) string {
	var envelope struct {
		MerchantOrderID string `json:"merchantOrderId"`
	}
	if len(payload) == 0 || json.Unmarshal(payload, &envelope) != nil {
		return ""
	}
	return envelope.MerchantOrderID
}
