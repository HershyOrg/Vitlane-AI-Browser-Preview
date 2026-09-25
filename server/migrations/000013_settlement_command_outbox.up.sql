CREATE TABLE settlement_command_outbox (
    settlement_payment_id UUID NOT NULL
        REFERENCES settlement_payments(id) ON DELETE CASCADE,
    chain_id BIGINT NOT NULL CHECK (chain_id > 0),
    purpose TEXT NOT NULL CHECK (purpose IN ('COMPLETE', 'REFUND')),
    signer_address TEXT NOT NULL,
    order_hash TEXT NOT NULL,
    fulfillment_hash TEXT,
    state TEXT NOT NULL CHECK (state IN (
        'PLANNED', 'NONCE_RESERVED', 'SIGNED', 'BROADCAST', 'FINALIZED', 'CONFLICT'
    )),
    signer_nonce BIGINT CHECK (signer_nonce >= 0),
    tx_hash TEXT,
    raw_transaction BYTEA,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error_code TEXT,
    planned_at TIMESTAMPTZ NOT NULL,
    signed_at TIMESTAMPTZ,
    broadcast_at TIMESTAMPTZ,
    reconciled_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (settlement_payment_id, purpose),
    CHECK (
        (purpose = 'COMPLETE' AND fulfillment_hash IS NOT NULL)
        OR (purpose = 'REFUND' AND fulfillment_hash IS NULL)
    ),
    CHECK (
        (state = 'PLANNED' AND signer_nonce IS NULL)
        OR (state <> 'PLANNED' AND signer_nonce IS NOT NULL)
    ),
    CHECK (
        (state IN ('SIGNED', 'BROADCAST', 'FINALIZED')
            AND tx_hash IS NOT NULL AND raw_transaction IS NOT NULL)
        OR (state NOT IN ('SIGNED', 'BROADCAST', 'FINALIZED'))
    )
);

CREATE UNIQUE INDEX idx_settlement_command_signer_nonce
    ON settlement_command_outbox(chain_id, lower(signer_address), signer_nonce)
    WHERE signer_nonce IS NOT NULL;

CREATE UNIQUE INDEX idx_settlement_command_tx_hash
    ON settlement_command_outbox(chain_id, lower(tx_hash))
    WHERE tx_hash IS NOT NULL;

CREATE INDEX idx_settlement_command_pending
    ON settlement_command_outbox(state, updated_at)
    WHERE state IN ('PLANNED', 'NONCE_RESERVED', 'SIGNED', 'BROADCAST');
