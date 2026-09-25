package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database *sharedpostgres.Database
}

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

func (r *Repository) Create(ctx context.Context, session shoppingsessiondomain.ShoppingSession) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO shopping_sessions(
			id, plan_target_id, user_id, target_snapshot, research_scope_snapshot,
			status, current_research_round_id, version, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, session.ID, session.PlanTargetID, session.UserID, session.TargetSnapshot,
		session.ResearchScopeSnapshot, session.Status, session.CurrentResearchRoundID,
		session.Version, session.CreatedAt, session.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert shopping session: %w", err)
	}
	return nil
}

func (r *Repository) FindByTarget(
	ctx context.Context, userID, targetID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	return scanSession(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, plan_target_id, user_id, target_snapshot, research_scope_snapshot,
		       status, current_research_round_id, version, created_at, updated_at
		FROM shopping_sessions
		WHERE plan_target_id=$1 AND user_id=$2
	`, targetID, userID))
}

func (r *Repository) Get(
	ctx context.Context, userID, sessionID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	return scanSession(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, plan_target_id, user_id, target_snapshot, research_scope_snapshot,
		       status, current_research_round_id, version, created_at, updated_at
		FROM shopping_sessions
		WHERE id=$1 AND user_id=$2
	`, sessionID, userID))
}

func (r *Repository) GetForUpdate(
	ctx context.Context, userID, sessionID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	return scanSession(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
			s.id, s.plan_target_id, s.user_id,
			s.target_snapshot, s.research_scope_snapshot,
			s.status, s.current_research_round_id,
			s.version,
			s.created_at, s.updated_at
		FROM shopping_sessions s
		JOIN plan_targets t ON t.id=s.plan_target_id
		WHERE s.id=$1 AND s.user_id=$2
		  AND t.plan_id IS NOT NULL
		  AND t.removed_at IS NULL
		FOR UPDATE OF t, s
	`, sessionID, userID))
}

func (r *Repository) Save(
	ctx context.Context,
	previousVersion int64,
	session shoppingsessiondomain.ShoppingSession,
) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE shopping_sessions s
		SET status=$1, current_research_round_id=$2,
		    version=$3, updated_at=$4
		FROM plan_targets t
		WHERE s.id=$5
		  AND s.user_id=$6
		  AND s.version=$7
		  AND t.id=s.plan_target_id
		  AND t.plan_id IS NOT NULL
		  AND t.removed_at IS NULL
	`, session.Status, session.CurrentResearchRoundID,
		session.Version, session.UpdatedAt, session.ID, session.UserID, previousVersion)
	if err != nil {
		return fmt.Errorf("save shopping session: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return shoppingsessiondomain.ErrSessionNotFound
	}
	return nil
}

func scanSession(row sharedpostgres.Row) (shoppingsessiondomain.ShoppingSession, error) {
	var session shoppingsessiondomain.ShoppingSession
	err := row.Scan(
		&session.ID, &session.PlanTargetID, &session.UserID,
		&session.TargetSnapshot, &session.ResearchScopeSnapshot,
		&session.Status, &session.CurrentResearchRoundID,
		&session.Version, &session.CreatedAt, &session.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return shoppingsessiondomain.ShoppingSession{}, shoppingsessiondomain.ErrSessionNotFound
	}
	if err != nil {
		return shoppingsessiondomain.ShoppingSession{}, fmt.Errorf("scan shopping session: %w", err)
	}
	return session, nil
}
