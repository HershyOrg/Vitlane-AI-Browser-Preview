package postgres

import (
	"context"
	"fmt"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
)

func (r *Repository) ListActiveSessions(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]accountapp.ActiveSessionView, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT s.id, s.user_id, COALESCE(i.email_snapshot, ''),
		       s.created_at, s.expires_at
		FROM auth_sessions s
		LEFT JOIN external_identities i ON i.user_id = s.user_id
		WHERE s.revoked_at IS NULL AND s.expires_at > $1
		ORDER BY s.created_at DESC, s.id
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list active auth sessions: %w", err)
	}
	defer rows.Close()
	var sessions []accountapp.ActiveSessionView
	for rows.Next() {
		var view accountapp.ActiveSessionView
		if err := rows.Scan(
			&view.SessionID, &view.UserID, &view.Email,
			&view.CreatedAt, &view.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan active auth session: %w", err)
		}
		sessions = append(sessions, view)
	}
	return sessions, rows.Err()
}

func (r *Repository) RevokeUserSessions(
	ctx context.Context,
	userID string,
	now time.Time,
) (int64, error) {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE auth_sessions
		SET revoked_at=$2
		WHERE user_id=$1 AND revoked_at IS NULL AND expires_at > $2
	`, userID, now)
	if err != nil {
		return 0, fmt.Errorf("revoke user auth sessions: %w", err)
	}
	revoked, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoke user auth sessions rows: %w", err)
	}
	return revoked, nil
}

func (r *Repository) InsertOperatorActionAudit(
	ctx context.Context,
	audit accountapp.OperatorActionAudit,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO operator_action_audits(
			id, operator_user_id, action, subject_user_id, reason_detail,
			created_at
		) VALUES ($1,$2,$3,NULLIF($4,'')::uuid,$5,$6)
	`, audit.ID, audit.OperatorUserID, audit.Action, audit.SubjectUserID,
		audit.ReasonDetail, audit.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert operator action audit: %w", err)
	}
	return nil
}
