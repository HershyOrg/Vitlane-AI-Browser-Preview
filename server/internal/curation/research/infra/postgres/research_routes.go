package postgres

import (
	"context"
	"encoding/json"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"time"
)

func (r *Repository) RecordResearchRoute(ctx context.Context, user, attempt, vertical string, route researchapp.ResearchRoute, decision, reason string, now time.Time) error {
	apis, err := json.Marshal(route.APIIDs)
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO research_route_decisions(user_id,attempt_key,route_id,policy_version,product_vertical,api_ids,pressure,weight,decision,reason,created_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
 ON CONFLICT(user_id,attempt_key,route_id) DO UPDATE SET decision=EXCLUDED.decision,reason=EXCLUDED.reason`,
		user, attempt, route.ID, researchapp.ResearchRoutePolicyVersion, vertical, apis, route.Resources.Pressure, route.Weight, decision, reason, now)
	return err
}
