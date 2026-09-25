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
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func (r *Repository) FindWalletRegistrationCreationReplay(
	ctx context.Context,
	userID accountdomain.UserID,
	clientOperationID string,
	requestHash string,
	now time.Time,
) (accountapp.WalletRegistrationAttemptRecord, bool, error) {
	var result accountapp.WalletRegistrationAttemptRecord
	found := false
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := lockWalletUser(txContext, queryer, userID); err != nil {
			return err
		}
		attempt, nonce, err := findWalletRegistrationAttemptByOperation(
			txContext, queryer, userID, clientOperationID,
		)
		if errors.Is(err, accountdomain.ErrWalletRegistrationAttemptMissing) {
			return nil
		}
		if err != nil {
			return err
		}
		if attempt.RequestHash != requestHash {
			return accountdomain.ErrWalletRegistrationOperationReused
		}
		if attempt.Status == accountdomain.WalletRegistrationAttemptPending &&
			!now.Before(attempt.ExpiresAt) {
			if _, err := queryer.ExecContext(txContext, `
				UPDATE wallet_registration_attempts
				SET status='EXPIRED', nonce=NULL, message=NULL,
				    secret_cleaned_at=$1, updated_at=$1
				WHERE id=$2 AND user_id=$3 AND status='PENDING'
			`, now, attempt.ID, attempt.UserID); err != nil {
				return fmt.Errorf(
					"expire replayed wallet registration attempt: %w", err,
				)
			}
			attempt.Status = accountdomain.WalletRegistrationAttemptExpired
			attempt.Message = ""
			attempt.SecretCleanedAt = &now
			attempt.UpdatedAt = now
			nonce = ""
		}
		result = accountapp.WalletRegistrationAttemptRecord{
			Attempt: attempt, Nonce: nonce,
		}
		found = true
		return nil
	})
	return result, found, err
}

func (r *Repository) CreateWalletRegistrationAttempt(
	ctx context.Context,
	attempt accountdomain.WalletRegistrationAttempt,
	nonce string,
) (accountapp.WalletRegistrationAttemptRecord, bool, error) {
	var result accountapp.WalletRegistrationAttemptRecord
	var replay bool
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := lockWalletUser(txContext, queryer, attempt.UserID); err != nil {
			return err
		}

		existing, storedNonce, err := findWalletRegistrationAttemptByOperation(
			txContext, queryer, attempt.UserID, attempt.ClientOperationID,
		)
		if err == nil {
			if existing.RequestHash != attempt.RequestHash {
				return accountdomain.ErrWalletRegistrationOperationReused
			}
			if existing.Status == accountdomain.WalletRegistrationAttemptPending &&
				!attempt.CreatedAt.Before(existing.ExpiresAt) {
				if _, err := queryer.ExecContext(txContext, `
					UPDATE wallet_registration_attempts
					SET status='EXPIRED', nonce=NULL, message=NULL,
					    secret_cleaned_at=$1, updated_at=$1
					WHERE id=$2 AND user_id=$3 AND status='PENDING'
				`, attempt.CreatedAt, existing.ID, existing.UserID); err != nil {
					return fmt.Errorf(
						"expire replayed wallet registration attempt: %w", err,
					)
				}
				existing.Status = accountdomain.WalletRegistrationAttemptExpired
				existing.Message = ""
				existing.SecretCleanedAt = &attempt.CreatedAt
				storedNonce = ""
				existing.UpdatedAt = attempt.CreatedAt
			}
			result = accountapp.WalletRegistrationAttemptRecord{
				Attempt: existing,
				Nonce:   storedNonce,
			}
			replay = true
			return nil
		}
		if !errors.Is(err, accountdomain.ErrWalletRegistrationAttemptMissing) {
			return err
		}
		if err := ensureSingleRegisteredWallet(
			txContext, queryer, attempt.UserID, attempt.ChainID, attempt.AddressKey,
		); err != nil {
			return err
		}

		if _, err := queryer.ExecContext(txContext, `
			UPDATE wallet_registration_attempts
			SET status=CASE
			        WHEN expires_at <= $1 THEN 'EXPIRED'
			        ELSE 'SUPERSEDED'
			    END,
			    nonce=NULL,
			    message=NULL,
			    secret_cleaned_at=$1,
			    updated_at=$1
			WHERE user_id=$2
			  AND chain_id=$3
			  AND address_key=$4
			  AND status='PENDING'
		`, attempt.CreatedAt, attempt.UserID, attempt.ChainID, attempt.AddressKey); err != nil {
			return fmt.Errorf("close previous wallet registration attempt: %w", err)
		}

		if _, err := queryer.ExecContext(txContext, `
			INSERT INTO wallet_registration_attempts(
				id, user_id, address, address_key, account_id, chain_id,
				origin, nonce, nonce_hash, message, message_hash,
				status, failure_count, client_operation_id, request_hash,
				completion_operation_id, completion_request_hash,
				wallet_id, ownership_proof_id, expires_at, completed_at,
				secret_cleaned_at, created_at, updated_at
			) VALUES (
				$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,
				$12,0,$13,$14,NULL,NULL,NULL,NULL,$15,NULL,NULL,$16,$16
			)
		`, attempt.ID, attempt.UserID, attempt.Address, attempt.AddressKey,
			attempt.AccountID, attempt.ChainID, attempt.Origin, nonce,
			attempt.NonceHash, attempt.Message, attempt.MessageHash,
			attempt.Status, attempt.ClientOperationID, attempt.RequestHash,
			attempt.ExpiresAt, attempt.CreatedAt); err != nil {
			return fmt.Errorf("insert wallet registration attempt: %w", err)
		}
		result = accountapp.WalletRegistrationAttemptRecord{
			Attempt: attempt,
			Nonce:   nonce,
		}
		return nil
	})
	return result, replay, err
}

func (r *Repository) FindWalletRegistrationAttempt(
	ctx context.Context,
	userID accountdomain.UserID,
	attemptID accountdomain.WalletRegistrationAttemptID,
) (accountdomain.WalletRegistrationAttempt, error) {
	attempt, _, err := scanWalletRegistrationAttempt(
		r.database.Queryer(ctx).QueryRowContext(ctx, `
			SELECT id, user_id, address, address_key, account_id, chain_id,
			       origin, nonce, nonce_hash, message, message_hash,
			       status, failure_count, client_operation_id, request_hash,
			       completion_operation_id, completion_request_hash,
			       wallet_id, ownership_proof_id, expires_at, completed_at,
			       secret_cleaned_at, created_at, updated_at
			FROM wallet_registration_attempts
			WHERE id=$1 AND user_id=$2
		`, attemptID, userID),
	)
	return attempt, err
}

func (r *Repository) ExpireWalletRegistrationAttempt(
	ctx context.Context,
	userID accountdomain.UserID,
	attemptID accountdomain.WalletRegistrationAttemptID,
	now time.Time,
) error {
	var state accountdomain.WalletRegistrationAttemptStatus
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		UPDATE wallet_registration_attempts
		SET status='EXPIRED', nonce=NULL, message=NULL,
		    secret_cleaned_at=$1, updated_at=$1
		WHERE id=$2 AND user_id=$3
		  AND status='PENDING' AND expires_at <= $1
		RETURNING status
	`, now, attemptID, userID).Scan(&state)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("expire wallet registration attempt: %w", err)
	}
	attempt, findErr := r.FindWalletRegistrationAttempt(ctx, userID, attemptID)
	if findErr != nil {
		return findErr
	}
	if attempt.Status == accountdomain.WalletRegistrationAttemptExpired {
		return nil
	}
	return accountdomain.ErrWalletRegistrationAttemptClosed
}

func (r *Repository) FindWalletRegistrationCompletionReplay(
	ctx context.Context,
	userID accountdomain.UserID,
	attemptID accountdomain.WalletRegistrationAttemptID,
	clientOperationID string,
	requestHash string,
) (accountapp.WalletRegistrationCompletion, bool, error) {
	attempt, _, err := scanWalletRegistrationAttempt(
		r.database.Queryer(ctx).QueryRowContext(ctx, `
			SELECT id, user_id, address, address_key, account_id, chain_id,
			       origin, nonce, nonce_hash, message, message_hash,
			       status, failure_count, client_operation_id, request_hash,
			       completion_operation_id, completion_request_hash,
			       wallet_id, ownership_proof_id, expires_at, completed_at,
			       secret_cleaned_at, created_at, updated_at
			FROM wallet_registration_attempts
			WHERE user_id=$1 AND completion_operation_id=$2
		`, userID, clientOperationID),
	)
	if errors.Is(err, accountdomain.ErrWalletRegistrationAttemptMissing) {
		return accountapp.WalletRegistrationCompletion{}, false, nil
	}
	if err != nil {
		return accountapp.WalletRegistrationCompletion{}, false, err
	}
	if attempt.ID != attemptID ||
		attempt.CompletionRequestHash != requestHash ||
		attempt.Status != accountdomain.WalletRegistrationAttemptCompleted ||
		attempt.WalletID == nil ||
		attempt.OwnershipProofID == nil {
		return accountapp.WalletRegistrationCompletion{}, false,
			accountdomain.ErrWalletRegistrationOperationReused
	}
	completion, err := r.walletRegistrationCompletion(
		ctx, userID, *attempt.WalletID, *attempt.OwnershipProofID,
	)
	return completion, err == nil, err
}

func (r *Repository) RecordWalletRegistrationFailure(
	ctx context.Context,
	userID accountdomain.UserID,
	attemptID accountdomain.WalletRegistrationAttemptID,
	now time.Time,
) error {
	var state accountdomain.WalletRegistrationAttemptStatus
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		UPDATE wallet_registration_attempts
		SET failure_count=failure_count+1,
		    status=CASE
		        WHEN failure_count+1 >= $1 THEN 'LOCKED'
		        ELSE status
		    END,
		    nonce=CASE
		        WHEN failure_count+1 >= $1 THEN NULL
		        ELSE nonce
		    END,
		    message=CASE
		        WHEN failure_count+1 >= $1 THEN NULL
		        ELSE message
		    END,
		    secret_cleaned_at=CASE
		        WHEN failure_count+1 >= $1 THEN $2
		        ELSE secret_cleaned_at
		    END,
		    updated_at=$2
		WHERE id=$3 AND user_id=$4
		  AND status='PENDING' AND expires_at > $2
		RETURNING status
	`, accountdomain.WalletRegistrationMaxFailures, now, attemptID, userID).Scan(&state)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("record wallet registration failure: %w", err)
	}
	attempt, findErr := r.FindWalletRegistrationAttempt(ctx, userID, attemptID)
	if findErr != nil {
		return findErr
	}
	if !now.Before(attempt.ExpiresAt) {
		_, _ = r.database.Queryer(ctx).ExecContext(ctx, `
			UPDATE wallet_registration_attempts
			SET status='EXPIRED', nonce=NULL, message=NULL,
			    secret_cleaned_at=$1, updated_at=$1
			WHERE id=$2 AND user_id=$3 AND status='PENDING'
		`, now, attemptID, userID)
		return accountdomain.ErrWalletRegistrationAttemptExpired
	}
	return accountdomain.ErrWalletRegistrationAttemptClosed
}

func (r *Repository) CompleteWalletRegistrationAttempt(
	ctx context.Context,
	expected accountdomain.WalletRegistrationAttempt,
	proposedWallet accountdomain.Wallet,
	proposedProof accountdomain.WalletOwnershipProof,
	clientOperationID string,
	requestHash string,
	now time.Time,
) (accountapp.WalletRegistrationCompletion, bool, error) {
	var completion accountapp.WalletRegistrationCompletion
	var replay bool
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := lockWalletUser(txContext, queryer, expected.UserID); err != nil {
			return err
		}
		locked, _, err := scanWalletRegistrationAttempt(
			queryer.QueryRowContext(txContext, `
				SELECT id, user_id, address, address_key, account_id, chain_id,
				       origin, nonce, nonce_hash, message, message_hash,
				       status, failure_count, client_operation_id, request_hash,
				       completion_operation_id, completion_request_hash,
				       wallet_id, ownership_proof_id, expires_at, completed_at,
				       secret_cleaned_at, created_at, updated_at
				FROM wallet_registration_attempts
				WHERE id=$1 AND user_id=$2
				FOR UPDATE
			`, expected.ID, expected.UserID),
		)
		if err != nil {
			return err
		}
		if locked.Status == accountdomain.WalletRegistrationAttemptCompleted {
			if locked.CompletionOperationID != clientOperationID ||
				locked.CompletionRequestHash != requestHash ||
				locked.WalletID == nil ||
				locked.OwnershipProofID == nil {
				return accountdomain.ErrWalletRegistrationOperationReused
			}
			found, err := r.walletRegistrationCompletion(
				txContext, locked.UserID, *locked.WalletID, *locked.OwnershipProofID,
			)
			if err != nil {
				return err
			}
			completion, replay = found, true
			return nil
		}
		if err := locked.CanCompleteAt(now); err != nil {
			if errors.Is(err, accountdomain.ErrWalletRegistrationAttemptExpired) {
				_, _ = queryer.ExecContext(txContext, `
					UPDATE wallet_registration_attempts
					SET status='EXPIRED', nonce=NULL, message=NULL,
					    secret_cleaned_at=$1, updated_at=$1
					WHERE id=$2 AND status='PENDING'
				`, now, locked.ID)
			}
			return err
		}
		if !sameWalletRegistrationAttempt(locked, expected) {
			return accountdomain.ErrWalletRegistrationAttemptInvalid
		}
		if err := ensureSingleRegisteredWallet(
			txContext, queryer, locked.UserID, locked.ChainID, locked.AddressKey,
		); err != nil {
			return err
		}

		wallet := proposedWallet
		existing, findErr := findWalletByAccount(
			txContext, queryer, locked.UserID, locked.ChainID, locked.AddressKey, true,
		)
		switch {
		case findErr == nil:
			wallet.ID = existing.ID
			wallet.CreatedAt = existing.CreatedAt
			if existing.RegistrationStatus == accountdomain.WalletRegistered {
				wallet.RegisteredAt = existing.RegisteredAt
			}
		case errors.Is(findErr, accountdomain.ErrWalletNotRegistered):
		default:
			return findErr
		}
		wallet.UserID = locked.UserID
		wallet.Address = locked.Address
		wallet.AddressKey = append([]byte(nil), locked.AddressKey...)
		wallet.AccountID = locked.AccountID
		wallet.ChainID = locked.ChainID
		wallet.RegistrationStatus = accountdomain.WalletRegistered
		if wallet.RegisteredAt.IsZero() {
			wallet.RegisteredAt = now
		}
		wallet.DeregisteredAt = nil
		wallet.UpdatedAt = now
		wallet.CurrentOwnershipProofID = &proposedProof.ID

		if _, err := queryer.ExecContext(txContext, `
			INSERT INTO wallets(
				id, user_id, address, address_key, account_id, chain_id,
				registration_status, current_ownership_proof_id,
				registered_at, deregistered_at, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,'REGISTERED',$7,$8,NULL,$9,$8)
			ON CONFLICT (user_id, chain_id, address_key)
			DO UPDATE SET
				address=EXCLUDED.address,
				account_id=EXCLUDED.account_id,
				registration_status='REGISTERED',
				current_ownership_proof_id=EXCLUDED.current_ownership_proof_id,
				registered_at=CASE
				    WHEN wallets.registration_status='REGISTERED'
				    THEN wallets.registered_at
				    ELSE EXCLUDED.registered_at
				END,
				deregistered_at=NULL,
				updated_at=EXCLUDED.updated_at
		`, wallet.ID, wallet.UserID, wallet.Address, wallet.AddressKey,
			wallet.AccountID, wallet.ChainID, proposedProof.ID, now,
			wallet.CreatedAt); err != nil {
			return fmt.Errorf("register wallet: %w", err)
		}

		proof := proposedProof
		proof.UserID = locked.UserID
		proof.WalletID = wallet.ID
		proof.RegistrationAttemptID = locked.ID
		proof.Address = locked.Address
		proof.AddressKey = append([]byte(nil), locked.AddressKey...)
		proof.AccountID = locked.AccountID
		proof.ChainID = locked.ChainID
		if _, err := queryer.ExecContext(txContext, `
			INSERT INTO wallet_ownership_proofs(
				id, user_id, wallet_id, registration_attempt_id,
				address, address_key, account_id, chain_id, origin, method,
				message_hash, verified_at, valid_until, revoked_at
			) VALUES (
				$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14
			)
		`, proof.ID, proof.UserID, proof.WalletID,
			proof.RegistrationAttemptID, proof.Address, proof.AddressKey,
			proof.AccountID, proof.ChainID, proof.Origin, proof.Method,
			proof.MessageHash, proof.VerifiedAt, proof.ValidUntil,
			proof.RevokedAt); err != nil {
			return fmt.Errorf("insert wallet ownership proof: %w", err)
		}
		if findErr == nil &&
			existing.CurrentOwnershipProofID != nil &&
			*existing.CurrentOwnershipProofID != proof.ID {
			if _, err := queryer.ExecContext(txContext, `
				UPDATE wallet_ownership_proofs
				SET revoked_at=COALESCE(revoked_at,$1)
				WHERE id=$2 AND user_id=$3 AND wallet_id=$4
			`, now, *existing.CurrentOwnershipProofID,
				wallet.UserID, wallet.ID); err != nil {
				return fmt.Errorf(
					"revoke superseded wallet ownership proof: %w", err,
				)
			}
		}

		result, err := queryer.ExecContext(txContext, `
			UPDATE wallet_registration_attempts
			SET status='COMPLETED',
			    nonce=NULL,
			    message=NULL,
			    secret_cleaned_at=$5,
			    completion_operation_id=$1,
			    completion_request_hash=$2,
			    wallet_id=$3,
			    ownership_proof_id=$4,
			    completed_at=$5,
			    updated_at=$5
			WHERE id=$6 AND user_id=$7 AND status='PENDING'
		`, clientOperationID, requestHash, wallet.ID, proof.ID, now,
			locked.ID, locked.UserID)
		if err != nil {
			return fmt.Errorf("complete wallet registration attempt: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return accountdomain.ErrWalletRegistrationAttemptClosed
		}

		wallet.IsDefault = true
		completion = accountapp.WalletRegistrationCompletion{
			Wallet:         wallet,
			OwnershipProof: proof,
		}
		return nil
	})
	return completion, replay, err
}

func (r *Repository) FindWallet(
	ctx context.Context,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
) (accountdomain.Wallet, error) {
	return scanWallet(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT w.id, w.user_id, w.address, w.address_key, w.account_id,
		       w.chain_id, w.registration_status, w.current_ownership_proof_id,
		       (w.registration_status='REGISTERED'),
		       w.registered_at, w.deregistered_at, w.created_at, w.updated_at
		FROM wallets w
		WHERE w.id=$1 AND w.user_id=$2
	`, walletID, userID))
}

func (r *Repository) FindWalletByAccount(
	ctx context.Context,
	userID accountdomain.UserID,
	chainID string,
	addressKey []byte,
) (accountdomain.Wallet, bool, error) {
	wallet, err := findWalletByAccount(
		ctx, r.database.Queryer(ctx), userID, chainID, addressKey, false,
	)
	if errors.Is(err, accountdomain.ErrWalletNotRegistered) {
		return accountdomain.Wallet{}, false, nil
	}
	if err != nil {
		return accountdomain.Wallet{}, false, err
	}
	return wallet, true, nil
}

func (r *Repository) FindWalletOwnershipProof(
	ctx context.Context,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
	proofID accountdomain.WalletOwnershipProofID,
) (accountdomain.WalletOwnershipProof, error) {
	return scanWalletOwnershipProof(
		r.database.Queryer(ctx).QueryRowContext(ctx, `
			SELECT id, user_id, wallet_id, registration_attempt_id,
			       address, address_key, account_id, chain_id, origin, method,
			       message_hash, verified_at, valid_until, revoked_at
			FROM wallet_ownership_proofs
			WHERE id=$1 AND user_id=$2 AND wallet_id=$3
		`, proofID, userID, walletID),
	)
}

func (r *Repository) LockPaymentIdentity(
	ctx context.Context,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
	proofID accountdomain.WalletOwnershipProofID,
) (
	accountdomain.Wallet,
	accountdomain.WalletOwnershipProof,
	error,
) {
	var wallet accountdomain.Wallet
	var proof accountdomain.WalletOwnershipProof
	var currentProofID sql.NullString
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT
		    w.id, w.user_id, w.address, w.address_key, w.account_id,
		    w.chain_id, w.registration_status, w.current_ownership_proof_id,
		    TRUE,
		    w.registered_at, w.deregistered_at, w.created_at, w.updated_at,
		    proof.id, proof.user_id, proof.wallet_id,
		    proof.registration_attempt_id, proof.address, proof.address_key,
		    proof.account_id, proof.chain_id, proof.origin, proof.method,
		    proof.message_hash, proof.verified_at, proof.valid_until,
		    proof.revoked_at
		FROM users owner
		JOIN wallets w
		  ON w.user_id=owner.id
		JOIN wallet_ownership_proofs proof
		  ON proof.id=w.current_ownership_proof_id
		 AND proof.user_id=w.user_id
		 AND proof.wallet_id=w.id
		WHERE owner.id=$1
		  AND w.id=$2
		  AND proof.id=$3
		  AND w.registration_status='REGISTERED'
		  AND proof.revoked_at IS NULL
		FOR UPDATE OF owner, w, proof
	`, userID, walletID, proofID).Scan(
		&wallet.ID, &wallet.UserID, &wallet.Address, &wallet.AddressKey,
		&wallet.AccountID, &wallet.ChainID, &wallet.RegistrationStatus,
		&currentProofID, &wallet.IsDefault, &wallet.RegisteredAt,
		&wallet.DeregisteredAt, &wallet.CreatedAt, &wallet.UpdatedAt,
		&proof.ID, &proof.UserID, &proof.WalletID,
		&proof.RegistrationAttemptID, &proof.Address, &proof.AddressKey,
		&proof.AccountID, &proof.ChainID, &proof.Origin, &proof.Method,
		&proof.MessageHash, &proof.VerifiedAt, &proof.ValidUntil,
		&proof.RevokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletOwnershipProofInvalid
	}
	if err != nil {
		return accountdomain.Wallet{}, accountdomain.WalletOwnershipProof{},
			fmt.Errorf("lock payment Wallet identity: %w", err)
	}
	if currentProofID.Valid {
		value := accountdomain.WalletOwnershipProofID(currentProofID.String)
		wallet.CurrentOwnershipProofID = &value
	}
	return wallet, proof, nil
}

func (r *Repository) ListWallets(
	ctx context.Context,
	userID accountdomain.UserID,
) ([]accountdomain.Wallet, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT w.id, w.user_id, w.address, w.address_key, w.account_id,
		       w.chain_id, w.registration_status, w.current_ownership_proof_id,
		       TRUE,
		       w.registered_at, w.deregistered_at, w.created_at, w.updated_at
		FROM wallets w
		WHERE w.user_id=$1
		  AND w.registration_status='REGISTERED'
		ORDER BY w.registered_at ASC, w.id ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list wallets: %w", err)
	}
	defer rows.Close()
	wallets := make([]accountdomain.Wallet, 0)
	for rows.Next() {
		wallet, err := scanWallet(rows)
		if err != nil {
			return nil, err
		}
		wallets = append(wallets, wallet)
	}
	return wallets, rows.Err()
}

func (r *Repository) ListWalletOwnershipProofs(
	ctx context.Context,
	userID accountdomain.UserID,
) ([]accountdomain.WalletOwnershipProof, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, user_id, wallet_id, registration_attempt_id,
		       address, address_key, account_id, chain_id, origin, method,
		       message_hash, verified_at, valid_until, revoked_at
		FROM wallet_ownership_proofs
		WHERE user_id=$1
		ORDER BY verified_at DESC, id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list wallet ownership proofs: %w", err)
	}
	defer rows.Close()
	proofs := make([]accountdomain.WalletOwnershipProof, 0)
	for rows.Next() {
		proof, err := scanWalletOwnershipProof(rows)
		if err != nil {
			return nil, err
		}
		proofs = append(proofs, proof)
	}
	return proofs, rows.Err()
}

func (r *Repository) DeregisterWallet(
	ctx context.Context,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
	now time.Time,
) error {
	return r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := lockWalletUser(txContext, queryer, userID); err != nil {
			return err
		}
		var (
			registrationStatus accountdomain.WalletRegistrationStatus
			currentProofID     sql.NullString
		)
		err := queryer.QueryRowContext(txContext, `
			SELECT registration_status, current_ownership_proof_id
			FROM wallets
			WHERE id=$1 AND user_id=$2
			FOR UPDATE
		`, walletID, userID).Scan(&registrationStatus, &currentProofID)
		if errors.Is(err, sql.ErrNoRows) {
			return accountdomain.ErrWalletNotRegistered
		}
		if err != nil {
			return fmt.Errorf("lock wallet for deregistration: %w", err)
		}
		if registrationStatus == accountdomain.WalletDeregistered {
			return nil
		}
		if registrationStatus != accountdomain.WalletRegistered {
			return accountdomain.ErrWalletNotRegistered
		}
		if currentProofID.Valid {
			if _, err := queryer.ExecContext(txContext, `
				UPDATE wallet_ownership_proofs
				SET revoked_at=COALESCE(revoked_at,$1)
				WHERE id=$2 AND user_id=$3 AND wallet_id=$4
			`, now, currentProofID.String, userID, walletID); err != nil {
				return fmt.Errorf("revoke current wallet ownership proof: %w", err)
			}
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE wallets
			SET registration_status='DEREGISTERED',
			    current_ownership_proof_id=NULL,
			    deregistered_at=$1,
			    updated_at=$1
			WHERE id=$2 AND user_id=$3
		`, now, walletID, userID); err != nil {
			return fmt.Errorf("deregister wallet: %w", err)
		}
		type activeKYC struct {
			id    string
			state accountdomain.KYCVerificationState
		}
		rows, err := queryer.QueryContext(txContext, `
			SELECT id, state
			FROM kyc_verification_cases
			WHERE user_id=$1 AND wallet_id=$2
			  AND state IN ('CREATED','PENDING_PROVIDER')
			FOR UPDATE
		`, userID, walletID)
		if err != nil {
			return fmt.Errorf("lock active wallet KYC cases: %w", err)
		}
		activeCases := make([]activeKYC, 0)
		for rows.Next() {
			var item activeKYC
			if err := rows.Scan(&item.id, &item.state); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan active wallet KYC case: %w", err)
			}
			activeCases = append(activeCases, item)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close active wallet KYC rows: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate active wallet KYC cases: %w", err)
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE kyc_verification_cases
			SET state='CANCELLED',
			    failure_code='WALLET_DEREGISTERED',
			    completed_at=$1,
			    updated_at=$1
			WHERE user_id=$2 AND wallet_id=$3
			  AND state IN ('CREATED','PENDING_PROVIDER')
		`, now, userID, walletID); err != nil {
			return fmt.Errorf("cancel active wallet KYC case: %w", err)
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE kyc_provider_operations
			SET state='FAILED',
			    failure_code='WALLET_DEREGISTERED',
			    retryable=FALSE,
			    completed_at=$1,
			    updated_at=$1
			WHERE user_id=$2 AND wallet_id=$3
			  AND state IN ('RESERVED','UNAVAILABLE')
		`, now, userID, walletID); err != nil {
			return fmt.Errorf("close active wallet KYC operations: %w", err)
		}
		for _, verification := range activeCases {
			if err := insertKYCAudit(
				txContext, queryer,
				kycAuditUUID("wallet-deregistered:"+verification.id),
				verification.id, "USER", userID, "CANCELLED",
				verification.state, accountdomain.KYCStateCancelled,
				"WALLET_DEREGISTERED",
				"wallet-deregistered:"+verification.id,
				[]byte(`{"reasonCode":"WALLET_DEREGISTERED"}`), now,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) walletRegistrationCompletion(
	ctx context.Context,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
	proofID accountdomain.WalletOwnershipProofID,
) (accountapp.WalletRegistrationCompletion, error) {
	wallet, err := r.FindWallet(ctx, userID, walletID)
	if err != nil {
		return accountapp.WalletRegistrationCompletion{}, err
	}
	proof, err := r.FindWalletOwnershipProof(ctx, userID, walletID, proofID)
	if err != nil {
		return accountapp.WalletRegistrationCompletion{}, err
	}
	return accountapp.WalletRegistrationCompletion{
		Wallet:         wallet,
		OwnershipProof: proof,
	}, nil
}

func findWalletRegistrationAttemptByOperation(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	},
	userID accountdomain.UserID,
	clientOperationID string,
) (accountdomain.WalletRegistrationAttempt, string, error) {
	return scanWalletRegistrationAttempt(queryer.QueryRowContext(ctx, `
		SELECT id, user_id, address, address_key, account_id, chain_id,
		       origin, nonce, nonce_hash, message, message_hash,
		       status, failure_count, client_operation_id, request_hash,
		       completion_operation_id, completion_request_hash,
		       wallet_id, ownership_proof_id, expires_at, completed_at,
		       secret_cleaned_at, created_at, updated_at
		FROM wallet_registration_attempts
		WHERE user_id=$1 AND client_operation_id=$2
	`, userID, clientOperationID))
}

func scanWalletRegistrationAttempt(
	row rowScanner,
) (accountdomain.WalletRegistrationAttempt, string, error) {
	var attempt accountdomain.WalletRegistrationAttempt
	var nonce, message sql.NullString
	var completionOperationID, completionRequestHash sql.NullString
	var walletID, proofID sql.NullString
	if err := row.Scan(
		&attempt.ID, &attempt.UserID, &attempt.Address, &attempt.AddressKey,
		&attempt.AccountID, &attempt.ChainID, &attempt.Origin, &nonce,
		&attempt.NonceHash, &message, &attempt.MessageHash,
		&attempt.Status, &attempt.FailureCount, &attempt.ClientOperationID,
		&attempt.RequestHash, &completionOperationID, &completionRequestHash,
		&walletID, &proofID, &attempt.ExpiresAt, &attempt.CompletedAt,
		&attempt.SecretCleanedAt,
		&attempt.CreatedAt, &attempt.UpdatedAt,
	); errors.Is(err, sql.ErrNoRows) {
		return accountdomain.WalletRegistrationAttempt{}, "",
			accountdomain.ErrWalletRegistrationAttemptMissing
	} else if err != nil {
		return accountdomain.WalletRegistrationAttempt{}, "",
			fmt.Errorf("scan wallet registration attempt: %w", err)
	}
	if completionOperationID.Valid {
		attempt.CompletionOperationID = completionOperationID.String
	}
	if message.Valid {
		attempt.Message = message.String
	}
	if completionRequestHash.Valid {
		attempt.CompletionRequestHash = completionRequestHash.String
	}
	if walletID.Valid {
		value := accountdomain.WalletID(walletID.String)
		attempt.WalletID = &value
	}
	if proofID.Valid {
		value := accountdomain.WalletOwnershipProofID(proofID.String)
		attempt.OwnershipProofID = &value
	}
	return attempt, nonce.String, nil
}

func scanWallet(row rowScanner) (accountdomain.Wallet, error) {
	var wallet accountdomain.Wallet
	var currentProofID sql.NullString
	if err := row.Scan(
		&wallet.ID, &wallet.UserID, &wallet.Address, &wallet.AddressKey,
		&wallet.AccountID, &wallet.ChainID, &wallet.RegistrationStatus,
		&currentProofID, &wallet.IsDefault, &wallet.RegisteredAt,
		&wallet.DeregisteredAt, &wallet.CreatedAt, &wallet.UpdatedAt,
	); errors.Is(err, sql.ErrNoRows) {
		return accountdomain.Wallet{}, accountdomain.ErrWalletNotRegistered
	} else if err != nil {
		return accountdomain.Wallet{}, fmt.Errorf("scan wallet: %w", err)
	}
	if currentProofID.Valid {
		value := accountdomain.WalletOwnershipProofID(currentProofID.String)
		wallet.CurrentOwnershipProofID = &value
	}
	return wallet, nil
}

func scanWalletOwnershipProof(
	row rowScanner,
) (accountdomain.WalletOwnershipProof, error) {
	var proof accountdomain.WalletOwnershipProof
	if err := row.Scan(
		&proof.ID, &proof.UserID, &proof.WalletID,
		&proof.RegistrationAttemptID, &proof.Address, &proof.AddressKey,
		&proof.AccountID, &proof.ChainID, &proof.Origin, &proof.Method,
		&proof.MessageHash, &proof.VerifiedAt, &proof.ValidUntil,
		&proof.RevokedAt,
	); errors.Is(err, sql.ErrNoRows) {
		return accountdomain.WalletOwnershipProof{},
			accountdomain.ErrWalletOwnershipProofMissing
	} else if err != nil {
		return accountdomain.WalletOwnershipProof{},
			fmt.Errorf("scan wallet ownership proof: %w", err)
	}
	return proof, nil
}

func findWalletByAccount(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	},
	userID accountdomain.UserID,
	chainID string,
	addressKey []byte,
	forUpdate bool,
) (accountdomain.Wallet, error) {
	suffix := ""
	if forUpdate {
		suffix = " FOR UPDATE OF w"
	}
	return scanWallet(queryer.QueryRowContext(ctx, `
		SELECT w.id, w.user_id, w.address, w.address_key, w.account_id,
		       w.chain_id, w.registration_status, w.current_ownership_proof_id,
		       (w.registration_status='REGISTERED'),
		       w.registered_at, w.deregistered_at, w.created_at, w.updated_at
		FROM wallets w
		WHERE w.user_id=$1 AND w.chain_id=$2 AND w.address_key=$3
	`+suffix, userID, chainID, addressKey))
}

func ensureSingleRegisteredWallet(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	},
	userID accountdomain.UserID,
	chainID string,
	addressKey []byte,
) error {
	var (
		currentChainID    string
		currentAddressKey []byte
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT chain_id, address_key
		FROM wallets
		WHERE user_id=$1 AND registration_status='REGISTERED'
		FOR UPDATE
	`, userID).Scan(&currentChainID, &currentAddressKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find current wallet: %w", err)
	}
	if currentChainID != chainID || !bytes.Equal(currentAddressKey, addressKey) {
		return accountdomain.ErrWalletAlreadyRegistered
	}
	return nil
}

func lockWalletUser(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
	},
	userID accountdomain.UserID,
) error {
	var locked accountdomain.UserID
	err := queryer.QueryRowContext(ctx, `
		SELECT id FROM users WHERE id=$1 FOR UPDATE
	`, userID).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return accountdomain.ErrAccountStateInvalid
	}
	if err != nil {
		return fmt.Errorf("lock wallet user: %w", err)
	}
	return nil
}

func sameWalletRegistrationAttempt(
	left accountdomain.WalletRegistrationAttempt,
	right accountdomain.WalletRegistrationAttempt,
) bool {
	return left.ID == right.ID &&
		left.UserID == right.UserID &&
		left.AccountID == right.AccountID &&
		left.ChainID == right.ChainID &&
		left.Address == right.Address &&
		bytes.Equal(left.AddressKey, right.AddressKey) &&
		left.Origin == right.Origin &&
		left.Message == right.Message &&
		bytes.Equal(left.MessageHash, right.MessageHash) &&
		bytes.Equal(left.NonceHash, right.NonceHash) &&
		left.RequestHash == right.RequestHash
}
