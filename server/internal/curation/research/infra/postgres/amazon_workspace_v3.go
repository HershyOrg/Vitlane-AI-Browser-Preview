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

func (r *Repository) SaveAmazonRelation(ctx context.Context, g researchapp.AmazonRelationGrant) error {
	raw, err := json.Marshal(g.Variants)
	if err != nil {
		return err
	}
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		// Bounded short-lived references; product descriptions/images/raw responses are never stored.
		if _, err := q.ExecContext(tx, `DELETE FROM research_amazon_relations WHERE expires_at<now()`); err != nil {
			return err
		}
		_, err := q.ExecContext(tx, `INSERT INTO research_amazon_relations(token_hash,user_id,curation_id,candidate_id,anchor_asin,variants,relation_status,truncated,observed_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, g.TokenHash, g.UserID, g.CurationID, g.CandidateID, g.AnchorASIN, raw, g.RelationStatus, g.Truncated, g.ObservedAt, g.ExpiresAt)
		return err
	})
}
func (r *Repository) ReadAmazonRelation(ctx context.Context, user, curation, candidate, hash string) (researchapp.AmazonRelationGrant, error) {
	g := researchapp.AmazonRelationGrant{TokenHash: hash, UserID: user, CurationID: curation, CandidateID: candidate}
	var raw []byte
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT anchor_asin,variants,relation_status,truncated,observed_at,expires_at FROM research_amazon_relations WHERE token_hash=$1 AND user_id=$2 AND curation_id=$3 AND candidate_id=$4`, hash, user, curation, candidate).Scan(&g.AnchorASIN, &raw, &g.RelationStatus, &g.Truncated, &g.ObservedAt, &g.ExpiresAt)
	if err == sql.ErrNoRows {
		return g, fault.New(fault.InvalidInput, "AMAZON_VARIANT_PAGE_EXPIRED", true)
	}
	if err != nil {
		return g, err
	}
	err = json.Unmarshal(raw, &g.Variants)
	return g, err
}
func (r *Repository) SaveAmazonConfiguration(ctx context.Context, user, curation string, v researchapp.CatalogCandidateConfigurationV2, expected int64) error {
	return r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		if _, err := q.ExecContext(tx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "amazon-config:"+user+":"+curation+":"+v.CandidateID); err != nil {
			return err
		}
		var version int64
		err := q.QueryRowContext(tx, `SELECT version FROM phase8_candidate_configurations WHERE user_id=$1 AND curation_id=$2 AND candidate_id=$3 FOR UPDATE`, user, curation, v.CandidateID).Scan(&version)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if version != expected {
			return fault.New(fault.Conflict, "AMAZON_CONFIGURATION_VERSION_CONFLICT", true)
		}
		raw, err := json.Marshal(v.SelectedOptions)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(tx, `INSERT INTO phase8_candidate_configurations(user_id,curation_id,plan_target_id,candidate_id,variant_id,selected_options,observed_at,updated_at,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,1) ON CONFLICT(user_id,curation_id,candidate_id) DO UPDATE SET variant_id=EXCLUDED.variant_id,selected_options=EXCLUDED.selected_options,observed_at=EXCLUDED.observed_at,updated_at=EXCLUDED.updated_at,version=phase8_candidate_configurations.version+1`, user, curation, v.TargetID, v.CandidateID, v.VariantID, raw, v.ObservedAt, v.UpdatedAt)
		return err
	})
}

// SaveAmazonObservation keeps the saved, dated display record for the selected
// ASIN. Only the Amazon candidate of that curation is touched (ADR-0077).
func (r *Repository) SaveAmazonObservation(ctx context.Context, user, curation, candidate string, observation researchdomain.AmazonObservation) error {
	if observation.Validate() != nil {
		return fault.New(fault.InvalidInput, "AMAZON_OBSERVATION_INVALID", false)
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `UPDATE phase8_research_candidates SET amazon_observation=$4 WHERE user_id=$1 AND curation_id=$2 AND candidate_id=$3 AND source_kind='AMAZON'`, user, curation, candidate, raw)
	return err
}

func (r *Repository) readAmazonObservation(ctx context.Context, user, curation, candidate string) (researchdomain.AmazonObservation, bool) {
	var raw []byte
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT amazon_observation FROM phase8_research_candidates WHERE user_id=$1 AND curation_id=$2 AND candidate_id=$3 AND source_kind='AMAZON'`, user, curation, candidate).Scan(&raw); err != nil || len(raw) == 0 {
		return researchdomain.AmazonObservation{}, false
	}
	var observation researchdomain.AmazonObservation
	if err := json.Unmarshal(raw, &observation); err != nil || observation.Validate() != nil {
		return researchdomain.AmazonObservation{}, false
	}
	return observation, true
}

func (r *Repository) lockPurchaseFeedback(ctx context.Context, user, curation string) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "purchase-feedback:"+user+":"+curation)
	return err
}

// ReadPurchaseFeedback answers card reads without taking the curation lock.
// One statement is one snapshot, so the feedback version and the records it
// describes cannot drift apart, which is the only reason the read used to lock.
// Writers and Round creation keep the exclusive lock; a card read no longer
// queues behind them or behind another card of the same curation.
func (r *Repository) ReadPurchaseFeedback(ctx context.Context, user, curation string) (researchapp.PurchaseFeedback, error) {
	return r.readPurchaseFeedback(ctx, user, curation)
}
func (r *Repository) readPurchaseFeedback(ctx context.Context, user, curation string) (researchapp.PurchaseFeedback, error) {
	result := researchapp.PurchaseFeedback{SchemaVersion: "vitlane.external-purchase-feedback.v3", Records: []researchapp.ExternalPurchaseRecord{}}
	// The left join keeps the version row even when the curation has no record yet.
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		WITH feedback AS (
			SELECT COALESCE((SELECT version FROM research_purchase_feedback WHERE user_id=$1 AND curation_id=$2),0) AS version
		)
		SELECT feedback.version,record.candidate_id,record.marketplace,record.asin,record.checked,record.version,record.recorded_at,record.source,record.product_id
		FROM feedback
		LEFT JOIN research_external_purchase_records record
		  ON record.user_id=$1 AND record.curation_id=$2
		ORDER BY record.source,record.marketplace,record.asin,record.product_id
	`, user, curation)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var candidateID, marketplace, asin, source, productID sql.NullString
		var checked sql.NullBool
		var recordVersion sql.NullInt64
		var recordedAt sql.NullTime
		if err = rows.Scan(&result.Version, &candidateID, &marketplace, &asin, &checked, &recordVersion, &recordedAt, &source, &productID); err != nil {
			return result, err
		}
		if !candidateID.Valid {
			continue
		}
		v := researchapp.ExternalPurchaseRecord{
			CandidateID: candidateID.String, Checked: checked.Bool, Version: recordVersion.Int64,
			RecordedAt: recordedAt.Time, Evidence: "SELF_REPORTED",
			VariantRef: researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: marketplace.String, ASIN: asin.String},
		}
		if productID.Valid && productID.String != "" {
			v.ProductRef = &researchdomain.SourceProductRef{Source: researchdomain.Source(source.String), Marketplace: marketplace.String, ProductID: productID.String}
			v.VariantRef = researchdomain.SourceVariantRef{}
			result.SchemaVersion = "vitlane.external-purchase-feedback.v4"
		}
		result.Records = append(result.Records, v)
	}
	return result, rows.Err()
}
func (r *Repository) PurchaseFeedbackForPlan(ctx context.Context, user, plan string) (researchapp.PurchaseFeedback, error) {
	var id string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT id FROM curations WHERE user_id=$1 AND shopping_plan_id=$2`, user, plan).Scan(&id)
	if err != nil {
		return researchapp.PurchaseFeedback{}, err
	}
	if err = r.lockPurchaseFeedback(ctx, user, id); err != nil {
		return researchapp.PurchaseFeedback{}, err
	}
	return r.ReadPurchaseFeedback(ctx, user, id)
}
func (r *Repository) MarkExternalPurchase(ctx context.Context, in researchapp.MarkExternalPurchaseInput, now time.Time) (researchapp.PurchaseFeedback, error) {
	if in.ProductRef != nil {
		return r.markExternalProductPurchase(ctx, in, now)
	}
	var result researchapp.PurchaseFeedback
	raw, err := json.Marshal(in)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	err = r.database.WithinTransaction(ctx, func(tx context.Context) error {
		// Acquire the parent before feedback/configuration locks, matching Round creation.
		var owned bool
		if err := r.database.Queryer(tx).QueryRowContext(tx, `SELECT true FROM curations WHERE user_id=$1 AND id=$2 FOR KEY SHARE`, in.UserID, in.CurationID).Scan(&owned); err != nil {
			return err
		}
		if err := r.lockPurchaseFeedback(tx, in.UserID, in.CurationID); err != nil {
			return err
		}
		q := r.database.Queryer(tx)
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
		// Lock the exact selection against a concurrent option change. No provider call is needed.
		if _, err = q.ExecContext(tx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "amazon-config:"+in.UserID+":"+in.CurationID+":"+in.CandidateID); err != nil {
			return err
		}
		var selected string
		err = q.QueryRowContext(tx, `SELECT COALESCE(cfg.variant_id,right(c.provider_product_id,10)) FROM phase8_research_candidates c LEFT JOIN phase8_candidate_configurations cfg USING(user_id,curation_id,candidate_id) WHERE c.user_id=$1 AND c.curation_id=$2 AND c.candidate_id=$3 AND c.source_kind='AMAZON'`, in.UserID, in.CurationID, in.CandidateID).Scan(&selected)
		if err != nil {
			return err
		}
		var recordVersion int64
		var checked bool
		err = q.QueryRowContext(tx, `SELECT version,checked FROM research_external_purchase_records WHERE user_id=$1 AND curation_id=$2 AND source='AMAZON' AND marketplace=$3 AND asin=$4`, in.UserID, in.CurationID, in.VariantRef.Marketplace, in.VariantRef.ASIN).Scan(&recordVersion, &checked)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if recordVersion != in.ExpectedVersion {
			return fault.New(fault.Conflict, "PURCHASE_RECORD_VERSION_CONFLICT", true)
		}
		if (in.Checked || recordVersion == 0) && selected != in.VariantRef.ASIN {
			return fault.New(fault.Conflict, "PURCHASE_RECORD_SELECTION_CHANGED", true)
		}
		if recordVersion == 0 || checked != in.Checked {
			// A client snapshot replaces the whole display set; without one the
			// previous snapshot (if any) stays, so an undo/redo never blanks it.
			// A check with no client value falls back to the saved observation,
			// the same way Korean products derive theirs (ADR-0077).
			display := in.Snapshot
			if display == nil && in.Checked {
				if observation, ok := r.readAmazonObservation(tx, in.UserID, in.CurationID, in.CandidateID); ok && observation.VariantRef.ASIN == in.VariantRef.ASIN {
					if derived, err := researchdomain.PurchaseCheckSnapshotFromAmazonObservation(observation); err == nil {
						display = &derived
					}
				}
			}
			snapshot := purchaseSnapshotColumns(display, now)
			_, err = q.ExecContext(tx, `INSERT INTO research_external_purchase_records(user_id,curation_id,source,marketplace,asin,candidate_id,checked,version,recorded_at,product_title,variant_title,merchant,price_minor,price_unknown,currency,snapshot_at) VALUES($1,$2,'AMAZON',$3,$4,$5,$6,1,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(user_id,curation_id,source,marketplace,asin) DO UPDATE SET checked=EXCLUDED.checked,version=research_external_purchase_records.version+1,recorded_at=EXCLUDED.recorded_at,candidate_id=EXCLUDED.candidate_id,`+purchaseSnapshotUpsertSet+``, in.UserID, in.CurationID, in.VariantRef.Marketplace, in.VariantRef.ASIN, in.CandidateID, in.Checked, now, snapshot.title, snapshot.variant, snapshot.merchant, snapshot.priceMinor, snapshot.priceUnknown, snapshot.currency, snapshot.at)
			if err != nil {
				return err
			}
			_, err = q.ExecContext(tx, `INSERT INTO research_purchase_feedback(user_id,curation_id,version) VALUES($1,$2,1) ON CONFLICT(user_id,curation_id) DO UPDATE SET version=research_purchase_feedback.version+1`, in.UserID, in.CurationID)
			if err != nil {
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
