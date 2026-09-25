package postgres

import (
	"context"
	"fmt"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
)

func (r *Repository) CleanupTerminalSecurityArtifacts(
	ctx context.Context,
	now time.Time,
	limit int,
) (accountapp.SecurityCleanupResult, error) {
	if limit <= 0 {
		limit = 100
	}
	var result accountapp.SecurityCleanupResult
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := queryer.QueryRowContext(txContext, `
			WITH candidates AS (
				SELECT id
				FROM wallet_registration_attempts
				WHERE (
					status='PENDING' AND expires_at <= $1
				) OR (
					status <> 'PENDING' AND secret_cleaned_at IS NULL
				)
				ORDER BY updated_at, id
				FOR UPDATE SKIP LOCKED
				LIMIT $2
			), cleaned AS (
				UPDATE wallet_registration_attempts attempt
				SET status=CASE
						WHEN attempt.status='PENDING' THEN 'EXPIRED'
						ELSE attempt.status
					END,
					nonce=NULL,
					message=NULL,
					secret_cleaned_at=$1,
					updated_at=CASE
						WHEN attempt.status='PENDING' THEN $1
						ELSE attempt.updated_at
					END
				FROM candidates
				WHERE attempt.id=candidates.id
				RETURNING attempt.id
			)
			SELECT COUNT(*) FROM cleaned
		`, now.UTC(), limit).Scan(&result.WalletAttempts); err != nil {
			return fmt.Errorf("clean terminal Wallet registration secrets: %w", err)
		}
		if err := queryer.QueryRowContext(txContext, `
			WITH candidates AS (
				SELECT id
				FROM oauth_login_attempts
				WHERE pkce_verifier IS NOT NULL
				  AND (consumed_at IS NOT NULL OR expires_at <= $1)
				ORDER BY COALESCE(consumed_at, expires_at), id
				FOR UPDATE SKIP LOCKED
				LIMIT $2
			), cleaned AS (
				UPDATE oauth_login_attempts attempt
				SET pkce_verifier=NULL, secret_cleaned_at=$1
				FROM candidates
				WHERE attempt.id=candidates.id
				RETURNING attempt.id
			)
			SELECT COUNT(*) FROM cleaned
		`, now.UTC(), limit).Scan(&result.LoginAttempts); err != nil {
			return fmt.Errorf("clean terminal OAuth login secrets: %w", err)
		}
		if err := queryer.QueryRowContext(txContext, `
			WITH candidates AS (
				SELECT id
				FROM mobile_auth_handoffs
				WHERE consumed_at IS NOT NULL OR expires_at <= $1
				ORDER BY COALESCE(consumed_at, expires_at), id
				FOR UPDATE SKIP LOCKED
				LIMIT $2
			), cleaned AS (
				DELETE FROM mobile_auth_handoffs handoff
				USING candidates
				WHERE handoff.id=candidates.id
				RETURNING handoff.id
			)
			SELECT COUNT(*) FROM cleaned
		`, now.UTC(), limit).Scan(&result.MobileHandoffs); err != nil {
			return fmt.Errorf("clean terminal mobile auth handoffs: %w", err)
		}
		if err := queryer.QueryRowContext(txContext, `
			WITH candidates AS (
				SELECT policy_id, dimension, subject_key_version, subject_hash
				FROM account_rate_limit_buckets
				WHERE expires_at < $1::timestamptz - INTERVAL '24 hours'
				ORDER BY expires_at
				FOR UPDATE SKIP LOCKED
				LIMIT $2
			), cleaned AS (
				DELETE FROM account_rate_limit_buckets bucket
				USING candidates
				WHERE bucket.policy_id=candidates.policy_id
				  AND bucket.dimension=candidates.dimension
				  AND bucket.subject_key_version=candidates.subject_key_version
				  AND bucket.subject_hash=candidates.subject_hash
				RETURNING bucket.policy_id
			)
			SELECT COUNT(*) FROM cleaned
		`, now.UTC(), limit).Scan(&result.RateBuckets); err != nil {
			return fmt.Errorf("clean expired Account rate limit buckets: %w", err)
		}
		return nil
	})
	return result, err
}
