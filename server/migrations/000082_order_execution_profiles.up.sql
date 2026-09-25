-- Live-switch readiness: every AgencyOrder owns one immutable execution
-- profile. Deployment configuration only chooses the profile for new orders;
-- payment/procurement handoffs carry the order's profile hash.

ALTER TABLE agency_orders
    ADD COLUMN payment_rail TEXT,
    ADD COLUMN provider_environment TEXT,
    ADD COLUMN asset TEXT,
    ADD COLUMN economic_effect TEXT,
    ADD COLUMN merchant_execution_mode TEXT,
    ADD COLUMN execution_profile_hash TEXT;

UPDATE agency_orders SET
    payment_rail = snapshot->'paymentSelection'->>'rail',
    provider_environment = snapshot->'paymentSelection'->>'providerEnvironment',
    asset = snapshot->'paymentSelection'->>'asset',
    economic_effect = snapshot->'paymentSelection'->>'economicEffect',
    merchant_execution_mode = CASE snapshot->'paymentSelection'->>'merchantExecution'
        WHEN 'SIMULATED' THEN 'SIMULATED_NO_EFFECT'
        WHEN 'SIMULATED_NO_EFFECT' THEN 'SIMULATED_NO_EFFECT'
        WHEN 'LIVE' THEN 'LIVE_MERCHANT_EFFECT'
        WHEN 'LIVE_MERCHANT_EFFECT' THEN 'LIVE_MERCHANT_EFFECT'
        ELSE NULL
    END,
    execution_profile_hash = CASE
        WHEN snapshot->'paymentSelection'->>'rail'='PAYPAL'
         AND snapshot->'paymentSelection'->>'providerEnvironment'='SANDBOX'
         AND snapshot->'paymentSelection'->>'asset'='USD'
         AND snapshot->'paymentSelection'->>'economicEffect'='NO_REAL_VALUE'
        THEN '0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665'
        WHEN snapshot->'paymentSelection'->>'rail'='GIWA'
         AND snapshot->'paymentSelection'->>'providerEnvironment' IN ('TEST','TESTNET')
         AND snapshot->'paymentSelection'->>'asset'='TVITUSD'
         AND snapshot->'paymentSelection'->>'economicEffect'='NO_REAL_VALUE'
        THEN '0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732'
        ELSE NULL
    END;

-- Older GIWA payment facts used the vague TEST label. Normalize them to the
-- exact order profile environment before adding cross-owner constraints.
-- The pre-000082 constraints only admit SANDBOX/TEST, so remove those checks
-- before writing TESTNET. They are recreated below after profile backfill.
ALTER TABLE payment_customer_payments
    DROP CONSTRAINT payment_customer_payments_provider_environment_check;
ALTER TABLE payment_funds_receipts
    DROP CONSTRAINT payment_funds_receipts_provider_environment_check;
UPDATE payment_customer_payments
SET provider_environment='TESTNET'
WHERE rail='GIWA' AND provider_environment='TEST';
UPDATE payment_funds_receipts
SET provider_environment='TESTNET'
WHERE kind='GIWA_FINALIZED_PAY' AND provider_environment='TEST';

ALTER TABLE agency_orders
    ALTER COLUMN payment_rail SET NOT NULL,
    ALTER COLUMN provider_environment SET NOT NULL,
    ALTER COLUMN asset SET NOT NULL,
    ALTER COLUMN economic_effect SET NOT NULL,
    ALTER COLUMN merchant_execution_mode SET NOT NULL,
    ALTER COLUMN execution_profile_hash SET NOT NULL,
    ADD CONSTRAINT agency_orders_execution_profile_check CHECK (
        (payment_rail='PAYPAL' AND provider_environment='SANDBOX' AND asset='USD'
         AND economic_effect='NO_REAL_VALUE'
         AND merchant_execution_mode='SIMULATED_NO_EFFECT'
         AND execution_profile_hash='0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665')
        OR
        (payment_rail='GIWA' AND provider_environment='TESTNET' AND asset='TVITUSD'
         AND economic_effect='NO_REAL_VALUE'
         AND merchant_execution_mode='SIMULATED_NO_EFFECT'
         AND execution_profile_hash='0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732')
        OR
        (payment_rail='PAYPAL' AND provider_environment='LIVE' AND asset='USD'
         AND economic_effect='REAL_MONEY'
         AND merchant_execution_mode='LIVE_MERCHANT_EFFECT'
         AND execution_profile_hash='0xba51a8eb9a32c1c6ede94a0ad7b8eb75b81ab1a1028dd7c536219895c37f096a')
    ),
    ADD CONSTRAINT agency_orders_id_profile_unique UNIQUE (id, execution_profile_hash),
    ADD CONSTRAINT agency_orders_full_profile_unique UNIQUE (
        id, payment_rail, provider_environment, asset, economic_effect,
        merchant_execution_mode, execution_profile_hash
    );

CREATE FUNCTION reject_agency_order_execution_profile_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(NEW.payment_rail, NEW.provider_environment, NEW.asset,
           NEW.economic_effect, NEW.merchant_execution_mode,
           NEW.execution_profile_hash)
       IS DISTINCT FROM
       ROW(OLD.payment_rail, OLD.provider_environment, OLD.asset,
           OLD.economic_effect, OLD.merchant_execution_mode,
           OLD.execution_profile_hash) THEN
        RAISE EXCEPTION 'agency order execution profile is immutable'
            USING ERRCODE = '23000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_agency_order_execution_profile_immutable
BEFORE UPDATE OF payment_rail, provider_environment, asset, economic_effect,
    merchant_execution_mode, execution_profile_hash
ON agency_orders
FOR EACH ROW EXECUTE FUNCTION reject_agency_order_execution_profile_change();

-- Terminal receipts carry the exact order profile too. TEST remains confined
-- to no-real-value orders; a PayPal Live order receives a distinct real-money
-- agency-order record and is never relabelled by a later deployment selector.
ALTER TABLE agency_order_receipts
    ADD COLUMN payment_rail TEXT,
    ADD COLUMN provider_environment TEXT,
    ADD COLUMN asset TEXT,
    ADD COLUMN economic_effect TEXT,
    ADD COLUMN merchant_execution_mode TEXT,
    ADD COLUMN execution_profile_hash TEXT;

UPDATE agency_order_receipts receipt SET
    payment_rail=orders.payment_rail,
    provider_environment=orders.provider_environment,
    asset=orders.asset,
    economic_effect=orders.economic_effect,
    merchant_execution_mode=orders.merchant_execution_mode,
    execution_profile_hash=orders.execution_profile_hash
FROM agency_orders orders
WHERE orders.id=receipt.agency_order_id;

ALTER TABLE agency_order_receipts
    DROP CONSTRAINT agency_order_receipts_kind_check,
    DROP CONSTRAINT agency_order_receipts_legal_sale_check,
    ALTER COLUMN payment_rail SET NOT NULL,
    ALTER COLUMN provider_environment SET NOT NULL,
    ALTER COLUMN asset SET NOT NULL,
    ALTER COLUMN economic_effect SET NOT NULL,
    ALTER COLUMN merchant_execution_mode SET NOT NULL,
    ALTER COLUMN execution_profile_hash SET NOT NULL,
    ADD CONSTRAINT agency_order_receipts_execution_profile_check CHECK (
        (kind='TEST' AND legal_sale=FALSE
         AND payment_rail='PAYPAL' AND provider_environment='SANDBOX' AND asset='USD'
         AND economic_effect='NO_REAL_VALUE'
         AND merchant_execution_mode='SIMULATED_NO_EFFECT'
         AND execution_profile_hash='0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665')
        OR
        (kind='TEST' AND legal_sale=FALSE
         AND payment_rail='GIWA' AND provider_environment='TESTNET' AND asset='TVITUSD'
         AND economic_effect='NO_REAL_VALUE'
         AND merchant_execution_mode='SIMULATED_NO_EFFECT'
         AND execution_profile_hash='0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732')
        OR
        (kind='LIVE_ORDER_RECORD'
         AND payment_rail='PAYPAL' AND provider_environment='LIVE' AND asset='USD'
         AND economic_effect='REAL_MONEY'
         AND merchant_execution_mode='LIVE_MERCHANT_EFFECT'
         AND execution_profile_hash='0xba51a8eb9a32c1c6ede94a0ad7b8eb75b81ab1a1028dd7c536219895c37f096a')
    ),
    ADD CONSTRAINT agency_order_receipts_order_profile_fk FOREIGN KEY (
        agency_order_id, payment_rail, provider_environment, asset,
        economic_effect, merchant_execution_mode, execution_profile_hash
    ) REFERENCES agency_orders(
        id, payment_rail, provider_environment, asset, economic_effect,
        merchant_execution_mode, execution_profile_hash
    ) ON DELETE RESTRICT;

ALTER TABLE agency_order_payment_instructions
    ADD COLUMN provider_environment TEXT,
    ADD COLUMN economic_effect TEXT,
    ADD COLUMN merchant_execution_mode TEXT,
    ADD COLUMN execution_profile_hash TEXT;

UPDATE agency_order_payment_instructions instruction SET
    provider_environment=orders.provider_environment,
    economic_effect=orders.economic_effect,
    merchant_execution_mode=orders.merchant_execution_mode,
    execution_profile_hash=orders.execution_profile_hash
FROM agency_orders orders
WHERE orders.id=instruction.agency_order_id;

ALTER TABLE agency_order_payment_instructions
    ALTER COLUMN provider_environment SET NOT NULL,
    ALTER COLUMN economic_effect SET NOT NULL,
    ALTER COLUMN merchant_execution_mode SET NOT NULL,
    ALTER COLUMN execution_profile_hash SET NOT NULL,
    ADD CONSTRAINT agency_order_payment_instructions_profile_check CHECK (
        (rail='PAYPAL' AND provider_environment='SANDBOX' AND asset='USD'
         AND economic_effect='NO_REAL_VALUE'
         AND merchant_execution_mode='SIMULATED_NO_EFFECT'
         AND execution_profile_hash='0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665')
        OR
        (rail='GIWA' AND provider_environment='TESTNET' AND asset='TVITUSD'
         AND economic_effect='NO_REAL_VALUE'
         AND merchant_execution_mode='SIMULATED_NO_EFFECT'
         AND execution_profile_hash='0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732')
        OR
        (rail='PAYPAL' AND provider_environment='LIVE' AND asset='USD'
         AND economic_effect='REAL_MONEY'
         AND merchant_execution_mode='LIVE_MERCHANT_EFFECT'
         AND execution_profile_hash='0xba51a8eb9a32c1c6ede94a0ad7b8eb75b81ab1a1028dd7c536219895c37f096a')
    ),
    ADD CONSTRAINT agency_order_payment_instructions_order_profile_fk FOREIGN KEY (
        agency_order_id, rail, provider_environment, asset, economic_effect,
        merchant_execution_mode, execution_profile_hash
    ) REFERENCES agency_orders(
        id, payment_rail, provider_environment, asset, economic_effect,
        merchant_execution_mode, execution_profile_hash
    ) ON DELETE RESTRICT;

ALTER TABLE payment_customer_payments
    ADD COLUMN merchant_execution_mode TEXT,
    ADD COLUMN execution_profile_hash TEXT;

UPDATE payment_customer_payments payment SET
    merchant_execution_mode=orders.merchant_execution_mode,
    execution_profile_hash=orders.execution_profile_hash
FROM agency_orders orders
WHERE orders.id=payment.agency_order_id;

ALTER TABLE payment_customer_payments
    DROP CONSTRAINT payment_customer_payments_economic_effect_check,
    ALTER COLUMN merchant_execution_mode SET NOT NULL,
    ALTER COLUMN execution_profile_hash SET NOT NULL,
    ADD CONSTRAINT payment_customer_payments_provider_environment_check
        CHECK (provider_environment IN ('SANDBOX','LIVE','TESTNET')),
    ADD CONSTRAINT payment_customer_payments_economic_effect_check
        CHECK (economic_effect IN ('NO_REAL_VALUE','REAL_MONEY')),
    ADD CONSTRAINT payment_customer_payments_execution_profile_check CHECK (
        (rail='PAYPAL' AND provider_environment='SANDBOX' AND asset='USD'
         AND economic_effect='NO_REAL_VALUE'
         AND merchant_execution_mode='SIMULATED_NO_EFFECT'
         AND execution_profile_hash='0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665')
        OR
        (rail='GIWA' AND provider_environment='TESTNET' AND asset='TVITUSD'
         AND economic_effect='NO_REAL_VALUE'
         AND merchant_execution_mode='SIMULATED_NO_EFFECT'
         AND execution_profile_hash='0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732')
        OR
        (rail='PAYPAL' AND provider_environment='LIVE' AND asset='USD'
         AND economic_effect='REAL_MONEY'
         AND merchant_execution_mode='LIVE_MERCHANT_EFFECT'
         AND execution_profile_hash='0xba51a8eb9a32c1c6ede94a0ad7b8eb75b81ab1a1028dd7c536219895c37f096a')
    ),
    ADD CONSTRAINT payment_customer_payments_order_profile_fk FOREIGN KEY (
        agency_order_id, rail, provider_environment, asset, economic_effect,
        merchant_execution_mode, execution_profile_hash
    ) REFERENCES agency_orders(
        id, payment_rail, provider_environment, asset, economic_effect,
        merchant_execution_mode, execution_profile_hash
    ) ON DELETE RESTRICT,
    ADD CONSTRAINT payment_customer_payments_receipt_profile_unique UNIQUE (
        id, agency_order_id, provider_environment, execution_profile_hash
    );

ALTER TABLE payment_funds_receipts
    ADD COLUMN execution_profile_hash TEXT;

UPDATE payment_funds_receipts receipt SET
    execution_profile_hash=orders.execution_profile_hash
FROM agency_orders orders
WHERE orders.id=receipt.agency_order_id;

ALTER TABLE payment_funds_receipts
    ALTER COLUMN execution_profile_hash SET NOT NULL,
    ADD CONSTRAINT payment_funds_receipts_provider_environment_check
        CHECK (provider_environment IN ('SANDBOX','LIVE','TESTNET')),
    ADD CONSTRAINT payment_funds_receipts_payment_profile_fk FOREIGN KEY (
        customer_payment_id, agency_order_id, provider_environment, execution_profile_hash
    ) REFERENCES payment_customer_payments(
        id, agency_order_id, provider_environment, execution_profile_hash
    ) ON DELETE RESTRICT;

ALTER TABLE procurement_manifests
    ADD COLUMN execution_profile_hash TEXT;
UPDATE procurement_manifests manifest SET
    execution_profile_hash=orders.execution_profile_hash
FROM agency_orders orders
WHERE orders.id=manifest.agency_order_id;
ALTER TABLE procurement_manifests
    ALTER COLUMN execution_profile_hash SET NOT NULL,
    ADD CONSTRAINT procurement_manifests_order_profile_fk FOREIGN KEY (
        agency_order_id, execution_profile_hash
    ) REFERENCES agency_orders(id, execution_profile_hash) ON DELETE RESTRICT;
