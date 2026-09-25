DROP INDEX IF EXISTS idx_settlement_reconcile_audit_payment_time;
DROP TABLE IF EXISTS settlement_reconcile_audit;

DROP INDEX IF EXISTS idx_chain_transactions_observation_due;

ALTER TABLE chain_transactions
    DROP COLUMN last_reason_code,
    DROP COLUMN observation_exhausted_at,
    DROP COLUMN next_observation_at,
    DROP COLUMN last_observed_at,
    DROP COLUMN observation_attempt_count,
    DROP CONSTRAINT chain_transactions_state_check;

ALTER TABLE chain_transactions
    ADD CONSTRAINT chain_transactions_state_check
        CHECK (state IN ('SUBMITTED', 'SAFE', 'FINALIZED', 'REORGED', 'FAILED'));

ALTER TABLE settlement_payments
    DROP COLUMN observation_exhausted_at,
    DROP COLUMN last_reason_code;
