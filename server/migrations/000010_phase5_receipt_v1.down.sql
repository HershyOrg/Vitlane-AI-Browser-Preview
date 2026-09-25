ALTER TABLE receipts
    DROP CONSTRAINT receipts_merchant_of_record_check,
    DROP CONSTRAINT receipts_refund_handler_check;
ALTER TABLE receipts
    ADD CONSTRAINT receipts_merchant_of_record_check CHECK (merchant_of_record = 'VITLANE'),
    ADD CONSTRAINT receipts_refund_handler_check CHECK (refund_handler = 'VITLANE');

ALTER TABLE chain_transactions
    DROP COLUMN from_address,
    DROP COLUMN effective_gas_price,
    DROP COLUMN gas_used,
    DROP CONSTRAINT chain_transactions_purpose_check;
ALTER TABLE chain_transactions
    ADD CONSTRAINT chain_transactions_purpose_check
        CHECK (purpose IN ('PAY', 'COMPLETE', 'REFUND'));

ALTER TABLE fulfillment_executions
    DROP CONSTRAINT fulfillment_executions_mode_check,
    DROP CONSTRAINT fulfillment_executions_state_check;
UPDATE fulfillment_executions
SET mode='SIMULATED', state='SIMULATED'
WHERE state='MANUAL_PURCHASE_ASSUMED';
ALTER TABLE fulfillment_executions
    ADD CONSTRAINT fulfillment_executions_mode_check CHECK (mode IN ('SIMULATED', 'UCP_REFERENCE')),
    ADD CONSTRAINT fulfillment_executions_state_check CHECK (state IN ('PENDING', 'RUNNING', 'SIMULATED', 'FAILED'));

ALTER TABLE merchant_registry_entries
    DROP CONSTRAINT merchant_registry_entries_fulfillment_mode_check;
UPDATE merchant_registry_entries SET fulfillment_mode='SIMULATED';
ALTER TABLE merchant_registry_entries
    ADD CONSTRAINT merchant_registry_entries_fulfillment_mode_check
        CHECK (fulfillment_mode IN ('SIMULATED', 'UCP_REFERENCE'));

ALTER TABLE checkout_quotes
    DROP COLUMN quote_basis,
    DROP COLUMN discount_basis,
    DROP COLUMN tax_basis,
    DROP COLUMN shipping_basis;
