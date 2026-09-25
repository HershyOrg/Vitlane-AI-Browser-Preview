package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	a "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func (r *Repository) ReadCriteria(ctx context.Context, user, curation, target string) (*d.TargetCriteriaSetV1, error) {
	var raw []byte
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT x.criteria FROM plan_targets t JOIN curations c ON c.id=t.curation_id LEFT JOIN curation_target_criteria x ON x.user_id=t.user_id AND x.curation_id=t.curation_id AND x.target_id=t.id WHERE t.user_id=$1 AND t.curation_id=$2 AND t.id=$3 AND t.removed_at IS NULL AND c.archived_at IS NULL`, user, curation, target).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, d.ErrTargetNotFound
	}
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var value d.TargetCriteriaSetV1
	err = json.Unmarshal(raw, &value)
	return &value, err
}
func (r *Repository) SaveCriteria(ctx context.Context, user, curation, target string, c a.CriteriaCommand) (d.TargetCriteriaSetV1, error) {
	q := r.database.Queryer(ctx)
	var cv int64
	err := q.QueryRowContext(ctx, `SELECT c.version FROM curations c JOIN plan_targets t ON t.curation_id=c.id AND t.user_id=c.user_id WHERE c.user_id=$1 AND c.id=$2 AND t.id=$3 AND c.archived_at IS NULL AND t.removed_at IS NULL FOR UPDATE OF c`, user, curation, target).Scan(&cv)
	if errors.Is(err, sql.ErrNoRows) {
		return d.TargetCriteriaSetV1{}, d.ErrTargetNotFound
	}
	if err != nil {
		return d.TargetCriteriaSetV1{}, err
	}
	raw, _ := json.Marshal(c)
	hash := sha256.Sum256(raw)
	var oldHash, result []byte
	err = q.QueryRowContext(ctx, `SELECT request_hash,result FROM curation_criteria_commands WHERE user_id=$1 AND curation_id=$2 AND target_id=$3 AND request_key=$4`, user, curation, target, c.IdempotencyKey).Scan(&oldHash, &result)
	if err == nil {
		if !bytes.Equal(oldHash, hash[:]) {
			return d.TargetCriteriaSetV1{}, fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", false)
		}
		var v d.TargetCriteriaSetV1
		err = json.Unmarshal(result, &v)
		return v, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return d.TargetCriteriaSetV1{}, err
	}
	if err := r.GuardThreadMutation(ctx, user, curation); err != nil {
		return d.TargetCriteriaSetV1{}, err
	}
	if cv != c.ExpectedCurationVersion {
		return d.TargetCriteriaSetV1{}, fault.New(fault.Conflict, "CURATION_VERSION_CONFLICT", false)
	}
	old, err := r.ReadCriteria(ctx, user, curation, target)
	if err != nil {
		return d.TargetCriteriaSetV1{}, err
	}
	version := int64(0)
	if old != nil {
		version = old.Version
	}
	if version != c.ExpectedCriteriaVersion {
		return d.TargetCriteriaSetV1{}, fault.New(fault.Conflict, "RESEARCH_CRITERIA_CHANGED", false)
	}
	if old != nil {
		if err := old.ValidateSuccessor(c.Criteria); err != nil {
			return d.TargetCriteriaSetV1{}, err
		}
	}
	if err := r.preserveAxisMeanings(ctx, user, curation, target, c.Criteria.Axes); err != nil {
		return d.TargetCriteriaSetV1{}, err
	}
	result, _ = json.Marshal(c.Criteria)
	_, err = q.ExecContext(ctx, `INSERT INTO curation_target_criteria(user_id,curation_id,target_id,version,criteria) VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id,curation_id,target_id) DO UPDATE SET version=EXCLUDED.version,criteria=EXCLUDED.criteria`, user, curation, target, c.Criteria.Version, result)
	if err != nil {
		return d.TargetCriteriaSetV1{}, err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO curation_criteria_commands(user_id,curation_id,target_id,request_key,request_hash,result) VALUES($1,$2,$3,$4,$5,$6)`, user, curation, target, c.IdempotencyKey, hash[:], result)
	if err == nil {
		before, _ := json.Marshal(old)
		err = r.recordManualThread(ctx, user, curation, c.IdempotencyKey, "CRITERIA", target, []d.ActionEffect{{Kind: "CRITERIA_CHANGED", TargetID: target, Before: before, After: result}}, d.CurationAction{Criteria: &c.Criteria})
	}
	return c.Criteria, err
}
func (r *Repository) InsertInitialCriteria(ctx context.Context, targets []d.PlanTarget, inputs []a.TargetInput) error {
	for i, t := range targets {
		if i >= len(inputs) || inputs[i].Criteria == nil {
			continue
		}
		c := *inputs[i].Criteria
		c.Version = 1
		c.SchemaVersion = d.CriteriaSchema
		if err := c.Validate(); err != nil {
			return err
		}
		locale := inputs[i].ContentLocale
		if locale == "" {
			locale = "ko-KR"
		}
		if err := r.SavePlanContentLocale(ctx, string(t.UserID), string(t.ID), locale); err != nil {
			return err
		}
		raw, _ := json.Marshal(c)
		_, err := r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO curation_target_criteria(user_id,curation_id,target_id,version,criteria) VALUES($1,$2,$3,1,$4)`, t.UserID, t.CurationID, t.ID, raw)
		if err != nil {
			return err
		}
		if err := r.preserveAxisMeanings(ctx, string(t.UserID), string(t.CurationID), string(t.ID), c.Axes); err != nil {
			return err
		}
	}
	return nil
}

// Retired IDs keep their original meaning, including after removal and re-addition.
func (r *Repository) preserveAxisMeanings(ctx context.Context, user, curation, target string, axes []d.ResearchAxis) error {
	q := r.database.Queryer(ctx)
	for _, axis := range axes {
		result, err := q.ExecContext(ctx, `INSERT INTO curation_criteria_axis_meanings(user_id,curation_id,target_id,axis_id,definition,uses_price,uses_visual_evidence) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, user, curation, target, axis.AxisID, axis.Definition, axis.UsesPrice, axis.UsesVisualEvidence)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted == 0 {
			var definition string
			var price, visual bool
			if err := q.QueryRowContext(ctx, `SELECT definition,uses_price,uses_visual_evidence FROM curation_criteria_axis_meanings WHERE user_id=$1 AND curation_id=$2 AND target_id=$3 AND axis_id=$4`, user, curation, target, axis.AxisID).Scan(&definition, &price, &visual); err != nil {
				return err
			}
			if definition != axis.Definition || price != axis.UsesPrice || visual != axis.UsesVisualEvidence {
				return fault.New(fault.InvalidInput, "RESEARCH_AXIS_MEANING_CHANGED", false)
			}
		}
	}
	return nil
}
func (r *Repository) SavePlanContentLocale(ctx context.Context, user, plan, locale string) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO curation_generation_locales(user_id,request_id,content_locale) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, user, plan, locale)
	return err
}
func (r *Repository) ReadPlanContentLocale(ctx context.Context, user, plan string) (string, error) {
	var v string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT COALESCE((SELECT content_locale FROM curation_generation_locales WHERE user_id=$1 AND request_id=$2),(SELECT l.content_locale FROM planning_tasks t JOIN curation_generation_locales l ON l.request_id=t.plan_id AND l.user_id=t.user_id WHERE t.user_id=$1 AND t.id=$2),'ko-KR')`, user, plan).Scan(&v)
	return v, err
}

func (r *Repository) ReadCriteriaForUpdate(ctx context.Context, user, curation, target string) (*d.TargetCriteriaSetV1, error) {
	var id string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT c.id FROM curations c JOIN plan_targets t ON t.curation_id=c.id AND t.user_id=c.user_id WHERE c.user_id=$1 AND c.id=$2 AND t.id=$3 AND c.archived_at IS NULL AND t.removed_at IS NULL FOR UPDATE OF c`, user, curation, target).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, d.ErrTargetNotFound
	}
	if err != nil {
		return nil, err
	}
	return r.ReadCriteria(ctx, user, curation, target)
}
