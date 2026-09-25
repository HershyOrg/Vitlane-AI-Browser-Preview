CREATE TABLE agency_order_sheet_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_cart_id UUID NOT NULL,
    source_cart_version BIGINT NOT NULL CHECK (source_cart_version >= 0),
    source_cart_snapshot_hash TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN (
        'EDITING','DISCOVERING_DELIVERY','DELIVERY_SELECTION_REQUIRED','PREFLIGHTING',
        'READY','ISSUING','CONSUMED','PRICE_CHANGED','RATE_LIMITED','BLOCKED','EXPIRED'
    )),
    block_reason TEXT,
    version BIGINT NOT NULL CHECK (version > 0),
    creation_key_hash TEXT NOT NULL,
    creation_request_hash TEXT NOT NULL,
    snapshot JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    UNIQUE (user_id, creation_key_hash)
);

CREATE INDEX idx_agency_order_sheets_user_created
    ON agency_order_sheet_sessions(user_id, created_at DESC);
CREATE INDEX idx_agency_order_sheets_cleanup
    ON agency_order_sheet_sessions(state, expires_at)
    WHERE state NOT IN ('CONSUMED','EXPIRED');

CREATE TABLE agency_order_provider_capabilities (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    order_sheet_session_id UUID NOT NULL REFERENCES agency_order_sheet_sessions(id) ON DELETE CASCADE,
    shop_domain TEXT NOT NULL,
    capability_kind TEXT NOT NULL CHECK (capability_kind IN ('STOREFRONT_CART','UCP_CHECKOUT')),
    encrypted_payload BYTEA NOT NULL,
    payload_nonce BYTEA NOT NULL,
    key_version TEXT NOT NULL,
    payload_hmac TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    UNIQUE (order_sheet_session_id, shop_domain, capability_kind)
);

CREATE TABLE agency_orders (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    order_sheet_session_id UUID NOT NULL UNIQUE
        REFERENCES agency_order_sheet_sessions(id) ON DELETE RESTRICT,
    source_cart_id UUID NOT NULL,
    source_cart_version BIGINT NOT NULL CHECK (source_cart_version >= 0),
    source_cart_snapshot_hash TEXT NOT NULL,
    shipping_snapshot_id UUID NOT NULL REFERENCES shipping_snapshots(id) ON DELETE RESTRICT,
    snapshot_hash TEXT NOT NULL UNIQUE,
    idempotency_key_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status = 'ISSUED'),
    customer_payable_minor BIGINT NOT NULL CHECK (customer_payable_minor > 0),
    currency CHAR(3) NOT NULL CHECK (currency = 'USD'),
    snapshot JSONB NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, idempotency_key_hash)
);

CREATE TABLE agency_order_payment_instructions (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL UNIQUE REFERENCES agency_orders(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    agency_order_snapshot_hash TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency CHAR(3) NOT NULL CHECK (currency = 'USD'),
    rail TEXT NOT NULL CHECK (rail = 'GIWA'),
    asset TEXT NOT NULL CHECK (asset = 'TVITUSD'),
    state TEXT NOT NULL CHECK (state IN ('PENDING','CONSUMED','EXPIRED','CONFLICT')),
    payload JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (agency_order_id, agency_order_snapshot_hash)
);

CREATE TABLE agency_order_payment_consents (
    agency_order_id UUID PRIMARY KEY REFERENCES agency_orders(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
    wallet_ownership_proof_id UUID NOT NULL REFERENCES wallet_ownership_proofs(id) ON DELETE RESTRICT,
    amount_base_units NUMERIC(78,0) NOT NULL CHECK (amount_base_units > 0),
    token_address TEXT NOT NULL,
    settlement_address TEXT NOT NULL,
    chain_id BIGINT NOT NULL CHECK (chain_id > 0),
    merchant_id TEXT NOT NULL REFERENCES merchant_registry_entries(merchant_id) ON DELETE RESTRICT,
    merchant_registry_version BIGINT NOT NULL CHECK (merchant_registry_version > 0),
    fee_bps INTEGER NOT NULL CHECK (fee_bps BETWEEN 0 AND 10000),
    fee_recipient TEXT NOT NULL,
    principal_recipient TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE settlement_authorizations ALTER COLUMN purchase_id DROP NOT NULL;
ALTER TABLE settlement_authorizations
    ADD COLUMN agency_order_id UUID UNIQUE REFERENCES agency_orders(id) ON DELETE RESTRICT;
ALTER TABLE settlement_authorizations
    ADD CONSTRAINT settlement_authorizations_one_source CHECK (
        (purchase_id IS NOT NULL)::integer + (agency_order_id IS NOT NULL)::integer = 1
    );

ALTER TABLE settlement_payments ALTER COLUMN purchase_id DROP NOT NULL;
ALTER TABLE settlement_payments
    ADD COLUMN agency_order_id UUID UNIQUE REFERENCES agency_orders(id) ON DELETE RESTRICT;
ALTER TABLE settlement_payments
    ADD CONSTRAINT settlement_payments_one_source CHECK (
        (purchase_id IS NOT NULL)::integer + (agency_order_id IS NOT NULL)::integer = 1
    );

CREATE TABLE agency_order_outbox (
    id UUID PRIMARY KEY,
    aggregate_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    event_type TEXT NOT NULL CHECK (event_type = 'PaymentInstructionIssued.v2'),
    payload JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'PENDING' CHECK (state IN ('PENDING','CONSUMED','CONFLICT')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    available_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    UNIQUE (aggregate_id, event_type)
);

CREATE TABLE agency_order_audits (
    id BIGSERIAL PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (action = 'ISSUED'),
    order_snapshot_hash TEXT NOT NULL,
    displayed_snapshot_hash TEXT NOT NULL,
    disclosure_version TEXT NOT NULL,
    idempotency_key_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agency_order_outbox_pending
    ON agency_order_outbox(state, available_at)
    WHERE state = 'PENDING';
