ALTER TABLE settlement_payments
    ADD COLUMN last_reason_code TEXT,
    ADD COLUMN observation_exhausted_at TIMESTAMPTZ;

ALTER TABLE chain_transactions
    DROP CONSTRAINT chain_transactions_state_check;

ALTER TABLE chain_transactions
    ADD CONSTRAINT chain_transactions_state_check
        CHECK (state IN (
            'SUBMITTED', 'SAFE', 'FINALIZED', 'REORGED', 'FAILED',
            'OBSERVATION_UNKNOWN'
        )),
    ADD COLUMN observation_attempt_count INTEGER NOT NULL DEFAULT 0
        CHECK (observation_attempt_count >= 0),
    ADD COLUMN last_observed_at TIMESTAMPTZ,
    ADD COLUMN next_observation_at TIMESTAMPTZ,
    ADD COLUMN observation_exhausted_at TIMESTAMPTZ,
    ADD COLUMN last_reason_code TEXT;

UPDATE chain_transactions
SET next_observation_at=updated_at
WHERE state IN ('SUBMITTED', 'SAFE');

CREATE INDEX idx_chain_transactions_observation_due
    ON chain_transactions(next_observation_at, submitted_at, chain_id, tx_hash)
    WHERE state IN ('SUBMITTED', 'SAFE');

CREATE TABLE settlement_reconcile_audit (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    settlement_payment_id UUID NOT NULL
        REFERENCES settlement_payments(id) ON DELETE CASCADE,
    chain_id BIGINT NOT NULL,
    tx_hash TEXT,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    reason_code TEXT NOT NULL,
    evidence_kind TEXT NOT NULL CHECK (evidence_kind IN (
        'CHAIN_RECEIPT', 'CANONICAL_EVENT', 'OBSERVATION_POLICY',
        'FINALIZED_CONTRACT_STATE'
    )),
    observed_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_settlement_reconcile_audit_payment_time
    ON settlement_reconcile_audit(settlement_payment_id, observed_at DESC, id DESC);
