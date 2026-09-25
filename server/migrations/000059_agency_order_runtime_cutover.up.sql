CREATE TABLE agency_order_processes (
    agency_order_id UUID PRIMARY KEY REFERENCES agency_orders(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN (
        'ISSUED','PAYMENT_PENDING','PAID','PROCESSING','COMPLETED',
        'PAYMENT_ATTENTION','PROCESSING_ATTENTION','REFUND_PENDING',
        'REFUNDED','CANCELLED','EXPIRED'
    )),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    last_reason_code TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agency_order_processes_state_updated
    ON agency_order_processes(state, updated_at DESC);

CREATE TABLE agency_order_execution_units (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    settlement_payment_id UUID REFERENCES settlement_payments(id) ON DELETE RESTRICT,
    merchant_id TEXT NOT NULL,
    shop_domain TEXT NOT NULL,
    checkout_ordinal INTEGER NOT NULL CHECK (checkout_ordinal > 0),
    checkout_snapshot JSONB NOT NULL,
    state TEXT NOT NULL CHECK (state IN (
        'WAITING_PAYMENT','READY','RUNNING','ORDER_ACCEPTED','FAILED','CANCELLED'
    )),
    assigned_operator_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    assigned_at TIMESTAMPTZ,
    handled_at TIMESTAMPTZ,
    failure_code TEXT,
    external_effect TEXT NOT NULL DEFAULT 'SIMULATED' CHECK (external_effect = 'SIMULATED'),
    merchant_order_created BOOLEAN NOT NULL DEFAULT FALSE CHECK (merchant_order_created = FALSE),
    merchant_order_flow_completed BOOLEAN NOT NULL DEFAULT FALSE,
    mock_order_reference TEXT,
    result_hash TEXT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (agency_order_id, checkout_ordinal),
    UNIQUE (agency_order_id, shop_domain),
    CHECK (
        (assigned_operator_user_id IS NULL AND assigned_at IS NULL)
        OR (assigned_operator_user_id IS NOT NULL AND assigned_at IS NOT NULL)
    ),
    CHECK (
        state <> 'ORDER_ACCEPTED'
        OR (
            merchant_order_flow_completed = TRUE
            AND mock_order_reference IS NOT NULL
            AND result_hash IS NOT NULL
        )
    ),
    CHECK (state <> 'FAILED' OR (failure_code IS NOT NULL AND result_hash IS NOT NULL))
);

CREATE INDEX idx_agency_order_execution_units_queue
    ON agency_order_execution_units(state, created_at, id);
CREATE INDEX idx_agency_order_execution_units_operator
    ON agency_order_execution_units(assigned_operator_user_id, updated_at DESC)
    WHERE assigned_operator_user_id IS NOT NULL;

CREATE TABLE agency_order_execution_audits (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    execution_unit_id UUID NOT NULL REFERENCES agency_order_execution_units(id) ON DELETE RESTRICT,
    actor_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (action IN (
        'PAYMENT_FINALIZED','OPERATOR_ASSIGNED','MERCHANT_ORDER_ACCEPTED',
        'FULFILLMENT_FAILED','SIBLING_CANCELLED'
    )),
    idempotency_key TEXT,
    details JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX idx_agency_order_execution_audits_idempotency
    ON agency_order_execution_audits(idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE agency_order_pii_access_audits (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    execution_unit_id UUID NOT NULL REFERENCES agency_order_execution_units(id) ON DELETE RESTRICT,
    shipping_snapshot_id UUID NOT NULL REFERENCES shipping_snapshots(id) ON DELETE RESTRICT,
    actor_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (action = 'SHIPPING_ADDRESS_REVEAL'),
    reason_code TEXT NOT NULL CHECK (reason_code IN (
        'PLACE_MERCHANT_ORDER','VERIFY_MERCHANT_ORDER','CUSTOMER_SUPPORT'
    )),
    reason_detail TEXT NOT NULL CHECK (char_length(reason_detail) BETWEEN 8 AND 500),
    outcome TEXT NOT NULL CHECK (outcome IN ('GRANTED','DENIED')),
    denial_code TEXT,
    correlation_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    previous_event_hash TEXT,
    event_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agency_order_pii_audits_order_time
    ON agency_order_pii_access_audits(agency_order_id, created_at DESC);

CREATE TABLE agency_order_receipts (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL UNIQUE REFERENCES agency_orders(id) ON DELETE RESTRICT,
    settlement_payment_id UUID NOT NULL UNIQUE REFERENCES settlement_payments(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind = 'TEST'),
    legal_sale BOOLEAN NOT NULL CHECK (legal_sale = FALSE),
    terminal_state TEXT NOT NULL CHECK (terminal_state IN ('COMPLETED','REFUNDED')),
    terminal_tx_hash TEXT NOT NULL,
    receipt_hash TEXT NOT NULL UNIQUE,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

INSERT INTO agency_order_processes(
    agency_order_id, state, version, created_at, updated_at
)
SELECT
    orders.id,
    CASE
        WHEN payment.state='COMPLETED' THEN 'COMPLETED'
        WHEN payment.state='REFUNDED' THEN 'REFUNDED'
        WHEN payment.state='REFUND_PENDING' THEN 'REFUND_PENDING'
        WHEN payment.state='FINALIZED' THEN 'PROCESSING'
        WHEN payment.state IN ('SUBMISSION_UNKNOWN','FAILED') THEN 'PAYMENT_ATTENTION'
        WHEN payment.id IS NOT NULL THEN 'PAYMENT_PENDING'
        ELSE 'ISSUED'
    END,
    1,
    orders.issued_at,
    COALESCE(payment.updated_at, orders.issued_at)
FROM agency_orders orders
LEFT JOIN settlement_payments payment ON payment.agency_order_id=orders.id
ON CONFLICT (agency_order_id) DO NOTHING;

INSERT INTO agency_order_execution_units(
    id, agency_order_id, settlement_payment_id, merchant_id, shop_domain,
    checkout_ordinal, checkout_snapshot, state, failure_code, external_effect,
    merchant_order_created, merchant_order_flow_completed,
    mock_order_reference, result_hash, created_at, updated_at
)
SELECT
    md5(orders.id::text || ':execution:' || checkout.ordinality::text)::uuid,
    orders.id,
    payment.id,
    COALESCE(NULLIF(checkout.value->>'merchantId',''), checkout.value->>'shopDomain'),
    checkout.value->>'shopDomain',
    checkout.ordinality::integer,
    checkout.value,
    CASE
        WHEN payment.state='COMPLETED' THEN 'ORDER_ACCEPTED'
        WHEN payment.state='REFUNDED' THEN 'FAILED'
        WHEN payment.state='FINALIZED' THEN 'READY'
        ELSE 'WAITING_PAYMENT'
    END,
    CASE WHEN payment.state='REFUNDED' THEN 'MIGRATED_REFUND' ELSE NULL END,
    'SIMULATED',
    FALSE,
    payment.state='COMPLETED',
    CASE WHEN payment.state='COMPLETED' THEN 'migrated-mock-' || orders.id::text ELSE NULL END,
    CASE WHEN payment.state IN ('COMPLETED','REFUNDED')
        THEN encode(sha256((orders.id::text || ':' || payment.state || ':' || checkout.ordinality::text)::bytea),'hex')
        ELSE NULL
    END,
    orders.issued_at,
    COALESCE(payment.updated_at, orders.issued_at)
FROM agency_orders orders
CROSS JOIN LATERAL jsonb_array_elements(orders.snapshot->'merchantCheckouts')
    WITH ORDINALITY AS checkout(value, ordinality)
LEFT JOIN settlement_payments payment ON payment.agency_order_id=orders.id
ON CONFLICT (agency_order_id, checkout_ordinal) DO NOTHING;

COMMENT ON TABLE purchases IS
    'Legacy evidence only after migration 59. Active commerce runtime uses AgencyOrder exclusively.';
COMMENT ON TABLE checkout_quotes IS
    'Legacy Purchase evidence only after migration 59. AgencyOrder embeds authoritative checkout snapshots.';
COMMENT ON TABLE user_approvals IS
    'Legacy Purchase evidence only after migration 59. AgencyOrder payment consent is the active authorization source.';
