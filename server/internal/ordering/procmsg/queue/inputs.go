package queue

import (
	"context"
	"encoding/json"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type ActionInputs struct {
	db    *sharedpostgres.Database
	table string
}

func NewActionInputs(db *sharedpostgres.Database, owner procmsg.Target) *ActionInputs {
	var table string
	switch owner {
	case procmsg.TargetProcurement:
		table = "procurement_process_inputs"
	case procmsg.TargetAgencyOrder:
		table = "agency_order_process_inputs"
	case procmsg.TargetLogistics:
		table = "logistics_process_inputs"
	case procmsg.TargetPayment:
		table = "payment_process_inputs"
	default:
		panic("action input owner required")
	}
	return &ActionInputs{db, table}
}

func (s *ActionInputs) Stage(ctx context.Context, r procmsg.ActionRequest, input any) (procmsg.ActionRequest, error) {
	if err := r.Validate(); err != nil {
		return r, err
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > 64*1024 {
		return r, procmsg.ErrRequestInvalid
	}
	r.InputRef = procmsg.EffectIdentity(r.AgencyOrderID, "input:"+s.table+":"+r.ID)
	r.InputHash = procmsg.PayloadHash(raw)
	requestHash := r.CanonicalHash()
	binding, _ := json.Marshal(r)
	var stored string
	err = s.db.Queryer(ctx).QueryRowContext(ctx, `INSERT INTO `+s.table+`(id,agency_order_id,merchant_order_id,actor_id,request_hash,input_hash,payload,request,created_at)
 VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,clock_timestamp())
 ON CONFLICT(id) DO UPDATE SET id=EXCLUDED.id RETURNING request_hash`, r.InputRef, r.AgencyOrderID, r.MerchantOrderID, r.ActorID, requestHash, r.InputHash, raw, binding).Scan(&stored)
	if err != nil {
		return r, err
	}
	if stored != requestHash {
		return r, procmsg.ErrRequestInvalid
	}
	return r, nil
}

func (s *ActionInputs) Load(ctx context.Context, r procmsg.ActionRequest) (json.RawMessage, error) {
	var raw, binding []byte
	var hash string
	err := s.db.Queryer(ctx).QueryRowContext(ctx, `SELECT payload,input_hash,request FROM `+s.table+` WHERE id=$1 AND agency_order_id=$2 AND COALESCE(merchant_order_id::text,'')=$3 AND actor_id=$4`, r.InputRef, r.AgencyOrderID, r.MerchantOrderID, r.ActorID).Scan(&raw, &hash, &binding)
	if err != nil {
		return nil, err
	}
	var original procmsg.ActionRequest
	if json.Unmarshal(binding, &original) != nil {
		return nil, procmsg.ErrEffectInvalid
	}
	compared := r
	if original.AllocationID == "" {
		compared.AllocationID = ""
	}
	if original.TaskID == "" {
		compared.TaskID = ""
	}
	if original.CanonicalHash() != compared.CanonicalHash() {
		return nil, procmsg.ErrEffectInvalid
	}
	if hash != r.InputHash || procmsg.PayloadHash(raw) != hash {
		return nil, procmsg.ErrEffectInvalid
	}
	return raw, nil
}

// Lookup returns the staged request only to its authenticated originating actor.
func (s *ActionInputs) Lookup(ctx context.Context, order, id, actor string) (r procmsg.ActionRequest, err error) {
	ref := procmsg.EffectIdentity(order, "input:"+s.table+":"+id)
	var raw []byte
	err = s.db.Queryer(ctx).QueryRowContext(ctx, `SELECT request FROM `+s.table+` WHERE id=$1 AND agency_order_id=$2 AND actor_id=$3`, ref, order, actor).Scan(&raw)
	if err != nil {
		return
	}
	err = json.Unmarshal(raw, &r)
	return
}
