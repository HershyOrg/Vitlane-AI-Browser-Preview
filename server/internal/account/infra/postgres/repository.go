package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type Repository struct {
	database *sharedpostgres.Database
}

func NewRepository(database *sharedpostgres.Database) *Repository {
	return &Repository{database: database}
}

func (r *Repository) CreateUser(ctx context.Context, user accountdomain.User) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO users(id, status, email, display_name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, user.ID, user.Status, user.Email, user.DisplayName, user.CreatedAt, user.UpdatedAt)
	return err
}

func (r *Repository) ListUsersByID(
	ctx context.Context,
	ids []accountdomain.UserID,
) ([]accountdomain.User, error) {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, string(id))
	}
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, status, email, display_name, created_at, updated_at
		FROM users WHERE id = ANY($1::uuid[])
	`, values)
	if err != nil {
		return nil, fmt.Errorf("list users by id: %w", err)
	}
	defer rows.Close()
	users := make([]accountdomain.User, 0, len(values))
	for rows.Next() {
		var user accountdomain.User
		if err := rows.Scan(
			&user.ID, &user.Status, &user.Email, &user.DisplayName,
			&user.CreatedAt, &user.UpdatedAt,
		); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (r *Repository) EnsureTestBuyerProfile(
	ctx context.Context,
	profile accountdomain.BuyerProfile,
) (accountdomain.BuyerProfile, error) {
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		INSERT INTO buyer_profiles(
			id, user_id, profile_kind, label, fixture_key, country, city,
			profile_version, snapshot_hash, contains_real_pii, is_default,
			retired_at, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,FALSE,
		          NOT EXISTS (
		              SELECT 1 FROM buyer_profiles
		              WHERE user_id=$2 AND retired_at IS NULL
		          ),
		          NULL,$10,$11)
		ON CONFLICT (user_id, fixture_key, profile_version)
		DO UPDATE SET label=EXCLUDED.label
		RETURNING id, user_id, profile_kind, label, fixture_key, country, city,
		          profile_version, snapshot_hash, contains_real_pii, is_default,
		          retired_at, created_at, updated_at
	`, profile.ID, profile.UserID, profile.ProfileKind, profile.Label, profile.FixtureKey,
		profile.Country, profile.City, profile.Version, profile.SnapshotHash,
		profile.CreatedAt, profile.UpdatedAt).Scan(
		&profile.ID, &profile.UserID, &profile.ProfileKind, &profile.Label,
		&profile.FixtureKey, &profile.Country, &profile.City, &profile.Version,
		&profile.SnapshotHash, &profile.ContainsRealPII, &profile.IsDefault,
		&profile.RetiredAt, &profile.CreatedAt, &profile.UpdatedAt,
	)
	if err != nil {
		return accountdomain.BuyerProfile{}, fmt.Errorf("ensure TEST buyer profile: %w", err)
	}
	return profile, nil
}

func (r *Repository) ListBuyerProfiles(
	ctx context.Context,
	userID accountdomain.UserID,
) ([]accountdomain.BuyerProfile, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, user_id, profile_kind, label, fixture_key, country, city,
		       profile_version, snapshot_hash, contains_real_pii, is_default,
		       retired_at, created_at, updated_at
		FROM buyer_profiles
		WHERE user_id=$1 AND retired_at IS NULL
		ORDER BY is_default DESC, created_at
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list buyer profiles: %w", err)
	}
	defer rows.Close()
	profiles := make([]accountdomain.BuyerProfile, 0)
	for rows.Next() {
		var profile accountdomain.BuyerProfile
		if err := rows.Scan(
			&profile.ID, &profile.UserID, &profile.ProfileKind, &profile.Label,
			&profile.FixtureKey, &profile.Country, &profile.City, &profile.Version,
			&profile.SnapshotHash, &profile.ContainsRealPII, &profile.IsDefault,
			&profile.RetiredAt, &profile.CreatedAt, &profile.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan buyer profile: %w", err)
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func (r *Repository) FindDefaultBuyerProfile(
	ctx context.Context,
	userID accountdomain.UserID,
) (accountdomain.BuyerProfile, error) {
	var profile accountdomain.BuyerProfile
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, user_id, profile_kind, label, fixture_key, country, city,
		       profile_version, snapshot_hash, contains_real_pii, is_default,
		       retired_at, created_at, updated_at
		FROM buyer_profiles
		WHERE user_id=$1 AND is_default AND retired_at IS NULL
	`, userID).Scan(
		&profile.ID, &profile.UserID, &profile.ProfileKind, &profile.Label,
		&profile.FixtureKey, &profile.Country, &profile.City, &profile.Version,
		&profile.SnapshotHash, &profile.ContainsRealPII, &profile.IsDefault,
		&profile.RetiredAt, &profile.CreatedAt, &profile.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return accountdomain.BuyerProfile{}, accountdomain.ErrBuyerProfileInvalid
	}
	if err != nil {
		return accountdomain.BuyerProfile{}, fmt.Errorf("find default buyer profile: %w", err)
	}
	return profile, nil
}

func (r *Repository) CreateShippingProfile(
	ctx context.Context,
	profile accountdomain.ShippingProfile,
) (accountdomain.ShippingProfile, error) {
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		if _, err := r.database.Queryer(txContext).ExecContext(
			txContext,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"shipping-profile:"+profile.UserID,
		); err != nil {
			return fmt.Errorf("lock shipping profile: %w", err)
		}
		if _, err := r.database.Queryer(txContext).ExecContext(txContext, `
			UPDATE shipping_profiles
			SET is_default=FALSE, updated_at=$1
			WHERE user_id=$2 AND is_default AND retired_at IS NULL
		`, profile.UpdatedAt, profile.UserID); err != nil {
			return fmt.Errorf("clear default shipping profile: %w", err)
		}
		return r.database.Queryer(txContext).QueryRowContext(txContext, `
			INSERT INTO shipping_profiles(
				id, user_id, label, country, masked_summary, encrypted_payload,
				payload_nonce, key_version, payload_hmac, profile_version,
				is_default, retired_at, created_at, updated_at
			)
			SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,
			       COALESCE(MAX(profile_version),0)+1,
			       TRUE,NULL,$10,$11
			FROM shipping_profiles
			WHERE user_id=$2
			RETURNING id, user_id, label, country, masked_summary,
			          encrypted_payload, payload_nonce, key_version, payload_hmac,
			          profile_version, is_default, retired_at, created_at, updated_at
		`, profile.ID, profile.UserID, profile.Label, profile.Country,
			profile.MaskedSummary, profile.EncryptedPayload, profile.PayloadNonce,
			profile.KeyVersion, profile.PayloadHMAC, profile.CreatedAt, profile.UpdatedAt,
		).Scan(
			&profile.ID, &profile.UserID, &profile.Label, &profile.Country,
			&profile.MaskedSummary, &profile.EncryptedPayload, &profile.PayloadNonce,
			&profile.KeyVersion, &profile.PayloadHMAC, &profile.Version,
			&profile.IsDefault, &profile.RetiredAt, &profile.CreatedAt, &profile.UpdatedAt,
		)
	})
	if err != nil {
		return accountdomain.ShippingProfile{}, fmt.Errorf("create shipping profile: %w", err)
	}
	return profile, nil
}

func (r *Repository) ListShippingProfiles(
	ctx context.Context,
	userID string,
) ([]accountdomain.ShippingProfile, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, user_id, label, country, masked_summary, key_version,
		       profile_version, is_default, retired_at, created_at, updated_at
		FROM shipping_profiles
		WHERE user_id=$1 AND retired_at IS NULL
		ORDER BY is_default DESC, profile_version DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list shipping profiles: %w", err)
	}
	defer rows.Close()
	profiles := make([]accountdomain.ShippingProfile, 0)
	for rows.Next() {
		var profile accountdomain.ShippingProfile
		if err := rows.Scan(
			&profile.ID, &profile.UserID, &profile.Label, &profile.Country,
			&profile.MaskedSummary, &profile.KeyVersion, &profile.Version,
			&profile.IsDefault, &profile.RetiredAt, &profile.CreatedAt, &profile.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan shipping profile: %w", err)
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func (r *Repository) GetShippingProfile(
	ctx context.Context,
	userID, profileID string,
) (accountdomain.ShippingProfile, error) {
	var profile accountdomain.ShippingProfile
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, user_id, label, country, masked_summary,
		       encrypted_payload, payload_nonce, key_version, payload_hmac,
		       profile_version, is_default, retired_at, created_at, updated_at
		FROM shipping_profiles
		WHERE id=$1 AND user_id=$2 AND retired_at IS NULL
	`, profileID, userID).Scan(
		&profile.ID, &profile.UserID, &profile.Label, &profile.Country,
		&profile.MaskedSummary, &profile.EncryptedPayload, &profile.PayloadNonce,
		&profile.KeyVersion, &profile.PayloadHMAC, &profile.Version,
		&profile.IsDefault, &profile.RetiredAt, &profile.CreatedAt, &profile.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return accountdomain.ShippingProfile{}, accountdomain.ErrShippingProfileMissing
	}
	if err != nil {
		return accountdomain.ShippingProfile{}, fmt.Errorf("get shipping profile: %w", err)
	}
	return profile, nil
}

func (r *Repository) RetireShippingProfile(
	ctx context.Context,
	userID, profileID string,
	now time.Time,
) error {
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE shipping_profiles
		SET retired_at=$1, is_default=FALSE, updated_at=$1
		WHERE id=$2 AND user_id=$3 AND retired_at IS NULL
	`, now, profileID, userID)
	if err != nil {
		return fmt.Errorf("retire shipping profile: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return accountdomain.ErrShippingProfileMissing
	}
	return nil
}

func (r *Repository) CreateDefaultShippingSnapshot(
	ctx context.Context,
	snapshotID, userID, _ string,
	now time.Time,
) (accountdomain.ShippingSnapshot, error) {
	var snapshot accountdomain.ShippingSnapshot
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		INSERT INTO shipping_snapshots(
			id, user_id, source_profile_id, profile_version, country,
			masked_summary, encrypted_payload, payload_nonce, key_version,
			snapshot_hmac, purge_after, purged_at, created_at
		)
		SELECT $1, user_id, id, profile_version, country, masked_summary,
		       encrypted_payload, payload_nonce, key_version, payload_hmac,
		       NULL,NULL,$3
		FROM shipping_profiles
		WHERE user_id=$2 AND is_default AND retired_at IS NULL
		RETURNING id, user_id, source_profile_id, profile_version, country,
		          masked_summary, encrypted_payload, payload_nonce, key_version,
		          snapshot_hmac, purge_after, purged_at, created_at
	`, snapshotID, userID, now).Scan(
		&snapshot.ID, &snapshot.UserID, &snapshot.SourceProfileID,
		&snapshot.ProfileVersion, &snapshot.Country, &snapshot.MaskedSummary,
		&snapshot.EncryptedPayload, &snapshot.PayloadNonce, &snapshot.KeyVersion,
		&snapshot.SnapshotHMAC, &snapshot.PurgeAfter, &snapshot.PurgedAt,
		&snapshot.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return accountdomain.ShippingSnapshot{}, accountdomain.ErrShippingProfileMissing
	}
	if err != nil {
		return accountdomain.ShippingSnapshot{}, fmt.Errorf("create shipping snapshot: %w", err)
	}
	return snapshot, nil
}

func (r *Repository) CreateOrderSheetShippingSnapshot(
	ctx context.Context,
	snapshot accountdomain.ShippingSnapshot,
) (accountdomain.ShippingSnapshot, error) {
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		INSERT INTO shipping_snapshots(
			id, user_id, source_profile_id, profile_version, country,
			masked_summary, encrypted_payload, payload_nonce, key_version,
			snapshot_hmac, purge_after, purged_at, created_at, source_kind
		) VALUES($1,$2,NULL,$3,$4,$5,$6,$7,$8,$9,NULL,NULL,$10,'ORDER_SHEET_INPUT')
		RETURNING id, user_id, COALESCE(source_profile_id::text,''), profile_version,
		          country, masked_summary, encrypted_payload, payload_nonce,
		          key_version, snapshot_hmac, purge_after, purged_at, created_at
	`, snapshot.ID, snapshot.UserID, snapshot.ProfileVersion, snapshot.Country,
		snapshot.MaskedSummary, snapshot.EncryptedPayload, snapshot.PayloadNonce,
		snapshot.KeyVersion, snapshot.SnapshotHMAC, snapshot.CreatedAt,
	).Scan(
		&snapshot.ID, &snapshot.UserID, &snapshot.SourceProfileID,
		&snapshot.ProfileVersion, &snapshot.Country, &snapshot.MaskedSummary,
		&snapshot.EncryptedPayload, &snapshot.PayloadNonce, &snapshot.KeyVersion,
		&snapshot.SnapshotHMAC, &snapshot.PurgeAfter, &snapshot.PurgedAt,
		&snapshot.CreatedAt,
	)
	if err != nil {
		return accountdomain.ShippingSnapshot{}, fmt.Errorf("create OrderSheet shipping snapshot: %w", err)
	}
	return snapshot, nil
}

func (r *Repository) GetShippingSnapshot(
	ctx context.Context,
	snapshotID string,
) (accountdomain.ShippingSnapshot, error) {
	var snapshot accountdomain.ShippingSnapshot
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, user_id, COALESCE(source_profile_id::text,''), profile_version, country,
		       masked_summary, encrypted_payload, payload_nonce, key_version,
		       snapshot_hmac, purge_after, purged_at, created_at
		FROM shipping_snapshots WHERE id=$1
	`, snapshotID).Scan(
		&snapshot.ID, &snapshot.UserID, &snapshot.SourceProfileID,
		&snapshot.ProfileVersion, &snapshot.Country, &snapshot.MaskedSummary,
		&snapshot.EncryptedPayload, &snapshot.PayloadNonce, &snapshot.KeyVersion,
		&snapshot.SnapshotHMAC, &snapshot.PurgeAfter, &snapshot.PurgedAt,
		&snapshot.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return accountdomain.ShippingSnapshot{}, accountdomain.ErrShippingProfileMissing
	}
	if err != nil {
		return accountdomain.ShippingSnapshot{}, fmt.Errorf("get shipping snapshot: %w", err)
	}
	return snapshot, nil
}

func (r *Repository) UpsertPolicyAcceptance(
	ctx context.Context,
	userID accountdomain.UserID,
	acceptance accountdomain.PolicyAcceptance,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO user_policy_acceptances(user_id, policy_id, policy_version, accepted_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (user_id, policy_id, policy_version)
		DO UPDATE SET accepted_at=EXCLUDED.accepted_at
	`, userID, acceptance.PolicyID, acceptance.PolicyVersion, acceptance.AcceptedAt)
	return err
}

func (r *Repository) ListPolicyAcceptances(
	ctx context.Context,
	userID accountdomain.UserID,
) ([]accountdomain.PolicyAcceptance, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT policy_id, policy_version, accepted_at
		FROM user_policy_acceptances
		WHERE user_id=$1
		ORDER BY accepted_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]accountdomain.PolicyAcceptance, 0)
	for rows.Next() {
		var item accountdomain.PolicyAcceptance
		if err := rows.Scan(&item.PolicyID, &item.PolicyVersion, &item.AcceptedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type rowScanner interface {
	Scan(...any) error
}

func (r *Repository) CreateLoginAttempt(
	ctx context.Context,
	attempt accountdomain.OAuthLoginAttempt,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO oauth_login_attempts(
			id, provider, state_hash, browser_binding_hash, nonce_hash,
			pkce_verifier, return_path, expires_at, consumed_at,
			secret_cleaned_at, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULL,$10)
	`, attempt.ID, attempt.Provider, attempt.StateHash, attempt.BrowserBindingHash,
		attempt.NonceHash, attempt.PKCEVerifier, attempt.ReturnPath,
		attempt.ExpiresAt, attempt.ConsumedAt, attempt.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert OAuth login attempt: %w", err)
	}
	return nil
}

func (r *Repository) ConsumeLoginAttempt(
	ctx context.Context,
	provider accountdomain.IdentityProvider,
	stateHash, browserBindingHash []byte,
	now time.Time,
) (accountdomain.OAuthLoginAttempt, error) {
	var attempt accountdomain.OAuthLoginAttempt
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		known, err := scanLoginAttempt(queryer.QueryRowContext(txContext, `
			SELECT id, provider, state_hash, browser_binding_hash, nonce_hash,
			       pkce_verifier, return_path, expires_at, consumed_at,
			       secret_cleaned_at, created_at
			FROM oauth_login_attempts
			WHERE provider=$1 AND state_hash=$2
			FOR UPDATE
		`, provider, stateHash))
		if err != nil {
			return accountdomain.ErrLoginAttemptInvalid
		}
		if !bytes.Equal(known.BrowserBindingHash, browserBindingHash) {
			return accountdomain.ErrLoginAttemptInvalid
		}
		if err := known.CanConsume(now); err != nil {
			return err
		}
		if strings.TrimSpace(known.PKCEVerifier) == "" {
			return accountdomain.ErrLoginAttemptInvalid
		}
		result, err := queryer.ExecContext(txContext, `
			UPDATE oauth_login_attempts
			SET consumed_at=$1, pkce_verifier=NULL, secret_cleaned_at=$1
			WHERE id=$2 AND consumed_at IS NULL
		`, now, known.ID)
		if err != nil {
			return fmt.Errorf("consume OAuth login attempt: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return accountdomain.ErrLoginAttemptConsumed
		}
		known.ConsumedAt = &now
		known.SecretCleanedAt = &now
		attempt = known
		return nil
	})
	return attempt, err
}

func scanLoginAttempt(row sharedpostgres.Row) (accountdomain.OAuthLoginAttempt, error) {
	var attempt accountdomain.OAuthLoginAttempt
	var pkceVerifier sql.NullString
	err := row.Scan(
		&attempt.ID, &attempt.Provider, &attempt.StateHash,
		&attempt.BrowserBindingHash, &attempt.NonceHash, &pkceVerifier,
		&attempt.ReturnPath, &attempt.ExpiresAt, &attempt.ConsumedAt,
		&attempt.SecretCleanedAt, &attempt.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return accountdomain.OAuthLoginAttempt{}, accountdomain.ErrLoginAttemptInvalid
	}
	if err != nil {
		return accountdomain.OAuthLoginAttempt{}, fmt.Errorf("scan OAuth login attempt: %w", err)
	}
	if pkceVerifier.Valid {
		attempt.PKCEVerifier = pkceVerifier.String
	}
	return attempt, nil
}

func (r *Repository) CompleteExternalLogin(
	ctx context.Context,
	proposedUser accountdomain.User,
	identity accountdomain.ExternalIdentity,
	session accountdomain.AuthSession,
	previousTokenHash []byte,
) (accountapp.ExternalLoginRecord, error) {
	var record accountapp.ExternalLoginRecord
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		lockKey := string(identity.Provider) + ":" + identity.ProviderSubject
		if _, err := queryer.ExecContext(txContext,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey,
		); err != nil {
			return fmt.Errorf("lock external identity: %w", err)
		}

		user, found, err := findExternalUser(
			txContext, queryer, identity.Provider, identity.ProviderSubject,
		)
		if err != nil {
			return err
		}
		if !found {
			if _, err := queryer.ExecContext(txContext, `
				INSERT INTO users(id, status, email, display_name, created_at, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6)
			`, proposedUser.ID, proposedUser.Status, proposedUser.Email,
				proposedUser.DisplayName, proposedUser.CreatedAt, proposedUser.UpdatedAt); err != nil {
				return fmt.Errorf("insert external user: %w", err)
			}
			identity.UserID = proposedUser.ID
			if _, err := queryer.ExecContext(txContext, `
				INSERT INTO external_identities(
					id, user_id, provider, provider_subject, email_snapshot,
					email_verified, display_name_snapshot, created_at, updated_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			`, identity.ID, identity.UserID, identity.Provider, identity.ProviderSubject,
				identity.EmailSnapshot, identity.EmailVerified, identity.DisplayNameSnapshot,
				identity.CreatedAt, identity.UpdatedAt); err != nil {
				return fmt.Errorf("insert external identity: %w", err)
			}
			user = proposedUser
		} else {
			if _, err := queryer.ExecContext(txContext, `
				UPDATE external_identities
				SET email_snapshot=$1, email_verified=$2,
				    display_name_snapshot=$3, updated_at=$4
				WHERE provider=$5 AND provider_subject=$6
			`, identity.EmailSnapshot, identity.EmailVerified, identity.DisplayNameSnapshot,
				identity.UpdatedAt, identity.Provider, identity.ProviderSubject); err != nil {
				return fmt.Errorf("update external identity snapshot: %w", err)
			}
			if _, err := queryer.ExecContext(txContext, `
				UPDATE users
				SET email=$1, display_name=$2, updated_at=$3
				WHERE id=$4
			`, identity.EmailSnapshot, identity.DisplayNameSnapshot,
				identity.UpdatedAt, user.ID); err != nil {
				return fmt.Errorf("update external user snapshot: %w", err)
			}
			user.Email = identity.EmailSnapshot
			user.DisplayName = identity.DisplayNameSnapshot
			user.UpdatedAt = identity.UpdatedAt
		}

		session.UserID = user.ID
		if err := revokePreviousSession(txContext, queryer, previousTokenHash, session.CreatedAt); err != nil {
			return err
		}
		if err := insertAuthSession(txContext, queryer, session); err != nil {
			return err
		}
		record = accountapp.ExternalLoginRecord{User: user, Session: session}
		return nil
	})
	return record, err
}

type authQueryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
}

func findExternalUser(
	ctx context.Context,
	queryer authQueryer,
	provider accountdomain.IdentityProvider,
	subject string,
) (accountdomain.User, bool, error) {
	var user accountdomain.User
	err := queryer.QueryRowContext(ctx, `
		SELECT u.id, u.status, u.email, u.display_name, u.created_at, u.updated_at
		FROM external_identities i
		JOIN users u ON u.id=i.user_id
		WHERE i.provider=$1 AND i.provider_subject=$2
		FOR UPDATE OF i, u
	`, provider, subject).Scan(
		&user.ID, &user.Status, &user.Email, &user.DisplayName,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return accountdomain.User{}, false, nil
	}
	if err != nil {
		return accountdomain.User{}, false, fmt.Errorf("find external user: %w", err)
	}
	return user, true, nil
}

func insertAuthSession(ctx context.Context, queryer authQueryer, session accountdomain.AuthSession) error {
	_, err := queryer.ExecContext(ctx, `
		INSERT INTO auth_sessions(
			id, user_id, token_hash, expires_at, revoked_at, created_at,
			authenticated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, session.ID, session.UserID, session.TokenHash,
		session.ExpiresAt, session.RevokedAt, session.CreatedAt,
		session.AuthenticatedAt)
	if err != nil {
		return fmt.Errorf("insert auth session: %w", err)
	}
	return nil
}

func revokePreviousSession(
	ctx context.Context,
	queryer authQueryer,
	tokenHash []byte,
	now time.Time,
) error {
	if len(tokenHash) == 0 {
		return nil
	}
	if _, err := queryer.ExecContext(ctx, `
		UPDATE auth_sessions SET revoked_at=COALESCE(revoked_at, $1)
		WHERE token_hash=$2
	`, now, tokenHash); err != nil {
		return fmt.Errorf("revoke previous auth session: %w", err)
	}
	return nil
}

func (r *Repository) CreateDevelopmentSession(
	ctx context.Context,
	requestedUserID *accountdomain.UserID,
	proposedUser accountdomain.User,
	session accountdomain.AuthSession,
	previousTokenHash []byte,
) (accountapp.ExternalLoginRecord, error) {
	var record accountapp.ExternalLoginRecord
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		user := proposedUser
		if requestedUserID == nil {
			if _, err := queryer.ExecContext(txContext, `
				INSERT INTO users(id, status, email, display_name, created_at, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6)
			`, user.ID, user.Status, user.Email, user.DisplayName,
				user.CreatedAt, user.UpdatedAt); err != nil {
				return fmt.Errorf("insert development user: %w", err)
			}
		} else {
			err := queryer.QueryRowContext(txContext, `
				SELECT id, status, email, display_name, created_at, updated_at
				FROM users WHERE id=$1 AND status='ACTIVE'
				FOR UPDATE
			`, *requestedUserID).Scan(
				&user.ID, &user.Status, &user.Email, &user.DisplayName,
				&user.CreatedAt, &user.UpdatedAt,
			)
			if errors.Is(err, sql.ErrNoRows) {
				return accountdomain.ErrSessionMissing
			}
			if err != nil {
				return fmt.Errorf("find development user: %w", err)
			}
		}
		session.UserID = user.ID
		if err := revokePreviousSession(txContext, queryer, previousTokenHash, session.CreatedAt); err != nil {
			return err
		}
		if err := insertAuthSession(txContext, queryer, session); err != nil {
			return err
		}
		record = accountapp.ExternalLoginRecord{User: user, Session: session}
		return nil
	})
	return record, err
}

func (r *Repository) ResetDevelopmentUser(
	ctx context.Context,
	userID accountdomain.UserID,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		var lockedUserID accountdomain.UserID
		err := queryer.QueryRowContext(
			txContext,
			`SELECT id FROM users WHERE id=$1 FOR UPDATE`,
			userID,
		).Scan(&lockedUserID)
		if errors.Is(err, sql.ErrNoRows) {
			return accountdomain.ErrSessionMissing
		}
		if err != nil {
			return fmt.Errorf("inspect development user: %w", err)
		}

		if _, err := queryer.ExecContext(
			txContext, `SET CONSTRAINTS ALL DEFERRED`,
		); err != nil {
			return fmt.Errorf("defer development reset constraints: %w", err)
		}
		if _, err := queryer.ExecContext(
			txContext, `SET LOCAL vitlane.account_reset='on'`,
		); err != nil {
			return fmt.Errorf("enable development reset deletes: %w", err)
		}
		statements := []string{
			`DELETE FROM phase8_cart_items WHERE user_id=$1`,
			`DELETE FROM phase8_cart_views WHERE user_id=$1`,
			`DELETE FROM phase8_variant_interactions WHERE user_id=$1`,
			`DELETE FROM phase8_candidate_configurations WHERE user_id=$1`,
			`DELETE FROM phase8_research_candidates WHERE user_id=$1`,
			`DELETE FROM phase8_research_pools WHERE user_id=$1`,
			`DELETE FROM phase8_liked_variants WHERE user_id=$1`,
			`DELETE FROM agency_order_payment_consents WHERE user_id=$1`,
			// Live-switch evidence is immutable during normal operation. The
			// transaction-local account_reset guard permits only this development
			// profile purge to remove the synthetic authorization rows.
			`DELETE FROM agency_order_procurement_authorizations WHERE user_id=$1`,
			`DELETE FROM agency_order_receipts
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM agency_order_pii_access_audits
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			// Exact lookup audits are append-only during normal runtime. The
			// transaction-local account_reset guard owns the synthetic local-review
			// cleanup, including lookups performed by this profile and lookups by a
			// different operator that matched one of this profile's orders.
			`DELETE FROM ordering_operator_order_lookup_audits
			 WHERE operator_user_id=$1
			    OR agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM agency_order_execution_audits
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM agency_order_execution_units
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			// Procurement Context(Step 4B): task -> unit -> order -> manifest ->
			// inbox 순으로 지운다. 고지함도 함께 정리한다.
			`DELETE FROM agency_order_customer_notices WHERE user_id=$1`,
			// Support 대화(ADR-0059): projection pointer/image를 먼저 지우고,
			// resolves_card self-reference의 응답을 원본보다 먼저 지운다.
			`DELETE FROM support_open_business_actions WHERE user_id=$1`,
			`DELETE FROM support_image_attachments WHERE user_id=$1`,
			`DELETE FROM support_messages
			 WHERE (user_id=$1 OR created_by_user_id=$1)
			   AND resolves_card_id IS NOT NULL`,
			`DELETE FROM support_messages
			 WHERE user_id=$1 OR created_by_user_id=$1`,
			// Logistics(Step 5A/5B): event/allocation -> shipment -> 판정/회수 ->
			// 기대 unit 순 — 전부 merchant_order_units/merchant_orders 삭제보다
			// 먼저다(FK RESTRICT).
			`DELETE FROM logistics_shipment_events
			 WHERE shipment_id IN (
				SELECT id FROM logistics_shipments
				WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)
			 )`,
			`DELETE FROM logistics_shipment_allocations
			 WHERE shipment_id IN (
				SELECT id FROM logistics_shipments
				WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)
			 )`,
			`DELETE FROM logistics_shipments
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM logistics_delivery_resolutions
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM logistics_returns
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM logistics_expected_units
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			// LIVE 계약(Step 4C): charge -> payment -> 회수 원장 -> 취소 기록.
			`DELETE FROM merchant_charges
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM merchant_payments
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM procurement_recovery_entries
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM agency_order_cancellations WHERE user_id=$1`,
			`DELETE FROM agency_order_refund_requests WHERE user_id=$1`,
			// Manual Procurement judgment owns RESTRICT edges into task/order.
			// Delete the mutable effect lock and customer request before its
			// append-only decision evidence.
			`DELETE FROM procurement_effect_locks
			 WHERE merchant_order_id IN (
				SELECT id FROM merchant_orders
				WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)
			 )`,
			`DELETE FROM procurement_customer_requests
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM procurement_decision_records
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			// Clean-cut MO funding/refund facts have RESTRICT edges into MerchantOrder.
			// Remove external outcomes and GIWA commands first, then the compensation,
			// exact cash receipt, position, and authorization roots.
			`DELETE FROM payment_paypal_reauthorization_adoptions
			 WHERE paypal_authorization_id IN (
				SELECT id FROM payment_paypal_authorizations
				WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)
			 )`,
			`DELETE FROM payment_paypal_reauthorizations
			 WHERE paypal_authorization_id IN (
				SELECT id FROM payment_paypal_authorizations
				WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)
			 )`,
			`DELETE FROM payment_paypal_refund_adoptions
			 WHERE compensation_id IN (
				SELECT id FROM payment_mo_compensations
				WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)
			 )`,
			`DELETE FROM payment_external_operations
			 WHERE owner_kind='MO_COMPENSATION' AND owner_id IN (
				SELECT id FROM payment_mo_compensations
				WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)
			 )`,
			`DELETE FROM payment_external_operations
			 WHERE owner_kind='MO_FUNDING_POSITION' AND owner_id IN (
				SELECT id FROM payment_mo_funding_positions
				WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)
			 )`,
			`DELETE FROM payment_external_operations
			 WHERE owner_kind='PAYPAL_AUTHORIZATION' AND owner_id IN (
				SELECT id FROM payment_paypal_authorizations
				WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)
			 )`,
			`DELETE FROM payment_paypal_dispute_manual_actions
			 WHERE dispute_case_id IN (
				SELECT id FROM payment_paypal_dispute_cases
				WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)
			 )`,
			`DELETE FROM payment_paypal_dispute_cases
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM settlement_command_outbox
			 WHERE mo_compensation_id IN (
				SELECT id FROM payment_mo_compensations
				WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)
			 )`,
			`DELETE FROM payment_mo_compensations
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM payment_mo_cash_receipts
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM payment_mo_funding_positions
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM payment_paypal_authorizations
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM logistics_cancellation_reservations WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM procurement_process_inputs WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM agency_order_process_inputs WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM logistics_process_inputs WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM payment_process_inputs WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM order_process_requests WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM order_process_effects WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM payment_instruction_claims WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM agency_order_instruction_claims WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM order_process_execution_history WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM merchant_order_execution_tasks
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM merchant_order_units
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM merchant_orders
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM procurement_manifests
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM agency_order_processes
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM payment_funds_receipts
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM payment_external_operations
			 WHERE owner_kind='PAYPAL_ATTEMPT' AND owner_id IN (
				SELECT attempt.id FROM payment_paypal_attempts attempt
				JOIN payment_customer_payments payment ON payment.id=attempt.customer_payment_id
				WHERE payment.user_id=$1
			 )`,
			`DELETE FROM payment_paypal_attempts
			 WHERE customer_payment_id IN (
				SELECT id FROM payment_customer_payments WHERE user_id=$1
			 )`,
			`DELETE FROM payment_customer_payments WHERE user_id=$1`,
			`DELETE FROM agency_order_mo_allocations
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM settlement_authorizations
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM settlement_payments
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM order_process_decisions
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM agency_order_process_merchant_orders
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM order_process_events
			 WHERE agency_order_id IN (SELECT id FROM agency_orders WHERE user_id=$1)`,
			`DELETE FROM agency_order_audits WHERE user_id=$1`,
			`DELETE FROM agency_order_payment_instructions WHERE user_id=$1`,
			`DELETE FROM agency_orders WHERE user_id=$1`,
			`DELETE FROM agency_order_provider_capabilities WHERE user_id=$1`,
			`DELETE FROM agency_order_sheet_sessions WHERE user_id=$1`,
			`DELETE FROM shipping_snapshots WHERE user_id=$1`,
			`DELETE FROM shipping_profiles WHERE user_id=$1`,
			`DELETE FROM kyc_evidence_observations WHERE user_id=$1`,
			`DELETE FROM kyc_provider_operations WHERE user_id=$1`,
			`DELETE FROM kyc_credentials WHERE user_id=$1`,
			`DELETE FROM kyc_verification_cases WHERE user_id=$1`,
			`DELETE FROM wallet_ownership_proofs WHERE user_id=$1`,
			`DELETE FROM wallet_registration_attempts WHERE user_id=$1`,
			`DELETE FROM wallets WHERE user_id=$1`,
			`DELETE FROM buyer_profiles WHERE user_id=$1`,
			`DELETE FROM user_policy_acceptances WHERE user_id=$1`,
			`UPDATE shopping_sessions
			 SET current_research_round_id=NULL
			 WHERE user_id=$1`,
			`UPDATE research_rounds
				 SET result_submission_id=NULL
				 WHERE user_id=$1`,
			`DELETE FROM curation_selection_commands WHERE user_id=$1`,
			`DELETE FROM curation_selections WHERE user_id=$1`,
			`DELETE FROM candidate_configurations WHERE user_id=$1`,
			`DELETE FROM research_feedback WHERE user_id=$1`,
			`DELETE FROM research_catalog_observations WHERE user_id=$1`,
			`DELETE FROM candidates
			 WHERE shopping_session_id IN (
				SELECT id FROM shopping_sessions WHERE user_id=$1
			 )`,
			`DELETE FROM research_submissions
			 WHERE research_round_id IN (SELECT id FROM research_rounds WHERE user_id=$1)`,
			`UPDATE plan_targets
				 SET created_by_curation_run_id=NULL
				 WHERE plan_id IN (SELECT id FROM shopping_plans WHERE user_id=$1)`,
			`DELETE FROM curation_runs WHERE user_id=$1`,
			`DELETE FROM planning_proposals WHERE user_id=$1`,
			// Jobs are held by RESTRICT from the product rows above, so they can
			// only be cleared once those rows are gone.
			`DELETE FROM curation_thread_jobs WHERE thread_id IN (SELECT id FROM curation_threads WHERE user_id=$1)`,
			`DELETE FROM intelligence_steps WHERE user_id=$1`,
			`DELETE FROM intelligence_attempts WHERE user_id=$1`,
			`DELETE FROM intelligence_jobs WHERE user_id=$1`,
			`DELETE FROM managed_runner_reservations WHERE user_id=$1`,
			// The daily ledger is keyed by (scope, scope_id), not user_id. Only
			// the USER scope belongs to this profile; the SERVER scope is the
			// deployment's own spend and must survive a profile reset.
			`DELETE FROM managed_runner_usage_daily
			 WHERE scope='USER' AND scope_id=$1::text`,
			// ADR-0069 auto resolution은 session·target·plan·curation을 RESTRICT로
			// 참조하므로 그 root들보다 먼저 지운다.
			`DELETE FROM curation_auto_resolutions WHERE user_id=$1`,
			`DELETE FROM curation_actions WHERE actor_user_id=$1`,
			`DELETE FROM curation_threads WHERE user_id=$1`,
			`DELETE FROM curation_control_modes WHERE curation_id IN (SELECT id FROM curations WHERE user_id=$1)`,
			`DELETE FROM research_rounds WHERE user_id=$1`,
			`DELETE FROM planning_tasks WHERE user_id=$1`,
			`DELETE FROM shopping_sessions WHERE user_id=$1`,
			`DELETE FROM plan_targets WHERE user_id=$1`,
			`DELETE FROM curations WHERE user_id=$1`,
			`DELETE FROM plan_creation_requests WHERE user_id=$1`,
			`DELETE FROM shopping_plans WHERE user_id=$1`,
			`DELETE FROM auth_sessions WHERE user_id=$1`,
		}
		for _, statement := range statements {
			if _, err := queryer.ExecContext(txContext, statement, userID); err != nil {
				return fmt.Errorf("reset development user: %w", err)
			}
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE users
			SET status='ACTIVE', created_at=$2, updated_at=$2
			WHERE id=$1
		`, userID, now); err != nil {
			return fmt.Errorf("refresh development user: %w", err)
		}
		return nil
	})
}

func (r *Repository) FindActiveAuthSession(
	ctx context.Context,
	tokenHash []byte,
	now time.Time,
) (accountapp.ExternalLoginRecord, error) {
	var record accountapp.ExternalLoginRecord
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
			u.id, u.status, u.email, u.display_name, u.created_at, u.updated_at,
			s.id, s.user_id, s.token_hash, s.expires_at, s.revoked_at, s.created_at,
			s.authenticated_at
		FROM auth_sessions s
		JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>$2
		  AND u.status='ACTIVE'
	`, tokenHash, now).Scan(
		&record.User.ID, &record.User.Status, &record.User.Email,
		&record.User.DisplayName, &record.User.CreatedAt, &record.User.UpdatedAt,
		&record.Session.ID, &record.Session.UserID, &record.Session.TokenHash,
		&record.Session.ExpiresAt, &record.Session.RevokedAt, &record.Session.CreatedAt,
		&record.Session.AuthenticatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return accountapp.ExternalLoginRecord{}, accountdomain.ErrSessionMissing
	}
	if err != nil {
		return accountapp.ExternalLoginRecord{}, fmt.Errorf("find active auth session: %w", err)
	}
	return record, nil
}

func (r *Repository) RevokeAuthSession(
	ctx context.Context,
	tokenHash []byte,
	now time.Time,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE auth_sessions SET revoked_at=COALESCE(revoked_at, $1)
		WHERE token_hash=$2
	`, now, tokenHash)
	if err != nil {
		return fmt.Errorf("revoke auth session: %w", err)
	}
	return nil
}

func (r *Repository) RevokeAllAuthSessions(
	ctx context.Context,
	userID accountdomain.UserID,
	now time.Time,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE auth_sessions
		SET revoked_at=COALESCE(revoked_at, $1)
		WHERE user_id=$2
	`, now, userID)
	if err != nil {
		return fmt.Errorf("revoke all auth sessions: %w", err)
	}
	return nil
}

func (r *Repository) RequestAccountDeletion(
	ctx context.Context,
	userID accountdomain.UserID,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		result, err := queryer.ExecContext(txContext, `
			UPDATE users
			SET status='DELETION_REQUESTED', updated_at=$1
			WHERE id=$2 AND status='ACTIVE'
		`, now, userID)
		if err != nil {
			return fmt.Errorf("request account deletion: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil || updated != 1 {
			return accountdomain.ErrSessionMissing
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE auth_sessions
			SET revoked_at=COALESCE(revoked_at, $1)
			WHERE user_id=$2
		`, now, userID); err != nil {
			return fmt.Errorf("revoke deletion sessions: %w", err)
		}
		// The deletion ledger row is the pipeline trigger and, through its
		// offsite export, the record that re-applies this decision after a
		// restore (ADR-0040 §8).
		if _, err := queryer.ExecContext(txContext, `
			INSERT INTO user_deletions(user_id, requested_at)
			VALUES ($2, $1)
			ON CONFLICT (user_id) DO NOTHING
		`, now, userID); err != nil {
			return fmt.Errorf("record deletion request: %w", err)
		}
		return nil
	})
}
