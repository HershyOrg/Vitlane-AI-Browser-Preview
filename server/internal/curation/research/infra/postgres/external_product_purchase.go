package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"time"
)

func (r *Repository) markExternalProductPurchase(ctx context.Context, in researchapp.MarkExternalPurchaseInput, now time.Time) (researchapp.PurchaseFeedback, error) {
	var result researchapp.PurchaseFeedback
	if in.ProductRef == nil || !in.ProductRef.Source.KoreanExternal() || in.ProductRef.Validate() != nil {
		return result, fault.New(fault.InvalidInput, "PURCHASE_RECORD_INVALID", false)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	err = r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		var owned bool
		if err := q.QueryRowContext(tx, `SELECT true FROM curations WHERE user_id=$1 AND id=$2 AND archived_at IS NULL FOR KEY SHARE`, in.UserID, in.CurationID).Scan(&owned); err != nil {
			return err
		}
		if err := r.lockPurchaseFeedback(tx, in.UserID, in.CurationID); err != nil {
			return err
		}
		var oldHash string
		var replay []byte
		err := q.QueryRowContext(tx, `SELECT command_hash,result FROM research_purchase_commands WHERE user_id=$1 AND curation_id=$2 AND idempotency_key=$3`, in.UserID, in.CurationID, in.IdempotencyKey).Scan(&oldHash, &replay)
		if err == nil {
			if oldHash != hash {
				return fault.New(fault.Conflict, "PURCHASE_RECORD_IDEMPOTENCY_CONFLICT", false)
			}
			return json.Unmarshal(replay, &result)
		}
		if err != sql.ErrNoRows {
			return err
		}
		var identity string
		var observed []byte
		if err = q.QueryRowContext(tx, `SELECT identity_key,external_observation FROM phase8_research_candidates WHERE user_id=$1 AND curation_id=$2 AND candidate_id=$3 AND source_kind=$4 FOR SHARE`, in.UserID, in.CurationID, in.CandidateID, in.ProductRef.Source).Scan(&identity, &observed); err != nil {
			return err
		}
		if identity != in.ProductRef.IdentityKey() {
			return fault.New(fault.Conflict, "PURCHASE_RECORD_SELECTION_CHANGED", false)
		}
		// The list snapshot is derived from the saved observation the server owns
		// (ADR-0075); no client value and no provider call is involved.
		var observation researchdomain.ExternalProductObservation
		if err = json.Unmarshal(observed, &observation); err != nil {
			return err
		}
		derived, err := researchdomain.PurchaseCheckSnapshotFromObservation(observation)
		if err != nil {
			return fault.New(fault.InternalFailure, "PURCHASE_RECORD_SNAPSHOT_INVALID", false)
		}
		snapshot := purchaseSnapshotColumns(&derived, now)
		var version int64
		var checked bool
		err = q.QueryRowContext(tx, `SELECT version,checked FROM research_external_purchase_records WHERE user_id=$1 AND curation_id=$2 AND source=$3 AND marketplace='KR' AND product_id=$4`, in.UserID, in.CurationID, in.ProductRef.Source, in.ProductRef.ProductID).Scan(&version, &checked)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if version != in.ExpectedVersion {
			return fault.New(fault.Conflict, "PURCHASE_RECORD_VERSION_CONFLICT", false)
		}
		if version == 0 || checked != in.Checked {
			if _, err = q.ExecContext(tx, `INSERT INTO research_external_purchase_records(user_id,curation_id,source,marketplace,product_id,candidate_id,checked,version,recorded_at,product_title,variant_title,merchant,price_minor,price_unknown,currency,snapshot_at) VALUES($1,$2,$3,'KR',$4,$5,$6,1,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(user_id,curation_id,source,marketplace,product_id) DO UPDATE SET checked=EXCLUDED.checked,version=research_external_purchase_records.version+1,recorded_at=EXCLUDED.recorded_at,candidate_id=EXCLUDED.candidate_id,`+purchaseSnapshotUpsertSet+``, in.UserID, in.CurationID, in.ProductRef.Source, in.ProductRef.ProductID, in.CandidateID, in.Checked, now, snapshot.title, snapshot.variant, snapshot.merchant, snapshot.priceMinor, snapshot.priceUnknown, snapshot.currency, snapshot.at); err != nil {
				return err
			}
			if _, err = q.ExecContext(tx, `INSERT INTO research_purchase_feedback(user_id,curation_id,version) VALUES($1,$2,1) ON CONFLICT(user_id,curation_id) DO UPDATE SET version=research_purchase_feedback.version+1`, in.UserID, in.CurationID); err != nil {
				return err
			}
		}
		result, err = r.ReadPurchaseFeedback(tx, in.UserID, in.CurationID)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(tx, `INSERT INTO research_purchase_commands(user_id,curation_id,idempotency_key,command_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6)`, in.UserID, in.CurationID, in.IdempotencyKey, hash, raw, now)
		return err
	})
	return result, err
}
