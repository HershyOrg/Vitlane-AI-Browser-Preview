ALTER TABLE payment_customer_refunds
    DROP CONSTRAINT payment_customer_refunds_cause_check;
UPDATE payment_customer_refunds
SET cause='MERCHANT_CANCEL_CONFIRMED'
WHERE cause='CUSTOMER_CANCEL_AFTER_PLACEMENT_CONFIRMED';
ALTER TABLE payment_customer_refunds
    ADD CONSTRAINT payment_customer_refunds_cause_check CHECK (cause IN (
        'ORDER_FAILURE','CUSTOMER_REQUEST',
        'CUSTOMER_CANCEL_PRE_EFFECT','DELAY_RULE_CANCEL',
        'MERCHANT_FAULT','MERCHANT_CANCEL_CONFIRMED'
    ));

-- Keep MerchantPayment rows. There is no safe marker that distinguishes a
-- backfilled row from a post-migration external fact, and rollback must not
-- delete a possible merchant obligation or charge observation.

DROP TABLE payment_order_reserve_entries;

ALTER TABLE payment_funds_receipts
    DROP CONSTRAINT payment_funds_receipts_paypal_economics_check,
    DROP COLUMN processor_fee_minor,
    DROP COLUMN net_receivable_minor;
