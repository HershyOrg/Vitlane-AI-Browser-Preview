package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
)

func (r *Repository) CreateMobileAuthHandoff(
	ctx context.Context,
	handoff accountdomain.MobileAuthHandoff,
	sourceTokenHash []byte,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		var sourceID accountdomain.AuthSessionID
		var userID accountdomain.UserID
		var expiresAt time.Time
		var authenticatedAt time.Time
		err := queryer.QueryRowContext(txContext, `
			SELECT id, user_id, expires_at, authenticated_at
			FROM auth_sessions
			WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>$2
			FOR UPDATE
		`, sourceTokenHash, handoff.CreatedAt).Scan(
			&sourceID, &userID, &expiresAt, &authenticatedAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return accountdomain.ErrSessionMissing
		}
		if err != nil {
			return fmt.Errorf("lock mobile auth source session: %w", err)
		}
		if userID != handoff.UserID || !expiresAt.Equal(handoff.SessionExpiresAt) ||
			!authenticatedAt.Equal(handoff.AuthenticatedAt) {
			return accountdomain.ErrMobileAuthInvalid
		}
		if _, err := queryer.ExecContext(txContext, `
			INSERT INTO mobile_auth_handoffs(
				id, user_id, code_hash, verifier_challenge,
				session_expires_at, authenticated_at,
				expires_at, consumed_at, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,NULL,$8)
		`, handoff.ID, handoff.UserID, handoff.CodeHash,
			handoff.VerifierChallenge, handoff.SessionExpiresAt,
			handoff.AuthenticatedAt, handoff.ExpiresAt,
			handoff.CreatedAt); err != nil {
			return fmt.Errorf("insert mobile auth handoff: %w", err)
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE auth_sessions SET revoked_at=$1
			WHERE id=$2 AND revoked_at IS NULL
		`, handoff.CreatedAt, sourceID); err != nil {
			return fmt.Errorf("revoke mobile auth source session: %w", err)
		}
		return nil
	})
}

func (r *Repository) ExchangeMobileAuthHandoff(
	ctx context.Context,
	codeHash, verifierChallenge []byte,
	grant accountapp.MobileSessionGrant,
) (accountapp.ExternalLoginRecord, error) {
	var record accountapp.ExternalLoginRecord
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		var handoff accountdomain.MobileAuthHandoff
		err := queryer.QueryRowContext(txContext, `
			SELECT h.id, h.user_id, h.code_hash, h.verifier_challenge,
			       h.session_expires_at, h.authenticated_at,
			       h.expires_at, h.consumed_at, h.created_at,
			       u.id, u.status, u.email, u.display_name,
			       u.created_at, u.updated_at
			FROM mobile_auth_handoffs h
			JOIN users u ON u.id=h.user_id
			WHERE h.code_hash=$1
			FOR UPDATE OF h, u
		`, codeHash).Scan(
			&handoff.ID, &handoff.UserID, &handoff.CodeHash,
			&handoff.VerifierChallenge, &handoff.SessionExpiresAt,
			&handoff.AuthenticatedAt, &handoff.ExpiresAt,
			&handoff.ConsumedAt, &handoff.CreatedAt,
			&record.User.ID, &record.User.Status, &record.User.Email,
			&record.User.DisplayName, &record.User.CreatedAt,
			&record.User.UpdatedAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return accountdomain.ErrMobileAuthInvalid
		}
		if err != nil {
			return fmt.Errorf("read mobile auth handoff: %w", err)
		}
		if record.User.Status != accountdomain.UserStatusActive ||
			!bytes.Equal(handoff.CodeHash, codeHash) {
			return accountdomain.ErrMobileAuthInvalid
		}
		if err := handoff.CanConsume(grant.CreatedAt, verifierChallenge); err != nil {
			return err
		}
		if grant.SessionID == "" || len(grant.TokenHash) != 32 {
			return accountdomain.ErrSessionInvalid
		}
		record.Session = accountdomain.AuthSession{
			ID: grant.SessionID, UserID: handoff.UserID,
			TokenHash:       append([]byte(nil), grant.TokenHash...),
			ExpiresAt:       handoff.SessionExpiresAt,
			CreatedAt:       grant.CreatedAt,
			AuthenticatedAt: handoff.AuthenticatedAt,
		}
		if err := insertAuthSession(txContext, queryer, record.Session); err != nil {
			return err
		}
		result, err := queryer.ExecContext(txContext, `
			UPDATE mobile_auth_handoffs
			SET consumed_at=$1
			WHERE id=$2 AND consumed_at IS NULL
		`, grant.CreatedAt, handoff.ID)
		if err != nil {
			return fmt.Errorf("consume mobile auth handoff: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return accountdomain.ErrMobileAuthConsumed
		}
		return nil
	})
	return record, err
}
