package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func (r *Repository) SaveProductReaction(ctx context.Context, user, curation string, value researchdomain.ProductReaction, expected int64) (researchdomain.ProductReaction, error) {
	if value.Validate() != nil || expected < 0 {
		return researchdomain.ProductReaction{}, fault.New(fault.InvalidInput, "PRODUCT_REACTION_INVALID", false)
	}
	var snapshot any
	if value.LikedSnapshot != nil {
		raw, err := json.Marshal(value.LikedSnapshot)
		if err != nil {
			return value, err
		}
		snapshot = raw
	}
	// The owner/Target/original-product match and absence of a resolved Variant
	// are checked again at the write boundary. A fetch error is not a fallback.
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		INSERT INTO research_product_reactions(user_id,curation_id,plan_target_id,candidate_id,source,marketplace,product_id,pinned,sentiment,version,updated_at,liked_snapshot)
		SELECT c.user_id,c.curation_id,c.plan_target_id,c.candidate_id,$5,$6,$7,$8,$9,1,$10,$11
		FROM phase8_research_candidates c
		WHERE c.user_id=$1 AND c.curation_id=$2 AND c.plan_target_id=$3 AND c.candidate_id=$4
		AND c.source_kind=$5 AND c.variant_id IS NULL
		AND c.external_observation->'productRef'->>'marketplace'=$6
		AND c.external_observation->'productRef'->>'productId'=$7
		AND c.external_observation->>'priceScope'='PRODUCT'
		AND NOT EXISTS (SELECT 1 FROM phase8_candidate_configurations v WHERE v.user_id=c.user_id AND v.curation_id=c.curation_id AND v.candidate_id=c.candidate_id)
		AND NOT EXISTS (SELECT 1 FROM phase8_variant_interactions v WHERE v.user_id=c.user_id AND v.curation_id=c.curation_id AND v.candidate_id=c.candidate_id)
		AND ($12=0 OR EXISTS (SELECT 1 FROM research_product_reactions p WHERE p.user_id=c.user_id AND p.curation_id=c.curation_id AND p.candidate_id=c.candidate_id))
		ON CONFLICT (user_id,curation_id,candidate_id) DO UPDATE
		SET pinned=EXCLUDED.pinned,sentiment=EXCLUDED.sentiment,version=research_product_reactions.version+1,updated_at=EXCLUDED.updated_at,liked_snapshot=EXCLUDED.liked_snapshot
		WHERE research_product_reactions.version=$12
		AND research_product_reactions.source=EXCLUDED.source AND research_product_reactions.marketplace=EXCLUDED.marketplace AND research_product_reactions.product_id=EXCLUDED.product_id
		RETURNING version
	`, user, curation, value.TargetID, value.CandidateID, value.ProductRef.Source, value.ProductRef.Marketplace, value.ProductRef.ProductID, value.Pinned, value.Sentiment, value.UpdatedAt, snapshot, expected).Scan(&value.Version)
	if errors.Is(err, sql.ErrNoRows) {
		err = fault.New(fault.Conflict, "PRODUCT_REACTION_STALE", false)
	}
	return value, err
}

func (r *Repository) ListProductReactions(ctx context.Context, user, curation string) ([]researchdomain.ProductReaction, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT plan_target_id,candidate_id,source,marketplace,product_id,pinned,sentiment,version,updated_at FROM research_product_reactions WHERE user_id=$1 AND curation_id=$2 ORDER BY candidate_id`, user, curation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []researchdomain.ProductReaction{}
	for rows.Next() {
		var v researchdomain.ProductReaction
		if err := rows.Scan(&v.TargetID, &v.CandidateID, &v.ProductRef.Source, &v.ProductRef.Marketplace, &v.ProductRef.ProductID, &v.Pinned, &v.Sentiment, &v.Version, &v.UpdatedAt); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

func (r *Repository) ListLikedProducts(ctx context.Context, user string, limit int) ([]researchdomain.LikedProduct, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `SELECT curation_id,candidate_id,liked_snapshot,updated_at FROM research_product_reactions WHERE user_id=$1 AND sentiment='LIKE' ORDER BY updated_at DESC,candidate_id LIMIT $2`, user, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []researchdomain.LikedProduct{}
	for rows.Next() {
		var v researchdomain.LikedProduct
		var raw []byte
		if err := rows.Scan(&v.CurationID, &v.CandidateID, &raw, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &v.Observation); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}
