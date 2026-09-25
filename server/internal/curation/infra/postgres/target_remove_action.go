package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

var _ curationapp.TargetRemovalRepository = (*Repository)(nil)

func (r *Repository) GetCurationTarget(
	ctx context.Context,
	userID, curationID, targetID string,
	forUpdate bool,
) (curationdomain.PlanTarget, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	target, err := scanTarget(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
			id, curation_id, user_id, plan_id,
			title, normalized_intent, category,
			allocated_amount::text, allocated_currency, country, city,
			array_to_json(allowed_items)::text,
			array_to_json(blocked_items)::text,
			min_price_amount::text, max_price_amount::text, price_currency,
			reference_url, url_mode, order_index, confirmed_at, target_hash,
			target_hash_schema, version, created_at, updated_at,
			created_by_curation_run_id, removed_at, removed_by_user_id, product_vertical
		FROM plan_targets
		WHERE id=$1
		  AND curation_id=$2
		  AND user_id=$3
		  AND removed_at IS NULL`+lock,
		targetID,
		curationID,
		userID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return curationdomain.PlanTarget{},
			curationdomain.ErrTargetNotFound
	}
	return target, err
}

func (r *Repository) SaveCurationTargetRemoval(
	ctx context.Context,
	previousVersion int64,
	target curationdomain.PlanTarget,
) error {
	if previousVersion < 1 ||
		target.Version != previousVersion+1 ||
		target.RemovedAt == nil ||
		target.RemovedByUserID == nil ||
		*target.RemovedByUserID != target.UserID ||
		!target.UpdatedAt.Equal(*target.RemovedAt) {
		return curationdomain.ErrCurationActionInvalid
	}
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE plan_targets
		SET removed_at=$1,
		    removed_by_user_id=$2,
		    version=$3,
		    updated_at=$4
		WHERE id=$5
		  AND curation_id=$6
		  AND user_id=$7
		  AND plan_id=$8
		  AND version=$9
		  AND removed_at IS NULL
	`, target.RemovedAt, target.RemovedByUserID, target.Version,
		target.UpdatedAt, target.ID, target.CurationID, target.UserID,
		target.PlanID, previousVersion,
	)
	if err != nil {
		return fmt.Errorf("save curation target removal: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return curationdomain.ErrVersionConflict
	}
	return r.removeTargetBudget(ctx, target)
}
