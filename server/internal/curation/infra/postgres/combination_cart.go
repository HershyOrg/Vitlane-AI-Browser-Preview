package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	a "github.com/vitlane/vitlane/server/internal/curation/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"time"
)

func (r *Repository) ReadCombinationCartCommand(ctx context.Context, user, id string) (string, a.CatalogCartStateV2, bool, error) {
	var hash string
	var raw []byte
	var state a.CatalogCartStateV2
	err := r.database.Queryer(ctx).QueryRowContext(ctx, "SELECT request_hash,result FROM curation_combination_cart_commands WHERE user_id=$1 AND id=$2", user, id).Scan(&hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", state, false, nil
	}
	if err != nil {
		return "", state, false, err
	}
	err = json.Unmarshal(raw, &state)
	return hash, state, true, err
}
func (r *Repository) SaveCombinationCartCommand(ctx context.Context, user, curation, id, hash string, state a.CatalogCartStateV2, now time.Time) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, "INSERT INTO curation_combination_cart_commands(user_id,curation_id,id,request_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6)", user, curation, id, hash, raw, now)
	return err
}

// ValidateCombinationCartSelections is a DB-only check. A recommendation or a
// client-supplied variant ID cannot replace a saved Shopify option selection.
// Prices remain fallible display observations; Prepare performs fresh pricing.
func (r *Repository) ValidateCombinationCartSelections(ctx context.Context, user, curation string, items []a.CatalogCartItemV2) error {
	for _, row := range items {
		var source string
		var variant sql.NullString
		err := r.database.Queryer(ctx).QueryRowContext(ctx, `
   SELECT candidate.source_kind, configuration.variant_id
   FROM phase8_research_candidates candidate
   LEFT JOIN phase8_candidate_configurations configuration
    USING(user_id,curation_id,plan_target_id,candidate_id)
   WHERE candidate.user_id=$1 AND candidate.curation_id=$2
    AND candidate.plan_target_id=$3 AND candidate.candidate_id=$4 AND candidate.visible=true
  `, user, curation, row.TargetID, row.Item.CandidateID).Scan(&source, &variant)
		if errors.Is(err, sql.ErrNoRows) {
			return fault.New(fault.InvalidInput, "COMBINATION_ITEM_NOT_OFFERED", false)
		}
		if err != nil {
			return err
		}
		if source != "SHOPIFY_LIVE" {
			return fault.New(fault.InvalidInput, "EXTERNAL_PRODUCT_CART_FORBIDDEN", false)
		}
		if !variant.Valid || variant.String == "" {
			return fault.New(fault.Conflict, "COMBINATION_VARIANT_NOT_CONFIRMED", false)
		}
		if variant.String != row.Item.VariantID {
			return fault.New(fault.Conflict, "COMBINATION_VARIANT_CHANGED", false)
		}
	}
	return nil
}
