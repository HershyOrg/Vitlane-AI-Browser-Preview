DROP TABLE IF EXISTS kyc_verification_audit_events;
DROP TABLE IF EXISTS kyc_verification_cases;

ALTER TABLE identity_assurances
    DROP CONSTRAINT identity_assurances_level_check;

UPDATE identity_assurances
SET level='MOCK_KYC'
WHERE level='MOCK_DOJANG_VERIFIED';
ALTER TABLE identity_assurances
    ADD CONSTRAINT identity_assurances_level_check
    CHECK (level IN (
        'DOJANG_VERIFIED_ADDRESS',
        'DOJANG_TEST_FAUCET',
        'MOCK_KYC',
        'WALLET_OWNERSHIP_ONLY'
    ));

DROP TABLE IF EXISTS merchant_shipment_events;
DROP TABLE IF EXISTS pii_access_audit_events;
DROP TABLE IF EXISTS fulfillment_assignments;

ALTER TABLE fulfillment_operator_settings
    DROP CONSTRAINT fulfillment_operator_settings_automation_mode_check;

ALTER TABLE fulfillment_operator_settings
    ADD CONSTRAINT fulfillment_operator_settings_automation_mode_check
    CHECK (automation_mode IN ('MANUAL_OPERATOR', 'AUTO_TEST'));

ALTER TABLE fulfillment_audit_events
    DROP CONSTRAINT fulfillment_audit_events_action_check;

UPDATE fulfillment_audit_events
SET action='PURCHASE_ASSUMED'
WHERE action='MERCHANT_ORDER_ACCEPTED';
DELETE FROM fulfillment_audit_events
WHERE action IN ('OPERATOR_ASSIGNED', 'MERCHANT_ORDER_UNKNOWN');

ALTER TABLE fulfillment_audit_events
    ADD CONSTRAINT fulfillment_audit_events_action_check
    CHECK (action IN (
        'AUTOMATION_CHANGED', 'PURCHASE_ASSUMED', 'FULFILLMENT_FAILED'
    ));

ALTER TABLE fulfillment_results
    DROP CONSTRAINT fulfillment_results_outcome_check,
    DROP CONSTRAINT fulfillment_results_result_kind_check,
    DROP CONSTRAINT fulfillment_results_effect_check,
    DROP CONSTRAINT fulfillment_results_mock_result_check;

UPDATE fulfillment_results
SET outcome=CASE WHEN outcome='ORDER_ACCEPTED' THEN 'PURCHASE_ASSUMED' ELSE outcome END,
    result_kind='TEST_NO_MERCHANT_ORDER'
WHERE outcome IN ('ORDER_ACCEPTED', 'FAILED');
DELETE FROM fulfillment_results WHERE outcome='UNKNOWN_NEEDS_RECONCILIATION';

ALTER TABLE fulfillment_results
    DROP COLUMN external_effect,
    DROP COLUMN merchant_order_flow_completed,
    DROP COLUMN mock_order_reference;
ALTER TABLE fulfillment_results
    ADD CONSTRAINT fulfillment_results_outcome_check
        CHECK (outcome IN ('PURCHASE_ASSUMED', 'FAILED')),
    ADD CONSTRAINT fulfillment_results_result_kind_check
        CHECK (result_kind = 'TEST_NO_MERCHANT_ORDER'),
    ADD CONSTRAINT fulfillment_results_merchant_order_created_check
        CHECK (merchant_order_created = FALSE),
    ADD CONSTRAINT fulfillment_results_external_order_reference_check
        CHECK (external_order_reference IS NULL);

ALTER TABLE fulfillment_attempts
    DROP CONSTRAINT fulfillment_attempts_adapter_kind_check;
UPDATE fulfillment_attempts SET adapter_kind='TEST_NO_MERCHANT_ORDER';
ALTER TABLE fulfillment_attempts
    ADD CONSTRAINT fulfillment_attempts_adapter_kind_check
    CHECK (adapter_kind IN ('TEST_NO_MERCHANT_ORDER', 'MANUAL_OPERATOR'));

ALTER TABLE fulfillment_executions
    DROP CONSTRAINT fulfillment_executions_mode_check,
    DROP CONSTRAINT fulfillment_executions_state_check,
    DROP CONSTRAINT fulfillment_executions_simulated_effect_check,
    DROP CONSTRAINT fulfillment_executions_mock_result_check;
UPDATE fulfillment_executions
SET mode='TEST_PURCHASE_ASSUMPTION',
    state=CASE WHEN state='ORDER_ACCEPTED' THEN 'PURCHASE_ASSUMED' ELSE state END
WHERE state <> 'UNKNOWN_NEEDS_RECONCILIATION';
DELETE FROM fulfillment_executions WHERE state='UNKNOWN_NEEDS_RECONCILIATION';
ALTER TABLE fulfillment_executions
    DROP COLUMN external_effect,
    DROP COLUMN adapter_kind,
    DROP COLUMN merchant_order_flow_completed,
    DROP COLUMN mock_order_reference;
ALTER TABLE fulfillment_executions
    ADD CONSTRAINT fulfillment_executions_mode_check
        CHECK (mode = 'TEST_PURCHASE_ASSUMPTION'),
    ADD CONSTRAINT fulfillment_executions_state_check
        CHECK (state IN ('PENDING', 'RUNNING', 'PURCHASE_ASSUMED', 'FAILED')),
    ADD CONSTRAINT fulfillment_executions_no_merchant_order_check
        CHECK (merchant_order_created = FALSE AND external_order_reference IS NULL);

ALTER TABLE merchant_registry_entries
    DROP CONSTRAINT merchant_registry_entries_fulfillment_mode_check;
UPDATE merchant_registry_entries
SET fulfillment_mode='TEST_PURCHASE_ASSUMPTION'
WHERE fulfillment_mode='MANUAL_MERCHANT_ORDER';
ALTER TABLE merchant_registry_entries
    ADD CONSTRAINT merchant_registry_entries_fulfillment_mode_check
    CHECK (fulfillment_mode = 'TEST_PURCHASE_ASSUMPTION');

ALTER TABLE purchases
    DROP COLUMN shipping_snapshot_id,
    DROP COLUMN shipping_profile_id,
    DROP COLUMN shipping_profile_version,
    DROP COLUMN shipping_country,
    DROP COLUMN shipping_masked_summary,
    DROP COLUMN shipping_snapshot_hmac;

DROP TABLE IF EXISTS shipping_snapshots;
DROP TABLE IF EXISTS shipping_profiles;
