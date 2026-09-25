package postgres

import (
	"context"
	"database/sql"
	"errors"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func (r *Repository) ReadResearchSettings(ctx context.Context, user, id string, byPlan bool) (curationdomain.ResearchSettings, error) {
	result := curationdomain.ResearchSettings{SchemaVersion: "vitlane.research-settings.v1"}
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT c.research_settings_version,COALESCE(c.research_country,p.country) FROM curations c JOIN shopping_plans p ON p.id=c.shopping_plan_id AND p.user_id=c.user_id WHERE c.user_id=$1 AND (($3 AND c.shopping_plan_id=$2) OR (NOT $3 AND c.id=$2)) AND c.archived_at IS NULL`, user, id, byPlan).Scan(&result.Version, &result.Country)
	if errors.Is(err, sql.ErrNoRows) {
		return result, curationdomain.ErrPlanNotFound
	}
	return result, err
}

func (r *Repository) SaveResearchSettings(ctx context.Context, user, id, country string, expected int64) (curationdomain.ResearchSettings, error) {
	result := curationdomain.ResearchSettings{SchemaVersion: "vitlane.research-settings.v1"}
	var version int64
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT research_settings_version FROM curations WHERE user_id=$1 AND id=$2 AND archived_at IS NULL FOR UPDATE`, user, id).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return result, curationdomain.ErrPlanNotFound
	}
	if err != nil {
		return result, err
	}
	if err := r.GuardThreadMutation(ctx, user, id); err != nil {
		return result, err
	}
	if version != expected {
		return result, fault.New(fault.Conflict, "RESEARCH_SETTINGS_VERSION_CONFLICT", false)
	}
	err = r.database.Queryer(ctx).QueryRowContext(ctx, `UPDATE curations SET research_country=$3,research_settings_version=research_settings_version+1 WHERE user_id=$1 AND id=$2 RETURNING research_settings_version,research_country`, user, id, country).Scan(&result.Version, &result.Country)
	return result, err
}
