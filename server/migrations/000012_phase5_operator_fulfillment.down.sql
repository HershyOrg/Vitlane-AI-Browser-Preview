DROP TABLE IF EXISTS fulfillment_audit_events;
DROP TABLE IF EXISTS fulfillment_operator_settings;

DROP INDEX IF EXISTS idx_fulfillment_executions_pending;
DROP INDEX IF EXISTS idx_fulfillment_executions_idempotency;

ALTER TABLE fulfillment_executions
    DROP CONSTRAINT IF EXISTS fulfillment_executions_terminal_fields_check,
    DROP CONSTRAINT IF EXISTS fulfillment_executions_no_merchant_order_check,
    DROP CONSTRAINT IF EXISTS fulfillment_executions_processing_mode_check,
    DROP CONSTRAINT IF EXISTS fulfillment_executions_state_check,
    DROP CONSTRAINT IF EXISTS fulfillment_executions_mode_check;

UPDATE fulfillment_executions
SET mode='MANUAL_PURCHASE_ASSUMED',
    state=CASE
        WHEN state='PURCHASE_ASSUMED' THEN 'MANUAL_PURCHASE_ASSUMED'
        ELSE state
    END;

ALTER TABLE fulfillment_executions
    DROP COLUMN IF EXISTS external_order_reference,
    DROP COLUMN IF EXISTS merchant_order_created,
    DROP COLUMN IF EXISTS eligible_at,
    DROP COLUMN IF EXISTS result_payload,
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS quote_snapshot,
    DROP COLUMN IF EXISTS quote_hash,
    DROP COLUMN IF EXISTS quote_id,
    DROP COLUMN IF EXISTS handled_at,
    DROP COLUMN IF EXISTS operator_user_id,
    DROP COLUMN IF EXISTS processing_mode;

ALTER TABLE fulfillment_executions
    ADD CONSTRAINT fulfillment_executions_mode_check
        CHECK (mode = 'MANUAL_PURCHASE_ASSUMED'),
    ADD CONSTRAINT fulfillment_executions_state_check
        CHECK (state IN ('PENDING', 'RUNNING', 'MANUAL_PURCHASE_ASSUMED', 'FAILED'));

ALTER TABLE merchant_registry_entries
    DROP CONSTRAINT merchant_registry_entries_fulfillment_mode_check;
UPDATE merchant_registry_entries
SET fulfillment_mode='MANUAL_PURCHASE_ASSUMED'
WHERE fulfillment_mode='TEST_PURCHASE_ASSUMPTION';
ALTER TABLE merchant_registry_entries
    ADD CONSTRAINT merchant_registry_entries_fulfillment_mode_check
    CHECK (fulfillment_mode = 'MANUAL_PURCHASE_ASSUMED');
