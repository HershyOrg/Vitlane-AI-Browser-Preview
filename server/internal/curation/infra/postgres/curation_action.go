package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

var _ curationapp.CurationActionRepository = (*Repository)(nil)

func (r *Repository) GetCurationAction(
	ctx context.Context,
	userID, actionID string,
	forUpdate bool,
) (curationdomain.CurationAction, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanCurationAction(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
			id, curation_id, actor_user_id, action_type,
			phase_at_request, requested_transition_to,
			subject_type, subject_id, effect_kind,
			source_ref_type, source_ref_id,
			expected_curation_version, request_hash, created_at,execution
		FROM curation_actions
		WHERE actor_user_id=$1 AND id=$2`+lock,
		userID,
		actionID,
	))
}

func (r *Repository) InsertCurationAction(
	ctx context.Context,
	action curationdomain.CurationAction,
) (bool, error) {
	if err := action.Validate(); err != nil {
		return false, err
	}
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO curation_actions(
			id, curation_id, actor_user_id, action_type,
			phase_at_request, requested_transition_to,
			subject_type, subject_id, effect_kind,
			source_ref_type, source_ref_id,
			expected_curation_version, request_hash, created_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14
		)
		ON CONFLICT (id) DO NOTHING
	`, action.ID, action.CurationID, action.ActorUserID, action.Type,
		action.PhaseAtRequest, action.RequestedTransitionTo,
		action.SubjectType, action.SubjectID, action.EffectKind,
		action.SourceRefType, action.SourceRefID,
		action.ExpectedCurationVersion, action.RequestHash[:],
		action.CreatedAt,
	)
	if err != nil {
		return false, fmt.Errorf("insert curation action: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read curation action insert count: %w", err)
	}
	return inserted == 1, nil
}

func (r *Repository) ListCurationActions(
	ctx context.Context,
	userID, curationID string,
	limit int,
) ([]curationdomain.CurationAction, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT
			id, curation_id, actor_user_id, action_type,
			phase_at_request, requested_transition_to,
			subject_type, subject_id, effect_kind,
			source_ref_type, source_ref_id,
			expected_curation_version, request_hash, created_at,execution
		FROM curation_actions
		WHERE actor_user_id=$1 AND curation_id=$2
		ORDER BY created_at, id
		LIMIT $3
	`, userID, curationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list curation actions: %w", err)
	}
	defer rows.Close()

	actions := make([]curationdomain.CurationAction, 0)
	for rows.Next() {
		action, scanErr := scanCurationAction(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate curation actions: %w", err)
	}
	return actions, nil
}

type curationActionRowScanner interface {
	Scan(...any) error
}

func scanCurationAction(
	row curationActionRowScanner,
) (curationdomain.CurationAction, error) {
	var action curationdomain.CurationAction
	var transition sql.NullString
	var subjectID sql.NullString
	var requestHash []byte
	var execution []byte
	err := row.Scan(
		&action.ID,
		&action.CurationID,
		&action.ActorUserID,
		&action.Type,
		&action.PhaseAtRequest,
		&transition,
		&action.SubjectType,
		&subjectID,
		&action.EffectKind,
		&action.SourceRefType,
		&action.SourceRefID,
		&action.ExpectedCurationVersion,
		&requestHash,
		&action.CreatedAt, &execution,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return curationdomain.CurationAction{},
			curationapp.ErrCurationActionNotFound
	}
	if err != nil {
		return curationdomain.CurationAction{},
			fmt.Errorf("scan curation action: %w", err)
	}
	if transition.Valid {
		value := curationdomain.CurationPhase(transition.String)
		action.RequestedTransitionTo = &value
	}
	if subjectID.Valid {
		value := subjectID.String
		action.SubjectID = &value
	}
	if len(requestHash) != sha256.Size {
		return curationdomain.CurationAction{},
			curationdomain.ErrCurationActionInvalid
	}
	copy(action.RequestHash[:], requestHash)
	if err := action.Validate(); err != nil {
		return curationdomain.CurationAction{}, err
	}

	if len(execution) > 2 {
		var a curationdomain.CurationAction
		if err := json.Unmarshal(execution, &a); err != nil {
			return action, err
		}
		a.ID = action.ID
		a.CurationID = action.CurationID
		a.ActorUserID = action.ActorUserID
		a.Type = action.Type
		a.PhaseAtRequest = action.PhaseAtRequest
		a.RequestedTransitionTo = action.RequestedTransitionTo
		a.SubjectType = action.SubjectType
		a.SubjectID = action.SubjectID
		a.EffectKind = action.EffectKind
		a.SourceRefType = action.SourceRefType
		a.SourceRefID = action.SourceRefID
		a.ExpectedCurationVersion = action.ExpectedCurationVersion
		a.RequestHash = action.RequestHash
		a.CreatedAt = action.CreatedAt
		action = a
	}
	if action.Status == "" {
		action.Status = "SUCCEEDED"
	}
	normalizeActionCollections(&action)
	return action, nil
}
