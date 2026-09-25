ALTER TABLE wallets
    ADD COLUMN IF NOT EXISTS ownership_message_hash BYTEA;

CREATE TABLE wallet_challenges (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    address TEXT NOT NULL,
    chain_id TEXT NOT NULL,
    origin TEXT NOT NULL,
    nonce_hash BYTEA NOT NULL UNIQUE,
    message TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_wallet_challenges_user_created
    ON wallet_challenges(user_id, created_at DESC);

CREATE TABLE identity_assurances (
    id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    level TEXT NOT NULL CHECK (level IN ('DOJANG_TEST', 'MOCK_KYC')),
    issuer_ref TEXT NOT NULL,
    schema_ref TEXT NOT NULL,
    evidence_hash TEXT NOT NULL,
    verified_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_identity_assurances_wallet_expiry
    ON identity_assurances(wallet_id, expires_at DESC);

CREATE TABLE merchant_registry_entries (
    merchant_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    domain_suffixes TEXT[] NOT NULL,
    country CHAR(2) NOT NULL,
    currency CHAR(3) NOT NULL,
    fulfillment_mode TEXT NOT NULL CHECK (fulfillment_mode IN ('SIMULATED', 'UCP_REFERENCE')),
    payment_enabled BOOLEAN NOT NULL,
    principal_recipient TEXT NOT NULL,
    registry_version BIGINT NOT NULL CHECK (registry_version > 0),
    active BOOLEAN NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE purchases (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    shopping_session_id UUID NOT NULL REFERENCES shopping_sessions(id) ON DELETE RESTRICT,
    candidate_id UUID NOT NULL,
    candidate_hash TEXT NOT NULL,
    candidate_snapshot JSONB NOT NULL,
    merchant_id TEXT NOT NULL REFERENCES merchant_registry_entries(merchant_id) ON DELETE RESTRICT,
    unit_price_amount NUMERIC(36, 18) NOT NULL CHECK (unit_price_amount > 0),
    currency CHAR(3) NOT NULL,
    quantity BIGINT NOT NULL CHECK (quantity > 0),
    purchase_hash TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN (
        'DRAFT', 'AWAITING_USER_APPROVAL', 'USER_APPROVED', 'SETTLEMENT_PENDING',
        'FUNDED', 'FULFILLMENT_PENDING', 'COMPLETED', 'SUPERSEDED', 'CANCELLED',
        'PAYMENT_FAILED', 'REFUND_PENDING', 'REFUNDED'
    )),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, shopping_session_id, candidate_id, quantity)
);

CREATE TABLE checkout_quotes (
    id UUID PRIMARY KEY,
    purchase_id UUID NOT NULL REFERENCES purchases(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    quote_hash TEXT NOT NULL UNIQUE,
    unit_price_amount NUMERIC(36, 18) NOT NULL,
    quantity BIGINT NOT NULL,
    item_subtotal_amount NUMERIC(36, 18) NOT NULL,
    shipping_amount NUMERIC(36, 18) NOT NULL,
    tax_amount NUMERIC(36, 18) NOT NULL,
    discount_amount NUMERIC(36, 18) NOT NULL,
    payable_total_amount NUMERIC(36, 18) NOT NULL CHECK (payable_total_amount > 0),
    currency CHAR(3) NOT NULL,
    settlement_amount_base_units NUMERIC(78, 0) NOT NULL CHECK (settlement_amount_base_units > 0),
    token_address TEXT NOT NULL,
    token_decimals SMALLINT NOT NULL CHECK (token_decimals = 6),
    settlement_address TEXT NOT NULL,
    chain_id BIGINT NOT NULL,
    merchant_registry_version BIGINT NOT NULL,
    fee_bps INTEGER NOT NULL CHECK (fee_bps BETWEEN 0 AND 10000),
    fee_recipient TEXT NOT NULL,
    principal_recipient TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_checkout_quotes_purchase_expiry
    ON checkout_quotes(purchase_id, expires_at DESC);

CREATE TABLE user_approvals (
    id UUID PRIMARY KEY,
    purchase_id UUID NOT NULL UNIQUE REFERENCES purchases(id) ON DELETE CASCADE,
    quote_id UUID NOT NULL REFERENCES checkout_quotes(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
    assurance_id UUID NOT NULL REFERENCES identity_assurances(id) ON DELETE RESTRICT,
    approval_hash TEXT NOT NULL UNIQUE,
    acknowledged_test_asset BOOLEAN NOT NULL,
    acknowledged_no_legal_sale BOOLEAN NOT NULL,
    approved_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE settlement_authorizations (
    id UUID PRIMARY KEY,
    purchase_id UUID NOT NULL UNIQUE REFERENCES purchases(id) ON DELETE CASCADE,
    order_hash TEXT NOT NULL UNIQUE,
    payer TEXT NOT NULL,
    nonce NUMERIC(78, 0) NOT NULL,
    pay_deadline TIMESTAMPTZ NOT NULL,
    refund_after TIMESTAMPTZ NOT NULL,
    signer_address TEXT NOT NULL,
    typed_data_hash TEXT NOT NULL,
    authorization_payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE settlement_payments (
    id UUID PRIMARY KEY,
    purchase_id UUID NOT NULL UNIQUE REFERENCES purchases(id) ON DELETE CASCADE,
    order_hash TEXT NOT NULL UNIQUE,
    chain_id BIGINT NOT NULL,
    settlement_address TEXT NOT NULL,
    payer TEXT NOT NULL,
    amount_base_units NUMERIC(78, 0) NOT NULL,
    claim_tx_hash TEXT,
    approve_tx_hash TEXT,
    pay_tx_hash TEXT,
    complete_tx_hash TEXT,
    refund_tx_hash TEXT,
    state TEXT NOT NULL CHECK (state IN (
        'AUTHORIZED', 'AWAITING_ALLOWANCE', 'PAYMENT_SUBMITTED', 'SUBMISSION_UNKNOWN',
        'SAFE', 'FINALIZED', 'COMPLETION_SUBMITTED', 'COMPLETED',
        'REFUND_PENDING', 'REFUNDED', 'FAILED'
    )),
    safe_block BIGINT,
    finalized_block BIGINT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX idx_settlement_payment_pay_tx
    ON settlement_payments(chain_id, pay_tx_hash) WHERE pay_tx_hash IS NOT NULL;

CREATE UNIQUE INDEX idx_settlement_payment_claim_tx
    ON settlement_payments(chain_id, claim_tx_hash) WHERE claim_tx_hash IS NOT NULL;

CREATE UNIQUE INDEX idx_settlement_payment_approve_tx
    ON settlement_payments(chain_id, approve_tx_hash) WHERE approve_tx_hash IS NOT NULL;

CREATE TABLE chain_transactions (
    chain_id BIGINT NOT NULL,
    tx_hash TEXT NOT NULL,
    settlement_payment_id UUID REFERENCES settlement_payments(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN ('PAY', 'COMPLETE', 'REFUND')),
    state TEXT NOT NULL CHECK (state IN ('SUBMITTED', 'SAFE', 'FINALIZED', 'REORGED', 'FAILED')),
    block_number BIGINT,
    block_hash TEXT,
    submitted_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (chain_id, tx_hash)
);

CREATE TABLE chain_events (
    chain_id BIGINT NOT NULL,
    tx_hash TEXT NOT NULL,
    log_index BIGINT NOT NULL,
    block_number BIGINT NOT NULL,
    block_hash TEXT NOT NULL,
    event_name TEXT NOT NULL,
    order_hash TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (chain_id, tx_hash, log_index)
);

CREATE TABLE chain_cursors (
    chain_id BIGINT NOT NULL,
    contract_address TEXT NOT NULL,
    finalized_block BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (chain_id, contract_address)
);

CREATE TABLE fulfillment_executions (
    id UUID PRIMARY KEY,
    settlement_payment_id UUID NOT NULL UNIQUE REFERENCES settlement_payments(id) ON DELETE CASCADE,
    merchant_id TEXT NOT NULL REFERENCES merchant_registry_entries(merchant_id) ON DELETE RESTRICT,
    mode TEXT NOT NULL CHECK (mode IN ('SIMULATED', 'UCP_REFERENCE')),
    state TEXT NOT NULL CHECK (state IN ('PENDING', 'RUNNING', 'SIMULATED', 'FAILED')),
    result_hash TEXT,
    failure_code TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE receipts (
    id UUID PRIMARY KEY,
    purchase_id UUID NOT NULL UNIQUE REFERENCES purchases(id) ON DELETE CASCADE,
    settlement_payment_id UUID NOT NULL UNIQUE REFERENCES settlement_payments(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind = 'TEST'),
    legal_sale BOOLEAN NOT NULL CHECK (legal_sale = FALSE),
    merchant_of_record TEXT NOT NULL CHECK (merchant_of_record = 'MERCHANT'),
    refund_handler TEXT NOT NULL CHECK (refund_handler = 'NOT_APPLICABLE'),
    terminal_tx_hash TEXT NOT NULL,
    receipt_hash TEXT NOT NULL UNIQUE,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
