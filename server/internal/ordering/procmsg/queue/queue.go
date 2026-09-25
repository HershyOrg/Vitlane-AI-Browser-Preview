// Package queue delivers immutable effects. It never selects the next Owner,
// decides an order transition, or interprets a provider result.
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Queue struct {
	database *sharedpostgres.Database
	notify   func()
	wakeMu   sync.RWMutex
}

func New(database *sharedpostgres.Database, notify func()) *Queue {
	return &Queue{database: database, notify: notify}
}

// Consume is called by a single Owner. Acceptance, that Owner's local state (or
// durable execution reservation) and its events share one transaction. Returning
// successfully transfers responsibility to that Owner; it does not mean success.
func (q *Queue) Wake() { q.wake() }

func (q *Queue) Consume(ctx context.Context, delivery procmsg.Delivery, accept func(context.Context, procmsg.ProcessEffect) error) error {
	if accept == nil {
		return procmsg.ErrEffectInvalid
	}
	return q.WithDelivery(ctx, delivery, func(tx context.Context, e procmsg.ProcessEffect) (procmsg.Disposition, error) {
		if err := accept(tx, e); err != nil {
			return procmsg.KeepClaim, err
		}
		return procmsg.Consumed, nil
	})
}

func (q *Queue) WithDelivery(ctx context.Context, delivery procmsg.Delivery, accept func(context.Context, procmsg.ProcessEffect) (procmsg.Disposition, error)) error {
	if sharedapp.InTransaction(ctx) || accept == nil {
		return procmsg.ErrEffectInvalid
	}
	inserted := false
	err := q.database.WithinTransaction(ctx, func(tx context.Context) error {
		tx, flush := procmsg.CollectEvents(tx, q.database.Queryer(tx))
		effect, version, consumed, err := q.lock(tx, delivery.Effect.ID)
		if err != nil {
			return err
		}
		if consumed {
			return nil
		}
		if version != delivery.ClaimVersion {
			return procmsg.ErrStaleDelivery
		}
		if effect.Target != delivery.Effect.Target || effect.InputHash != delivery.Effect.InputHash || procmsg.PayloadHash(effect.Payload) != effect.InputHash {
			return procmsg.ErrEffectInvalid
		}
		// Scope is reconstructed from the locked durable message, never from a
		// client header. Owner repositories also bind their records to this ID.
		tx = procmsg.WithExecutionScope(tx, procmsg.ExecutionScope{AgencyOrderID: effect.AgencyOrderID, MerchantOrderID: effect.MerchantOrderID, FlowID: effect.FlowID, EffectID: effect.ID, Action: effect.Type, ClaimVersion: version, InputHash: effect.InputHash})
		disposition, err := accept(tx, effect)
		if err != nil {
			return err
		}
		switch disposition {
		case procmsg.KeepClaim:
		case procmsg.RetryDelivery:
			_, err = q.database.Queryer(tx).ExecContext(tx, `UPDATE order_process_effects SET lease_until=NULL,next_attempt_at=clock_timestamp()+interval '5 seconds' WHERE id=$1 AND claim_version=$2`, effect.ID, version)
		case procmsg.Consumed:
			_, err = q.database.Queryer(tx).ExecContext(tx, `UPDATE order_process_effects SET delivery_state='CONSUMED',consumed_at=clock_timestamp(),lease_until=NULL WHERE id=$1 AND claim_version=$2`, effect.ID, version)
		default:
			return procmsg.ErrEffectInvalid
		}
		inserted = err == nil
		if err != nil {
			return err
		}
		return flush()
	})
	if err == nil && inserted {
		q.wake()
	}
	return err
}

// Observe preserves a late verified Owner fact after another worker reclaimed
// the message. It never acknowledges or changes that newer delivery claim.
func (q *Queue) Observe(ctx context.Context, delivery procmsg.Delivery, observe func(context.Context, procmsg.ProcessEffect) error) error {
	return q.WithinTransaction(ctx, func(tx context.Context) error {
		e, err := q.Read(tx, delivery.Effect.ID)
		if err != nil {
			return err
		}
		if e.InputHash != delivery.Effect.InputHash || e.Target != delivery.Effect.Target {
			return procmsg.ErrEffectInvalid
		}
		return observe(procmsg.EffectContext(tx, e), e)
	})
}

const columns = `id::text,agency_order_id::text,COALESCE(merchant_order_id::text,''),COALESCE(flow_id::text,''),request_id,target,type,idempotency_key,input_hash,payload,COALESCE(caused_by_event_id,0),created_at,claim_version,delivery_state`

func scanEffect(row interface{ Scan(...any) error }) (procmsg.ProcessEffect, int64, bool, error) {
	var e procmsg.ProcessEffect
	var version int64
	var state string
	err := row.Scan(&e.ID, &e.AgencyOrderID, &e.MerchantOrderID, &e.FlowID, &e.RequestID, &e.Target, &e.Type, &e.IdempotencyKey, &e.InputHash, &e.Payload, &e.CausedByEventID, &e.CreatedAt, &version, &state)
	return e, version, state == "CONSUMED", err
}
func (q *Queue) lock(ctx context.Context, id string) (procmsg.ProcessEffect, int64, bool, error) {
	return scanEffect(q.database.Queryer(ctx).QueryRowContext(ctx, `SELECT `+columns+` FROM order_process_effects WHERE id=$1 FOR UPDATE`, id))
}

func (q *Queue) Claim(ctx context.Context, targets []string, now time.Time, limit int) ([]procmsg.Delivery, error) {
	if len(targets) == 0 || limit < 1 {
		return nil, nil
	}
	rows, err := q.database.Queryer(ctx).QueryContext(ctx, `WITH due AS (
 SELECT id FROM order_process_effects WHERE delivery_state='PENDING' AND next_attempt_at<=$2
 AND (lease_until IS NULL OR lease_until<=$2) AND target=ANY(string_to_array($1,','))
 ORDER BY next_attempt_at,id LIMIT $3 FOR UPDATE SKIP LOCKED
 ), claimed AS (UPDATE order_process_effects e SET claim_version=e.claim_version+1,
 attempt_count=e.attempt_count+1,lease_until=$2::timestamptz+interval '2 minutes'
 FROM due WHERE e.id=due.id RETURNING e.*)
 SELECT `+columns+`,attempt_count FROM claimed`, strings.Join(targets, ","), now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []procmsg.Delivery
	for rows.Next() {
		var d procmsg.Delivery
		var state string
		e := &d.Effect
		if err := rows.Scan(&e.ID, &e.AgencyOrderID, &e.MerchantOrderID, &e.FlowID, &e.RequestID, &e.Target, &e.Type, &e.IdempotencyKey, &e.InputHash, &e.Payload, &e.CausedByEventID, &e.CreatedAt, &d.ClaimVersion, &state, &d.Attempt); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// Report runs inside the Owner transaction that persisted the verified result.
// A report key includes a caller supplied Owner revision, never a delivery claim.
func (q *Queue) Report(ctx context.Context, e procmsg.ProcessEffect, outcome, code string, result any, revision string, now time.Time) error {
	if !sharedapp.InTransaction(ctx) || revision == "" {
		return procmsg.ErrEffectInvalid
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = procmsg.AppendEvent(ctx, q.database.Queryer(ctx), procmsg.ProcessEvent{AgencyOrderID: e.AgencyOrderID, FlowID: e.FlowID, CausationEffectID: e.ID, Source: procmsg.Source(e.Target), Type: procmsg.EventEffectReported, Payload: procmsg.EffectReport{EffectID: e.ID, EffectType: e.Type, MerchantOrderID: e.MerchantOrderID, RequestID: e.RequestID, Outcome: outcome, Code: code, Result: raw}, DedupKey: "effect-report:" + e.ID + ":" + revision, OccurredAt: now}, now)
	return err
}

// Retry only concerns delivery. Domain outcomes and provider UNKNOWN states are
// reported by the Owner, and never fabricated from a handler error or timeout.
func (q *Queue) Retry(ctx context.Context, d procmsg.Delivery, now time.Time) error {
	delay := time.Duration(min(60, d.Attempt*d.Attempt)) * time.Second
	_, err := q.database.Queryer(ctx).ExecContext(ctx, `UPDATE order_process_effects SET lease_until=NULL,next_attempt_at=$3 WHERE id=$1 AND claim_version=$2 AND delivery_state='PENDING'`, d.Effect.ID, d.ClaimVersion, now.Add(delay))
	return err
}

func (q *Queue) Read(ctx context.Context, id string) (procmsg.ProcessEffect, error) {
	e, _, _, err := scanEffect(q.database.Queryer(ctx).QueryRowContext(ctx, `SELECT `+columns+` FROM order_process_effects WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, procmsg.ErrEffectInvalid
	}
	return e, err
}

func (q *Queue) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	if sharedapp.InTransaction(ctx) {
		return procmsg.ErrEffectInvalid
	}
	err := q.database.WithinTransaction(ctx, func(tx context.Context) error {
		tx, flush := procmsg.CollectEvents(tx, q.database.Queryer(tx))
		if err := fn(tx); err != nil {
			return err
		}
		return flush()
	})
	if err == nil {
		q.wake()
	}
	return err
}

func ResultRevision(outcome, code string) string { return fmt.Sprintf("%s:%s", outcome, code) }

func (q *Queue) SetWake(fn func()) { q.wakeMu.Lock(); defer q.wakeMu.Unlock(); q.notify = fn }
func (q *Queue) wake() {
	q.wakeMu.RLock()
	fn := q.notify
	q.wakeMu.RUnlock()
	if fn != nil {
		fn()
	}
}
