-- Phase 8 Step 3 PR-1 (ADR-0050): rail-neutral PayPal Sandbox 고객 결제.
-- 결제수단은 결제 어댑터이며 수납 후에는 기존 AgencyOrder lifecycle로 합류한다.

CREATE TABLE paypal_account_bindings (
    environment TEXT PRIMARY KEY CHECK (environment IN ('SANDBOX','LIVE')),
    merchant_id TEXT NOT NULL,
    client_id_fingerprint TEXT NOT NULL,
    webhook_id TEXT NOT NULL,
    verified_by TEXT NOT NULL,
    evidence_note TEXT NOT NULL DEFAULT '',
    verified_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE payment_customer_payments (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    rail TEXT NOT NULL CHECK (rail = 'PAYPAL'),
    provider_environment TEXT NOT NULL CHECK (provider_environment = 'SANDBOX'),
    asset TEXT NOT NULL CHECK (asset = 'USD'),
    economic_effect TEXT NOT NULL CHECK (economic_effect = 'NO_REAL_VALUE'),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency = 'USD'),
    state TEXT NOT NULL CHECK (state IN (
        'CREATED','ACTION_REQUIRED','PROCESSING','OUTCOME_UNKNOWN',
        'SUCCEEDED','CAPTURED_NONCONFORMING','FAILED','ABANDONED','EXPIRED','SUPERSEDED'
    )),
    -- SUCCEEDED는 불변 수납 사실이고, 이후 주문 terminal 국면만 별도 축으로 둔다.
    terminal_state TEXT CHECK (terminal_state IN ('COMPLETED','REFUND_PENDING','REFUNDED')),
    last_reason_code TEXT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (terminal_state IS NULL OR state = 'SUCCEEDED')
);

CREATE UNIQUE INDEX idx_payment_customer_payments_open
    ON payment_customer_payments(agency_order_id)
    WHERE state NOT IN ('FAILED','ABANDONED','EXPIRED','SUPERSEDED');
CREATE UNIQUE INDEX idx_payment_customer_payments_succeeded
    ON payment_customer_payments(agency_order_id)
    WHERE state = 'SUCCEEDED';

CREATE TABLE payment_paypal_attempts (
    id UUID PRIMARY KEY,
    customer_payment_id UUID NOT NULL
        REFERENCES payment_customer_payments(id) ON DELETE RESTRICT,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    state TEXT NOT NULL CHECK (state IN (
        'ORDER_PREPARED','CANCELLED_BEFORE_CREATE','ORDER_CREATE_SUBMITTED',
        'ORDER_CREATE_UNKNOWN','ORDER_CREATE_FAILED','PAYER_ACTION_REQUIRED',
        'PAYER_APPROVED','APPROVAL_REVERSED','CANCELLED_BY_USER','EXPIRED',
        'SUPERSEDED_BEFORE_CAPTURE','ABANDONED_BEFORE_CAPTURE','CAPTURE_SUBMITTED',
        'CAPTURE_PENDING','CAPTURE_ACTION_REQUIRED','CAPTURE_OUTCOME_UNKNOWN',
        'CAPTURE_COMPLETED','CAPTURE_COMPLETED_MISMATCH','CAPTURE_DECLINED','CAPTURE_FAILED'
    )),
    paypal_order_id TEXT,
    approval_url TEXT,
    return_nonce TEXT NOT NULL,
    capture_id TEXT,
    last_reason_code TEXT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (customer_payment_id, sequence),
    UNIQUE (return_nonce)
);

CREATE UNIQUE INDEX idx_payment_paypal_attempts_order
    ON payment_paypal_attempts(paypal_order_id)
    WHERE paypal_order_id IS NOT NULL;

CREATE TABLE payment_external_operations (
    id UUID PRIMARY KEY,
    purpose TEXT NOT NULL CHECK (purpose IN (
        'PAYPAL_ORDER_CREATE','PAYPAL_CAPTURE','PAYPAL_REFUND'
    )),
    owner_kind TEXT NOT NULL CHECK (owner_kind IN (
        'PAYPAL_ATTEMPT','CUSTOMER_REFUND_ATTEMPT'
    )),
    owner_id UUID NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    request_hash TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN (
        'PREPARED','SENT','SUCCEEDED','FAILED','UNKNOWN','CANCELLED'
    )),
    provider_resource_id TEXT,
    first_sent_at TIMESTAMPTZ,
    idempotency_deadline TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    last_reason_code TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (state NOT IN ('SENT','UNKNOWN') OR first_sent_at IS NOT NULL)
);

-- 돈이 나가는 외부 호출은 owner당 미해결 최대 하나 (ADR-0050 슬림 코어 불변 1).
CREATE UNIQUE INDEX idx_payment_external_operations_unresolved
    ON payment_external_operations(owner_kind, owner_id)
    WHERE state IN ('PREPARED','SENT','UNKNOWN');

CREATE TABLE payment_funds_receipts (
    id UUID PRIMARY KEY,
    customer_payment_id UUID NOT NULL
        REFERENCES payment_customer_payments(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind = 'PAYPAL_CAPTURE'),
    provider_environment TEXT NOT NULL CHECK (provider_environment = 'SANDBOX'),
    capture_id TEXT NOT NULL,
    paypal_order_id TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency = 'USD'),
    accepted BOOLEAN NOT NULL,
    occurred_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (provider_environment, capture_id)
);

CREATE UNIQUE INDEX idx_payment_funds_receipts_accepted
    ON payment_funds_receipts(customer_payment_id)
    WHERE accepted;

CREATE TABLE payment_customer_refunds (
    id UUID PRIMARY KEY,
    customer_payment_id UUID NOT NULL
        REFERENCES payment_customer_payments(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    funds_receipt_id UUID NOT NULL REFERENCES payment_funds_receipts(id) ON DELETE RESTRICT,
    obligation_kind TEXT NOT NULL CHECK (obligation_kind = 'MANDATORY_SYSTEM'),
    basis TEXT NOT NULL CHECK (basis = 'FULL_ORDER_FAILURE'),
    gross_minor BIGINT NOT NULL CHECK (gross_minor > 0),
    state TEXT NOT NULL CHECK (state IN ('APPROVED','EXECUTION_PENDING','SUCCEEDED')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (customer_payment_id, basis)
);

CREATE TABLE payment_customer_refund_attempts (
    id UUID PRIMARY KEY,
    customer_refund_id UUID NOT NULL
        REFERENCES payment_customer_refunds(id) ON DELETE RESTRICT,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    request_key TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL CHECK (state IN (
        'PREPARED','SUBMISSION_PENDING','PROCESSING','CREDIT_LINKED',
        'SETTLED_FULL','FAILED','FAILED_NO_EFFECT','OUTCOME_UNKNOWN','CANCELLED'
    )),
    paypal_refund_id TEXT,
    last_reason_code TEXT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (customer_refund_id, sequence)
);

-- provider Refund resource는 한 번만 계상한다 (ADR-0050 슬림 코어 불변 2).
CREATE UNIQUE INDEX idx_payment_customer_refund_attempts_resource
    ON payment_customer_refund_attempts(paypal_refund_id)
    WHERE paypal_refund_id IS NOT NULL;

-- 서명 검증을 통과한 event의 safe field만 저장한다. raw payload는 저장하지 않는다.
CREATE TABLE payment_paypal_webhook_inbox (
    id UUID PRIMARY KEY,
    environment TEXT NOT NULL CHECK (environment IN ('SANDBOX','LIVE')),
    webhook_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    transmission_id TEXT NOT NULL,
    resource_kind TEXT,
    resource_id TEXT,
    received_at TIMESTAMPTZ NOT NULL,
    processed_at TIMESTAMPTZ,
    UNIQUE (environment, webhook_id, event_id)
);

-- PaymentInstruction rail 개방: PAYPAL/USD 발행 허용 (기존 GIWA/TVITUSD 유지).
ALTER TABLE agency_order_payment_instructions
    DROP CONSTRAINT agency_order_payment_instructions_rail_check,
    DROP CONSTRAINT agency_order_payment_instructions_asset_check,
    ADD CONSTRAINT agency_order_payment_instructions_rail_check
        CHECK (rail IN ('GIWA','PAYPAL')),
    ADD CONSTRAINT agency_order_payment_instructions_asset_check
        CHECK (asset IN ('TVITUSD','USD'));

ALTER TABLE agency_order_execution_units
    ADD COLUMN customer_payment_id UUID
        REFERENCES payment_customer_payments(id) ON DELETE RESTRICT,
    ADD CONSTRAINT agency_order_execution_units_single_payment_source CHECK (
        settlement_payment_id IS NULL OR customer_payment_id IS NULL
    );

ALTER TABLE agency_order_receipts
    ALTER COLUMN settlement_payment_id DROP NOT NULL;
ALTER TABLE agency_order_receipts
    ADD COLUMN customer_payment_id UUID UNIQUE
        REFERENCES payment_customer_payments(id) ON DELETE RESTRICT,
    ADD CONSTRAINT agency_order_receipts_single_payment_source CHECK (
        (settlement_payment_id IS NOT NULL AND customer_payment_id IS NULL)
        OR (settlement_payment_id IS NULL AND customer_payment_id IS NOT NULL)
    );
