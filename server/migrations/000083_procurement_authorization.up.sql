-- Customer-approved manual procurement scope. The Shopify checkout is quote
-- evidence/reference only; this immutable row is the operator authorization.
--
-- Orders issued before this migration did not collect the v1 agency/privacy
-- consent fields. They receive an explicitly non-v1 legacy authorization only
-- when their immutable execution profile has no real economic or merchant
-- effect. This keeps existing Sandbox/GIWA work operable without inventing
-- customer consent, and makes the legacy row unusable by the PayPal Live
-- profile at the database boundary.
CREATE TABLE agency_order_procurement_authorizations (
    agency_order_id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    order_sheet_session_id UUID NOT NULL UNIQUE
        REFERENCES agency_order_sheet_sessions(id) ON DELETE RESTRICT,
    authorization_kind TEXT NOT NULL CHECK (authorization_kind IN (
        'MANUAL_OPERATOR_PURCHASE','LEGACY_NO_REAL_VALUE_V0'
    )),
    authorization_hash TEXT NOT NULL UNIQUE CHECK (length(authorization_hash) >= 32),
    execution_profile_hash TEXT NOT NULL CHECK (length(execution_profile_hash) >= 32),
    source_cart_snapshot_hash TEXT NOT NULL,
    displayed_snapshot_hash TEXT NOT NULL,
    order_snapshot_hash TEXT NOT NULL UNIQUE,
    locale TEXT,
    copy_version TEXT NOT NULL,
    accepted_at TIMESTAMPTZ NOT NULL,
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (agency_order_id, execution_profile_hash)
        REFERENCES agency_orders(id, execution_profile_hash) ON DELETE RESTRICT,
    CHECK (payload->>'kind' IS NOT DISTINCT FROM authorization_kind),
    CHECK (payload->>'authorizationHash' IS NOT DISTINCT FROM authorization_hash),
    CHECK (payload->>'executionProfileHash' IS NOT DISTINCT FROM execution_profile_hash),
    CHECK (payload->>'sourceCartSnapshotHash' IS NOT DISTINCT FROM source_cart_snapshot_hash),
    CHECK (payload->>'displayedSnapshotHash' IS NOT DISTINCT FROM displayed_snapshot_hash),
    CHECK (payload->>'orderSheetSessionId' IS NOT DISTINCT FROM order_sheet_session_id::text),
    CHECK ((payload->>'acceptedAt')::timestamptz IS NOT DISTINCT FROM accepted_at),
    CHECK (CASE
        WHEN jsonb_typeof(payload->'shops') = 'array'
        THEN jsonb_array_length(payload->'shops') > 0
        ELSE FALSE
    END),
    CHECK (payload#>>'{approvedPassThrough,currency}' IS NOT DISTINCT FROM 'USD'),
    CHECK (payload#>>'{approvedAgencyFee,currency}' IS NOT DISTINCT FROM 'USD'),
    CHECK (payload#>>'{approvedCustomerPayable,currency}' IS NOT DISTINCT FROM 'USD'),
    CHECK (
        (
            authorization_kind = 'MANUAL_OPERATOR_PURCHASE'
            AND locale IN ('en-US','ko-KR')
            AND copy_version = 'procurement-authorization.v1'
            AND jsonb_typeof(payload->'customerApproval') IS NOT DISTINCT FROM 'object'
            AND payload#>>'{customerApproval,locale}' IS NOT DISTINCT FROM locale
            AND payload#>>'{customerApproval,copyVersion}' IS NOT DISTINCT FROM copy_version
            AND (payload#>>'{customerApproval,agencyConsent}')::boolean IS TRUE
            AND (payload#>>'{customerApproval,privacyConsent}')::boolean IS TRUE
            AND char_length(COALESCE(payload#>>'{customerApproval,orderMessage}', '')) <= 500
            AND char_length(COALESCE(payload#>>'{customerApproval,deliveryMessage}', '')) <= 500
        ) OR (
            authorization_kind = 'LEGACY_NO_REAL_VALUE_V0'
            AND execution_profile_hash IN (
                '0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665',
                '0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732'
            )
            AND locale IS NULL
            AND copy_version = 'legacy-no-real-value.v0'
            AND payload->>'copyVersion' IS NOT DISTINCT FROM copy_version
            AND NOT (payload ? 'customerApproval')
            AND payload->>'agencyOrderId' IS NOT DISTINCT FROM agency_order_id::text
            AND payload->>'orderSnapshotHash' IS NOT DISTINCT FROM order_snapshot_hash
            AND jsonb_typeof(payload->'legacyEvidence') IS NOT DISTINCT FROM 'object'
            AND payload#>>'{legacyEvidence,authorizationBasis}'
                IS NOT DISTINCT FROM 'PRE_V1_ORDER_ISSUANCE'
            AND payload#>>'{legacyEvidence,timestampBasis}'
                IS NOT DISTINCT FROM 'AGENCY_ORDER_ISSUED_AT'
            AND (payload#>>'{legacyEvidence,v1CustomerApprovalRecorded}')::boolean IS FALSE
            AND payload#>>'{legacyEvidence,economicEffect}'
                IS NOT DISTINCT FROM 'NO_REAL_VALUE'
            AND payload#>>'{legacyEvidence,merchantExecutionMode}'
                IS NOT DISTINCT FROM 'SIMULATED_NO_EFFECT'
        )
    )
);

-- Build the hash from the exact immutable legacy payload with an empty hash
-- field, then embed the digest. jsonb::text is deterministic on the supported
-- PostgreSQL 16 migration runtime. The payload labels its evidence basis and
-- explicitly records that v1 consent was not collected.
WITH legacy_payloads AS (
    SELECT
        orders.id AS agency_order_id,
        orders.user_id,
        orders.order_sheet_session_id,
        orders.execution_profile_hash,
        orders.source_cart_snapshot_hash,
        orders.snapshot#>>'{issuanceEvidence,displayedSnapshotHash}'
            AS displayed_snapshot_hash,
        orders.snapshot_hash AS order_snapshot_hash,
        orders.issued_at AS accepted_at,
        jsonb_build_object(
            'kind', 'LEGACY_NO_REAL_VALUE_V0',
            'agencyOrderId', orders.id::text,
            'shops', orders.snapshot->'merchantCheckouts',
            'approvedPassThrough', orders.snapshot->'passThroughTotal',
            'approvedAgencyFee', orders.snapshot#>'{agencyFee,total}',
            'approvedCustomerPayable', orders.snapshot->'customerPayableTotal',
            'orderSheetSessionId', orders.order_sheet_session_id::text,
            'sourceCartSnapshotHash', orders.source_cart_snapshot_hash,
            'displayedSnapshotHash',
                orders.snapshot#>>'{issuanceEvidence,displayedSnapshotHash}',
            'executionProfileHash', orders.execution_profile_hash,
            'acceptedAt', to_jsonb(orders.issued_at),
            'authorizationHash', '',
            'copyVersion', 'legacy-no-real-value.v0',
            'legacyEvidence', jsonb_build_object(
                'authorizationBasis', 'PRE_V1_ORDER_ISSUANCE',
                'timestampBasis', 'AGENCY_ORDER_ISSUED_AT',
                'v1CustomerApprovalRecorded', FALSE,
                'economicEffect', orders.economic_effect,
                'merchantExecutionMode', orders.merchant_execution_mode,
                'orderSnapshotHash', orders.snapshot_hash,
                'disclosureVersion', orders.snapshot#>>'{issuanceEvidence,disclosureVersion}'
            ),
            'orderSnapshotHash', orders.snapshot_hash
        ) AS base_payload
    FROM agency_orders orders
    WHERE orders.economic_effect = 'NO_REAL_VALUE'
      AND orders.merchant_execution_mode = 'SIMULATED_NO_EFFECT'
), legacy_hashed AS (
    SELECT legacy_payloads.*,
           '0x' || encode(
               sha256(convert_to(base_payload::text, 'UTF8')),
               'hex'
           ) AS authorization_hash
    FROM legacy_payloads
)
INSERT INTO agency_order_procurement_authorizations(
    agency_order_id, user_id, order_sheet_session_id, authorization_kind,
    authorization_hash, execution_profile_hash, source_cart_snapshot_hash,
    displayed_snapshot_hash, order_snapshot_hash, locale, copy_version,
    accepted_at, payload, created_at
)
SELECT
    agency_order_id, user_id, order_sheet_session_id,
    'LEGACY_NO_REAL_VALUE_V0', authorization_hash, execution_profile_hash,
    source_cart_snapshot_hash, displayed_snapshot_hash, order_snapshot_hash,
    NULL, 'legacy-no-real-value.v0', accepted_at,
    base_payload || jsonb_build_object('authorizationHash', authorization_hash),
    accepted_at
FROM legacy_hashed;

-- The compatibility kind is migration-owned. Once the populated-row backfill
-- above finishes, no newly issued order (including a new Sandbox order) may
-- mint it; all runtime issuance must use MANUAL_OPERATOR_PURCHASE v1.
CREATE FUNCTION reject_legacy_procurement_authorization_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.authorization_kind = 'LEGACY_NO_REAL_VALUE_V0' THEN
        RAISE EXCEPTION 'legacy procurement authorization is migration-only'
            USING ERRCODE = '23000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_procurement_authorization_legacy_insert
BEFORE INSERT ON agency_order_procurement_authorizations
FOR EACH ROW EXECUTE FUNCTION reject_legacy_procurement_authorization_insert();

CREATE INDEX idx_procurement_authorizations_user_accepted
    ON agency_order_procurement_authorizations(user_id, accepted_at DESC);

CREATE FUNCTION reject_procurement_authorization_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- The local development-profile reset purges the complete test graph in
    -- one transaction. It may delete immutable evidence, but it must never
    -- make that evidence mutable or open a production deletion path.
    IF TG_OP = 'DELETE'
       AND current_setting('vitlane.account_reset', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'procurement authorization is immutable'
        USING ERRCODE = '23000';
END;
$$;

CREATE TRIGGER trg_procurement_authorization_immutable
BEFORE UPDATE OR DELETE ON agency_order_procurement_authorizations
FOR EACH ROW EXECUTE FUNCTION reject_procurement_authorization_update();
