package postgres

import "context"

func (r *Repository) SetTargetProductVertical(ctx context.Context, user, target, vertical string) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `UPDATE plan_targets SET product_vertical=$3 WHERE user_id=$1 AND id=$2 AND removed_at IS NULL`, user, target, vertical)
	return err
}
