DROP TABLE IF EXISTS agency_order_audits;
DROP TABLE IF EXISTS agency_order_outbox;
-- AgencyOrder-backed rows cannot satisfy the restored purchase_id NOT NULL
-- constraint. Remove only this migration's source rows before dropping the
-- source discriminator; legacy Purchase-backed settlement rows are preserved.
DELETE FROM settlement_payments WHERE agency_order_id IS NOT NULL;
DELETE FROM settlement_authorizations WHERE agency_order_id IS NOT NULL;
ALTER TABLE settlement_payments DROP CONSTRAINT IF EXISTS settlement_payments_one_source;
ALTER TABLE settlement_payments DROP COLUMN IF EXISTS agency_order_id;
ALTER TABLE settlement_payments ALTER COLUMN purchase_id SET NOT NULL;
ALTER TABLE settlement_authorizations DROP CONSTRAINT IF EXISTS settlement_authorizations_one_source;
ALTER TABLE settlement_authorizations DROP COLUMN IF EXISTS agency_order_id;
ALTER TABLE settlement_authorizations ALTER COLUMN purchase_id SET NOT NULL;
DROP TABLE IF EXISTS agency_order_payment_consents;
DROP TABLE IF EXISTS agency_order_payment_instructions;
DROP TABLE IF EXISTS agency_orders;
DROP TABLE IF EXISTS agency_order_provider_capabilities;
DROP TABLE IF EXISTS agency_order_sheet_sessions;
