CREATE TABLE shipping_profiles (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label TEXT NOT NULL CHECK (char_length(label) BETWEEN 1 AND 80),
    country CHAR(2) NOT NULL,
    masked_summary TEXT NOT NULL,
    encrypted_payload BYTEA NOT NULL,
    payload_nonce BYTEA NOT NULL,
    key_version TEXT NOT NULL,
    payload_hmac TEXT NOT NULL,
    profile_version BIGINT NOT NULL CHECK (profile_version > 0),
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    retired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, profile_version)
);

CREATE UNIQUE INDEX idx_shipping_profiles_one_active_default
    ON shipping_profiles(user_id)
    WHERE is_default AND retired_at IS NULL;

CREATE TABLE shipping_snapshots (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    source_profile_id UUID NOT NULL REFERENCES shipping_profiles(id) ON DELETE RESTRICT,
    profile_version BIGINT NOT NULL CHECK (profile_version > 0),
    country CHAR(2) NOT NULL,
    masked_summary TEXT NOT NULL,
    encrypted_payload BYTEA,
    payload_nonce BYTEA,
    key_version TEXT NOT NULL,
    snapshot_hmac TEXT NOT NULL,
    purge_after TIMESTAMPTZ,
    purged_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (
        (purged_at IS NULL AND encrypted_payload IS NOT NULL AND payload_nonce IS NOT NULL)
        OR (purged_at IS NOT NULL AND encrypted_payload IS NULL AND payload_nonce IS NULL)
    )
);

CREATE INDEX idx_shipping_snapshots_source
    ON shipping_snapshots(source_profile_id, profile_version, created_at DESC);

ALTER TABLE purchases
    ADD COLUMN shipping_snapshot_id UUID REFERENCES shipping_snapshots(id) ON DELETE RESTRICT,
    ADD COLUMN shipping_profile_id UUID REFERENCES shipping_profiles(id) ON DELETE RESTRICT,
    ADD COLUMN shipping_profile_version BIGINT,
    ADD COLUMN shipping_country CHAR(2),
    ADD COLUMN shipping_masked_summary TEXT,
    ADD COLUMN shipping_snapshot_hmac TEXT;

ALTER TABLE merchant_registry_entries
    DROP CONSTRAINT merchant_registry_entries_fulfillment_mode_check;

UPDATE merchant_registry_entries
SET fulfillment_mode='MANUAL_MERCHANT_ORDER'
WHERE fulfillment_mode='TEST_PURCHASE_ASSUMPTION';

ALTER TABLE merchant_registry_entries
    ADD CONSTRAINT merchant_registry_entries_fulfillment_mode_check
    CHECK (fulfillment_mode = 'MANUAL_MERCHANT_ORDER');

ALTER TABLE fulfillment_executions
    DROP CONSTRAINT fulfillment_executions_mode_check,
    DROP CONSTRAINT fulfillment_executions_state_check,
    DROP CONSTRAINT fulfillment_executions_no_merchant_order_check;

UPDATE fulfillment_executions
SET mode='MANUAL_MERCHANT_ORDER',
    state=CASE WHEN state='PURCHASE_ASSUMED' THEN 'ORDER_ACCEPTED' ELSE state END;

ALTER TABLE fulfillment_executions
    ADD COLUMN external_effect TEXT NOT NULL DEFAULT 'SIMULATED'
        CHECK (external_effect IN ('SIMULATED', 'LIVE')),
    ADD COLUMN adapter_kind TEXT NOT NULL DEFAULT 'MOCK_MERCHANT_ORDER'
        CHECK (adapter_kind IN ('MOCK_MERCHANT_ORDER', 'LIVE_MERCHANT_ORDER')),
    ADD COLUMN merchant_order_flow_completed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN mock_order_reference TEXT;

UPDATE fulfillment_executions
SET merchant_order_flow_completed=(state='ORDER_ACCEPTED'),
    mock_order_reference=CASE
        WHEN state='ORDER_ACCEPTED' THEN 'legacy-mock-' || id::text
        ELSE NULL
    END;

ALTER TABLE fulfillment_executions
    ADD CONSTRAINT fulfillment_executions_mode_check
        CHECK (mode = 'MANUAL_MERCHANT_ORDER'),
    ADD CONSTRAINT fulfillment_executions_state_check
        CHECK (state IN (
            'PENDING', 'RUNNING', 'ORDER_ACCEPTED', 'FAILED',
            'UNKNOWN_NEEDS_RECONCILIATION'
        )),
    ADD CONSTRAINT fulfillment_executions_simulated_effect_check
        CHECK (
            external_effect <> 'SIMULATED'
            OR (
                merchant_order_created = FALSE
                AND external_order_reference IS NULL
            )
        ),
    ADD CONSTRAINT fulfillment_executions_mock_result_check
        CHECK (
            state <> 'ORDER_ACCEPTED'
            OR (
                merchant_order_flow_completed = TRUE
                AND mock_order_reference IS NOT NULL
            )
        );

ALTER TABLE fulfillment_attempts
    DROP CONSTRAINT fulfillment_attempts_adapter_kind_check;

UPDATE fulfillment_attempts
SET adapter_kind='MOCK_MERCHANT_ORDER'
WHERE adapter_kind IN ('TEST_NO_MERCHANT_ORDER', 'MANUAL_OPERATOR');

ALTER TABLE fulfillment_attempts
    ADD CONSTRAINT fulfillment_attempts_adapter_kind_check
    CHECK (adapter_kind IN ('MOCK_MERCHANT_ORDER', 'LIVE_MERCHANT_ORDER'));

ALTER TABLE fulfillment_results
    DROP CONSTRAINT fulfillment_results_outcome_check,
    DROP CONSTRAINT fulfillment_results_result_kind_check,
    DROP CONSTRAINT fulfillment_results_merchant_order_created_check,
    DROP CONSTRAINT fulfillment_results_external_order_reference_check;

UPDATE fulfillment_results
SET outcome=CASE WHEN outcome='PURCHASE_ASSUMED' THEN 'ORDER_ACCEPTED' ELSE outcome END,
    result_kind='MOCK_MERCHANT_ORDER';

ALTER TABLE fulfillment_results
    ADD COLUMN external_effect TEXT NOT NULL DEFAULT 'SIMULATED'
        CHECK (external_effect IN ('SIMULATED', 'LIVE')),
    ADD COLUMN merchant_order_flow_completed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN mock_order_reference TEXT;

UPDATE fulfillment_results
SET merchant_order_flow_completed=(outcome='ORDER_ACCEPTED'),
    mock_order_reference=CASE
        WHEN outcome='ORDER_ACCEPTED' THEN 'legacy-mock-' || id::text
        ELSE NULL
    END;

ALTER TABLE fulfillment_results
    ADD CONSTRAINT fulfillment_results_outcome_check
        CHECK (outcome IN ('ORDER_ACCEPTED', 'FAILED', 'UNKNOWN_NEEDS_RECONCILIATION')),
    ADD CONSTRAINT fulfillment_results_result_kind_check
        CHECK (result_kind IN ('MOCK_MERCHANT_ORDER', 'LIVE_MERCHANT_ORDER')),
    ADD CONSTRAINT fulfillment_results_effect_check
        CHECK (
            external_effect <> 'SIMULATED'
            OR (
                merchant_order_created = FALSE
                AND external_order_reference IS NULL
            )
        ),
    ADD CONSTRAINT fulfillment_results_mock_result_check
        CHECK (
            outcome <> 'ORDER_ACCEPTED'
            OR (
                merchant_order_flow_completed = TRUE
                AND mock_order_reference IS NOT NULL
            )
        );

ALTER TABLE fulfillment_audit_events
    DROP CONSTRAINT fulfillment_audit_events_action_check;

UPDATE fulfillment_audit_events
SET action='MERCHANT_ORDER_ACCEPTED'
WHERE action='PURCHASE_ASSUMED';

ALTER TABLE fulfillment_audit_events
    ADD CONSTRAINT fulfillment_audit_events_action_check
    CHECK (action IN (
        'AUTOMATION_CHANGED',
        'OPERATOR_ASSIGNED',
        'MERCHANT_ORDER_ACCEPTED',
        'MERCHANT_ORDER_UNKNOWN',
        'FULFILLMENT_FAILED'
    ));

CREATE TABLE fulfillment_assignments (
    fulfillment_request_id UUID PRIMARY KEY
        REFERENCES fulfillment_requests(id) ON DELETE CASCADE,
    operator_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN ('ACTIVE', 'RELEASED')),
    assigned_at TIMESTAMPTZ NOT NULL,
    released_at TIMESTAMPTZ
);

CREATE INDEX idx_fulfillment_assignments_operator
    ON fulfillment_assignments(operator_user_id, assigned_at DESC)
    WHERE state='ACTIVE';

UPDATE fulfillment_operator_settings
SET automation_mode='MANUAL_OPERATOR',
    version=version+1,
    updated_at=NOW()
WHERE automation_mode='AUTO_TEST';

ALTER TABLE fulfillment_operator_settings
    DROP CONSTRAINT fulfillment_operator_settings_automation_mode_check;

ALTER TABLE fulfillment_operator_settings
    ADD CONSTRAINT fulfillment_operator_settings_automation_mode_check
    CHECK (automation_mode = 'MANUAL_OPERATOR');

CREATE TABLE pii_access_audit_events (
    id UUID PRIMARY KEY,
    purchase_id UUID NOT NULL REFERENCES purchases(id) ON DELETE RESTRICT,
    shipping_snapshot_id UUID REFERENCES shipping_snapshots(id) ON DELETE RESTRICT,
    actor_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (action = 'SHIPPING_ADDRESS_REVEAL'),
    reason_code TEXT NOT NULL CHECK (reason_code IN (
        'PLACE_MERCHANT_ORDER', 'VERIFY_MERCHANT_ORDER', 'CUSTOMER_SUPPORT'
    )),
    reason_detail TEXT NOT NULL CHECK (char_length(reason_detail) BETWEEN 8 AND 500),
    outcome TEXT NOT NULL CHECK (outcome IN ('GRANTED', 'DENIED')),
    denial_code TEXT,
    correlation_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    previous_event_hash TEXT,
    event_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_pii_access_audit_purchase_time
    ON pii_access_audit_events(purchase_id, created_at DESC);
CREATE INDEX idx_pii_access_audit_actor_time
    ON pii_access_audit_events(actor_user_id, created_at DESC);

CREATE TABLE merchant_shipment_events (
    id UUID PRIMARY KEY,
    fulfillment_request_id UUID NOT NULL
        REFERENCES fulfillment_requests(id) ON DELETE CASCADE,
    sequence_number INTEGER NOT NULL CHECK (sequence_number > 0),
    state TEXT NOT NULL CHECK (state IN (
        'ORDER_ACCEPTED', 'SHIPMENT_CREATED', 'SHIPPED', 'DELIVERED'
    )),
    provider_event_ref TEXT NOT NULL UNIQUE,
    external_effect TEXT NOT NULL CHECK (external_effect IN ('SIMULATED', 'LIVE')),
    payload_hash TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    UNIQUE (fulfillment_request_id, sequence_number)
);

ALTER TABLE identity_assurances
    DROP CONSTRAINT identity_assurances_level_check;

UPDATE identity_assurances
SET level='MOCK_DOJANG_VERIFIED'
WHERE level='MOCK_KYC';

ALTER TABLE identity_assurances
    ADD CONSTRAINT identity_assurances_level_check
    CHECK (level IN (
        'DOJANG_VERIFIED_ADDRESS',
        'DOJANG_TEST_FAUCET',
        'MOCK_DOJANG_VERIFIED',
        'WALLET_OWNERSHIP_ONLY'
    ));

CREATE TABLE kyc_verification_cases (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
    requested_level TEXT NOT NULL CHECK (requested_level = 'ADVANCED_TEST_KYC'),
    provider_kind TEXT NOT NULL CHECK (provider_kind IN ('DOJANG', 'MOCK_DOJANG')),
    external_effect TEXT NOT NULL CHECK (external_effect IN ('SIMULATED', 'LIVE')),
    state TEXT NOT NULL CHECK (state IN (
        'CREATED', 'PROVIDER_STARTED', 'PENDING', 'VERIFIED',
        'REJECTED', 'EXPIRED', 'CANCELLED'
    )),
    provider_case_ref TEXT NOT NULL UNIQUE,
    assurance_id UUID REFERENCES identity_assurances(id) ON DELETE RESTRICT,
    failure_code TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_kyc_cases_user_time
    ON kyc_verification_cases(user_id, created_at DESC);

CREATE TABLE kyc_verification_audit_events (
    id UUID PRIMARY KEY,
    case_id UUID NOT NULL REFERENCES kyc_verification_cases(id) ON DELETE CASCADE,
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('USER', 'PROVIDER', 'SYSTEM')),
    actor_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (action IN (
        'CASE_CREATED', 'PROVIDER_STARTED', 'VERIFICATION_CHECKED',
        'VERIFIED', 'REJECTED', 'EXPIRED', 'CANCELLED'
    )),
    previous_state TEXT,
    next_state TEXT NOT NULL,
    reason_code TEXT,
    idempotency_key TEXT NOT NULL UNIQUE,
    details JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (
        (actor_kind='USER' AND actor_user_id IS NOT NULL)
        OR (actor_kind<>'USER')
    )
);

CREATE INDEX idx_kyc_audit_case_time
    ON kyc_verification_audit_events(case_id, created_at);
