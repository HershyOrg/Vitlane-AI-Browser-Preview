-- Preview/beta hard cutover.
--
-- The product owner explicitly authorized discarding all shopping-domain data
-- instead of migrating the legacy SELECT/REJECT and synthetic cart projection.
-- TRUNCATE ... CASCADE removes plans, targets, sessions, research, purchases,
-- quotes, approvals, settlement/fulfillment rows, and plan-scoped agent state.
-- Account identities and deployment/configuration tables are intentionally kept.
TRUNCATE TABLE shopping_plans RESTART IDENTITY CASCADE;

-- OAuth AgentConnection + task-scoped AgentGrant is now the only agent
-- provenance model. The reset above guarantees all dependent rows are empty,
-- so the preview bearer capability representation is removed rather than
-- migrated or kept as a compatibility branch.
DROP INDEX IF EXISTS research_catalog_observations_round_access_idx;

ALTER TABLE planning_proposals
    DROP COLUMN IF EXISTS agent_capability_id CASCADE,
    ALTER COLUMN agent_grant_id SET NOT NULL;

ALTER TABLE research_submissions
    DROP COLUMN IF EXISTS agent_capability_id CASCADE,
    ALTER COLUMN agent_grant_id SET NOT NULL,
    ADD CONSTRAINT research_submissions_current_schema_check
        CHECK (
            schema_version IN (
                'vitlane.research-submission.v3',
                'vitlane.research-submission.v4'
            )
        );

ALTER TABLE research_catalog_observations
    DROP COLUMN IF EXISTS agent_capability_id CASCADE,
    ALTER COLUMN agent_grant_id SET NOT NULL;

CREATE INDEX research_catalog_observations_round_access_idx
    ON research_catalog_observations(
        research_round_id, user_id, agent_grant_id, expires_at
    );

DROP TABLE IF EXISTS agent_capability_sessions;
DROP TABLE IF EXISTS agent_capabilities;

-- The hard cutover also removes the preview.11 rollback representation.
-- A Target keeps its stable Plan membership after removal; removed_at is the
-- only active/inactive discriminator. Old-image raw-write triggers are no
-- longer part of the supported runtime contract.
DROP TRIGGER IF EXISTS purchases_active_target_guard ON purchases;
DROP FUNCTION IF EXISTS require_active_plan_target_for_purchase_insert();
DROP TRIGGER IF EXISTS shopping_sessions_active_target_guard ON shopping_sessions;
DROP FUNCTION IF EXISTS require_active_plan_target_for_session_write();

DROP INDEX IF EXISTS idx_plan_targets_active_order;
DROP INDEX IF EXISTS idx_plan_targets_removed_from_plan;

ALTER TABLE plan_targets
    DROP CONSTRAINT IF EXISTS plan_targets_removal_membership_check,
    ALTER COLUMN plan_id SET NOT NULL,
    DROP COLUMN IF EXISTS removed_from_plan_id,
    ADD CONSTRAINT plan_targets_removal_audit_check CHECK (
        (removed_at IS NULL AND removed_by_user_id IS NULL)
        OR
        (removed_at IS NOT NULL AND removed_by_user_id IS NOT NULL)
    );

CREATE UNIQUE INDEX idx_plan_targets_active_order
    ON plan_targets(plan_id, order_index)
    WHERE removed_at IS NULL;

ALTER TABLE shopping_sessions
    DROP CONSTRAINT IF EXISTS shopping_sessions_status_check,
    DROP COLUMN IF EXISTS selected_candidate_id,
    ADD CONSTRAINT shopping_sessions_status_check
        CHECK (status IN ('READY', 'RESEARCHING', 'REVIEWING'));

-- Candidate reads are current-schema only. Preview data was truncated above,
-- so retaining the legacy parallel representation would only preserve an
-- unsupported write/read path.
ALTER TABLE candidates
    DROP COLUMN IF EXISTS variant_options,
    ADD CONSTRAINT candidates_current_hash_schema_check
        CHECK (candidate_hash_schema = 'vitlane.candidate.v2'),
    ADD CONSTRAINT candidates_variant_discovery_current_check CHECK (
        jsonb_typeof(variant_discovery) = 'object'
        AND variant_discovery->>'schemaVersion' = 'vitlane.variant-discovery.v1'
        AND COALESCE(variant_discovery->>'status', '') <> ''
        AND jsonb_typeof(variant_discovery->'fields') = 'array'
        AND jsonb_typeof(variant_discovery->'providerVariantRefs') = 'array'
    ),
    ADD CONSTRAINT candidates_purchase_path_current_check CHECK (
        jsonb_typeof(purchase_path) = 'object'
        AND purchase_path->>'schemaVersion' = 'vitlane.purchase-path.v1'
        AND COALESCE(purchase_path->>'providerKind', '') <> ''
        AND COALESCE(purchase_path->>'executionMode', '') <> ''
        AND COALESCE(purchase_path->>'externalEffect', '') <> ''
        AND COALESCE(purchase_path->>'liveOrderability', '') <> ''
        AND COALESCE(purchase_path->>'settlementStatus', '') <> ''
        AND COALESCE(purchase_path->>'status', '') <> ''
        AND jsonb_typeof(purchase_path->'reasonCodes') = 'array'
    );

ALTER TABLE purchases
    ALTER COLUMN buyer_profile_id SET NOT NULL,
    ALTER COLUMN buyer_profile_version SET NOT NULL,
    ALTER COLUMN buyer_profile_snapshot_hash SET NOT NULL,
    ALTER COLUMN shipping_snapshot_id SET NOT NULL,
    ALTER COLUMN shipping_profile_id SET NOT NULL,
    ALTER COLUMN shipping_profile_version SET NOT NULL,
    ALTER COLUMN shipping_country SET NOT NULL,
    ALTER COLUMN shipping_masked_summary SET NOT NULL,
    ALTER COLUMN shipping_snapshot_hmac SET NOT NULL,
    ADD CONSTRAINT purchases_candidate_snapshot_current_check CHECK (
        jsonb_typeof(candidate_snapshot) = 'object'
        AND COALESCE(candidate_snapshot->>'candidateId', '') <> ''
        AND COALESCE(candidate_snapshot->>'candidateHash', '') <> ''
        AND COALESCE(candidate_snapshot->>'sessionId', '') <> ''
        AND COALESCE(
            candidate_snapshot->'providerRef'->>'schemaVersion', ''
        ) <> ''
        AND COALESCE(candidate_snapshot->'providerRef'->>'provider', '') <> ''
        AND COALESCE(
            candidate_snapshot->'merchantRef'->>'canonicalHost', ''
        ) <> ''
        AND COALESCE(
            candidate_snapshot->'offerRef'->>'offerFingerprint', ''
        ) <> ''
        AND candidate_snapshot->'purchasePath'->>'schemaVersion'
            = 'vitlane.purchase-path.v1'
        AND jsonb_typeof(
            candidate_snapshot->'purchasePath'->'reasonCodes'
        ) = 'array'
        AND candidate_snapshot->'variantDiscovery'->>'schemaVersion'
            = 'vitlane.variant-discovery.v1'
        AND jsonb_typeof(
            candidate_snapshot->'variantDiscovery'->'fields'
        ) = 'array'
        AND jsonb_typeof(
            candidate_snapshot->'variantDiscovery'->'providerVariantRefs'
        ) = 'array'
        AND candidate_snapshot->'candidateConfiguration'->>'schemaVersion'
            = 'vitlane.candidate-configuration.v1'
        AND COALESCE(
            candidate_snapshot->'candidateConfiguration'->>'configurationHash',
            ''
        ) <> ''
        AND jsonb_typeof(
            candidate_snapshot->'candidateConfiguration'->'fields'
        ) = 'array'
        AND jsonb_typeof(
            candidate_snapshot->'candidateConfiguration'->'selections'
        ) = 'array'
        AND candidate_snapshot->'variantResolution'->>'configurationHash'
            = candidate_snapshot->'candidateConfiguration'->>'configurationHash'
        AND COALESCE(candidate_snapshot->>'evidenceHash', '') <> ''
        AND COALESCE(candidate_snapshot->>'observedAt', '') <> ''
    );

ALTER TABLE purchase_intent_snapshots
    ADD CONSTRAINT purchase_intent_snapshot_current_check CHECK (
        COALESCE(provider_ref->>'schemaVersion', '') <> ''
        AND COALESCE(provider_ref->>'provider', '') <> ''
        AND COALESCE(merchant_ref->>'displayName', '') <> ''
        AND COALESCE(merchant_ref->>'canonicalHost', '') <> ''
        AND COALESCE(offer_ref->>'offerFingerprint', '') <> ''
        AND purchase_path->>'schemaVersion' = 'vitlane.purchase-path.v1'
        AND jsonb_typeof(purchase_path->'reasonCodes') = 'array'
        AND variant_discovery->>'schemaVersion'
            = 'vitlane.variant-discovery.v1'
        AND jsonb_typeof(variant_discovery->'fields') = 'array'
        AND jsonb_typeof(variant_discovery->'providerVariantRefs') = 'array'
        AND candidate_configuration->>'schemaVersion'
            = 'vitlane.candidate-configuration.v1'
        AND candidate_configuration->>'configurationHash' = configuration_hash
        AND jsonb_typeof(candidate_configuration->'fields') = 'array'
        AND jsonb_typeof(candidate_configuration->'selections') = 'array'
        AND variant_resolution->>'configurationHash' = configuration_hash
        AND COALESCE(variant_resolution->>'resolvedBy', '') <> ''
    );

ALTER TABLE candidate_configurations
    DROP COLUMN IF EXISTS select_decision_id;

ALTER TABLE candidate_configurations
    ADD CONSTRAINT candidate_configurations_candidate_user_configuration_unique
        UNIQUE (candidate_id, user_id, configuration_hash);

DROP TABLE IF EXISTS candidate_decisions;
DROP TABLE IF EXISTS plan_purchase_item_decisions;
DROP TABLE IF EXISTS plan_purchase_closures;

ALTER TABLE research_feedback
    RENAME COLUMN decision_snapshot TO interaction_snapshot;

CREATE TABLE candidate_interactions (
    shopping_session_id UUID NOT NULL
        REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    candidate_id UUID NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    pinned BOOLEAN NOT NULL DEFAULT FALSE,
    sentiment TEXT NOT NULL DEFAULT 'NONE'
        CHECK (sentiment IN ('NONE', 'LIKE', 'DISLIKE')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (shopping_session_id, candidate_id),
    FOREIGN KEY (candidate_id, shopping_session_id)
        REFERENCES candidates(id, shopping_session_id) ON DELETE CASCADE
);

CREATE INDEX idx_candidate_interactions_user_liked
    ON candidate_interactions(user_id, updated_at DESC)
    WHERE sentiment = 'LIKE';

CREATE INDEX idx_candidate_interactions_user_pinned
    ON candidate_interactions(user_id, updated_at DESC)
    WHERE pinned;

CREATE TABLE candidate_interaction_events (
    id UUID PRIMARY KEY,
    shopping_session_id UUID NOT NULL
        REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    candidate_id UUID NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action TEXT NOT NULL
        CHECK (action IN ('PIN', 'UNPIN', 'LIKE', 'DISLIKE', 'CLEAR_SENTIMENT')),
    client_command_id UUID NOT NULL,
    command_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (candidate_id, shopping_session_id)
        REFERENCES candidates(id, shopping_session_id) ON DELETE CASCADE,
    UNIQUE (user_id, client_command_id)
);

CREATE INDEX idx_candidate_interaction_events_session_created
    ON candidate_interaction_events(shopping_session_id, created_at DESC);

CREATE TABLE shopping_carts (
    id UUID PRIMARY KEY,
    plan_id UUID NOT NULL UNIQUE
        REFERENCES shopping_plans(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'OPEN'
        CHECK (status IN ('OPEN', 'PARTIALLY_PURCHASED', 'COMPLETED', 'CLOSED_PARTIAL')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_shopping_carts_user_updated
    ON shopping_carts(user_id, updated_at DESC);

CREATE OR REPLACE FUNCTION vitlane_create_plan_cart()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO shopping_carts(
        id, plan_id, user_id, status, version, created_at, updated_at
    ) VALUES (
        (
            substr(md5('vitlane-cart:' || NEW.id::text), 1, 8) || '-' ||
            substr(md5('vitlane-cart:' || NEW.id::text), 9, 4) || '-' ||
            substr(md5('vitlane-cart:' || NEW.id::text), 13, 4) || '-' ||
            substr(md5('vitlane-cart:' || NEW.id::text), 17, 4) || '-' ||
            substr(md5('vitlane-cart:' || NEW.id::text), 21, 12)
        )::uuid,
        NEW.id, NEW.user_id, 'OPEN', 1, NEW.created_at, NEW.created_at
    );
    RETURN NEW;
END
$$;

CREATE TRIGGER shopping_plans_create_cart
AFTER INSERT ON shopping_plans
FOR EACH ROW EXECUTE FUNCTION vitlane_create_plan_cart();

CREATE TABLE shopping_cart_items (
    id UUID PRIMARY KEY,
    cart_id UUID NOT NULL REFERENCES shopping_carts(id) ON DELETE CASCADE,
    plan_target_id UUID NOT NULL REFERENCES plan_targets(id) ON DELETE CASCADE,
    shopping_session_id UUID NOT NULL
        REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    candidate_id UUID NOT NULL,
    candidate_configuration_id UUID NOT NULL
        REFERENCES candidate_configurations(id) ON DELETE RESTRICT,
    quantity BIGINT NOT NULL CHECK (quantity > 0 AND quantity <= 99),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (candidate_id, shopping_session_id)
        REFERENCES candidates(id, shopping_session_id) ON DELETE RESTRICT,
    UNIQUE (cart_id, shopping_session_id)
);

CREATE INDEX idx_shopping_cart_items_cart_updated
    ON shopping_cart_items(cart_id, updated_at DESC);

CREATE TABLE shopping_cart_commands (
    id UUID PRIMARY KEY,
    cart_id UUID NOT NULL REFERENCES shopping_carts(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_command_id UUID NOT NULL,
    request_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, client_command_id)
);

CREATE INDEX idx_shopping_cart_commands_cart_created
    ON shopping_cart_commands(cart_id, created_at DESC);
