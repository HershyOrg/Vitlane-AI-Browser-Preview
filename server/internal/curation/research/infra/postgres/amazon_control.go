package postgres

import (
	"context"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func (r *Repository) ReadAmazonControl(ctx context.Context) (researchapp.AmazonSourceControl, error) {
	var result researchapp.AmazonSourceControl
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT enabled,version,updated_at FROM research_catalog_source_control WHERE source='AMAZON'`).Scan(&result.Enabled, &result.Version, &result.UpdatedAt)
	return result, err
}
func (r *Repository) UpdateAmazonControl(ctx context.Context, operator string, enabled bool, version int64, now time.Time) (researchapp.AmazonSourceControl, error) {
	var result researchapp.AmazonSourceControl
	err := r.database.WithinTransaction(ctx, func(tx context.Context) error {
		q := r.database.Queryer(tx)
		if err := q.QueryRowContext(tx, `SELECT enabled,version,updated_at FROM research_catalog_source_control WHERE source='AMAZON' FOR UPDATE`).Scan(&result.Enabled, &result.Version, &result.UpdatedAt); err != nil {
			return err
		}
		// A lost response may be replayed, but an intervening change must never be overwritten.
		if version != result.Version {
			if version+1 == result.Version && enabled == result.Enabled {
				return nil
			}
			return fault.New(fault.Conflict, "AMAZON_CONTROL_VERSION_CONFLICT", false)
		}
		if enabled == result.Enabled {
			return nil
		}
		result.Enabled = enabled
		result.Version++
		result.UpdatedAt = now
		if _, err := q.ExecContext(tx, `UPDATE research_catalog_source_control SET enabled=$1,version=$2,updated_at=$3 WHERE source='AMAZON'`, enabled, result.Version, now); err != nil {
			return err
		}
		_, err := q.ExecContext(tx, `INSERT INTO research_catalog_source_control_audit(source,version,enabled,operator_user_id,changed_at) VALUES('AMAZON',$1,$2,$3,$4)`, result.Version, enabled, operator, now)
		return err
	})
	return result, err
}
