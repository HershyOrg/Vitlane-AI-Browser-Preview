-- 외부 money fact를 지우는 down migration은 만들지 않는다. 이 down은
-- PayPal row가 0건인 pre-effect 상태에서만 유효하다 (docs/phases/phase-8 §16.2-8).
ALTER TABLE agency_order_receipts
    DROP CONSTRAINT agency_order_receipts_single_payment_source,
    DROP COLUMN customer_payment_id;
ALTER TABLE agency_order_receipts
    ALTER COLUMN settlement_payment_id SET NOT NULL;

ALTER TABLE agency_order_execution_units
    DROP CONSTRAINT agency_order_execution_units_single_payment_source,
    DROP COLUMN customer_payment_id;

ALTER TABLE agency_order_payment_instructions
    DROP CONSTRAINT agency_order_payment_instructions_rail_check,
    DROP CONSTRAINT agency_order_payment_instructions_asset_check,
    ADD CONSTRAINT agency_order_payment_instructions_rail_check CHECK (rail = 'GIWA'),
    ADD CONSTRAINT agency_order_payment_instructions_asset_check CHECK (asset = 'TVITUSD');

DROP TABLE payment_paypal_webhook_inbox;
DROP TABLE payment_customer_refund_attempts;
DROP TABLE payment_customer_refunds;
DROP TABLE payment_funds_receipts;
DROP TABLE payment_external_operations;
DROP TABLE payment_paypal_attempts;
DROP TABLE payment_customer_payments;
DROP TABLE paypal_account_bindings;
