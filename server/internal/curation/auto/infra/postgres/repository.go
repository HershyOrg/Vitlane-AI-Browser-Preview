package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	autoapp "github.com/vitlane/vitlane/server/internal/curation/auto/app"
	autodomain "github.com/vitlane/vitlane/server/internal/curation/auto/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database *sharedpostgres.Database
}

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

func (r *Repository) Get(
	ctx context.Context,
	userID, id string,
) (autoapp.Record, bool, error) {
	record, err := scanRecord(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, user_id, curation_id, plan_id, request_hash,
		       target_snapshot_hash, expected_curation_version, status,
		       COALESCE(decision,''), COALESCE(source,''),
		       COALESCE(target_id::text,''), COALESCE(session_id::text,''),
		       COALESCE(session_version,0), COALESCE(reason_code,''), created_at
		FROM curation_auto_resolutions
		WHERE user_id=$1 AND id=$2
	`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return autoapp.Record{}, false, nil
	}
	return record, err == nil, err
}

func (r *Repository) Begin(
	ctx context.Context,
	record autoapp.Record,
) (autoapp.Record, bool, error) {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO curation_auto_resolutions(
			id, user_id, curation_id, plan_id, request_hash,
			target_snapshot_hash, expected_curation_version, status, created_at, updated_at, request_text, expected_conversation_version
		) VALUES ($1,$2,$3,$4,$5,$6,$7,'PENDING',$8,$8,$9,$10)
		ON CONFLICT (id) DO NOTHING
	`, record.ID, record.UserID, record.CurationID, record.PlanID,
		record.RequestHash, record.TargetSnapshotHash,
		record.ExpectedCurationVersion, record.CreatedAt, record.RequestText, record.ExpectedConversationVersion)
	if err != nil {
		return autoapp.Record{}, false, fmt.Errorf("begin Auto resolution: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return autoapp.Record{}, false, fmt.Errorf("begin Auto resolution rows: %w", err)
	}
	if affected == 1 {
		return record, true, nil
	}
	existing, found, err := r.Get(ctx, record.UserID, record.ID)
	if err != nil {
		return autoapp.Record{}, false, fmt.Errorf("read concurrent Auto resolution: %w", err)
	}
	if !found {
		return autoapp.Record{}, false, autodomain.ErrIdempotencyConflict
	}
	return existing, false, nil
}

func (r *Repository) Resolve(
	ctx context.Context,
	userID, id string,
	resolution autoapp.ResolutionRecord,
) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE curation_auto_resolutions
		SET status='RESOLVED', decision=$3, source=$4,
		    target_id=NULLIF($5,'')::uuid, session_id=NULLIF($6,'')::uuid,
		    session_version=NULLIF($7,0), reason_code=$8, updated_at=NOW()
		WHERE user_id=$1 AND id=$2 AND status='PENDING'
	`, userID, id, resolution.Decision, resolution.Source, resolution.TargetID,
		resolution.SessionID, resolution.SessionVersion, resolution.ReasonCode)
	return requireOne(result, err, "resolve Auto request")
}

func (r *Repository) MarkNeedsSelection(
	ctx context.Context,
	userID, id, reason string,
	source autodomain.Source,
) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE curation_auto_resolutions
		SET status='NEEDS_SELECTION', decision='NEEDS_SELECTION', source=$3,
		    target_id=NULL, session_id=NULL, session_version=NULL,
		    reason_code=$4, updated_at=NOW()
		WHERE user_id=$1 AND id=$2 AND status IN ('PENDING','RESOLVED')
	`, userID, id, source, reason)
	return requireOne(result, err, "mark Auto selection")
}

func (r *Repository) MarkExecuted(ctx context.Context, userID, id string) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE curation_auto_resolutions
		SET status='EXECUTED', updated_at=NOW(), completed_at=NOW()
		WHERE user_id=$1 AND id=$2 AND status='RESOLVED'
	`, userID, id)
	return requireOne(result, err, "complete Auto request")
}

type rowScanner interface {
	Scan(...any) error
}

func scanRecord(row rowScanner) (autoapp.Record, error) {
	var record autoapp.Record
	if err := row.Scan(
		&record.ID, &record.UserID, &record.CurationID, &record.PlanID,
		&record.RequestHash, &record.TargetSnapshotHash,
		&record.ExpectedCurationVersion, &record.Status,
		&record.Decision, &record.Source, &record.TargetID, &record.SessionID,
		&record.SessionVersion, &record.ReasonCode, &record.CreatedAt,
	); err != nil {
		return autoapp.Record{}, err
	}
	return record, nil
}

func requireOne(result sql.Result, err error, operation string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows: %w", operation, err)
	}
	if affected != 1 {
		return autodomain.ErrInProgress
	}
	return nil
}
