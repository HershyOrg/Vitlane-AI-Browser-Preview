ALTER TABLE candidates
    ADD COLUMN variant_discovery JSONB,
    ADD COLUMN purchase_path JSONB;

UPDATE candidates
SET variant_discovery = jsonb_build_object(
        'schemaVersion', 'vitlane.variant-discovery.v1',
        'status', CASE
            WHEN jsonb_array_length(variant_options) = 0 THEN 'UNKNOWN'
            ELSE 'OBSERVED_PARTIAL'
        END,
        'fields', COALESCE((
            SELECT jsonb_agg(
                jsonb_build_object(
                    'key', lower(trim(option->>'name')),
                    'label', trim(option->>'name'),
                    'inputKind', 'ENUM_OR_VALUE',
                    'required', true,
                    'knownValues', COALESCE((
                        SELECT jsonb_agg(
                            jsonb_build_object('value', value, 'label', value)
                            ORDER BY lower(value)
                        )
                        FROM jsonb_array_elements_text(
                            COALESCE(option->'values', '[]'::jsonb)
                        ) AS values(value)
                    ), '[]'::jsonb),
                    'source', 'LEGACY_VARIANT_OPTIONS',
                    'discoveryStatus', 'OBSERVED_PARTIAL'
                )
                ORDER BY lower(option->>'name')
            )
            FROM jsonb_array_elements(variant_options) AS options(option)
        ), '[]'::jsonb),
        'providerVariantRefs', '[]'::jsonb,
        'observedAt', to_jsonb(observed_at),
        'evidence', jsonb_build_object(
            'summary', COALESCE(evidence->>'summary', ''),
            'sourceUrls', COALESCE(evidence->'sourceUrls', '[]'::jsonb)
        )
    ),
    purchase_path = jsonb_build_object(
        'schemaVersion', 'vitlane.purchase-path.v1',
        'providerKind', CASE
            WHEN evidence->'catalog'->>'provider' = 'SHOPIFY_UCP_GLOBAL'
                THEN 'SHOPIFY_UCP'
            ELSE 'GENERIC_WEB'
        END,
        'executionMode', 'MANUAL_MERCHANT_ORDER',
        'externalEffect', 'SIMULATED',
        'liveOrderability', 'UNVERIFIED',
        'settlementStatus', CASE
            WHEN price_currency = 'USD' THEN 'SUPPORTED'
            ELSE 'UNSUPPORTED'
        END,
        'status', CASE
            WHEN price_currency = 'USD' THEN 'TEST_ORDER_FLOW_AVAILABLE'
            ELSE 'SETTLEMENT_CURRENCY_UNSUPPORTED'
        END,
        'reasonCodes', CASE
            WHEN price_currency = 'USD' THEN '[]'::jsonb
            ELSE '["NON_USD_SETTLEMENT_UNSUPPORTED"]'::jsonb
        END
    );

ALTER TABLE candidates
    ALTER COLUMN variant_discovery SET NOT NULL,
    ALTER COLUMN purchase_path SET NOT NULL;

ALTER TABLE purchases
    ADD COLUMN configuration_hash TEXT;

UPDATE purchases
SET configuration_hash = 'legacy:' || candidate_hash;

ALTER TABLE purchases
    ALTER COLUMN configuration_hash SET NOT NULL;

DROP INDEX idx_purchases_active_candidate_quantity;

WITH ranked_active AS (
    SELECT
        id,
        status,
        row_number() OVER (
            PARTITION BY user_id, shopping_session_id
            ORDER BY
                CASE
                    WHEN status IN ('DRAFT', 'AWAITING_USER_APPROVAL') THEN 1
                    ELSE 0
                END,
                created_at DESC,
                id DESC
        ) AS active_rank
    FROM purchases
    WHERE status IN (
        'DRAFT',
        'AWAITING_USER_APPROVAL',
        'USER_APPROVED',
        'SETTLEMENT_PENDING',
        'FUNDED',
        'FULFILLMENT_PENDING',
        'REFUND_PENDING'
    )
)
UPDATE purchases AS purchase
SET status = 'SUPERSEDED',
    updated_at = now()
FROM ranked_active AS ranked
WHERE ranked.id = purchase.id
  AND ranked.active_rank > 1
  AND ranked.status IN ('DRAFT', 'AWAITING_USER_APPROVAL');

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM purchases
        WHERE status IN (
            'DRAFT',
            'AWAITING_USER_APPROVAL',
            'USER_APPROVED',
            'SETTLEMENT_PENDING',
            'FUNDED',
            'FULFILLMENT_PENDING',
            'REFUND_PENDING'
        )
        GROUP BY user_id, shopping_session_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION
            '000017 requires manual resolution of multiple approved/funded purchases in one shopping session';
    END IF;
END
$$;

CREATE UNIQUE INDEX idx_purchases_active_candidate_quantity_configuration
    ON purchases(
        user_id,
        shopping_session_id,
        candidate_id,
        quantity,
        configuration_hash
    )
    WHERE status IN (
        'DRAFT',
        'AWAITING_USER_APPROVAL',
        'USER_APPROVED',
        'SETTLEMENT_PENDING',
        'FUNDED',
        'FULFILLMENT_PENDING',
        'REFUND_PENDING'
    );

CREATE UNIQUE INDEX idx_purchases_one_active_per_session
    ON purchases(user_id, shopping_session_id)
    WHERE status IN (
        'DRAFT',
        'AWAITING_USER_APPROVAL',
        'USER_APPROVED',
        'SETTLEMENT_PENDING',
        'FUNDED',
        'FULFILLMENT_PENDING',
        'REFUND_PENDING'
    );

CREATE TABLE candidate_configurations (
    id UUID PRIMARY KEY,
    configuration_sequence BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,
    shopping_session_id UUID NOT NULL
        REFERENCES shopping_sessions(id) ON DELETE RESTRICT,
    candidate_id UUID NOT NULL
        REFERENCES candidates(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL
        REFERENCES users(id) ON DELETE RESTRICT,
    select_decision_id UUID NOT NULL UNIQUE
        REFERENCES candidate_decisions(id) ON DELETE RESTRICT,
    schema_version TEXT NOT NULL
        CHECK (schema_version = 'vitlane.candidate-configuration.v1'),
    fields JSONB NOT NULL,
    selections JSONB NOT NULL,
    confirms_no_options BOOLEAN NOT NULL,
    configuration_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (jsonb_typeof(fields) = 'array'),
    CHECK (jsonb_typeof(selections) = 'array'),
    CHECK (
        (confirms_no_options AND jsonb_array_length(selections) = 0)
        OR (NOT confirms_no_options AND jsonb_array_length(fields) > 0)
    ),
    UNIQUE (candidate_id, configuration_hash, select_decision_id)
);

CREATE INDEX idx_candidate_configurations_session_sequence
    ON candidate_configurations(shopping_session_id, configuration_sequence DESC);
CREATE INDEX idx_candidate_configurations_candidate_sequence
    ON candidate_configurations(candidate_id, configuration_sequence DESC);

-- Older selected sessions may predate the append-only decision record. Create
-- an explicit migration event only where no active SELECT evidence exists.
INSERT INTO candidate_decisions(
    id,
    shopping_session_id,
    candidate_id,
    decision,
    feedback,
    user_id,
    reverses_decision_id,
    client_command_id,
    command_hash,
    created_at
)
SELECT
    (
        substr(md5('vitlane-legacy-select:' || session.id::text), 1, 8) || '-' ||
        substr(md5('vitlane-legacy-select:' || session.id::text), 9, 4) || '-' ||
        substr(md5('vitlane-legacy-select:' || session.id::text), 13, 4) || '-' ||
        substr(md5('vitlane-legacy-select:' || session.id::text), 17, 4) || '-' ||
        substr(md5('vitlane-legacy-select:' || session.id::text), 21, 12)
    )::uuid,
    session.id,
    session.selected_candidate_id,
    'SELECT',
    '000017 legacy selected-session backfill',
    session.user_id,
    NULL,
    (
        substr(md5('vitlane-legacy-command:' || session.id::text), 1, 8) || '-' ||
        substr(md5('vitlane-legacy-command:' || session.id::text), 9, 4) || '-' ||
        substr(md5('vitlane-legacy-command:' || session.id::text), 13, 4) || '-' ||
        substr(md5('vitlane-legacy-command:' || session.id::text), 17, 4) || '-' ||
        substr(md5('vitlane-legacy-command:' || session.id::text), 21, 12)
    )::uuid,
    'vitlane.migration.000017.legacy-select:' || candidate.candidate_hash,
    session.updated_at
FROM shopping_sessions AS session
JOIN candidates AS candidate
  ON candidate.id = session.selected_candidate_id
 AND candidate.shopping_session_id = session.id
WHERE session.selected_candidate_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1
      FROM candidate_decisions AS selection
      WHERE selection.shopping_session_id = session.id
        AND selection.candidate_id = session.selected_candidate_id
        AND selection.decision = 'SELECT'
        AND NOT EXISTS (
            SELECT 1
            FROM candidate_decisions AS reversal
            WHERE reversal.reverses_decision_id = selection.id
        )
  );

-- A legacy option list never identified the user's exact selection. Reverse
-- those selections explicitly so the user must create a complete configuration.
INSERT INTO candidate_decisions(
    id,
    shopping_session_id,
    candidate_id,
    decision,
    feedback,
    user_id,
    reverses_decision_id,
    client_command_id,
    command_hash,
    created_at
)
SELECT
    (
        substr(md5('vitlane-legacy-undo:' || session.id::text), 1, 8) || '-' ||
        substr(md5('vitlane-legacy-undo:' || session.id::text), 9, 4) || '-' ||
        substr(md5('vitlane-legacy-undo:' || session.id::text), 13, 4) || '-' ||
        substr(md5('vitlane-legacy-undo:' || session.id::text), 17, 4) || '-' ||
        substr(md5('vitlane-legacy-undo:' || session.id::text), 21, 12)
    )::uuid,
    session.id,
    candidate.id,
    'UNDO',
    '000017 exact variant configuration required',
    session.user_id,
    selection.id,
    (
        substr(md5('vitlane-legacy-undo-command:' || session.id::text), 1, 8) || '-' ||
        substr(md5('vitlane-legacy-undo-command:' || session.id::text), 9, 4) || '-' ||
        substr(md5('vitlane-legacy-undo-command:' || session.id::text), 13, 4) || '-' ||
        substr(md5('vitlane-legacy-undo-command:' || session.id::text), 17, 4) || '-' ||
        substr(md5('vitlane-legacy-undo-command:' || session.id::text), 21, 12)
    )::uuid,
    'vitlane.migration.000017.variant-reconfiguration:' || candidate.candidate_hash,
    session.updated_at
FROM shopping_sessions AS session
JOIN candidates AS candidate
  ON candidate.id = session.selected_candidate_id
 AND candidate.shopping_session_id = session.id
JOIN LATERAL (
    SELECT decision.id
    FROM candidate_decisions AS decision
    WHERE decision.shopping_session_id = session.id
      AND decision.candidate_id = candidate.id
      AND decision.decision = 'SELECT'
      AND NOT EXISTS (
          SELECT 1
          FROM candidate_decisions AS reversal
          WHERE reversal.reverses_decision_id = decision.id
      )
    ORDER BY decision.event_sequence DESC
    LIMIT 1
) AS selection ON true
WHERE candidate.variant_discovery->>'status' <> 'NOT_APPLICABLE';

UPDATE shopping_sessions AS session
SET status = 'REVIEWING',
    selected_candidate_id = NULL,
    version = version + 1
FROM candidates AS candidate
WHERE candidate.id = session.selected_candidate_id
  AND candidate.shopping_session_id = session.id
  AND candidate.variant_discovery->>'status' <> 'NOT_APPLICABLE';

-- Only a future/evidence-backed NOT_APPLICABLE selection could be migrated
-- without inventing intent. Legacy [] was backfilled as UNKNOWN and was
-- therefore reversed above.
INSERT INTO candidate_configurations(
    id,
    shopping_session_id,
    candidate_id,
    user_id,
    select_decision_id,
    schema_version,
    fields,
    selections,
    confirms_no_options,
    configuration_hash,
    created_at
)
SELECT
    (
        substr(md5('vitlane-legacy-config:' || session.id::text), 1, 8) || '-' ||
        substr(md5('vitlane-legacy-config:' || session.id::text), 9, 4) || '-' ||
        substr(md5('vitlane-legacy-config:' || session.id::text), 13, 4) || '-' ||
        substr(md5('vitlane-legacy-config:' || session.id::text), 17, 4) || '-' ||
        substr(md5('vitlane-legacy-config:' || session.id::text), 21, 12)
    )::uuid,
    session.id,
    candidate.id,
    session.user_id,
    selection.id,
    'vitlane.candidate-configuration.v1',
    '[]'::jsonb,
    '[]'::jsonb,
    true,
    'legacy:' || candidate.candidate_hash,
    selection.created_at
FROM shopping_sessions AS session
JOIN candidates AS candidate
  ON candidate.id = session.selected_candidate_id
 AND candidate.shopping_session_id = session.id
JOIN LATERAL (
    SELECT decision.id, decision.created_at
    FROM candidate_decisions AS decision
    WHERE decision.shopping_session_id = session.id
      AND decision.candidate_id = candidate.id
      AND decision.decision = 'SELECT'
      AND NOT EXISTS (
          SELECT 1
          FROM candidate_decisions AS reversal
          WHERE reversal.reverses_decision_id = decision.id
      )
    ORDER BY decision.event_sequence DESC
    LIMIT 1
) AS selection ON true
WHERE session.selected_candidate_id IS NOT NULL
  AND candidate.variant_discovery->>'status' = 'NOT_APPLICABLE';

CREATE TABLE purchase_intent_snapshots (
    purchase_id UUID PRIMARY KEY
        REFERENCES purchases(id) ON DELETE RESTRICT,
    schema_version TEXT NOT NULL
        CHECK (schema_version = 'vitlane.purchase-intent.v2'),
    provider_ref JSONB NOT NULL,
    merchant_ref JSONB NOT NULL,
    offer_ref JSONB NOT NULL,
    purchase_path JSONB NOT NULL,
    variant_discovery JSONB NOT NULL,
    candidate_configuration JSONB NOT NULL,
    variant_resolution JSONB NOT NULL,
    configuration_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (jsonb_typeof(provider_ref) = 'object'),
    CHECK (jsonb_typeof(merchant_ref) = 'object'),
    CHECK (jsonb_typeof(offer_ref) = 'object'),
    CHECK (jsonb_typeof(purchase_path) = 'object'),
    CHECK (jsonb_typeof(variant_discovery) = 'object'),
    CHECK (jsonb_typeof(candidate_configuration) = 'object'),
    CHECK (jsonb_typeof(variant_resolution) = 'object'),
    CHECK (
        variant_resolution->>'status' IN (
            'USER_SPECIFIED_UNVERIFIED',
            'PROVIDER_VERIFIED',
            'OPERATOR_VERIFIED',
            'UNAVAILABLE',
            'INVALID_COMBINATION',
            'AMBIGUOUS',
            'PRICE_CHANGED'
        )
    )
);

CREATE INDEX idx_purchase_intent_configuration_hash
    ON purchase_intent_snapshots(configuration_hash);
