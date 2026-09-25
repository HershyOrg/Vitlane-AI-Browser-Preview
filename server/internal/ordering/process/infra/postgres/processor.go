package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	processdomain "github.com/vitlane/vitlane/server/internal/ordering/process/domain"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
)

func (r *Store) LockOrder(ctx context.Context, order string) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "order_processor:"+order)
	return err
}

func (r *Store) AppendRequest(ctx context.Context, request procmsg.ActionRequest, now time.Time) (bool, error) {
	hash := request.CanonicalHash()
	var existing string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT request_hash FROM order_process_requests WHERE agency_order_id=$1 AND request_id=$2`, request.AgencyOrderID, request.ID).Scan(&existing)
	if err == nil {
		if existing != hash {
			return false, processdomain.ErrRequestConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	initial := procmsg.RequestReceipt{SchemaVersion: "vitlane.order-process-receipt.v1", AgencyOrderID: request.AgencyOrderID, RequestID: request.ID, FlowID: procmsg.RequestFlowID(request.AgencyOrderID, request.ID), MerchantOrderID: request.MerchantOrderID, Kind: request.Kind, Outcome: "RECEIVED", Guidance: procmsg.Guidance{ReasonCode: "PROCESS_PENDING", CustomerAction: "WAIT", OperatorAction: "WAIT"}}
	body, err := json.Marshal(initial)
	if err != nil {
		return false, err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO order_process_requests(agency_order_id,request_id,request_hash,merchant_order_id,actor_id,receipt,created_at,updated_at) VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$7)`, request.AgencyOrderID, request.ID, hash, request.MerchantOrderID, request.ActorID, body, now)
	if err != nil {
		return false, err
	}
	return procmsg.AppendEvent(ctx, r.database.Queryer(ctx), procmsg.ProcessEvent{AgencyOrderID: request.AgencyOrderID, Source: procmsg.Source(request.ActorRole), Type: procmsg.EventActionRequested, Payload: request, DedupKey: "process-request:" + request.AgencyOrderID + ":" + request.ID, OccurredAt: now}, now)
}

func (r *Store) ReadRequestReceipt(ctx context.Context, order, id string) (result procmsg.RequestReceipt, err error) {
	var raw []byte
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT receipt FROM order_process_requests WHERE agency_order_id=$1 AND request_id=$2`, order, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, procmsg.ErrRequestNotFound
	}
	if err != nil {
		return
	}
	err = json.Unmarshal(raw, &result)
	return
}
func (r *Store) SaveRequestReceipt(ctx context.Context, order string, result procmsg.RequestReceipt, now time.Time) error {
	if result.RequestID == "" {
		return nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `UPDATE order_process_requests SET receipt=jsonb_set($3::jsonb,'{requestId}',to_jsonb(request_id)),updated_at=$4 WHERE agency_order_id=$1 AND (request_id=$2 OR receipt->>'flowId'=$5)`, order, result.RequestID, raw, now, procmsg.RequestFlowID(order, result.RequestID))
	return err
}

func (r *Store) InsertEffect(ctx context.Context, order string, draft processdomain.EffectDraft, now time.Time) (bool, error) {
	raw, err := json.Marshal(draft.Payload)
	if err != nil {
		return false, err
	}
	var scope struct {
		MerchantOrderID string `json:"merchantOrderId"`
		RequestID       string `json:"requestId"`
	}
	if err = json.Unmarshal(raw, &scope); err != nil {
		return false, err
	}
	id := procmsg.EffectIdentity(order, draft.IdempotencyKey)
	flow := procmsg.EffectIdentity(order, "flow:"+draft.IdempotencyKey)
	if scope.RequestID != "" {
		flow = procmsg.RequestFlowID(order, scope.RequestID)
	}
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO order_process_effects(id,agency_order_id,merchant_order_id,flow_id,request_id,target,type,idempotency_key,input_hash,payload,caused_by_event_id,delivery_state,next_attempt_at,created_at,updated_at)
 VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,0),'PENDING',$12,$12,$12)
 ON CONFLICT(id) DO NOTHING`, id, order, scope.MerchantOrderID, flow, scope.RequestID, draft.Target, draft.Type, draft.IdempotencyKey, procmsg.PayloadHash(raw), raw, draft.CausedByEventID, now)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		var persistedHash, target, kind string
		if err = r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT input_hash,target,type FROM order_process_effects WHERE id=$1`, id).Scan(&persistedHash, &target, &kind); err != nil {
			return false, err
		}
		if persistedHash != procmsg.PayloadHash(raw) || target != draft.Target || kind != draft.Type {
			return false, procmsg.ErrEffectInvalid
		}
	}
	return n > 0, err
}

func (r *Store) ListRequestReceipts(ctx context.Context, order string) ([]procmsg.RequestReceipt, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT receipt FROM (SELECT DISTINCT ON (COALESCE(receipt->>'flowId',request_id)) receipt,created_at,request_id FROM order_process_requests WHERE agency_order_id=$1 ORDER BY COALESCE(receipt->>'flowId',request_id),created_at DESC,request_id) flows ORDER BY (receipt->>'outcome' IN ('COMPLETED','REJECTED')),created_at DESC,request_id LIMIT 100`, order)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []procmsg.RequestReceipt{}
	for rows.Next() {
		var raw []byte
		var receipt procmsg.RequestReceipt
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return nil, err
		}
		out = append(out, receipt)
	}
	return out, rows.Err()
}
