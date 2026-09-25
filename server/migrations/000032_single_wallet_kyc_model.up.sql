-- Destructive simplification: one currently registered Wallet and one active
-- KYC verification per user. Historical Wallet, proof and KYC rows remain for
-- audit, but only the selected current Wallet remains usable.

CREATE TABLE single_wallet_kyc_cutover_audits (
    migration_version TEXT PRIMARY KEY,
    pre_cutover_counts JSONB NOT NULL,
    post_cutover_counts JSONB NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL
);

CREATE TEMP TABLE single_wallet_cutover_keep (
    user_id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL UNIQUE
) ON COMMIT DROP;

INSERT INTO single_wallet_cutover_keep(user_id, wallet_id)
SELECT owner.id, COALESCE(
    (
        SELECT preferred.id
        FROM account_wallet_preferences preference
        JOIN wallets preferred
          ON preferred.id=preference.default_wallet_id
         AND preferred.user_id=preference.user_id
         AND preferred.registration_status='REGISTERED'
        WHERE preference.user_id=owner.id
    ),
    (
        SELECT candidate.id
        FROM wallets candidate
        WHERE candidate.user_id=owner.id
          AND candidate.registration_status='REGISTERED'
        ORDER BY candidate.registered_at ASC, candidate.id ASC
        LIMIT 1
    )
)
FROM users owner
WHERE EXISTS (
    SELECT 1
    FROM wallets registered
    WHERE registered.user_id=owner.id
      AND registered.registration_status='REGISTERED'
);

CREATE TEMP TABLE single_wallet_cutover_cancelled_cases (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    previous_state TEXT NOT NULL
) ON COMMIT DROP;

INSERT INTO single_wallet_cutover_cancelled_cases(id, user_id, previous_state)
SELECT verification.id, verification.user_id, verification.state
FROM kyc_verification_cases verification
JOIN wallets wallet
  ON wallet.id=verification.wallet_id
 AND wallet.user_id=verification.user_id
LEFT JOIN single_wallet_cutover_keep keep
  ON keep.user_id=verification.user_id
WHERE (keep.wallet_id IS NULL OR wallet.id<>keep.wallet_id)
  AND verification.state IN ('CREATED','PENDING_PROVIDER');

INSERT INTO single_wallet_kyc_cutover_audits(
    migration_version, pre_cutover_counts, post_cutover_counts, applied_at
)
SELECT
    '000032_single_wallet_kyc_model',
    jsonb_build_object(
        'registeredWallets', (
            SELECT COUNT(*) FROM wallets
            WHERE registration_status='REGISTERED'
        ),
        'usersWithRegisteredWallet', (
            SELECT COUNT(*) FROM single_wallet_cutover_keep
        ),
        'usersWithMultipleRegisteredWallets', (
            SELECT COUNT(*)
            FROM (
                SELECT user_id
                FROM wallets
                WHERE registration_status='REGISTERED'
                GROUP BY user_id
                HAVING COUNT(*)>1
            ) duplicate_owner
        ),
        'walletsToDeregister', (
            SELECT COUNT(*)
            FROM wallets wallet
            JOIN single_wallet_cutover_keep keep ON keep.user_id=wallet.user_id
            WHERE wallet.registration_status='REGISTERED'
              AND wallet.id<>keep.wallet_id
        ),
        'activeKycToCancel', (
            SELECT COUNT(*) FROM single_wallet_cutover_cancelled_cases
        ),
        'pendingAttemptsToClose', (
            SELECT COUNT(*)
            FROM wallet_registration_attempts attempt
            JOIN single_wallet_cutover_keep keep ON keep.user_id=attempt.user_id
            JOIN wallets current_wallet ON current_wallet.id=keep.wallet_id
            WHERE attempt.status='PENDING'
              AND (
                  attempt.chain_id<>current_wallet.chain_id
                  OR attempt.address_key<>current_wallet.address_key
              )
        )
    ),
    '{}'::jsonb,
    CURRENT_TIMESTAMP;

UPDATE kyc_provider_operations operation
SET state='FAILED',
    failure_code='WALLET_REPLACED_BY_SINGLE_WALLET_CUTOVER',
    retryable=FALSE,
    completed_at=COALESCE(operation.completed_at, CURRENT_TIMESTAMP),
    updated_at=CURRENT_TIMESTAMP
FROM single_wallet_cutover_cancelled_cases cancelled
WHERE operation.case_id=cancelled.id
  AND operation.user_id=cancelled.user_id
  AND operation.state IN ('RESERVED','UNAVAILABLE');

UPDATE kyc_verification_cases verification
SET state='CANCELLED',
    credential_id=NULL,
    failure_code='WALLET_REPLACED_BY_SINGLE_WALLET_CUTOVER',
    completed_at=CURRENT_TIMESTAMP,
    updated_at=CURRENT_TIMESTAMP
FROM single_wallet_cutover_cancelled_cases cancelled
WHERE verification.id=cancelled.id
  AND verification.user_id=cancelled.user_id;

INSERT INTO kyc_verification_audit_events(
    id, case_id, actor_kind, actor_user_id, action,
    previous_state, next_state, reason_code, idempotency_key,
    details, created_at
)
SELECT (
        substr(md5('single-wallet-cutover:' || cancelled.id::text),1,8) || '-' ||
        substr(md5('single-wallet-cutover:' || cancelled.id::text),9,4) || '-' ||
        '4' || substr(md5('single-wallet-cutover:' || cancelled.id::text),14,3) || '-' ||
        'a' || substr(md5('single-wallet-cutover:' || cancelled.id::text),18,3) || '-' ||
        substr(md5('single-wallet-cutover:' || cancelled.id::text),21,12)
    )::uuid,
    cancelled.id,
    'SYSTEM',
    NULL,
    'CANCELLED',
    cancelled.previous_state,
    'CANCELLED',
    'WALLET_REPLACED_BY_SINGLE_WALLET_CUTOVER',
    'single-wallet-cutover:' || cancelled.id::text,
    '{"reasonCode":"WALLET_REPLACED_BY_SINGLE_WALLET_CUTOVER"}'::jsonb,
    CURRENT_TIMESTAMP
FROM single_wallet_cutover_cancelled_cases cancelled
ON CONFLICT (idempotency_key) DO NOTHING;

UPDATE wallet_ownership_proofs proof
SET revoked_at=COALESCE(proof.revoked_at, CURRENT_TIMESTAMP)
FROM wallets wallet
JOIN single_wallet_cutover_keep keep ON keep.user_id=wallet.user_id
WHERE proof.wallet_id=wallet.id
  AND proof.user_id=wallet.user_id
  AND wallet.registration_status='REGISTERED'
  AND wallet.id<>keep.wallet_id;

UPDATE wallet_registration_attempts attempt
SET status=CASE
        WHEN attempt.expires_at<=CURRENT_TIMESTAMP THEN 'EXPIRED'
        ELSE 'SUPERSEDED'
    END,
    updated_at=CURRENT_TIMESTAMP
FROM single_wallet_cutover_keep keep
JOIN wallets current_wallet ON current_wallet.id=keep.wallet_id
WHERE attempt.user_id=keep.user_id
  AND attempt.status='PENDING'
  AND (
      attempt.chain_id<>current_wallet.chain_id
      OR attempt.address_key<>current_wallet.address_key
  );

UPDATE wallets wallet
SET registration_status='DEREGISTERED',
    current_ownership_proof_id=NULL,
    deregistered_at=CURRENT_TIMESTAMP,
    updated_at=CURRENT_TIMESTAMP
FROM single_wallet_cutover_keep keep
WHERE wallet.user_id=keep.user_id
  AND wallet.registration_status='REGISTERED'
  AND wallet.id<>keep.wallet_id;

DROP TABLE account_wallet_preferences;

CREATE UNIQUE INDEX idx_wallets_single_registered_user
    ON wallets(user_id)
    WHERE registration_status='REGISTERED';

DROP INDEX idx_kyc_cases_active_wallet;

CREATE UNIQUE INDEX idx_kyc_cases_single_active_user
    ON kyc_verification_cases(user_id)
    WHERE state IN ('CREATED','PENDING_PROVIDER');

UPDATE single_wallet_kyc_cutover_audits
SET post_cutover_counts=jsonb_build_object(
    'registeredWallets', (
        SELECT COUNT(*) FROM wallets
        WHERE registration_status='REGISTERED'
    ),
    'usersWithMultipleRegisteredWallets', (
        SELECT COUNT(*)
        FROM (
            SELECT user_id
            FROM wallets
            WHERE registration_status='REGISTERED'
            GROUP BY user_id
            HAVING COUNT(*)>1
        ) duplicate_owner
    ),
    'usersWithMultipleActiveKyc', (
        SELECT COUNT(*)
        FROM (
            SELECT user_id
            FROM kyc_verification_cases
            WHERE state IN ('CREATED','PENDING_PROVIDER')
            GROUP BY user_id
            HAVING COUNT(*)>1
        ) duplicate_owner
    ),
    'walletPreferenceTablePresent', (
        SELECT CASE
            WHEN to_regclass('public.account_wallet_preferences') IS NULL
            THEN 0 ELSE 1
        END
    )
)
WHERE migration_version='000032_single_wallet_kyc_model';
