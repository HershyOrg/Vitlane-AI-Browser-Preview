-- ADR-0026 Purchase provenance expand phase.
--
-- An explicit DIRECT or CART OriginRequest is the idempotency boundary. A new
-- OriginRequest always creates a distinct Purchase; only replaying the same
-- request ID and request hash returns the already-created Purchase.
DROP INDEX IF EXISTS idx_purchases_active_candidate_quantity_configuration;
DROP INDEX IF EXISTS idx_purchases_one_active_per_session;

ALTER TABLE purchases
    ADD CONSTRAINT purchases_user_id_id_unique UNIQUE (user_id, id),
    ADD CONSTRAINT purchases_origin_lineage_unique UNIQUE (
        user_id, id, shopping_session_id, candidate_id, configuration_hash
    );

-- PostgreSQL requires the referenced column tuple to have its own unique
-- constraint. CurationAction.id is already globally unique, but this
-- redundant tuple lets the OriginRequest FK prove that the action belongs to
-- the same user and Curation instead of checking only the action ID.
ALTER TABLE curation_actions
    ADD CONSTRAINT curation_actions_id_actor_curation_unique
        UNIQUE (id, actor_user_id, curation_id);

CREATE TABLE purchase_origin_requests (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    curation_action_id UUID NOT NULL,
    root_origin_request_id UUID NOT NULL,
    retry_of_origin_request_id UUID,
    retry_of_ordinal INTEGER,
    curation_id UUID NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('DIRECT', 'CART')),
    request_hash TEXT NOT NULL CHECK (
        request_hash ~ '^sha256:[0-9a-f]{64}$'
    ),
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    response_hash TEXT CHECK (
        response_hash IS NULL
        OR response_hash ~ '^sha256:[0-9a-f]{64}$'
    ),
    CONSTRAINT purchase_origin_requests_user_id_unique
        UNIQUE (user_id, id),
    CONSTRAINT purchase_origin_requests_curation_action_unique
        UNIQUE (curation_action_id),
    CONSTRAINT purchase_origin_requests_user_id_curation_unique
        UNIQUE (user_id, id, curation_id),
    CONSTRAINT purchase_origin_requests_user_id_curation_origin_unique
        UNIQUE (user_id, id, curation_id, origin),
    CONSTRAINT purchase_origin_requests_retry_lineage_unique
        UNIQUE (
            user_id, id, root_origin_request_id, curation_id, origin
        ),
    CONSTRAINT purchase_origin_requests_user_curation_fkey
        FOREIGN KEY (user_id, curation_id)
        REFERENCES curations(user_id, id) ON DELETE RESTRICT,
    CONSTRAINT purchase_origin_requests_curation_action_fkey
        FOREIGN KEY (curation_action_id, user_id, curation_id)
        REFERENCES curation_actions(id, actor_user_id, curation_id)
        ON DELETE RESTRICT,
    CONSTRAINT purchase_origin_requests_root_fkey
        FOREIGN KEY (user_id, root_origin_request_id)
        REFERENCES purchase_origin_requests(user_id, id)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT purchase_origin_requests_retry_fkey
        FOREIGN KEY (
            user_id, retry_of_origin_request_id,
            root_origin_request_id, curation_id, origin
        ) REFERENCES purchase_origin_requests(
            user_id, id, root_origin_request_id, curation_id, origin
        )
        ON DELETE RESTRICT,
    CONSTRAINT purchase_origin_requests_lineage_check CHECK (
        (
            id=root_origin_request_id
            AND retry_of_origin_request_id IS NULL
            AND retry_of_ordinal IS NULL
        )
        OR
        (
            id<>root_origin_request_id
            AND retry_of_origin_request_id IS NOT NULL
            AND retry_of_ordinal >= 0
        )
    ),
    CONSTRAINT purchase_origin_requests_action_identity_check CHECK (
        curation_action_id=id
    ),
    CONSTRAINT purchase_origin_requests_completion_check CHECK (
        (completed_at IS NULL AND response_hash IS NULL)
        OR (completed_at IS NOT NULL AND response_hash IS NOT NULL)
    )
);

CREATE INDEX idx_purchase_origin_requests_root_created
    ON purchase_origin_requests(
        user_id, root_origin_request_id, created_at, id
    );

CREATE TABLE purchase_origin_request_items (
    origin_request_id UUID NOT NULL,
    user_id UUID NOT NULL,
    curation_id UUID NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    origin TEXT NOT NULL CHECK (origin IN ('DIRECT', 'CART')),
    input_snapshot JSONB NOT NULL CHECK (
        jsonb_typeof(input_snapshot)='object'
    ),
    selection_id UUID,
    expected_selection_version BIGINT,
    outcome TEXT NOT NULL CHECK (outcome IN ('CREATED', 'REJECTED')),
    purchase_id UUID,
    plan_target_id UUID,
    shopping_session_id UUID,
    candidate_id UUID,
    candidate_configuration_id UUID,
    candidate_configuration_hash TEXT,
    source_selection_id UUID,
    source_selection_version BIGINT,
    source_candidate_configuration_id UUID,
    source_candidate_configuration_hash TEXT,
    source_quantity BIGINT,
    source_selection_snapshot_hash TEXT,
    reason_code TEXT,
    retryable BOOLEAN,
    resolution_code TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (origin_request_id, ordinal),
    CONSTRAINT purchase_origin_request_items_user_request_ordinal_unique
        UNIQUE (user_id, origin_request_id, ordinal),
    CONSTRAINT purchase_origin_request_items_purchase_unique
        UNIQUE (purchase_id),
    CONSTRAINT purchase_origin_request_items_request_fkey
        FOREIGN KEY (
            user_id, origin_request_id, curation_id, origin
        ) REFERENCES purchase_origin_requests(
            user_id, id, curation_id, origin
        ) ON DELETE CASCADE,
    CONSTRAINT purchase_origin_request_items_purchase_fkey
        FOREIGN KEY (user_id, purchase_id)
        REFERENCES purchases(user_id, id) ON DELETE RESTRICT,
    CONSTRAINT purchase_origin_request_items_curation_fkey
        FOREIGN KEY (user_id, curation_id)
        REFERENCES curations(user_id, id) ON DELETE RESTRICT,
    CONSTRAINT purchase_origin_request_items_target_fkey
        FOREIGN KEY (user_id, plan_target_id, curation_id)
        REFERENCES plan_targets(user_id, id, curation_id)
        ON DELETE RESTRICT,
    CONSTRAINT purchase_origin_request_items_session_fkey
        FOREIGN KEY (user_id, shopping_session_id, plan_target_id)
        REFERENCES shopping_sessions(user_id, id, plan_target_id)
        ON DELETE RESTRICT,
    CONSTRAINT purchase_origin_request_items_candidate_fkey
        FOREIGN KEY (candidate_id, shopping_session_id)
        REFERENCES candidates(id, shopping_session_id)
        ON DELETE RESTRICT,
    CONSTRAINT purchase_origin_request_items_configuration_fkey
        FOREIGN KEY (
            candidate_configuration_id, user_id, shopping_session_id,
            candidate_id, candidate_configuration_hash
        ) REFERENCES candidate_configurations(
            id, user_id, shopping_session_id, candidate_id,
            configuration_hash
        ) ON DELETE RESTRICT,
    CONSTRAINT purchase_origin_request_items_purchase_lineage_fkey
        FOREIGN KEY (
            user_id, purchase_id, shopping_session_id,
            candidate_id, candidate_configuration_hash
        ) REFERENCES purchases(
            user_id, id, shopping_session_id, candidate_id,
            configuration_hash
        ) ON DELETE RESTRICT,
    CONSTRAINT purchase_origin_request_items_selection_fkey
        FOREIGN KEY (
            user_id, source_selection_id, curation_id, plan_target_id,
            shopping_session_id, candidate_id
        ) REFERENCES curation_selections(
            user_id, id, curation_id, plan_target_id,
            shopping_session_id, candidate_id
        ) ON DELETE RESTRICT,
    CONSTRAINT purchase_origin_request_items_expected_version_check CHECK (
        (origin='DIRECT'
            AND selection_id IS NULL
            AND expected_selection_version IS NULL)
        OR
        (origin='CART'
            AND selection_id IS NOT NULL
            AND expected_selection_version > 0)
    ),
    CONSTRAINT purchase_origin_request_items_outcome_terminal_shape_check CHECK (
        (
            outcome='CREATED'
            AND purchase_id IS NOT NULL
            AND plan_target_id IS NOT NULL
            AND shopping_session_id IS NOT NULL
            AND candidate_id IS NOT NULL
            AND candidate_configuration_id IS NOT NULL
            AND candidate_configuration_hash IS NOT NULL
            AND reason_code IS NULL
            AND retryable IS NULL
            AND resolution_code IS NULL
        )
        OR
        (
            outcome='REJECTED'
            AND purchase_id IS NULL
            AND plan_target_id IS NULL
            AND shopping_session_id IS NULL
            AND candidate_id IS NULL
            AND candidate_configuration_id IS NULL
            AND candidate_configuration_hash IS NULL
            AND source_selection_id IS NULL
            AND source_selection_version IS NULL
            AND source_candidate_configuration_id IS NULL
            AND source_candidate_configuration_hash IS NULL
            AND source_quantity IS NULL
            AND source_selection_snapshot_hash IS NULL
            AND reason_code IS NOT NULL
            AND retryable IS NOT NULL
            AND resolution_code IS NOT NULL
        )
    ),
    CONSTRAINT purchase_origin_request_items_source_check CHECK (
        (
            outcome='REJECTED'
        )
        OR
        (
            origin='DIRECT'
            AND source_selection_id IS NULL
            AND source_selection_version IS NULL
            AND source_candidate_configuration_id IS NULL
            AND source_candidate_configuration_hash IS NULL
            AND source_quantity IS NULL
            AND source_selection_snapshot_hash IS NULL
        )
        OR
        (
            origin='CART'
            AND source_selection_id=selection_id
            AND source_selection_version=expected_selection_version
            AND source_selection_version > 0
            AND source_candidate_configuration_id
                =candidate_configuration_id
            AND source_candidate_configuration_hash
                =candidate_configuration_hash
            AND source_quantity BETWEEN 1 AND 99
            AND source_selection_snapshot_hash
                ~ '^sha256:[0-9a-f]{64}$'
        )
    )
);

CREATE INDEX idx_purchase_origin_request_items_curation_created
    ON purchase_origin_request_items(curation_id, created_at, origin_request_id);

CREATE INDEX idx_purchase_origin_request_items_target
    ON purchase_origin_request_items(plan_target_id, created_at)
    WHERE purchase_id IS NOT NULL;

CREATE FUNCTION enforce_purchase_origin_request_immutability()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        IF pg_trigger_depth()>1
           OR current_setting('vitlane.account_reset', true)='on' THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'PURCHASE_ORIGIN_REQUEST_IMMUTABLE'
            USING
                ERRCODE='23514',
                CONSTRAINT='purchase_origin_request_immutable';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.user_id IS DISTINCT FROM OLD.user_id
       OR NEW.curation_action_id IS DISTINCT FROM OLD.curation_action_id
       OR NEW.root_origin_request_id
            IS DISTINCT FROM OLD.root_origin_request_id
       OR NEW.retry_of_origin_request_id
            IS DISTINCT FROM OLD.retry_of_origin_request_id
       OR NEW.retry_of_ordinal IS DISTINCT FROM OLD.retry_of_ordinal
       OR NEW.curation_id IS DISTINCT FROM OLD.curation_id
       OR NEW.origin IS DISTINCT FROM OLD.origin
       OR NEW.request_hash IS DISTINCT FROM OLD.request_hash
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR OLD.completed_at IS NOT NULL
       OR NEW.completed_at IS NULL
       OR NEW.response_hash IS NULL THEN
        RAISE EXCEPTION 'PURCHASE_ORIGIN_REQUEST_IMMUTABLE'
            USING
                ERRCODE='23514',
                CONSTRAINT='purchase_origin_request_immutable';
    END IF;

    RETURN NEW;
END
$$;

CREATE TRIGGER purchase_origin_request_immutable
BEFORE UPDATE OR DELETE ON purchase_origin_requests
FOR EACH ROW
EXECUTE FUNCTION enforce_purchase_origin_request_immutability();

CREATE FUNCTION reject_purchase_origin_request_item_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP='DELETE'
       AND (
           pg_trigger_depth()>1
           OR current_setting('vitlane.account_reset', true)='on'
       ) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'PURCHASE_ORIGIN_REQUEST_ITEM_IMMUTABLE'
        USING
            ERRCODE='23514',
            CONSTRAINT='purchase_origin_request_item_immutable';
END
$$;

CREATE TRIGGER purchase_origin_request_item_immutable
BEFORE UPDATE OR DELETE ON purchase_origin_request_items
FOR EACH ROW
EXECUTE FUNCTION reject_purchase_origin_request_item_mutation();
