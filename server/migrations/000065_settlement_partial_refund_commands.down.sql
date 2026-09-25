-- GIWA 합류 행을 FK 순서대로 걷어낸 뒤 PayPal 전용 CHECK를 복원한다.
DELETE FROM chain_transactions WHERE purpose = 'REFUND_PARTIAL';
ALTER TABLE chain_transactions
    DROP CONSTRAINT chain_transactions_purpose_check;
ALTER TABLE chain_transactions
    ADD CONSTRAINT chain_transactions_purpose_check
        CHECK (purpose IN ('CLAIM', 'APPROVE', 'PAY', 'COMPLETE', 'REFUND'));
DELETE FROM payment_external_operations WHERE owner_kind = 'CUSTOMER_REFUND_ATTEMPT'
  AND owner_id IN (
    SELECT attempt.id FROM payment_customer_refund_attempts attempt
    JOIN payment_customer_refunds refund ON refund.id = attempt.customer_refund_id
    JOIN payment_funds_receipts receipt ON receipt.id = refund.funds_receipt_id
    WHERE receipt.kind = 'GIWA_FINALIZED_PAY'
);
DELETE FROM payment_customer_refund_attempts WHERE customer_refund_id IN (
    SELECT refund.id FROM payment_customer_refunds refund
    JOIN payment_funds_receipts receipt ON receipt.id = refund.funds_receipt_id
    WHERE receipt.kind = 'GIWA_FINALIZED_PAY'
);
DELETE FROM settlement_command_outbox WHERE purpose = 'REFUND_PARTIAL';
DELETE FROM payment_customer_refunds WHERE funds_receipt_id IN (
    SELECT id FROM payment_funds_receipts WHERE kind = 'GIWA_FINALIZED_PAY'
);
DELETE FROM payment_refund_slice_claims WHERE funds_receipt_id IN (
    SELECT id FROM payment_funds_receipts WHERE kind = 'GIWA_FINALIZED_PAY'
);
UPDATE agency_order_execution_units SET customer_payment_id = NULL
WHERE customer_payment_id IN (
    SELECT id FROM payment_customer_payments WHERE rail = 'GIWA'
);
UPDATE agency_order_receipts SET customer_payment_id = NULL
WHERE customer_payment_id IN (
    SELECT id FROM payment_customer_payments WHERE rail = 'GIWA'
);
DELETE FROM payment_funds_receipts WHERE kind = 'GIWA_FINALIZED_PAY';
DELETE FROM payment_customer_payments WHERE rail = 'GIWA';

DROP INDEX IF EXISTS idx_payment_funds_receipts_giwa_order;
ALTER TABLE payment_funds_receipts
    DROP CONSTRAINT payment_funds_receipts_kind_shape_check;
ALTER TABLE payment_funds_receipts
    DROP COLUMN pay_tx_hash,
    DROP COLUMN order_hash;
UPDATE payment_funds_receipts SET capture_id = '' WHERE capture_id IS NULL;
UPDATE payment_funds_receipts SET paypal_order_id = '' WHERE paypal_order_id IS NULL;
ALTER TABLE payment_funds_receipts
    ALTER COLUMN capture_id SET NOT NULL,
    ALTER COLUMN paypal_order_id SET NOT NULL;
ALTER TABLE payment_funds_receipts
    DROP CONSTRAINT payment_funds_receipts_provider_environment_check;
ALTER TABLE payment_funds_receipts
    ADD CONSTRAINT payment_funds_receipts_provider_environment_check
        CHECK (provider_environment = 'SANDBOX');
ALTER TABLE payment_funds_receipts
    DROP CONSTRAINT payment_funds_receipts_kind_check;
ALTER TABLE payment_funds_receipts
    ADD CONSTRAINT payment_funds_receipts_kind_check
        CHECK (kind = 'PAYPAL_CAPTURE');

ALTER TABLE payment_customer_payments
    DROP CONSTRAINT payment_customer_payments_asset_check;
ALTER TABLE payment_customer_payments
    ADD CONSTRAINT payment_customer_payments_asset_check
        CHECK (asset = 'USD');
ALTER TABLE payment_customer_payments
    DROP CONSTRAINT payment_customer_payments_provider_environment_check;
ALTER TABLE payment_customer_payments
    ADD CONSTRAINT payment_customer_payments_provider_environment_check
        CHECK (provider_environment = 'SANDBOX');
ALTER TABLE payment_customer_payments
    DROP CONSTRAINT payment_customer_payments_rail_check;
ALTER TABLE payment_customer_payments
    ADD CONSTRAINT payment_customer_payments_rail_check
        CHECK (rail = 'PAYPAL');

DROP INDEX IF EXISTS idx_settlement_command_refund_key;
DROP INDEX IF EXISTS idx_settlement_command_customer_refund;
DROP INDEX IF EXISTS idx_settlement_command_lifecycle_purpose;

ALTER TABLE settlement_command_outbox
    DROP CONSTRAINT settlement_command_outbox_partial_shape_check;

ALTER TABLE settlement_command_outbox
    DROP CONSTRAINT settlement_command_outbox_purpose_check;
ALTER TABLE settlement_command_outbox
    ADD CONSTRAINT settlement_command_outbox_purpose_check
        CHECK (purpose IN ('COMPLETE', 'REFUND'));

ALTER TABLE settlement_command_outbox
    DROP COLUMN refund_key,
    DROP COLUMN fee_part,
    DROP COLUMN pass_through_part,
    DROP COLUMN customer_refund_id,
    DROP COLUMN id;

ALTER TABLE settlement_command_outbox
    ADD PRIMARY KEY (settlement_payment_id, purpose);
