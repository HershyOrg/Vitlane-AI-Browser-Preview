-- ADR-0025 hard cutover.
--
-- This migration is intentionally destructive for preview Wallet/KYC and the
-- Purchase/Settlement graph that depends on the legacy ownership assurance.
-- It must only run during an approved maintenance deployment after the
-- release runbook preflight and backup/restore rehearsal.

CREATE TABLE wallet_identity_cutover_audits (
    migration_version TEXT PRIMARY KEY,
    captured_at TIMESTAMPTZ NOT NULL,
    row_counts JSONB NOT NULL
);

INSERT INTO wallet_identity_cutover_audits(
    migration_version,
    captured_at,
    row_counts
)
VALUES (
    '000031_wallet_identity_kyc_redesign',
    NOW(),
    jsonb_build_object(
        'wallets', (SELECT COUNT(*) FROM wallets),
        'walletChallenges', (SELECT COUNT(*) FROM wallet_challenges),
        'identityAssurances', (SELECT COUNT(*) FROM identity_assurances),
        'kycCases', (SELECT COUNT(*) FROM kyc_verification_cases),
        'kycAuditEvents', (SELECT COUNT(*) FROM kyc_verification_audit_events),
        'purchases', (SELECT COUNT(*) FROM purchases),
        'checkoutQuotes', (SELECT COUNT(*) FROM checkout_quotes),
        'approvals', (SELECT COUNT(*) FROM user_approvals),
        'settlementAuthorizations', (SELECT COUNT(*) FROM settlement_authorizations),
        'settlementPayments', (SELECT COUNT(*) FROM settlement_payments),
        'settlementCommandOutbox', (SELECT COUNT(*) FROM settlement_command_outbox),
        'chainTransactions', (SELECT COUNT(*) FROM chain_transactions),
        'chainEvents', (SELECT COUNT(*) FROM chain_events),
        'fulfillmentExecutions', (SELECT COUNT(*) FROM fulfillment_executions),
        'fulfillmentRequests', (SELECT COUNT(*) FROM fulfillment_requests),
        'fulfillmentAttempts', (SELECT COUNT(*) FROM fulfillment_attempts),
        'fulfillmentResults', (SELECT COUNT(*) FROM fulfillment_results),
        'fulfillmentAssignments', (SELECT COUNT(*) FROM fulfillment_assignments),
        'fulfillmentAuditEvents', (SELECT COUNT(*) FROM fulfillment_audit_events),
        'piiAccessAuditEvents', (SELECT COUNT(*) FROM pii_access_audit_events),
        'merchantShipmentEvents', (SELECT COUNT(*) FROM merchant_shipment_events),
        'purchaseIntentSnapshots', (SELECT COUNT(*) FROM purchase_intent_snapshots),
        'receipts', (SELECT COUNT(*) FROM receipts),
        'purchaseShippingSnapshots', (
            SELECT COUNT(DISTINCT snapshot.id)
            FROM shipping_snapshots snapshot
            JOIN purchases purchase
              ON purchase.shipping_snapshot_id=snapshot.id
        )
    )
);

-- An Approval cannot survive without its exact ownership evidence. Reset the
-- whole preview Purchase graph instead of leaving USER_APPROVED orphan rows.
CREATE TEMP TABLE wallet_identity_cutover_shipping_snapshots
ON COMMIT DROP
AS
SELECT DISTINCT shipping_snapshot_id AS id
FROM purchases
WHERE shipping_snapshot_id IS NOT NULL;

TRUNCATE TABLE purchases, chain_events CASCADE;

DELETE FROM shipping_snapshots
WHERE id IN (
    SELECT id FROM wallet_identity_cutover_shipping_snapshots
);

DROP TABLE kyc_verification_audit_events;
DROP TABLE kyc_verification_cases;

ALTER TABLE user_approvals
    DROP CONSTRAINT IF EXISTS user_approvals_wallet_id_fkey,
    DROP CONSTRAINT IF EXISTS user_approvals_assurance_id_fkey,
    DROP COLUMN assurance_id;

DROP TABLE identity_assurances;
DROP TABLE wallet_challenges;
DROP TABLE wallets;

CREATE TABLE wallets (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    address TEXT NOT NULL,
    address_key BYTEA NOT NULL CHECK (octet_length(address_key) = 20),
    account_id TEXT NOT NULL,
    chain_id TEXT NOT NULL CHECK (chain_id ~ '^eip155:[1-9][0-9]*$'),
    registration_status TEXT NOT NULL CHECK (
        registration_status IN ('REGISTERED', 'DEREGISTERED')
    ),
    current_ownership_proof_id UUID,
    registered_at TIMESTAMPTZ NOT NULL,
    deregistered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (id, user_id),
    UNIQUE (id, user_id, account_id),
    UNIQUE (id, user_id, chain_id, address_key, account_id),
    UNIQUE (user_id, chain_id, address_key),
    UNIQUE (user_id, account_id),
    CHECK (address ~ '^0x[0-9A-Fa-f]{40}$'),
    CHECK (address_key = decode(substr(lower(address), 3), 'hex')),
    CHECK (account_id = chain_id || ':' || address),
    CHECK (
        (
            registration_status = 'REGISTERED'
            AND current_ownership_proof_id IS NOT NULL
            AND deregistered_at IS NULL
        )
        OR
        (
            registration_status = 'DEREGISTERED'
            AND deregistered_at IS NOT NULL
            AND current_ownership_proof_id IS NULL
        )
    )
);

CREATE TABLE wallet_registration_attempts (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    address TEXT NOT NULL,
    address_key BYTEA NOT NULL CHECK (octet_length(address_key) = 20),
    account_id TEXT NOT NULL,
    chain_id TEXT NOT NULL CHECK (chain_id ~ '^eip155:[1-9][0-9]*$'),
    origin TEXT NOT NULL,
    nonce TEXT NOT NULL,
    nonce_hash BYTEA NOT NULL UNIQUE,
    message TEXT NOT NULL,
    message_hash BYTEA NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN (
            'PENDING', 'COMPLETED', 'EXPIRED',
            'LOCKED', 'CANCELLED', 'SUPERSEDED'
        )
    ),
    failure_count INTEGER NOT NULL DEFAULT 0 CHECK (
        failure_count BETWEEN 0 AND 5
    ),
    client_operation_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    completion_operation_id TEXT,
    completion_request_hash TEXT,
    wallet_id UUID,
    ownership_proof_id UUID,
    expires_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (id, user_id, chain_id, address_key, account_id),
    UNIQUE (user_id, client_operation_id),
    UNIQUE (user_id, completion_operation_id),
    CHECK (address ~ '^0x[0-9A-Fa-f]{40}$'),
    CHECK (address_key = decode(substr(lower(address), 3), 'hex')),
    CHECK (account_id = chain_id || ':' || address),
    CHECK (
        (
            status = 'COMPLETED'
            AND completion_operation_id IS NOT NULL
            AND completion_request_hash IS NOT NULL
            AND wallet_id IS NOT NULL
            AND ownership_proof_id IS NOT NULL
            AND completed_at IS NOT NULL
        )
        OR
        (
            status <> 'COMPLETED'
            AND wallet_id IS NULL
            AND ownership_proof_id IS NULL
            AND completed_at IS NULL
        )
    )
);

CREATE UNIQUE INDEX idx_wallet_registration_attempts_pending_account
    ON wallet_registration_attempts(user_id, chain_id, address_key)
    WHERE status = 'PENDING';

CREATE INDEX idx_wallet_registration_attempts_expiry
    ON wallet_registration_attempts(status, expires_at)
    WHERE status = 'PENDING';

CREATE TABLE wallet_ownership_proofs (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wallet_id UUID NOT NULL,
    registration_attempt_id UUID NOT NULL,
    address TEXT NOT NULL,
    address_key BYTEA NOT NULL CHECK (octet_length(address_key) = 20),
    account_id TEXT NOT NULL,
    chain_id TEXT NOT NULL CHECK (chain_id ~ '^eip155:[1-9][0-9]*$'),
    origin TEXT NOT NULL,
    method TEXT NOT NULL CHECK (method = 'EIP191_PERSONAL_SIGN'),
    message_hash TEXT NOT NULL,
    verified_at TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    UNIQUE (id, user_id, wallet_id),
    UNIQUE (id, user_id, wallet_id, registration_attempt_id),
    UNIQUE (
        id, user_id, wallet_id, address, account_id, chain_id, method,
        message_hash, verified_at, valid_until
    ),
    UNIQUE (registration_attempt_id),
    FOREIGN KEY (wallet_id, user_id, chain_id, address_key, account_id)
        REFERENCES wallets(id, user_id, chain_id, address_key, account_id)
        ON DELETE CASCADE,
    FOREIGN KEY (
        registration_attempt_id, user_id, chain_id, address_key, account_id
    )
        REFERENCES wallet_registration_attempts(
            id, user_id, chain_id, address_key, account_id
        )
        ON DELETE RESTRICT,
    CHECK (address ~ '^0x[0-9A-Fa-f]{40}$'),
    CHECK (address_key = decode(substr(lower(address), 3), 'hex')),
    CHECK (account_id = chain_id || ':' || address),
    CHECK (valid_until = verified_at + INTERVAL '24 hours'),
    CHECK (revoked_at IS NULL OR revoked_at >= verified_at)
);

ALTER TABLE wallets
    ADD CONSTRAINT wallets_current_ownership_proof_fkey
    FOREIGN KEY (current_ownership_proof_id, user_id, id)
    REFERENCES wallet_ownership_proofs(id, user_id, wallet_id)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE wallet_registration_attempts
    ADD CONSTRAINT wallet_registration_attempts_result_wallet_fkey
        FOREIGN KEY (wallet_id, user_id)
        REFERENCES wallets(id, user_id)
        DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT wallet_registration_attempts_result_proof_fkey
        FOREIGN KEY (ownership_proof_id, user_id, wallet_id, id)
        REFERENCES wallet_ownership_proofs(
            id, user_id, wallet_id, registration_attempt_id
        )
        DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE account_wallet_preferences (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    default_wallet_id UUID,
    updated_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (default_wallet_id, user_id)
        REFERENCES wallets(id, user_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE kyc_verification_cases (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wallet_id UUID NOT NULL,
    started_with_proof_id UUID NOT NULL,
    requested_level TEXT NOT NULL CHECK (requested_level = 'ADVANCED_TEST_KYC'),
    provider_kind TEXT NOT NULL CHECK (
        provider_kind IN ('MOCK_DOJANG', 'DOJANG')
    ),
    provider_version TEXT NOT NULL CHECK (length(provider_version) BETWEEN 1 AND 200),
    external_effect TEXT NOT NULL CHECK (
        external_effect IN ('SIMULATED', 'LIVE')
    ),
    state TEXT NOT NULL CHECK (
        state IN (
            'CREATED', 'PENDING_PROVIDER', 'VERIFIED',
            'REJECTED', 'EXPIRED', 'CANCELLED'
        )
    ),
    provider_case_ref TEXT,
    credential_id UUID,
    failure_code TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE (id, user_id, wallet_id),
    FOREIGN KEY (wallet_id, user_id)
        REFERENCES wallets(id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (started_with_proof_id, user_id, wallet_id)
        REFERENCES wallet_ownership_proofs(id, user_id, wallet_id) ON DELETE RESTRICT,
    CHECK (
        (provider_kind = 'MOCK_DOJANG' AND external_effect = 'SIMULATED')
        OR provider_kind = 'DOJANG'
    ),
    CHECK (
        (state = 'CREATED' AND provider_case_ref IS NULL)
        OR (
            state IN (
                'PENDING_PROVIDER', 'VERIFIED', 'REJECTED', 'EXPIRED'
            )
            AND provider_case_ref IS NOT NULL
        )
        OR state = 'CANCELLED'
    ),
    CHECK (
        (
            state = 'CREATED'
            AND credential_id IS NULL
            AND failure_code IS NULL
            AND completed_at IS NULL
        )
        OR
        (
            state = 'PENDING_PROVIDER'
            AND credential_id IS NULL
            AND failure_code IS NULL
            AND completed_at IS NULL
        )
        OR
        (
            state = 'VERIFIED'
            AND credential_id IS NOT NULL
            AND failure_code IS NULL
            AND completed_at IS NOT NULL
        )
        OR
        (
            state IN ('REJECTED', 'EXPIRED', 'CANCELLED')
            AND credential_id IS NULL
            AND failure_code IS NOT NULL
            AND completed_at IS NOT NULL
        )
    )
);

CREATE UNIQUE INDEX idx_kyc_cases_provider_reference
    ON kyc_verification_cases(provider_kind, provider_case_ref)
    WHERE provider_case_ref IS NOT NULL;

CREATE UNIQUE INDEX idx_kyc_cases_active_wallet
    ON kyc_verification_cases(wallet_id)
    WHERE state IN ('CREATED', 'PENDING_PROVIDER');

CREATE INDEX idx_kyc_cases_user_time
    ON kyc_verification_cases(user_id, created_at DESC);

CREATE TABLE kyc_provider_operations (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wallet_id UUID NOT NULL,
    case_id UUID NOT NULL,
    operation_kind TEXT NOT NULL CHECK (
        operation_kind IN ('START', 'CHECK', 'CANCEL')
    ),
    client_operation_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    provider_request_key TEXT NOT NULL,
    state TEXT NOT NULL CHECK (
        state IN ('RESERVED', 'SUCCEEDED', 'UNAVAILABLE', 'FAILED')
    ),
    provider_case_ref TEXT,
    result_case_state TEXT CHECK (
        result_case_state IS NULL OR result_case_state IN (
            'PENDING_PROVIDER', 'VERIFIED', 'REJECTED', 'EXPIRED', 'CANCELLED'
        )
    ),
    result_credential_id UUID,
    failure_code TEXT,
    retryable BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE (id, user_id, case_id),
    UNIQUE (id, user_id, operation_kind),
    UNIQUE (user_id, operation_kind, client_operation_id),
    UNIQUE (provider_request_key),
    FOREIGN KEY (case_id, user_id, wallet_id)
        REFERENCES kyc_verification_cases(id, user_id, wallet_id)
        ON DELETE CASCADE,
    CHECK (
        (state = 'RESERVED' AND completed_at IS NULL)
        OR (state <> 'RESERVED' AND completed_at IS NOT NULL)
    ),
    CHECK (
        state <> 'SUCCEEDED'
        OR (result_case_state IS NOT NULL AND failure_code IS NULL)
    ),
    CHECK (
        state NOT IN ('UNAVAILABLE', 'FAILED')
        OR failure_code IS NOT NULL
    ),
    CHECK (
        (
            state = 'RESERVED'
            AND provider_case_ref IS NULL
            AND result_case_state IS NULL
            AND result_credential_id IS NULL
            AND failure_code IS NULL
            AND retryable
        )
        OR
        (
            state = 'SUCCEEDED'
            AND result_case_state IS NOT NULL
            AND failure_code IS NULL
            AND NOT retryable
            AND (
                (
                    result_case_state = 'VERIFIED'
                    AND result_credential_id IS NOT NULL
                )
                OR
                (
                    result_case_state <> 'VERIFIED'
                    AND result_credential_id IS NULL
                )
            )
        )
        OR
        (
            state IN ('UNAVAILABLE', 'FAILED')
            AND result_case_state IS NULL
            AND result_credential_id IS NULL
            AND failure_code IS NOT NULL
        )
    )
);

CREATE INDEX idx_kyc_provider_operations_recovery
    ON kyc_provider_operations(state, updated_at)
    WHERE state IN ('RESERVED', 'UNAVAILABLE');

CREATE UNIQUE INDEX idx_kyc_provider_operations_active_case_kind
    ON kyc_provider_operations(case_id, operation_kind)
    WHERE state IN ('RESERVED', 'UNAVAILABLE');

CREATE TABLE kyc_provider_operation_aliases (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    operation_kind TEXT NOT NULL CHECK (
        operation_kind IN ('START', 'CHECK', 'CANCEL')
    ),
    client_operation_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    operation_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, operation_kind, client_operation_id),
    FOREIGN KEY (operation_id, user_id, operation_kind)
        REFERENCES kyc_provider_operations(id, user_id, operation_kind)
        ON DELETE CASCADE
);

CREATE TABLE kyc_credentials (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wallet_id UUID NOT NULL,
    case_id UUID NOT NULL UNIQUE,
    level TEXT NOT NULL CHECK (
        level IN ('MOCK_DOJANG_VERIFIED', 'DOJANG_VERIFIED_ADDRESS')
    ),
    provider_kind TEXT NOT NULL CHECK (
        provider_kind IN ('MOCK_DOJANG', 'DOJANG')
    ),
    provider_version TEXT NOT NULL CHECK (length(provider_version) BETWEEN 1 AND 200),
    external_effect TEXT NOT NULL CHECK (
        external_effect IN ('SIMULATED', 'LIVE')
    ),
    subject_account_id TEXT NOT NULL,
    issuer_ref TEXT NOT NULL,
    schema_ref TEXT NOT NULL,
    evidence_hash TEXT NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (id, user_id, wallet_id),
    UNIQUE (id, user_id, wallet_id, case_id),
    FOREIGN KEY (wallet_id, user_id, subject_account_id)
        REFERENCES wallets(id, user_id, account_id) ON DELETE RESTRICT,
    FOREIGN KEY (case_id, user_id, wallet_id)
        REFERENCES kyc_verification_cases(id, user_id, wallet_id) ON DELETE RESTRICT,
    CHECK (valid_until > issued_at),
    CHECK (
        (
            provider_kind = 'MOCK_DOJANG'
            AND level = 'MOCK_DOJANG_VERIFIED'
            AND external_effect = 'SIMULATED'
        )
        OR
        (
            provider_kind = 'DOJANG'
            AND level = 'DOJANG_VERIFIED_ADDRESS'
        )
    )
);

ALTER TABLE kyc_verification_cases
    ADD CONSTRAINT kyc_cases_credential_fkey
    FOREIGN KEY (credential_id, user_id, wallet_id, id)
    REFERENCES kyc_credentials(id, user_id, wallet_id, case_id)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE kyc_provider_operations
    ADD CONSTRAINT kyc_provider_operations_result_credential_fkey
    FOREIGN KEY (result_credential_id, user_id, wallet_id, case_id)
    REFERENCES kyc_credentials(id, user_id, wallet_id, case_id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE kyc_evidence_observations (
    id UUID PRIMARY KEY,
    credential_id UUID NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wallet_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('VALID', 'EXPIRED', 'REVOKED')),
    evidence_hash TEXT NOT NULL,
    source_version BIGINT NOT NULL CHECK (source_version > 0),
    observed_at TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL,
    recheck_after TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (credential_id, user_id, wallet_id)
        REFERENCES kyc_credentials(id, user_id, wallet_id) ON DELETE CASCADE,
    UNIQUE (credential_id, source_version),
    CHECK (valid_until >= observed_at),
    CHECK (recheck_after >= observed_at)
);

CREATE TABLE kyc_verification_audit_events (
    id UUID PRIMARY KEY,
    case_id UUID NOT NULL REFERENCES kyc_verification_cases(id) ON DELETE CASCADE,
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('USER', 'PROVIDER', 'SYSTEM')),
    actor_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (
        action IN (
            'CASE_CREATED', 'OPERATION_RESERVED', 'PROVIDER_STARTED',
            'PROVIDER_UNAVAILABLE', 'PROVIDER_FAILED',
            'VERIFICATION_CHECKED',
            'EVIDENCE_OBSERVED', 'VERIFIED', 'REJECTED',
            'EXPIRED', 'CANCELLED'
        )
    ),
    previous_state TEXT,
    next_state TEXT NOT NULL,
    reason_code TEXT,
    idempotency_key TEXT NOT NULL UNIQUE,
    details JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (
        (actor_kind = 'USER' AND actor_user_id IS NOT NULL)
        OR actor_kind <> 'USER'
    )
);

CREATE INDEX idx_kyc_audit_case_time
    ON kyc_verification_audit_events(case_id, created_at);

ALTER TABLE user_approvals
    ADD COLUMN wallet_ownership_proof_id UUID NOT NULL,
    ADD COLUMN payer_account_id TEXT NOT NULL,
    ADD COLUMN payer_address TEXT NOT NULL,
    ADD COLUMN payer_chain_id TEXT NOT NULL,
    ADD COLUMN ownership_proof_method TEXT NOT NULL,
    ADD COLUMN ownership_message_hash TEXT NOT NULL,
    ADD COLUMN ownership_verified_at TIMESTAMPTZ NOT NULL,
    ADD COLUMN ownership_valid_until TIMESTAMPTZ NOT NULL,
    ADD CONSTRAINT user_approvals_wallet_owner_fkey
        FOREIGN KEY (wallet_id, user_id)
        REFERENCES wallets(id, user_id) ON DELETE RESTRICT,
    ADD CONSTRAINT user_approvals_ownership_proof_fkey
        FOREIGN KEY (
            wallet_ownership_proof_id, user_id, wallet_id, payer_address,
            payer_account_id, payer_chain_id, ownership_proof_method,
            ownership_message_hash, ownership_verified_at,
            ownership_valid_until
        )
        REFERENCES wallet_ownership_proofs(
            id, user_id, wallet_id, address, account_id, chain_id, method,
            message_hash, verified_at, valid_until
        ) ON DELETE RESTRICT,
    ADD CONSTRAINT user_approvals_ownership_window
        CHECK (
            ownership_verified_at <= approved_at
            AND approved_at < ownership_valid_until
        ),
    ADD CONSTRAINT user_approvals_payer_account
        CHECK (payer_account_id = payer_chain_id || ':' || payer_address);

CREATE INDEX idx_wallet_ownership_proofs_wallet_validity
    ON wallet_ownership_proofs(wallet_id, valid_until DESC);

CREATE INDEX idx_kyc_observations_credential_version
    ON kyc_evidence_observations(credential_id, source_version DESC);
