package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	a "github.com/vitlane/vitlane/server/internal/curation/research/app"
)

func (r *Repository) ReadCriteriaCheckpoint(ctx context.Context, user, round string) (*a.CatalogIntelligenceCatalogQuery, error) {
	var raw []byte
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT execution FROM research_criteria_checkpoints WHERE user_id=$1 AND round_id=$2`, user, round).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v a.CatalogIntelligenceCatalogQuery
	err = json.Unmarshal(raw, &v)
	return &v, err
}
func (r *Repository) SaveCriteriaCheckpoint(ctx context.Context, user, round string, v a.CatalogIntelligenceCatalogQuery) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO research_criteria_checkpoints(user_id,round_id,execution) VALUES($1,$2,$3)`, user, round, raw)
	return err
}

func (r *Repository) ReadSourceProgress(ctx context.Context, user, curation, target, fingerprint string) (a.SourceProgressSet, error) {
	var raw []byte
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT progress FROM research_source_progress WHERE user_id=$1 AND curation_id=$2 AND target_id=$3 AND fingerprint=$4`, user, curation, target, fingerprint).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return a.SourceProgressSet{}, nil
	}
	if err != nil {
		return nil, err
	}
	var v a.SourceProgressSet
	err = json.Unmarshal(raw, &v)
	return v, err
}
func (r *Repository) SaveSourceProgress(ctx context.Context, user, curation, target, fingerprint string, v a.SourceProgressSet) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = r.database.Queryer(ctx).ExecContext(ctx, `INSERT INTO research_source_progress(user_id,curation_id,target_id,fingerprint,progress) VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id,curation_id,target_id,fingerprint) DO UPDATE SET progress=research_source_progress.progress||EXCLUDED.progress`, user, curation, target, fingerprint, raw)
	return err
}
func (r *Repository) LatestCriteriaQuery(ctx context.Context, user, target string) (*a.CatalogIntelligenceCatalogQuery, error) {
	var raw []byte
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `SELECT x.execution FROM research_criteria_checkpoints x JOIN research_rounds r ON r.id=x.round_id JOIN shopping_sessions s ON s.id=r.shopping_session_id WHERE x.user_id=$1 AND s.plan_target_id=$2 ORDER BY r.created_at DESC LIMIT 1`, user, target).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v a.CatalogIntelligenceCatalogQuery
	err = json.Unmarshal(raw, &v)
	return &v, err
}
