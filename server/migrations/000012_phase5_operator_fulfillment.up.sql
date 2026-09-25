ALTER TABLE merchant_registry_entries
    DROP CONSTRAINT merchant_registry_entries_fulfillment_mode_check;
UPDATE merchant_registry_entries
SET fulfillment_mode='TEST_PURCHASE_ASSUMPTION'
WHERE fulfillment_mode='MANUAL_PURCHASE_ASSUMED';
ALTER TABLE merchant_registry_entries
    ADD CONSTRAINT merchant_registry_entries_fulfillment_mode_check
    CHECK (fulfillment_mode = 'TEST_PURCHASE_ASSUMPTION');

ALTER TABLE fulfillment_executions
    DROP CONSTRAINT fulfillment_executions_mode_check,
    DROP CONSTRAINT fulfillment_executions_state_check;

ALTER TABLE fulfillment_executions
    ADD COLUMN processing_mode TEXT,
    ADD COLUMN operator_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    ADD COLUMN handled_at TIMESTAMPTZ,
    ADD COLUMN quote_id UUID REFERENCES checkout_quotes(id) ON DELETE RESTRICT,
    ADD COLUMN quote_hash TEXT,
    ADD COLUMN quote_snapshot JSONB,
    ADD COLUMN idempotency_key TEXT,
    ADD COLUMN result_payload JSONB,
    ADD COLUMN eligible_at TIMESTAMPTZ,
    ADD COLUMN merchant_order_created BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN external_order_reference TEXT;

UPDATE fulfillment_executions
SET mode='TEST_PURCHASE_ASSUMPTION',
    state='PURCHASE_ASSUMED',
    processing_mode='AUTO_TEST',
    handled_at=updated_at,
    eligible_at=created_at,
    idempotency_key='legacy-auto:' || settlement_payment_id::text,
    result_payload=jsonb_build_object(
        'schema', 'vitlane.test-purchase-assumption.v1',
        'paymentId', settlement_payment_id::text,
        'processingMode', 'AUTO_TEST',
        'resultKind', 'TEST_NO_MERCHANT_ORDER',
        'merchantOrderCreated', false,
        'externalOrderReference', NULL
    )
WHERE state='MANUAL_PURCHASE_ASSUMED';

ALTER TABLE fulfillment_executions
    ADD CONSTRAINT fulfillment_executions_mode_check
        CHECK (mode = 'TEST_PURCHASE_ASSUMPTION'),
    ADD CONSTRAINT fulfillment_executions_state_check
        CHECK (state IN ('PENDING', 'RUNNING', 'PURCHASE_ASSUMED', 'FAILED')),
    ADD CONSTRAINT fulfillment_executions_processing_mode_check
        CHECK (processing_mode IS NULL OR processing_mode IN ('MANUAL_OPERATOR', 'AUTO_TEST')),
    ADD CONSTRAINT fulfillment_executions_no_merchant_order_check
        CHECK (merchant_order_created = FALSE AND external_order_reference IS NULL),
    ADD CONSTRAINT fulfillment_executions_terminal_fields_check
        CHECK (
            state IN ('PENDING', 'RUNNING')
            OR (
                handled_at IS NOT NULL
                AND idempotency_key IS NOT NULL
                AND result_payload IS NOT NULL
                AND result_hash IS NOT NULL
            )
        );

CREATE UNIQUE INDEX idx_fulfillment_executions_idempotency
    ON fulfillment_executions(idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_fulfillment_executions_pending
    ON fulfillment_executions(state, eligible_at, created_at)
    WHERE state='PENDING';

CREATE TABLE fulfillment_operator_settings (
    environment TEXT PRIMARY KEY CHECK (environment IN ('LOCAL', 'GIWA_TESTNET')),
    automation_mode TEXT NOT NULL CHECK (automation_mode IN ('MANUAL_OPERATOR', 'AUTO_TEST')),
    delay_seconds INTEGER NOT NULL CHECK (delay_seconds BETWEEN 1 AND 60),
    version BIGINT NOT NULL CHECK (version > 0),
    updated_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE fulfillment_audit_events (
    id UUID PRIMARY KEY,
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('SYSTEM', 'USER')),
    actor_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (action IN (
        'AUTOMATION_CHANGED', 'PURCHASE_ASSUMED', 'FULFILLMENT_FAILED'
    )),
    settlement_payment_id UUID REFERENCES settlement_payments(id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL UNIQUE,
    details JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (
        (actor_kind='SYSTEM' AND actor_user_id IS NULL)
        OR (actor_kind='USER' AND actor_user_id IS NOT NULL)
    )
);

CREATE INDEX idx_fulfillment_audit_payment_time
    ON fulfillment_audit_events(settlement_payment_id, created_at DESC);
