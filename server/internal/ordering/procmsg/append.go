package procmsg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Queryer는 shared postgres queryer의 부분 시그니처다 — owner repository가
// `database.Queryer(txContext)`를 그대로 넘긴다.
type Queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

var ErrEventInvalid = errors.New("ORDER_PROCESS_EVENT_INVALID")

// AppendTimerEvent returns the actual INSERT result, including concurrent dedup.
// A timer transaction has no Owner resource writes, so it can acquire the event
// stream lock immediately. Owner facts must continue using the deferred collector.
func AppendTimerEvent(ctx context.Context, q Queryer, event ProcessEvent, now time.Time) (bool, error) {
	if event.Source != SourceTimer || event.Type != EventTimerFired {
		return false, ErrEventInvalid
	}
	return AppendEvent(context.WithValue(ctx, collectorKey{}, struct{}{}), q, event, now)
}

// LockOrderEventStream must be acquired before computing any aggregate that
// will be embedded in an order process event. The transaction-scoped lock
// makes the aggregate snapshot and the following event sequence one serialized
// observation across otherwise independent MerchantOrder/unit/payment rows.
func LockOrderEventStream(
	ctx context.Context, q Queryer, agencyOrderID string,
) error {
	if strings.TrimSpace(agencyOrderID) == "" {
		return ErrEventInvalid
	}
	if _, collecting := ctx.Value(collectorKey{}).(*collector); collecting {
		return nil
	}
	if _, err := q.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"order_process_events:"+agencyOrderID,
	); err != nil {
		return fmt.Errorf("acquire process event lock: %w", err)
	}
	return nil
}

// AppendEvent는 이벤트 한 건을 durable 기록한다. 반드시 owner 상태 변경과
// **같은 transaction 안에서** 호출해야 한다(ADR-0056 §2) — 주문별
// pg_advisory_xact_lock으로 seq를 채번하므로, transaction 밖에서 부르면 락이
// 문장 단위로 풀려 직렬성이 깨진다.
//
// dedup_key 충돌(같은 전이의 재기록)은 no-op이며 (false, nil)을 돌려준다.
func AppendEvent(
	ctx context.Context,
	q Queryer,
	event ProcessEvent,
	now time.Time,
) (bool, error) {
	if strings.TrimSpace(event.AgencyOrderID) == "" || event.Source == "" ||
		strings.TrimSpace(event.Type) == "" || strings.TrimSpace(event.DedupKey) == "" {
		return false, ErrEventInvalid
	}
	if expected, known := EventOwner(event.Type); known && expected != event.Source {
		return false, ErrEventInvalid
	}
	// Row-version keys are created by the Owner emitter. Persist the version
	// explicitly so delivery order cannot overwrite a newer entity observation.
	if event.SourceEntityVersion == 0 {
		if _, suffix, ok := strings.Cut(event.DedupKey, ":v"); ok {
			v, e := strconv.ParseInt(suffix, 10, 64)
			if e == nil && v > 0 {
				event.SourceEntityVersion = v
			}
		}
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return false, fmt.Errorf("marshal process event payload: %w", err)
	}
	if scope, ok := ExecutionFrom(ctx); ok {
		if scope.AgencyOrderID != event.AgencyOrderID {
			return false, ErrEventInvalid
		}
		event.FlowID = scope.FlowID
		event.CausationEffectID = scope.EffectID
	}
	if collected, inserted := collectEvent(ctx, event, payload, now); collected {
		return inserted, nil
	}
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = now
	}
	// Reacquiring a transaction-scoped advisory lock is safe. Aggregate-bearing
	// emitters acquire it before COUNT; scalar events acquire it here.
	if err := LockOrderEventStream(ctx, q, event.AgencyOrderID); err != nil {
		return false, err
	}
	result, err := q.ExecContext(ctx, `
		INSERT INTO order_process_events(
			agency_order_id, seq, source, type, payload, dedup_key,
			occurred_at, recorded_at, flow_id, causation_effect_id, source_entity_version
		)
		SELECT $1,
		       COALESCE((SELECT MAX(seq) FROM order_process_events
		                 WHERE agency_order_id=$1), 0) + 1,
		       $2, $3, $4, $5, $6, $7, NULLIF($8,'')::uuid, NULLIF($9,'')::uuid, NULLIF($10,0)
		ON CONFLICT (dedup_key) DO NOTHING
	`, event.AgencyOrderID, string(event.Source), event.Type, payload,
		event.DedupKey, occurredAt, now, event.FlowID, event.CausationEffectID, event.SourceEntityVersion)
	if err != nil {
		return false, fmt.Errorf("append process event: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return inserted > 0, nil
}
