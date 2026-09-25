ALTER TABLE checkout_quotes
    ADD COLUMN shipping_basis TEXT NOT NULL DEFAULT 'NOT_QUOTED',
    ADD COLUMN tax_basis TEXT NOT NULL DEFAULT 'NOT_QUOTED',
    ADD COLUMN discount_basis TEXT NOT NULL DEFAULT 'NOT_APPLICABLE',
    ADD COLUMN quote_basis TEXT NOT NULL DEFAULT 'RESEARCH_SNAPSHOT';

ALTER TABLE merchant_registry_entries
    DROP CONSTRAINT merchant_registry_entries_fulfillment_mode_check;
UPDATE merchant_registry_entries
SET fulfillment_mode='MANUAL_PURCHASE_ASSUMED';
ALTER TABLE merchant_registry_entries
    ADD CONSTRAINT merchant_registry_entries_fulfillment_mode_check
    CHECK (fulfillment_mode = 'MANUAL_PURCHASE_ASSUMED');

ALTER TABLE fulfillment_executions
    DROP CONSTRAINT fulfillment_executions_mode_check,
    DROP CONSTRAINT fulfillment_executions_state_check;
UPDATE fulfillment_executions
SET mode='MANUAL_PURCHASE_ASSUMED', state='MANUAL_PURCHASE_ASSUMED'
WHERE state='SIMULATED';
ALTER TABLE fulfillment_executions
    ADD CONSTRAINT fulfillment_executions_mode_check
        CHECK (mode = 'MANUAL_PURCHASE_ASSUMED'),
    ADD CONSTRAINT fulfillment_executions_state_check
        CHECK (state IN ('PENDING', 'RUNNING', 'MANUAL_PURCHASE_ASSUMED', 'FAILED'));

ALTER TABLE chain_transactions
    DROP CONSTRAINT chain_transactions_purpose_check;
ALTER TABLE chain_transactions
    ADD CONSTRAINT chain_transactions_purpose_check
        CHECK (purpose IN ('CLAIM', 'APPROVE', 'PAY', 'COMPLETE', 'REFUND')),
    ADD COLUMN gas_used NUMERIC(78, 0),
    ADD COLUMN effective_gas_price NUMERIC(78, 0),
    ADD COLUMN from_address TEXT;

ALTER TABLE receipts
    DROP CONSTRAINT receipts_merchant_of_record_check,
    DROP CONSTRAINT receipts_refund_handler_check;
ALTER TABLE receipts
    ADD CONSTRAINT receipts_merchant_of_record_check CHECK (merchant_of_record = 'MERCHANT'),
    ADD CONSTRAINT receipts_refund_handler_check CHECK (refund_handler = 'NOT_APPLICABLE');
