-- ADR-0054: simulation-stage destructive hard cutover.
--
-- Purchase/Fulfillment rows are not production facts and are intentionally
-- discarded without a compatibility phase. AgencyOrder is the only
-- settlement owner after this migration.

DROP TABLE IF EXISTS merchant_shipment_events CASCADE;
DROP TABLE IF EXISTS fulfillment_assignments CASCADE;
DROP TABLE IF EXISTS fulfillment_results CASCADE;
DROP TABLE IF EXISTS fulfillment_attempts CASCADE;
DROP TABLE IF EXISTS fulfillment_audit_events CASCADE;
DROP TABLE IF EXISTS fulfillment_operator_settings CASCADE;
DROP TABLE IF EXISTS fulfillment_requests CASCADE;
DROP TABLE IF EXISTS fulfillment_executions CASCADE;
DROP TABLE IF EXISTS pii_access_audit_events CASCADE;
DROP TABLE IF EXISTS receipts CASCADE;
DROP TABLE IF EXISTS purchase_intent_snapshots CASCADE;
DROP TABLE IF EXISTS purchase_origin_request_items CASCADE;
DROP TABLE IF EXISTS purchase_origin_requests CASCADE;
DROP TABLE IF EXISTS candidate_interaction_events CASCADE;
DROP TABLE IF EXISTS candidate_interactions CASCADE;

ALTER TABLE candidates
    DROP CONSTRAINT IF EXISTS candidates_purchase_path_current_check,
    DROP CONSTRAINT IF EXISTS candidates_current_hash_schema_check;
ALTER TABLE candidates RENAME COLUMN purchase_support TO order_support;
ALTER TABLE candidates RENAME COLUMN purchase_path TO orderability;
UPDATE candidates
SET orderability = jsonb_set(
        orderability,
        '{schemaVersion}',
        '"vitlane.orderability.v1"'::jsonb
    ),
    candidate_hash_schema = 'vitlane.candidate.v3';
ALTER TABLE candidates
    ADD CONSTRAINT candidates_orderability_current_check CHECK (
        jsonb_typeof(orderability) = 'object'
        AND orderability->>'schemaVersion' = 'vitlane.orderability.v1'
        AND COALESCE(orderability->>'providerKind', '') <> ''
        AND COALESCE(orderability->>'executionMode', '') <> ''
        AND COALESCE(orderability->>'externalEffect', '') <> ''
        AND COALESCE(orderability->>'liveOrderability', '') <> ''
        AND COALESCE(orderability->>'settlementStatus', '') <> ''
        AND COALESCE(orderability->>'status', '') <> ''
        AND jsonb_typeof(orderability->'reasonCodes') = 'array'
    ),
    ADD CONSTRAINT candidates_current_hash_schema_check CHECK (
        candidate_hash_schema = 'vitlane.candidate.v3'
    );

-- Simulation-only transcript rows for removed commands have no archival
-- value. Delete them and narrow the closed action catalog in place.
DELETE FROM curation_actions
WHERE action_type IN (
        'CANDIDATE_INTERACTION',
        'PURCHASE_DIRECT',
        'PURCHASE_CART'
    )
   OR effect_kind = 'PURCHASE_THREAD'
   OR source_ref_type IN (
        'CANDIDATE_INTERACTION_EVENT',
        'PURCHASE_ORIGIN_REQUEST'
    );

ALTER TABLE curation_actions
    DROP CONSTRAINT IF EXISTS curation_actions_action_type_check,
    DROP CONSTRAINT IF EXISTS curation_actions_effect_kind_check,
    ADD CONSTRAINT curation_actions_action_type_check CHECK (
        action_type IN (
            'INTENT_NEXT_STEP',
            'PLANNING_ADD_TARGETS',
            'PLANNING_START_CURATING',
            'CURATION_ADD_TARGETS',
            'TARGET_RESEARCH_AGAIN',
            'TARGET_REMOVE',
            'SELECTION_MUTATION'
        )
    ),
    ADD CONSTRAINT curation_actions_effect_kind_check CHECK (
        effect_kind IN ('NONE', 'INTELLIGENCE')
    );

-- Child chain/outbox/reconcile rows cascade from settlement_payments. Any
-- remaining Purchase authorization without a payment is removed explicitly.
DELETE FROM settlement_payments WHERE purchase_id IS NOT NULL;
DELETE FROM settlement_authorizations WHERE purchase_id IS NOT NULL;

ALTER TABLE settlement_authorizations
    DROP CONSTRAINT IF EXISTS settlement_authorizations_one_source,
    DROP COLUMN purchase_id,
    ALTER COLUMN agency_order_id SET NOT NULL;

ALTER TABLE settlement_payments
    DROP CONSTRAINT IF EXISTS settlement_payments_one_source,
    DROP COLUMN purchase_id,
    ALTER COLUMN agency_order_id SET NOT NULL;

DROP TABLE IF EXISTS user_approvals CASCADE;
DROP TABLE IF EXISTS checkout_quotes CASCADE;
DROP TABLE IF EXISTS purchases CASCADE;

COMMENT ON COLUMN settlement_authorizations.agency_order_id IS
    'Canonical AgencyOrder owner. No legacy source discriminator exists.';
COMMENT ON COLUMN settlement_payments.agency_order_id IS
    'Canonical AgencyOrder owner. No legacy source discriminator exists.';
