package postgres

import (
	"context"
	"fmt"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
)

func (r *Repository) DueDeletions(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]string, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT d.user_id
		FROM user_deletions d
		JOIN users u ON u.id = d.user_id
		WHERE d.purged_at IS NULL
		  AND u.status = 'DELETION_REQUESTED'
		  AND d.requested_at <= $1
		ORDER BY d.requested_at, d.user_id
		LIMIT $2
	`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list due deletions: %w", err)
	}
	defer rows.Close()
	var userIDs []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("scan due deletion: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	return userIDs, rows.Err()
}

// PurgeUserPII erases the account's identifying data in one transaction.
// The users row itself survives as a DELETED tombstone: almost every product
// table cascades on users(id), so deleting the row would erase AgencyOrders and
// audit history the retention policy requires.
func (r *Repository) PurgeUserPII(
	ctx context.Context,
	userID string,
	now time.Time,
	snapshotRetention time.Duration,
) (bool, bool, error) {
	handled := false
	snapshotsRetained := false
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		var status string
		err := queryer.QueryRowContext(txContext, `
			SELECT status FROM users WHERE id=$1 FOR UPDATE
		`, userID).Scan(&status)
		if err != nil {
			return fmt.Errorf("lock user for purge: %w", err)
		}
		if status != "DELETION_REQUESTED" {
			return nil
		}
		handled = true

		// Issued-order snapshots stay under the commerce retention clock;
		// everything else about the person is erased now.
		if err := queryer.QueryRowContext(txContext, `
			SELECT EXISTS (
				SELECT 1 FROM shipping_snapshots
				WHERE user_id=$1 AND purged_at IS NULL
			)
		`, userID).Scan(&snapshotsRetained); err != nil {
			return fmt.Errorf("check retained snapshots: %w", err)
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE shipping_snapshots
			SET purge_after = COALESCE(purge_after, $2::timestamptz + $3::interval)
			WHERE user_id=$1 AND purged_at IS NULL
		`, userID, now, fmt.Sprintf(
			"%d seconds", int64(snapshotRetention.Seconds()),
		)); err != nil {
			return fmt.Errorf("schedule snapshot retention: %w", err)
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE shipping_profiles
			SET encrypted_payload=NULL, payload_nonce=NULL, purged_at=$2,
			    is_default=FALSE, label='(purged)', masked_summary='—',
			    updated_at=$2
			WHERE user_id=$1 AND purged_at IS NULL
		`, userID, now); err != nil {
			return fmt.Errorf("purge shipping profiles: %w", err)
		}
		// Removing the identity removes the person's identifiers; a later
		// sign-in with the same Google subject starts a fresh account.
		if _, err := queryer.ExecContext(txContext, `
			DELETE FROM external_identities WHERE user_id=$1
		`, userID); err != nil {
			return fmt.Errorf("delete external identities: %w", err)
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE auth_sessions
			SET revoked_at=COALESCE(revoked_at, $2)
			WHERE user_id=$1
		`, userID, now); err != nil {
			return fmt.Errorf("revoke sessions on purge: %w", err)
		}
		// The tombstone keeps no identifiers: users carries its own email and
		// display name copies since migration 000002.
		if _, err := queryer.ExecContext(txContext, `
			UPDATE users
			SET status='DELETED', email='', display_name='', updated_at=$2
			WHERE id=$1
		`, userID, now); err != nil {
			return fmt.Errorf("mark user deleted: %w", err)
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE user_deletions
			SET purged_at=$2, snapshots_retained=$3
			WHERE user_id=$1
		`, userID, now, snapshotsRetained); err != nil {
			return fmt.Errorf("close deletion ledger: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, false, err
	}
	return handled, snapshotsRetained, nil
}

func (r *Repository) PurgeExpiredSnapshots(
	ctx context.Context,
	now time.Time,
	limit int,
) (int, error) {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE shipping_snapshots
		SET encrypted_payload=NULL, payload_nonce=NULL, purged_at=$1
		WHERE id IN (
			SELECT id FROM shipping_snapshots
			WHERE purged_at IS NULL
			  AND purge_after IS NOT NULL AND purge_after <= $1
			ORDER BY purge_after
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
	`, now, limit)
	if err != nil {
		return 0, fmt.Errorf("purge expired snapshots: %w", err)
	}
	purged, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge expired snapshots rows: %w", err)
	}
	return int(purged), nil
}

func (r *Repository) StaleEncryptedRows(
	ctx context.Context,
	activeVersion string,
	limit int,
) ([]accountapp.EncryptedRowRef, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT 'PROFILE', id, user_id, encrypted_payload, payload_nonce,
		       key_version, payload_hmac
		FROM shipping_profiles
		WHERE purged_at IS NULL AND key_version <> $1
		UNION ALL
		SELECT 'SNAPSHOT', id, user_id, encrypted_payload, payload_nonce,
		       key_version, snapshot_hmac
		FROM shipping_snapshots
		WHERE purged_at IS NULL AND key_version <> $1
		LIMIT $2
	`, activeVersion, limit)
	if err != nil {
		return nil, fmt.Errorf("list stale encrypted rows: %w", err)
	}
	defer rows.Close()
	var refs []accountapp.EncryptedRowRef
	for rows.Next() {
		var ref accountapp.EncryptedRowRef
		if err := rows.Scan(
			&ref.Kind, &ref.ID, &ref.UserID, &ref.Encrypted.Ciphertext,
			&ref.Encrypted.Nonce, &ref.Encrypted.KeyVersion,
			&ref.Encrypted.Fingerprint,
		); err != nil {
			return nil, fmt.Errorf("scan stale encrypted row: %w", err)
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// UpdateEncryptedRow rewrites one row only while it still carries the old
// key version; a concurrent purge or rotation makes this a no-op.
func (r *Repository) UpdateEncryptedRow(
	ctx context.Context,
	ref accountapp.EncryptedRowRef,
	encrypted accountapp.EncryptedPII,
	now time.Time,
) (bool, error) {
	var query string
	switch ref.Kind {
	case "PROFILE":
		query = `
			UPDATE shipping_profiles
			SET encrypted_payload=$3, payload_nonce=$4, key_version=$5,
			    payload_hmac=$6, updated_at=$7
			WHERE id=$1 AND key_version=$2 AND purged_at IS NULL
		`
	case "SNAPSHOT":
		query = `
			UPDATE shipping_snapshots
			SET encrypted_payload=$3, payload_nonce=$4, key_version=$5,
			    snapshot_hmac=$6
			WHERE id=$1 AND key_version=$2 AND purged_at IS NULL
		`
	default:
		return false, fmt.Errorf("unknown encrypted row kind %q", ref.Kind)
	}
	arguments := []any{
		ref.ID, ref.Encrypted.KeyVersion, encrypted.Ciphertext,
		encrypted.Nonce, encrypted.KeyVersion, encrypted.Fingerprint,
	}
	if ref.Kind == "PROFILE" {
		arguments = append(arguments, now)
	}
	result, err := r.database.Queryer(ctx).ExecContext(ctx, query, arguments...)
	if err != nil {
		return false, fmt.Errorf("update encrypted row: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update encrypted row count: %w", err)
	}
	return updated == 1, nil
}
