-- Never erase or relabel a Live/real-money order or any downstream payment
-- fact during rollback. Issuance precedes capture/receipt, so guarding only
-- terminal receipts would lose the execution identity of a perfectly valid
-- in-flight Live order. A release that has produced any such fact must be
-- forward-fixed with its matching schema.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM agency_orders
        WHERE provider_environment='LIVE' OR economic_effect='REAL_MONEY'
           OR merchant_execution_mode='LIVE_MERCHANT_EFFECT'
    ) OR EXISTS (
        SELECT 1 FROM agency_order_payment_instructions
        WHERE provider_environment='LIVE' OR economic_effect='REAL_MONEY'
           OR merchant_execution_mode='LIVE_MERCHANT_EFFECT'
    ) OR EXISTS (
        SELECT 1 FROM payment_customer_payments
        WHERE provider_environment='LIVE' OR economic_effect='REAL_MONEY'
           OR merchant_execution_mode='LIVE_MERCHANT_EFFECT'
    ) OR EXISTS (
        SELECT 1 FROM payment_funds_receipts
        WHERE provider_environment='LIVE'
    ) OR EXISTS (
        SELECT 1 FROM agency_order_receipts
        WHERE kind='LIVE_ORDER_RECORD' OR provider_environment='LIVE'
           OR economic_effect='REAL_MONEY' OR legal_sale
    ) THEN
        RAISE EXCEPTION 'cannot remove execution profiles while Live or real-money facts exist'
            USING ERRCODE = '23000';
    END IF;
END;
$$;

ALTER TABLE agency_order_receipts
    DROP CONSTRAINT agency_order_receipts_order_profile_fk,
    DROP CONSTRAINT agency_order_receipts_execution_profile_check,
    DROP COLUMN payment_rail,
    DROP COLUMN provider_environment,
    DROP COLUMN asset,
    DROP COLUMN economic_effect,
    DROP COLUMN merchant_execution_mode,
    DROP COLUMN execution_profile_hash,
    ADD CONSTRAINT agency_order_receipts_kind_check CHECK (kind='TEST'),
    ADD CONSTRAINT agency_order_receipts_legal_sale_check CHECK (legal_sale=FALSE);

ALTER TABLE procurement_manifests
    DROP CONSTRAINT procurement_manifests_order_profile_fk,
    DROP COLUMN execution_profile_hash;

DROP TRIGGER trg_agency_order_execution_profile_immutable ON agency_orders;
DROP FUNCTION reject_agency_order_execution_profile_change();

ALTER TABLE payment_funds_receipts
    DROP CONSTRAINT payment_funds_receipts_payment_profile_fk,
    DROP CONSTRAINT payment_funds_receipts_provider_environment_check,
    DROP COLUMN execution_profile_hash;
UPDATE payment_funds_receipts
SET provider_environment='TEST'
WHERE kind='GIWA_FINALIZED_PAY' AND provider_environment='TESTNET';
ALTER TABLE payment_funds_receipts
    ADD CONSTRAINT payment_funds_receipts_provider_environment_check
        CHECK (provider_environment IN ('SANDBOX','TEST'));

ALTER TABLE payment_customer_payments
    DROP CONSTRAINT payment_customer_payments_receipt_profile_unique,
    DROP CONSTRAINT payment_customer_payments_order_profile_fk,
    DROP CONSTRAINT payment_customer_payments_execution_profile_check,
    DROP CONSTRAINT payment_customer_payments_provider_environment_check,
    DROP CONSTRAINT payment_customer_payments_economic_effect_check,
    DROP COLUMN merchant_execution_mode,
    DROP COLUMN execution_profile_hash;
UPDATE payment_customer_payments
SET provider_environment='TEST'
WHERE rail='GIWA' AND provider_environment='TESTNET';
ALTER TABLE payment_customer_payments
    ADD CONSTRAINT payment_customer_payments_provider_environment_check
        CHECK (provider_environment IN ('SANDBOX','TEST')),
    ADD CONSTRAINT payment_customer_payments_economic_effect_check
        CHECK (economic_effect='NO_REAL_VALUE');

ALTER TABLE agency_order_payment_instructions
    DROP CONSTRAINT agency_order_payment_instructions_order_profile_fk,
    DROP CONSTRAINT agency_order_payment_instructions_profile_check,
    DROP COLUMN provider_environment,
    DROP COLUMN economic_effect,
    DROP COLUMN merchant_execution_mode,
    DROP COLUMN execution_profile_hash;

ALTER TABLE agency_orders
    DROP CONSTRAINT agency_orders_full_profile_unique,
    DROP CONSTRAINT agency_orders_id_profile_unique,
    DROP CONSTRAINT agency_orders_execution_profile_check,
    DROP COLUMN payment_rail,
    DROP COLUMN provider_environment,
    DROP COLUMN asset,
    DROP COLUMN economic_effect,
    DROP COLUMN merchant_execution_mode,
    DROP COLUMN execution_profile_hash;
