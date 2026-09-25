package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func (r *Repository) FindKYCVerificationCase(
	ctx context.Context,
	userID accountdomain.UserID,
	caseID string,
) (accountdomain.KYCVerificationCase, error) {
	return scanKYCVerificationCase(r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, user_id, wallet_id, started_with_proof_id,
		       requested_level, provider_kind, provider_version,
		       external_effect, state, provider_case_ref, credential_id,
		       COALESCE(failure_code,''), created_at, updated_at, completed_at
		FROM kyc_verification_cases
		WHERE id=$1 AND user_id=$2
	`, caseID, userID))
}

func (r *Repository) FindKYCProviderOperation(
	ctx context.Context,
	userID accountdomain.UserID,
	kind accountdomain.KYCProviderOperationKind,
	clientOperationID string,
	requestHash string,
) (accountapp.KYCOperationRecord, bool, error) {
	operation, found, err := findKYCProviderOperationForRequest(
		ctx, r.database.Queryer(ctx), userID, kind, clientOperationID,
		requestHash,
	)
	if err != nil || !found {
		return accountapp.KYCOperationRecord{}, found, err
	}
	record, err := r.loadKYCOperationRecord(ctx, operation)
	return record, err == nil, err
}

func (r *Repository) ReserveKYCStart(
	ctx context.Context,
	verification accountdomain.KYCVerificationCase,
	operation accountdomain.KYCProviderOperation,
) (accountapp.KYCOperationRecord, bool, error) {
	var record accountapp.KYCOperationRecord
	var replay bool
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := lockWalletUser(txContext, queryer, verification.UserID); err != nil {
			return err
		}
		existing, found, err := findKYCProviderOperationForRequest(
			txContext, queryer, operation.UserID, operation.Kind,
			operation.ClientOperationID,
			operation.RequestHash,
		)
		if err != nil {
			return err
		}
		if found {
			record, err = r.loadKYCOperationRecord(txContext, existing)
			replay = err == nil
			return err
		}
		if verification.State != accountdomain.KYCStateCreated ||
			operation.State != accountdomain.KYCOperationReserved ||
			!sameKYCOperationIdentity(operation, verification) {
			return accountdomain.ErrKYCVerificationInvalid
		}

		wallet, err := r.FindWallet(
			txContext, verification.UserID, verification.WalletID,
		)
		if err != nil {
			return err
		}
		proof, err := r.FindWalletOwnershipProof(
			txContext,
			verification.UserID,
			verification.WalletID,
			verification.StartedWithOwnershipProofID,
		)
		if err != nil {
			return err
		}
		if !wallet.IsRegistered() ||
			wallet.CurrentOwnershipProofID == nil ||
			*wallet.CurrentOwnershipProofID != proof.ID ||
			!proof.FreshAt(
				verification.CreatedAt,
				accountapp.KYCStartOwnershipProofMaximumAge,
			) {
			return accountdomain.ErrWalletOwnershipProofNotFresh
		}

		var activeCaseID string
		err = queryer.QueryRowContext(txContext, `
			SELECT id
			FROM kyc_verification_cases
			WHERE user_id=$1
			  AND state IN ('CREATED','PENDING_PROVIDER')
			FOR UPDATE
		`, verification.UserID).Scan(&activeCaseID)
		if err == nil {
			activeOperation, found, findErr := findKYCStartOperationForCase(
				txContext, queryer, verification.UserID, activeCaseID,
			)
			if findErr != nil {
				return findErr
			}
			if !found {
				return accountdomain.ErrKYCVerificationState
			}
			if activeOperation.ClientOperationID !=
				operation.ClientOperationID {
				if err := insertKYCProviderOperationAlias(
					txContext,
					queryer,
					operation.UserID,
					operation.Kind,
					operation.ClientOperationID,
					operation.RequestHash,
					activeOperation.ID,
					operation.CreatedAt,
				); err != nil {
					return err
				}
			}
			record, err = r.loadKYCOperationRecord(
				txContext, activeOperation,
			)
			replay = err == nil
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("find active KYC verification case: %w", err)
		}

		if _, err := queryer.ExecContext(txContext, `
			INSERT INTO kyc_verification_cases(
				id, user_id, wallet_id, started_with_proof_id,
				requested_level, provider_kind, provider_version,
				external_effect, state, provider_case_ref, credential_id,
				failure_code, created_at, updated_at, completed_at
			) VALUES (
				$1,$2,$3,$4,$5,$6,$7,$8,$9,NULL,NULL,NULL,$10,$10,NULL
			)
		`, verification.ID, verification.UserID, verification.WalletID,
			verification.StartedWithOwnershipProofID,
			verification.RequestedLevel, verification.ProviderKind,
			verification.ProviderVersion, verification.ExternalEffect,
			verification.State, verification.CreatedAt); err != nil {
			return fmt.Errorf("insert KYC verification case: %w", err)
		}
		if err := insertKYCProviderOperation(
			txContext, queryer, operation,
		); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]any{
			"walletId":         verification.WalletID,
			"ownershipProofId": verification.StartedWithOwnershipProofID,
			"providerKind":     verification.ProviderKind,
			"providerVersion":  verification.ProviderVersion,
			"externalEffect":   verification.ExternalEffect,
		})
		if err := insertKYCAudit(
			txContext, queryer,
			kycAuditUUID("case-created:"+verification.ID),
			verification.ID, "USER", verification.UserID,
			"CASE_CREATED", nil, verification.State, nil,
			"case-created:"+verification.ID, details, verification.CreatedAt,
		); err != nil {
			return err
		}
		if err := insertKYCAudit(
			txContext, queryer,
			kycAuditUUID("operation-reserved:"+string(operation.ID)),
			verification.ID, "USER", verification.UserID,
			"OPERATION_RESERVED", nil, verification.State, nil,
			"operation-reserved:"+string(operation.ID), details,
			operation.CreatedAt,
		); err != nil {
			return err
		}
		record = accountapp.KYCOperationRecord{
			Case: verification, Operation: operation,
		}
		return nil
	})
	return record, replay, err
}

func (r *Repository) CompleteKYCStart(
	ctx context.Context,
	verification accountdomain.KYCVerificationCase,
	operation accountdomain.KYCProviderOperation,
	now time.Time,
) (accountapp.KYCOperationRecord, bool, error) {
	var record accountapp.KYCOperationRecord
	var replay bool
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := lockWalletUser(txContext, queryer, verification.UserID); err != nil {
			return err
		}
		storedOperation, err := scanKYCProviderOperation(
			queryer.QueryRowContext(txContext, `
				SELECT id, user_id, wallet_id, case_id, operation_kind,
				       client_operation_id, request_hash, provider_request_key,
				       state, provider_case_ref, result_case_state,
				       result_credential_id, COALESCE(failure_code,''),
				       retryable, created_at, updated_at, completed_at
				FROM kyc_provider_operations
				WHERE id=$1 AND user_id=$2
				FOR UPDATE
			`, operation.ID, operation.UserID),
		)
		if err != nil {
			return err
		}
		if !sameKYCOperationBase(storedOperation, operation) {
			return accountdomain.ErrKYCIdempotencyReused
		}
		if storedOperation.State == accountdomain.KYCOperationSucceeded {
			record, err = r.loadKYCOperationRecord(txContext, storedOperation)
			replay = err == nil
			return err
		}
		if (storedOperation.State != accountdomain.KYCOperationReserved &&
			storedOperation.State != accountdomain.KYCOperationUnavailable) ||
			operation.State != accountdomain.KYCOperationSucceeded ||
			verification.State != accountdomain.KYCStatePendingProvider ||
			verification.ProviderCaseRef == nil {
			return accountdomain.ErrKYCVerificationState
		}

		storedCase, err := scanKYCVerificationCase(
			queryer.QueryRowContext(txContext, `
				SELECT id, user_id, wallet_id, started_with_proof_id,
				       requested_level, provider_kind, provider_version,
				       external_effect, state, provider_case_ref, credential_id,
				       COALESCE(failure_code,''), created_at, updated_at, completed_at
				FROM kyc_verification_cases
				WHERE id=$1 AND user_id=$2
				FOR UPDATE
			`, verification.ID, verification.UserID),
		)
		if err != nil {
			return err
		}
		if storedCase.State != accountdomain.KYCStateCreated ||
			!sameKYCVerificationIdentity(storedCase, verification) {
			return accountdomain.ErrKYCVerificationState
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE kyc_verification_cases
			SET state=$1, provider_case_ref=$2, updated_at=$3
			WHERE id=$4 AND user_id=$5 AND state='CREATED'
		`, verification.State, verification.ProviderCaseRef, now,
			verification.ID, verification.UserID); err != nil {
			return fmt.Errorf("complete KYC provider start: %w", err)
		}
		if err := updateKYCProviderOperation(
			txContext, queryer, operation,
		); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]any{
			"providerCaseRef": verification.ProviderCaseRef,
			"externalEffect":  verification.ExternalEffect,
		})
		if err := insertKYCAudit(
			txContext, queryer,
			kycAuditUUID("provider-started:"+string(operation.ID)),
			verification.ID, "PROVIDER", nil,
			"PROVIDER_STARTED", accountdomain.KYCStateCreated,
			verification.State, nil,
			"provider-started:"+string(operation.ID), details, now,
		); err != nil {
			return err
		}
		record, err = r.loadKYCOperationRecord(txContext, operation)
		return err
	})
	return record, replay, err
}

func (r *Repository) ReserveKYCCheck(
	ctx context.Context,
	verification accountdomain.KYCVerificationCase,
	operation accountdomain.KYCProviderOperation,
) (accountapp.KYCOperationRecord, bool, error) {
	var record accountapp.KYCOperationRecord
	var replay bool
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := lockWalletUser(txContext, queryer, verification.UserID); err != nil {
			return err
		}
		existing, found, err := findKYCProviderOperationForRequest(
			txContext, queryer, operation.UserID, operation.Kind,
			operation.ClientOperationID,
			operation.RequestHash,
		)
		if err != nil {
			return err
		}
		if found {
			record, err = r.loadKYCOperationRecord(txContext, existing)
			replay = err == nil
			return err
		}
		storedCase, err := scanKYCVerificationCase(
			queryer.QueryRowContext(txContext, `
				SELECT id, user_id, wallet_id, started_with_proof_id,
				       requested_level, provider_kind, provider_version,
				       external_effect, state, provider_case_ref, credential_id,
				       COALESCE(failure_code,''), created_at, updated_at, completed_at
				FROM kyc_verification_cases
				WHERE id=$1 AND user_id=$2
				FOR UPDATE
			`, verification.ID, verification.UserID),
		)
		if err != nil {
			return err
		}
		if !storedCase.CanCheck() ||
			operation.State != accountdomain.KYCOperationReserved ||
			operation.Kind != accountdomain.KYCOperationCheck ||
			!sameKYCVerificationIdentity(storedCase, verification) ||
			!sameKYCOperationIdentity(operation, storedCase) {
			return accountdomain.ErrKYCVerificationState
		}
		activeOperation, found, err := findReservedKYCOperationForCase(
			txContext, queryer, storedCase.UserID, storedCase.ID,
			accountdomain.KYCOperationCheck,
		)
		if err != nil {
			return err
		}
		if found {
			if activeOperation.ClientOperationID !=
				operation.ClientOperationID {
				if err := insertKYCProviderOperationAlias(
					txContext,
					queryer,
					operation.UserID,
					operation.Kind,
					operation.ClientOperationID,
					operation.RequestHash,
					activeOperation.ID,
					operation.CreatedAt,
				); err != nil {
					return err
				}
			}
			record, err = r.loadKYCOperationRecord(
				txContext, activeOperation,
			)
			replay = err == nil
			return err
		}
		if err := insertKYCProviderOperation(
			txContext, queryer, operation,
		); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]any{
			"providerCaseRef": storedCase.ProviderCaseRef,
			"providerKind":    storedCase.ProviderKind,
		})
		if err := insertKYCAudit(
			txContext, queryer,
			kycAuditUUID("operation-reserved:"+string(operation.ID)),
			storedCase.ID, "USER", storedCase.UserID,
			"OPERATION_RESERVED", storedCase.State, storedCase.State, nil,
			"operation-reserved:"+string(operation.ID), details,
			operation.CreatedAt,
		); err != nil {
			return err
		}
		record = accountapp.KYCOperationRecord{
			Case: storedCase, Operation: operation,
		}
		return nil
	})
	return record, replay, err
}

func (r *Repository) CompleteKYCCheck(
	ctx context.Context,
	verification accountdomain.KYCVerificationCase,
	credential *accountdomain.KYCCredential,
	observation *accountdomain.KYCEvidenceObservation,
	operation accountdomain.KYCProviderOperation,
	now time.Time,
) (accountapp.KYCOperationRecord, bool, error) {
	var record accountapp.KYCOperationRecord
	var replay bool
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := lockWalletUser(txContext, queryer, verification.UserID); err != nil {
			return err
		}
		storedOperation, err := scanKYCProviderOperation(
			queryer.QueryRowContext(txContext, `
				SELECT id, user_id, wallet_id, case_id, operation_kind,
				       client_operation_id, request_hash, provider_request_key,
				       state, provider_case_ref, result_case_state,
				       result_credential_id, COALESCE(failure_code,''),
				       retryable, created_at, updated_at, completed_at
				FROM kyc_provider_operations
				WHERE id=$1 AND user_id=$2
				FOR UPDATE
			`, operation.ID, operation.UserID),
		)
		if err != nil {
			return err
		}
		if !sameKYCOperationBase(storedOperation, operation) {
			return accountdomain.ErrKYCIdempotencyReused
		}
		if storedOperation.State == accountdomain.KYCOperationSucceeded {
			record, err = r.loadKYCOperationRecord(txContext, storedOperation)
			replay = err == nil
			return err
		}
		if (storedOperation.State != accountdomain.KYCOperationReserved &&
			storedOperation.State != accountdomain.KYCOperationUnavailable) ||
			operation.State != accountdomain.KYCOperationSucceeded {
			return accountdomain.ErrKYCVerificationState
		}
		storedCase, err := scanKYCVerificationCase(
			queryer.QueryRowContext(txContext, `
				SELECT id, user_id, wallet_id, started_with_proof_id,
				       requested_level, provider_kind, provider_version,
				       external_effect, state, provider_case_ref, credential_id,
				       COALESCE(failure_code,''), created_at, updated_at, completed_at
				FROM kyc_verification_cases
				WHERE id=$1 AND user_id=$2
				FOR UPDATE
			`, verification.ID, verification.UserID),
		)
		if err != nil {
			return err
		}
		if !storedCase.CanCheck() ||
			!sameKYCVerificationIdentity(storedCase, verification) {
			return accountdomain.ErrKYCVerificationState
		}
		if verification.State == accountdomain.KYCStateVerified {
			if credential == nil || observation == nil ||
				verification.CredentialID == nil ||
				*verification.CredentialID != credential.ID ||
				operation.ResultCredentialID == nil ||
				*operation.ResultCredentialID != credential.ID ||
				credential.CaseID != verification.ID ||
				observation.CredentialID != credential.ID {
				return accountdomain.ErrKYCCredentialInvalid
			}
			if err := insertKYCCredential(
				txContext, queryer, *credential,
			); err != nil {
				return err
			}
			if err := insertKYCEvidenceObservation(
				txContext, queryer, *observation,
			); err != nil {
				return err
			}
		} else if credential != nil || observation != nil ||
			verification.CredentialID != nil ||
			operation.ResultCredentialID != nil {
			return accountdomain.ErrKYCVerificationInvalid
		}
		if _, err := queryer.ExecContext(txContext, `
			UPDATE kyc_verification_cases
			SET state=$1, credential_id=$2, failure_code=$3,
			    updated_at=$4, completed_at=$5
			WHERE id=$6 AND user_id=$7 AND state='PENDING_PROVIDER'
		`, verification.State, verification.CredentialID,
			nullableString(verification.FailureCode), now,
			verification.CompletedAt, verification.ID,
			verification.UserID); err != nil {
			return fmt.Errorf("complete KYC provider check: %w", err)
		}
		if err := updateKYCProviderOperation(
			txContext, queryer, operation,
		); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]any{
			"providerCaseRef": verification.ProviderCaseRef,
			"resultState":     verification.State,
			"credentialId":    verification.CredentialID,
			"failureCode":     verification.FailureCode,
			"externalEffect":  verification.ExternalEffect,
		})
		if err := insertKYCAudit(
			txContext, queryer,
			kycAuditUUID("verification-checked:"+string(operation.ID)),
			verification.ID, "PROVIDER", nil,
			"VERIFICATION_CHECKED", storedCase.State, verification.State,
			nullableString(verification.FailureCode),
			"verification-checked:"+string(operation.ID), details, now,
		); err != nil {
			return err
		}
		if credential != nil && observation != nil {
			if err := insertKYCAudit(
				txContext, queryer,
				kycAuditUUID("evidence-observed:"+string(observation.ID)),
				verification.ID, "PROVIDER", nil,
				"EVIDENCE_OBSERVED", storedCase.State, verification.State, nil,
				"evidence-observed:"+string(observation.ID), details, now,
			); err != nil {
				return err
			}
			if err := insertKYCAudit(
				txContext, queryer,
				kycAuditUUID("verified:"+string(credential.ID)),
				verification.ID, "SYSTEM", nil,
				"VERIFIED", storedCase.State, verification.State, nil,
				"verified:"+string(credential.ID), details, now,
			); err != nil {
				return err
			}
		}
		record, err = r.loadKYCOperationRecord(txContext, operation)
		return err
	})
	return record, replay, err
}

func (r *Repository) CompleteKYCProviderFailure(
	ctx context.Context,
	operation accountdomain.KYCProviderOperation,
	now time.Time,
) (accountapp.KYCOperationRecord, bool, error) {
	var record accountapp.KYCOperationRecord
	var replay bool
	err := r.database.WithinTransaction(ctx, func(txContext context.Context) error {
		queryer := r.database.Queryer(txContext)
		if err := lockWalletUser(
			txContext, queryer, operation.UserID,
		); err != nil {
			return err
		}
		storedOperation, err := scanKYCProviderOperation(
			queryer.QueryRowContext(txContext, `
				SELECT id, user_id, wallet_id, case_id, operation_kind,
				       client_operation_id, request_hash, provider_request_key,
				       state, provider_case_ref, result_case_state,
				       result_credential_id, COALESCE(failure_code,''),
				       retryable, created_at, updated_at, completed_at
				FROM kyc_provider_operations
				WHERE id=$1 AND user_id=$2
				FOR UPDATE
			`, operation.ID, operation.UserID),
		)
		if err != nil {
			return err
		}
		if !sameKYCOperationBase(storedOperation, operation) {
			return accountdomain.ErrKYCIdempotencyReused
		}
		if storedOperation.State == accountdomain.KYCOperationSucceeded {
			record, err = r.loadKYCOperationRecord(
				txContext, storedOperation,
			)
			replay = err == nil
			return err
		}
		if (storedOperation.State != accountdomain.KYCOperationReserved &&
			storedOperation.State != accountdomain.KYCOperationUnavailable) ||
			(operation.State != accountdomain.KYCOperationUnavailable &&
				operation.State != accountdomain.KYCOperationFailed) ||
			operation.FailureCode == "" {
			return accountdomain.ErrKYCVerificationState
		}
		if err := updateKYCProviderOperation(
			txContext, queryer, operation,
		); err != nil {
			return err
		}
		if storedOperation.State != operation.State {
			verification, err := r.FindKYCVerificationCase(
				txContext, operation.UserID, operation.CaseID,
			)
			if err != nil {
				return err
			}
			previousCaseState := verification.State
			if operation.Kind == accountdomain.KYCOperationStart &&
				operation.State == accountdomain.KYCOperationFailed {
				verification, err = verification.CancelBeforeProviderStarted(
					operation.FailureCode, now,
				)
				if err != nil {
					return err
				}
				if _, err := queryer.ExecContext(txContext, `
					UPDATE kyc_verification_cases
					SET state=$1, failure_code=$2, completed_at=$3, updated_at=$3
					WHERE id=$4 AND user_id=$5 AND state='CREATED'
				`, verification.State, verification.FailureCode, now,
					verification.ID, verification.UserID); err != nil {
					return fmt.Errorf(
						"cancel KYC case after provider start failure: %w", err,
					)
				}
			}
			action := "PROVIDER_FAILED"
			if operation.State == accountdomain.KYCOperationUnavailable {
				action = "PROVIDER_UNAVAILABLE"
			}
			details, _ := json.Marshal(map[string]any{
				"operationId":        operation.ID,
				"operationKind":      operation.Kind,
				"providerRequestKey": operation.ProviderRequestKey,
				"retryable":          operation.Retryable,
				"failureCode":        operation.FailureCode,
			})
			if err := insertKYCAudit(
				txContext, queryer,
				kycAuditUUID(
					"provider-failure:"+string(operation.ID)+":"+
						string(operation.State),
				),
				verification.ID, "SYSTEM", nil, action,
				previousCaseState, verification.State,
				operation.FailureCode,
				"provider-failure:"+string(operation.ID)+":"+
					string(operation.State),
				details, now,
			); err != nil {
				return err
			}
		}
		record, err = r.loadKYCOperationRecord(txContext, operation)
		return err
	})
	return record, replay, err
}

func (r *Repository) ListKYCVerificationCases(
	ctx context.Context,
	userID accountdomain.UserID,
) ([]accountdomain.KYCVerificationCase, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, user_id, wallet_id, started_with_proof_id,
		       requested_level, provider_kind, provider_version,
		       external_effect, state, provider_case_ref, credential_id,
		       COALESCE(failure_code,''), created_at, updated_at, completed_at
		FROM kyc_verification_cases
		WHERE user_id=$1
		ORDER BY created_at DESC, id DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list KYC verification cases: %w", err)
	}
	defer rows.Close()
	items := make([]accountdomain.KYCVerificationCase, 0)
	for rows.Next() {
		item, err := scanKYCVerificationCase(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ListLatestKYCProviderOperations(
	ctx context.Context,
	userID accountdomain.UserID,
) ([]accountdomain.KYCProviderOperation, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT DISTINCT ON (case_id)
		       id, user_id, wallet_id, case_id, operation_kind,
		       client_operation_id, request_hash, provider_request_key,
		       state, provider_case_ref, result_case_state,
		       result_credential_id, COALESCE(failure_code,''),
		       retryable, created_at, updated_at, completed_at
		FROM kyc_provider_operations
		WHERE user_id=$1
		ORDER BY case_id, updated_at DESC, created_at DESC, id DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list latest KYC provider operations: %w", err)
	}
	defer rows.Close()
	items := make([]accountdomain.KYCProviderOperation, 0)
	for rows.Next() {
		item, err := scanKYCProviderOperation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ListKYCCredentials(
	ctx context.Context,
	userID accountdomain.UserID,
) ([]accountdomain.KYCCredential, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, user_id, wallet_id, case_id, level, provider_kind,
		       provider_version, external_effect, subject_account_id,
		       issuer_ref, schema_ref, evidence_hash, issued_at,
		       valid_until, created_at
		FROM kyc_credentials
		WHERE user_id=$1
		ORDER BY issued_at DESC, id DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list KYC credentials: %w", err)
	}
	defer rows.Close()
	items := make([]accountdomain.KYCCredential, 0)
	for rows.Next() {
		item, err := scanKYCCredential(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ListKYCEvidenceObservations(
	ctx context.Context,
	userID accountdomain.UserID,
) ([]accountdomain.KYCEvidenceObservation, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT id, credential_id, user_id, wallet_id, status,
		       evidence_hash, source_version, observed_at,
		       valid_until, recheck_after
		FROM kyc_evidence_observations
		WHERE user_id=$1
		ORDER BY observed_at DESC, source_version DESC, id DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list KYC evidence observations: %w", err)
	}
	defer rows.Close()
	items := make([]accountdomain.KYCEvidenceObservation, 0)
	for rows.Next() {
		item, err := scanKYCEvidenceObservation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) loadKYCOperationRecord(
	ctx context.Context,
	operation accountdomain.KYCProviderOperation,
) (accountapp.KYCOperationRecord, error) {
	verification, err := r.FindKYCVerificationCase(
		ctx, operation.UserID, operation.CaseID,
	)
	if err != nil {
		return accountapp.KYCOperationRecord{}, err
	}
	record := accountapp.KYCOperationRecord{
		Case: verification, Operation: operation,
	}
	if operation.ResultCredentialID == nil {
		return record, nil
	}
	credential, err := findKYCCredential(
		ctx, r.database.Queryer(ctx), operation.UserID,
		operation.WalletID, *operation.ResultCredentialID,
	)
	if err != nil {
		return accountapp.KYCOperationRecord{}, err
	}
	observation, err := findLatestKYCEvidenceObservation(
		ctx, r.database.Queryer(ctx), operation.UserID,
		operation.WalletID, credential.ID,
	)
	if err != nil {
		return accountapp.KYCOperationRecord{}, err
	}
	record.Credential = &credential
	record.Observation = &observation
	return record, nil
}

type kycQueryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) sharedpostgres.Row
}

func findKYCProviderOperation(
	ctx context.Context,
	queryer kycQueryer,
	userID accountdomain.UserID,
	kind accountdomain.KYCProviderOperationKind,
	clientOperationID string,
) (accountdomain.KYCProviderOperation, bool, error) {
	operation, err := scanKYCProviderOperation(queryer.QueryRowContext(ctx, `
		SELECT id, user_id, wallet_id, case_id, operation_kind,
		       client_operation_id, request_hash, provider_request_key,
		       state, provider_case_ref, result_case_state,
		       result_credential_id, COALESCE(failure_code,''),
		       retryable, created_at, updated_at, completed_at
		FROM kyc_provider_operations
		WHERE user_id=$1 AND operation_kind=$2 AND client_operation_id=$3
	`, userID, kind, clientOperationID))
	if errors.Is(err, accountdomain.ErrKYCVerificationMissing) {
		return accountdomain.KYCProviderOperation{}, false, nil
	}
	return operation, err == nil, err
}

func findKYCProviderOperationForRequest(
	ctx context.Context,
	queryer kycQueryer,
	userID accountdomain.UserID,
	kind accountdomain.KYCProviderOperationKind,
	clientOperationID string,
	requestHash string,
) (accountdomain.KYCProviderOperation, bool, error) {
	operation, found, err := findKYCProviderOperation(
		ctx, queryer, userID, kind, clientOperationID,
	)
	if err != nil {
		return accountdomain.KYCProviderOperation{}, false, err
	}
	if found {
		if operation.RequestHash != requestHash {
			return accountdomain.KYCProviderOperation{}, false,
				accountdomain.ErrKYCIdempotencyReused
		}
		return operation, true, nil
	}

	var operationID accountdomain.KYCProviderOperationID
	var storedRequestHash string
	err = queryer.QueryRowContext(ctx, `
		SELECT operation_id, request_hash
		FROM kyc_provider_operation_aliases
		WHERE user_id=$1
		  AND operation_kind=$2
		  AND client_operation_id=$3
	`, userID, kind, clientOperationID).Scan(
		&operationID, &storedRequestHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return accountdomain.KYCProviderOperation{}, false, nil
	}
	if err != nil {
		return accountdomain.KYCProviderOperation{}, false,
			fmt.Errorf("find KYC provider operation alias: %w", err)
	}
	if storedRequestHash != requestHash {
		return accountdomain.KYCProviderOperation{}, false,
			accountdomain.ErrKYCIdempotencyReused
	}
	operation, err = scanKYCProviderOperation(queryer.QueryRowContext(ctx, `
		SELECT id, user_id, wallet_id, case_id, operation_kind,
		       client_operation_id, request_hash, provider_request_key,
		       state, provider_case_ref, result_case_state,
		       result_credential_id, COALESCE(failure_code,''),
		       retryable, created_at, updated_at, completed_at
		FROM kyc_provider_operations
		WHERE id=$1 AND user_id=$2 AND operation_kind=$3
	`, operationID, userID, kind))
	if err != nil {
		return accountdomain.KYCProviderOperation{}, false, err
	}
	return operation, true, nil
}

func insertKYCProviderOperationAlias(
	ctx context.Context,
	queryer kycQueryer,
	userID accountdomain.UserID,
	kind accountdomain.KYCProviderOperationKind,
	clientOperationID string,
	requestHash string,
	operationID accountdomain.KYCProviderOperationID,
	now time.Time,
) error {
	if _, err := queryer.ExecContext(ctx, `
		INSERT INTO kyc_provider_operation_aliases(
			user_id, operation_kind, client_operation_id,
			request_hash, operation_id, created_at
		) VALUES ($1,$2,$3,$4,$5,$6)
	`, userID, kind, clientOperationID, requestHash, operationID, now); err != nil {
		return fmt.Errorf("insert KYC provider operation alias: %w", err)
	}
	return nil
}

func findKYCStartOperationForCase(
	ctx context.Context,
	queryer kycQueryer,
	userID accountdomain.UserID,
	caseID string,
) (accountdomain.KYCProviderOperation, bool, error) {
	operation, err := scanKYCProviderOperation(queryer.QueryRowContext(ctx, `
		SELECT id, user_id, wallet_id, case_id, operation_kind,
		       client_operation_id, request_hash, provider_request_key,
		       state, provider_case_ref, result_case_state,
		       result_credential_id, COALESCE(failure_code,''),
		       retryable, created_at, updated_at, completed_at
		FROM kyc_provider_operations
		WHERE user_id=$1 AND case_id=$2 AND operation_kind='START'
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, userID, caseID))
	if errors.Is(err, accountdomain.ErrKYCVerificationMissing) {
		return accountdomain.KYCProviderOperation{}, false, nil
	}
	return operation, err == nil, err
}

func findReservedKYCOperationForCase(
	ctx context.Context,
	queryer kycQueryer,
	userID accountdomain.UserID,
	caseID string,
	kind accountdomain.KYCProviderOperationKind,
) (accountdomain.KYCProviderOperation, bool, error) {
	operation, err := scanKYCProviderOperation(queryer.QueryRowContext(ctx, `
		SELECT id, user_id, wallet_id, case_id, operation_kind,
		       client_operation_id, request_hash, provider_request_key,
		       state, provider_case_ref, result_case_state,
		       result_credential_id, COALESCE(failure_code,''),
		       retryable, created_at, updated_at, completed_at
		FROM kyc_provider_operations
		WHERE user_id=$1 AND case_id=$2 AND operation_kind=$3
		  AND state IN ('RESERVED','UNAVAILABLE')
		ORDER BY created_at ASC, id ASC
		LIMIT 1
		FOR UPDATE
	`, userID, caseID, kind))
	if errors.Is(err, accountdomain.ErrKYCVerificationMissing) {
		return accountdomain.KYCProviderOperation{}, false, nil
	}
	return operation, err == nil, err
}

func insertKYCProviderOperation(
	ctx context.Context,
	queryer kycQueryer,
	operation accountdomain.KYCProviderOperation,
) error {
	_, err := queryer.ExecContext(ctx, `
		INSERT INTO kyc_provider_operations(
			id, user_id, wallet_id, case_id, operation_kind,
			client_operation_id, request_hash, provider_request_key,
			state, provider_case_ref, result_case_state,
			result_credential_id, failure_code, retryable,
			created_at, updated_at, completed_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17
		)
	`, operation.ID, operation.UserID, operation.WalletID,
		operation.CaseID, operation.Kind, operation.ClientOperationID,
		operation.RequestHash, operation.ProviderRequestKey, operation.State,
		operation.ProviderCaseRef, operation.ResultCaseState,
		operation.ResultCredentialID, nullableString(operation.FailureCode),
		operation.Retryable, operation.CreatedAt, operation.UpdatedAt,
		operation.CompletedAt)
	if err != nil {
		return fmt.Errorf("insert KYC provider operation: %w", err)
	}
	return nil
}

func updateKYCProviderOperation(
	ctx context.Context,
	queryer kycQueryer,
	operation accountdomain.KYCProviderOperation,
) error {
	result, err := queryer.ExecContext(ctx, `
		UPDATE kyc_provider_operations
		SET state=$1, provider_case_ref=$2, result_case_state=$3,
		    result_credential_id=$4, failure_code=$5, retryable=$6,
		    updated_at=$7, completed_at=$8
		WHERE id=$9 AND user_id=$10
		  AND state IN ('RESERVED','UNAVAILABLE')
	`, operation.State, operation.ProviderCaseRef,
		operation.ResultCaseState, operation.ResultCredentialID,
		nullableString(operation.FailureCode), operation.Retryable,
		operation.UpdatedAt, operation.CompletedAt,
		operation.ID, operation.UserID)
	if err != nil {
		return fmt.Errorf("update KYC provider operation: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return accountdomain.ErrKYCVerificationState
	}
	return nil
}

func insertKYCCredential(
	ctx context.Context,
	queryer kycQueryer,
	credential accountdomain.KYCCredential,
) error {
	_, err := queryer.ExecContext(ctx, `
		INSERT INTO kyc_credentials(
			id, user_id, wallet_id, case_id, level, provider_kind,
			provider_version, external_effect, subject_account_id,
			issuer_ref, schema_ref, evidence_hash, issued_at,
			valid_until, created_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15
		)
	`, credential.ID, credential.UserID, credential.WalletID,
		credential.CaseID, credential.Level, credential.ProviderKind,
		credential.ProviderVersion, credential.ExternalEffect,
		credential.SubjectAccountID, credential.IssuerRef,
		credential.SchemaRef, credential.EvidenceHash,
		credential.IssuedAt, credential.ValidUntil, credential.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert KYC credential: %w", err)
	}
	return nil
}

func insertKYCEvidenceObservation(
	ctx context.Context,
	queryer kycQueryer,
	observation accountdomain.KYCEvidenceObservation,
) error {
	_, err := queryer.ExecContext(ctx, `
		INSERT INTO kyc_evidence_observations(
			id, credential_id, user_id, wallet_id, status,
			evidence_hash, source_version, observed_at,
			valid_until, recheck_after
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, observation.ID, observation.CredentialID,
		observation.UserID, observation.WalletID, observation.Status,
		observation.EvidenceHash, observation.SourceVersion,
		observation.ObservedAt, observation.ValidUntil,
		observation.RecheckAfter)
	if err != nil {
		return fmt.Errorf("insert KYC evidence observation: %w", err)
	}
	return nil
}

func findKYCCredential(
	ctx context.Context,
	queryer kycQueryer,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
	credentialID accountdomain.KYCCredentialID,
) (accountdomain.KYCCredential, error) {
	return scanKYCCredential(queryer.QueryRowContext(ctx, `
		SELECT id, user_id, wallet_id, case_id, level, provider_kind,
		       provider_version, external_effect, subject_account_id,
		       issuer_ref, schema_ref, evidence_hash, issued_at,
		       valid_until, created_at
		FROM kyc_credentials
		WHERE id=$1 AND user_id=$2 AND wallet_id=$3
	`, credentialID, userID, walletID))
}

func findLatestKYCEvidenceObservation(
	ctx context.Context,
	queryer kycQueryer,
	userID accountdomain.UserID,
	walletID accountdomain.WalletID,
	credentialID accountdomain.KYCCredentialID,
) (accountdomain.KYCEvidenceObservation, error) {
	return scanKYCEvidenceObservation(queryer.QueryRowContext(ctx, `
		SELECT id, credential_id, user_id, wallet_id, status,
		       evidence_hash, source_version, observed_at,
		       valid_until, recheck_after
		FROM kyc_evidence_observations
		WHERE credential_id=$1 AND user_id=$2 AND wallet_id=$3
		ORDER BY source_version DESC
		LIMIT 1
	`, credentialID, userID, walletID))
}

func scanKYCVerificationCase(
	row rowScanner,
) (accountdomain.KYCVerificationCase, error) {
	var item accountdomain.KYCVerificationCase
	var providerCaseRef, credentialID sql.NullString
	if err := row.Scan(
		&item.ID, &item.UserID, &item.WalletID,
		&item.StartedWithOwnershipProofID, &item.RequestedLevel,
		&item.ProviderKind, &item.ProviderVersion, &item.ExternalEffect,
		&item.State, &providerCaseRef, &credentialID, &item.FailureCode,
		&item.CreatedAt, &item.UpdatedAt, &item.CompletedAt,
	); errors.Is(err, sql.ErrNoRows) {
		return accountdomain.KYCVerificationCase{},
			accountdomain.ErrKYCVerificationMissing
	} else if err != nil {
		return accountdomain.KYCVerificationCase{},
			fmt.Errorf("scan KYC verification case: %w", err)
	}
	if providerCaseRef.Valid {
		item.ProviderCaseRef = &providerCaseRef.String
	}
	if credentialID.Valid {
		value := accountdomain.KYCCredentialID(credentialID.String)
		item.CredentialID = &value
	}
	return item, nil
}

func scanKYCProviderOperation(
	row rowScanner,
) (accountdomain.KYCProviderOperation, error) {
	var item accountdomain.KYCProviderOperation
	var providerCaseRef, resultState, resultCredentialID sql.NullString
	if err := row.Scan(
		&item.ID, &item.UserID, &item.WalletID, &item.CaseID,
		&item.Kind, &item.ClientOperationID, &item.RequestHash,
		&item.ProviderRequestKey, &item.State, &providerCaseRef,
		&resultState, &resultCredentialID, &item.FailureCode,
		&item.Retryable, &item.CreatedAt, &item.UpdatedAt,
		&item.CompletedAt,
	); errors.Is(err, sql.ErrNoRows) {
		return accountdomain.KYCProviderOperation{},
			accountdomain.ErrKYCVerificationMissing
	} else if err != nil {
		return accountdomain.KYCProviderOperation{},
			fmt.Errorf("scan KYC provider operation: %w", err)
	}
	if providerCaseRef.Valid {
		item.ProviderCaseRef = &providerCaseRef.String
	}
	if resultState.Valid {
		value := accountdomain.KYCVerificationState(resultState.String)
		item.ResultCaseState = &value
	}
	if resultCredentialID.Valid {
		value := accountdomain.KYCCredentialID(resultCredentialID.String)
		item.ResultCredentialID = &value
	}
	return item, nil
}

func scanKYCCredential(row rowScanner) (accountdomain.KYCCredential, error) {
	var item accountdomain.KYCCredential
	if err := row.Scan(
		&item.ID, &item.UserID, &item.WalletID, &item.CaseID,
		&item.Level, &item.ProviderKind, &item.ProviderVersion,
		&item.ExternalEffect, &item.SubjectAccountID, &item.IssuerRef,
		&item.SchemaRef, &item.EvidenceHash, &item.IssuedAt,
		&item.ValidUntil, &item.CreatedAt,
	); errors.Is(err, sql.ErrNoRows) {
		return accountdomain.KYCCredential{},
			accountdomain.ErrKYCCredentialInvalid
	} else if err != nil {
		return accountdomain.KYCCredential{},
			fmt.Errorf("scan KYC credential: %w", err)
	}
	return item, nil
}

func scanKYCEvidenceObservation(
	row rowScanner,
) (accountdomain.KYCEvidenceObservation, error) {
	var item accountdomain.KYCEvidenceObservation
	if err := row.Scan(
		&item.ID, &item.CredentialID, &item.UserID, &item.WalletID,
		&item.Status, &item.EvidenceHash, &item.SourceVersion,
		&item.ObservedAt, &item.ValidUntil, &item.RecheckAfter,
	); errors.Is(err, sql.ErrNoRows) {
		return accountdomain.KYCEvidenceObservation{},
			accountdomain.ErrKYCObservationInvalid
	} else if err != nil {
		return accountdomain.KYCEvidenceObservation{},
			fmt.Errorf("scan KYC evidence observation: %w", err)
	}
	return item, nil
}

func insertKYCAudit(
	ctx context.Context,
	queryer kycQueryer,
	id string,
	caseID string,
	actorKind string,
	actorUserID any,
	action string,
	previousState any,
	nextState accountdomain.KYCVerificationState,
	reasonCode any,
	idempotencyKey string,
	details []byte,
	createdAt time.Time,
) error {
	_, err := queryer.ExecContext(ctx, `
		INSERT INTO kyc_verification_audit_events(
			id, case_id, actor_kind, actor_user_id, action,
			previous_state, next_state, reason_code,
			idempotency_key, details, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`, id, caseID, actorKind, actorUserID, action, previousState,
		nextState, reasonCode, idempotencyKey, string(details), createdAt)
	if err != nil {
		return fmt.Errorf("insert KYC audit event: %w", err)
	}
	return nil
}

func sameKYCOperationIdentity(
	operation accountdomain.KYCProviderOperation,
	verification accountdomain.KYCVerificationCase,
) bool {
	return operation.UserID == verification.UserID &&
		operation.WalletID == verification.WalletID &&
		operation.CaseID == verification.ID
}

func sameKYCOperationBase(
	left accountdomain.KYCProviderOperation,
	right accountdomain.KYCProviderOperation,
) bool {
	return left.ID == right.ID &&
		left.UserID == right.UserID &&
		left.WalletID == right.WalletID &&
		left.CaseID == right.CaseID &&
		left.Kind == right.Kind &&
		left.ClientOperationID == right.ClientOperationID &&
		left.RequestHash == right.RequestHash &&
		left.ProviderRequestKey == right.ProviderRequestKey
}

func sameKYCVerificationIdentity(
	left accountdomain.KYCVerificationCase,
	right accountdomain.KYCVerificationCase,
) bool {
	return left.ID == right.ID &&
		left.UserID == right.UserID &&
		left.WalletID == right.WalletID &&
		left.StartedWithOwnershipProofID ==
			right.StartedWithOwnershipProofID &&
		left.RequestedLevel == right.RequestedLevel &&
		left.ProviderKind == right.ProviderKind &&
		left.ProviderVersion == right.ProviderVersion &&
		left.ExternalEffect == right.ExternalEffect
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func kycAuditUUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	bytes := append([]byte(nil), sum[:16]...)
	bytes[6] = (bytes[6] & 0x0f) | 0x50
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(bytes)
	return encoded[0:8] + "-" + encoded[8:12] + "-" +
		encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}
