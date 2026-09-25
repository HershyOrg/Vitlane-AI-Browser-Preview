ALTER TABLE wallets
    ADD COLUMN is_default BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN disconnected_at TIMESTAMPTZ;

WITH ranked AS (
    SELECT id, row_number() OVER (
        PARTITION BY user_id
        ORDER BY (verified_at IS NOT NULL) DESC, created_at, id
    ) AS position
    FROM wallets
)
UPDATE wallets wallet
SET is_default = TRUE
FROM ranked
WHERE ranked.id = wallet.id AND ranked.position = 1;

CREATE UNIQUE INDEX idx_wallets_one_active_default
    ON wallets(user_id)
    WHERE is_default AND disconnected_at IS NULL;

ALTER TABLE identity_assurances
    DROP CONSTRAINT IF EXISTS identity_assurances_level_check;

ALTER TABLE identity_assurances
    ADD CONSTRAINT identity_assurances_level_check
    CHECK (level IN (
        'DOJANG_VERIFIED_ADDRESS',
        'DOJANG_TEST_FAUCET',
        'MOCK_KYC',
        'WALLET_OWNERSHIP_ONLY'
    ));

CREATE TABLE buyer_profiles (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    profile_kind TEXT NOT NULL CHECK (profile_kind = 'TEST_PROFILE'),
    label TEXT NOT NULL,
    fixture_key TEXT NOT NULL,
    country CHAR(2) NOT NULL,
    city TEXT NOT NULL,
    profile_version BIGINT NOT NULL CHECK (profile_version > 0),
    snapshot_hash TEXT NOT NULL,
    contains_real_pii BOOLEAN NOT NULL DEFAULT FALSE
        CHECK (contains_real_pii = FALSE),
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    retired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, fixture_key, profile_version)
);

CREATE UNIQUE INDEX idx_buyer_profiles_one_active_default
    ON buyer_profiles(user_id)
    WHERE is_default AND retired_at IS NULL;

CREATE TABLE user_policy_acceptances (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    policy_id TEXT NOT NULL,
    policy_version TEXT NOT NULL,
    accepted_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, policy_id, policy_version)
);

ALTER TABLE purchases
    ADD COLUMN buyer_profile_id UUID REFERENCES buyer_profiles(id) ON DELETE RESTRICT,
    ADD COLUMN buyer_profile_version BIGINT,
    ADD COLUMN buyer_profile_snapshot_hash TEXT;

ALTER TABLE checkout_quotes
    ADD COLUMN fulfillment_spec JSONB,
    ADD COLUMN fulfillment_spec_hash TEXT;

CREATE TABLE fulfillment_requests (
    id UUID PRIMARY KEY,
    settlement_payment_id UUID NOT NULL UNIQUE
        REFERENCES settlement_payments(id) ON DELETE CASCADE,
    purchase_id UUID NOT NULL REFERENCES purchases(id) ON DELETE RESTRICT,
    quote_id UUID NOT NULL REFERENCES checkout_quotes(id) ON DELETE RESTRICT,
    schema_version TEXT NOT NULL CHECK (schema_version = 'vitlane.fulfillment-request.v1'),
    fulfillment_spec_hash TEXT NOT NULL,
    request_hash TEXT NOT NULL UNIQUE,
    payload JSONB NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('REQUESTED', 'PROCESSING', 'TERMINAL')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

INSERT INTO fulfillment_requests(
    id, settlement_payment_id, purchase_id, quote_id, schema_version,
    fulfillment_spec_hash, request_hash, payload, state, created_at, updated_at
)
SELECT
    fe.id, fe.settlement_payment_id, sp.purchase_id, approval.quote_id,
    'vitlane.fulfillment-request.v1',
    COALESCE(quote.fulfillment_spec_hash, fe.quote_hash, 'legacy:' || fe.id::text),
    'legacy:' || fe.id::text,
    jsonb_build_object(
        'schemaVersion', 'vitlane.fulfillment-request.v1',
        'settlementPaymentId', fe.settlement_payment_id::text,
        'purchaseId', sp.purchase_id::text,
        'quoteId', approval.quote_id::text,
        'orderHash', sp.order_hash,
        'payer', sp.payer,
        'amountBaseUnits', sp.amount_base_units::text,
        'finalizedBlock', COALESCE(sp.finalized_block,0),
        'fulfillmentSpecHash',
            COALESCE(quote.fulfillment_spec_hash, fe.quote_hash, 'legacy:' || fe.id::text),
        'fulfillmentSpec', COALESCE(quote.fulfillment_spec, '{}'::jsonb)
    ),
    CASE
        WHEN fe.state IN ('PURCHASE_ASSUMED','FAILED') THEN 'TERMINAL'
        ELSE 'REQUESTED'
    END,
    fe.created_at, fe.updated_at
FROM fulfillment_executions fe
JOIN settlement_payments sp ON sp.id=fe.settlement_payment_id
JOIN LATERAL (
    SELECT ua.quote_id
    FROM user_approvals ua
    WHERE ua.purchase_id=sp.purchase_id
    ORDER BY ua.approved_at DESC
    LIMIT 1
) approval ON TRUE
JOIN checkout_quotes quote ON quote.id=approval.quote_id;

CREATE TABLE fulfillment_attempts (
    id UUID PRIMARY KEY,
    request_id UUID NOT NULL REFERENCES fulfillment_requests(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    adapter_kind TEXT NOT NULL CHECK (adapter_kind IN (
        'TEST_NO_MERCHANT_ORDER', 'MANUAL_OPERATOR'
    )),
    processing_mode TEXT NOT NULL CHECK (processing_mode IN (
        'MANUAL_OPERATOR', 'AUTO_TEST'
    )),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('SYSTEM', 'USER')),
    actor_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL CHECK (state IN (
        'STARTED', 'SUCCEEDED', 'FAILED', 'UNKNOWN_NEEDS_RECONCILIATION'
    )),
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE (request_id, attempt_number),
    CHECK (
        (actor_kind='SYSTEM' AND actor_user_id IS NULL)
        OR (actor_kind='USER' AND actor_user_id IS NOT NULL)
    )
);

CREATE TABLE fulfillment_results (
    id UUID PRIMARY KEY,
    request_id UUID NOT NULL UNIQUE REFERENCES fulfillment_requests(id) ON DELETE CASCADE,
    attempt_id UUID NOT NULL UNIQUE REFERENCES fulfillment_attempts(id) ON DELETE RESTRICT,
    schema_version TEXT NOT NULL CHECK (schema_version = 'vitlane.fulfillment-result.v1'),
    outcome TEXT NOT NULL CHECK (outcome IN ('PURCHASE_ASSUMED', 'FAILED')),
    result_kind TEXT NOT NULL CHECK (result_kind = 'TEST_NO_MERCHANT_ORDER'),
    merchant_order_created BOOLEAN NOT NULL CHECK (merchant_order_created = FALSE),
    external_order_reference TEXT CHECK (external_order_reference IS NULL),
    failure_code TEXT,
    result_hash TEXT NOT NULL UNIQUE,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE fulfillment_executions
    ADD COLUMN request_id UUID REFERENCES fulfillment_requests(id) ON DELETE RESTRICT;

UPDATE fulfillment_executions execution
SET request_id=request.id
FROM fulfillment_requests request
WHERE request.settlement_payment_id=execution.settlement_payment_id;

CREATE UNIQUE INDEX idx_fulfillment_executions_request
    ON fulfillment_executions(request_id)
    WHERE request_id IS NOT NULL;

CREATE INDEX idx_fulfillment_attempts_request_time
    ON fulfillment_attempts(request_id, started_at DESC);
