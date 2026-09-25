-- ADR-0026 Selection expand phase.
--
-- ShoppingCart remains available during this expand migration. New writes can
-- move to CurationSelection and its status-free CartView before the later hard
-- cutover removes the legacy Cart lifecycle.

-- These composite keys let Selection prove the complete ownership lineage
-- without treating ShoppingPlan.id as Curation.id.
ALTER TABLE shopping_sessions
    ADD CONSTRAINT shopping_sessions_user_id_id_target_unique
        UNIQUE (user_id, id, plan_target_id),
    ADD CONSTRAINT shopping_sessions_user_target_fkey
        FOREIGN KEY (user_id, plan_target_id)
        REFERENCES plan_targets(user_id, id) ON DELETE RESTRICT;

ALTER TABLE candidate_configurations
    ADD CONSTRAINT candidate_configurations_selection_identity_unique
        UNIQUE (id, user_id, shopping_session_id, candidate_id),
    ADD CONSTRAINT candidate_configurations_selection_lineage_unique
        UNIQUE (
            id, user_id, shopping_session_id, candidate_id,
            configuration_hash
        );

CREATE TABLE curation_selections (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    curation_id UUID NOT NULL,
    plan_target_id UUID NOT NULL,
    shopping_session_id UUID NOT NULL,
    candidate_id UUID NOT NULL,
    candidate_configuration_id UUID NOT NULL,
    candidate_configuration_hash TEXT NOT NULL CHECK (
        btrim(candidate_configuration_hash) <> ''
    ),
    quantity BIGINT NOT NULL CHECK (quantity BETWEEN 1 AND 99),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    selected_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    removed_at TIMESTAMPTZ,
    removed_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT curation_selections_user_curation_fkey
        FOREIGN KEY (user_id, curation_id)
        REFERENCES curations(user_id, id) ON DELETE CASCADE,
    CONSTRAINT curation_selections_target_lineage_fkey
        FOREIGN KEY (user_id, plan_target_id, curation_id)
        REFERENCES plan_targets(user_id, id, curation_id) ON DELETE RESTRICT,
    CONSTRAINT curation_selections_session_lineage_fkey
        FOREIGN KEY (user_id, shopping_session_id, plan_target_id)
        REFERENCES shopping_sessions(user_id, id, plan_target_id)
        ON DELETE RESTRICT,
    CONSTRAINT curation_selections_candidate_lineage_fkey
        FOREIGN KEY (candidate_id, shopping_session_id)
        REFERENCES candidates(id, shopping_session_id) ON DELETE RESTRICT,
    CONSTRAINT curation_selections_configuration_lineage_fkey
        FOREIGN KEY (
            candidate_configuration_id, user_id, shopping_session_id,
            candidate_id, candidate_configuration_hash
        ) REFERENCES candidate_configurations(
            id, user_id, shopping_session_id, candidate_id, configuration_hash
        ) ON DELETE RESTRICT,
    CONSTRAINT curation_selections_time_check CHECK (
        updated_at >= selected_at
    ),
    CONSTRAINT curation_selections_removal_audit_check CHECK (
        (removed_at IS NULL AND removed_by_user_id IS NULL)
        OR (
            removed_at IS NOT NULL
            AND removed_by_user_id IS NOT NULL
            AND removed_at=updated_at
        )
    ),
    CONSTRAINT curation_selections_user_id_curation_unique
        UNIQUE (user_id, id, curation_id),
    CONSTRAINT curation_selections_immutable_lineage_unique
        UNIQUE (
            user_id, id, curation_id, plan_target_id,
            shopping_session_id, candidate_id
        )
);

CREATE INDEX idx_curation_selections_cart_view
    ON curation_selections(curation_id, selected_at, id)
    WHERE removed_at IS NULL;

CREATE INDEX idx_curation_selections_user_updated
    ON curation_selections(user_id, updated_at DESC, id);

-- Identity and ownership lineage never change. An active Selection can revise
-- only its configuration snapshot and quantity. Removal is a single terminal
-- soft-removal update, preserving the row for future Purchase provenance.
CREATE FUNCTION enforce_curation_selection_immutability()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        IF pg_trigger_depth()>1
           OR current_setting('vitlane.account_reset', true)='on' THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'CURATION_SELECTION_IMMUTABLE'
            USING
                ERRCODE='23514',
                CONSTRAINT='curation_selection_immutable';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.user_id IS DISTINCT FROM OLD.user_id
       OR NEW.curation_id IS DISTINCT FROM OLD.curation_id
       OR NEW.plan_target_id IS DISTINCT FROM OLD.plan_target_id
       OR NEW.shopping_session_id IS DISTINCT FROM OLD.shopping_session_id
       OR NEW.candidate_id IS DISTINCT FROM OLD.candidate_id
       OR NEW.selected_at IS DISTINCT FROM OLD.selected_at THEN
        RAISE EXCEPTION 'CURATION_SELECTION_LINEAGE_IMMUTABLE'
            USING
                ERRCODE='23514',
                CONSTRAINT='curation_selection_lineage_immutable';
    END IF;

    IF OLD.removed_at IS NOT NULL THEN
        RAISE EXCEPTION 'CURATION_SELECTION_REMOVED'
            USING
                ERRCODE='23514',
                CONSTRAINT='curation_selection_removed';
    END IF;

    IF NEW.version <> OLD.version + 1
       OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'CURATION_SELECTION_VERSION_INVALID'
            USING
                ERRCODE='23514',
                CONSTRAINT='curation_selection_version';
    END IF;

    IF NEW.removed_at IS NOT NULL
       AND (
           NEW.removed_by_user_id IS DISTINCT FROM OLD.user_id
           OR NEW.removed_at IS DISTINCT FROM NEW.updated_at
           OR NEW.candidate_configuration_id
                IS DISTINCT FROM OLD.candidate_configuration_id
           OR NEW.candidate_configuration_hash
                IS DISTINCT FROM OLD.candidate_configuration_hash
           OR NEW.quantity IS DISTINCT FROM OLD.quantity
       ) THEN
        RAISE EXCEPTION 'CURATION_SELECTION_REMOVAL_INVALID'
            USING
                ERRCODE='23514',
                CONSTRAINT='curation_selection_removal';
    END IF;

    RETURN NEW;
END
$$;

CREATE TRIGGER curation_selection_immutable
BEFORE UPDATE OR DELETE ON curation_selections
FOR EACH ROW
EXECUTE FUNCTION enforce_curation_selection_immutability();

-- This byte preimage exactly matches the application
-- CurationSelectionSnapshotHash.v1 struct field order. UUIDs and positive
-- integers are already canonical; to_jsonb(text)::text supplies JSON string
-- escaping for the configuration hash.
CREATE FUNCTION curation_selection_snapshot_hash_v1(
    selection_id UUID,
    curation_id UUID,
    target_id UUID,
    shopping_session_id UUID,
    candidate_id UUID,
    candidate_configuration_id UUID,
    candidate_configuration_hash TEXT,
    quantity BIGINT,
    version BIGINT
)
RETURNS TEXT
LANGUAGE sql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT 'sha256:' || encode(
        sha256(
            convert_to(
                '{"schema":"CurationSelectionSnapshotHash.v1","snapshot":{'
                || '"candidateConfigurationHash":'
                || to_jsonb(candidate_configuration_hash)::text
                || ',"candidateConfigurationId":'
                || to_jsonb(candidate_configuration_id::text)::text
                || ',"candidateId":'
                || to_jsonb(candidate_id::text)::text
                || ',"curationId":'
                || to_jsonb(curation_id::text)::text
                || ',"quantity":' || quantity::text
                || ',"selectionId":'
                || to_jsonb(selection_id::text)::text
                || ',"shoppingSessionId":'
                || to_jsonb(shopping_session_id::text)::text
                || ',"targetId":'
                || to_jsonb(target_id::text)::text
                || ',"version":' || version::text
                || '}}',
                'UTF8'
            )
        ),
        'hex'
    )
$$;

CREATE TABLE curation_selection_commands (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    curation_id UUID NOT NULL,
    selection_id UUID NOT NULL,
    client_command_id UUID NOT NULL,
    command_kind TEXT NOT NULL CHECK (
        command_kind IN ('CREATE', 'UPDATE', 'REMOVE')
    ),
    request_hash TEXT NOT NULL CHECK (
        request_hash ~ '^[0-9a-f]{64}$'
    ),
    response_snapshot JSONB NOT NULL CHECK (
        jsonb_typeof(response_snapshot)='object'
        AND response_snapshot->>'id'=selection_id::text
        AND response_snapshot->>'curationId'=curation_id::text
    ),
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT curation_selection_commands_user_curation_fkey
        FOREIGN KEY (user_id, curation_id)
        REFERENCES curations(user_id, id) ON DELETE CASCADE,
    CONSTRAINT curation_selection_commands_selection_fkey
        FOREIGN KEY (user_id, selection_id, curation_id)
        REFERENCES curation_selections(user_id, id, curation_id)
        ON DELETE CASCADE,
    CONSTRAINT curation_selection_commands_user_client_unique
        UNIQUE (user_id, client_command_id)
);

CREATE INDEX idx_curation_selection_commands_curation_created
    ON curation_selection_commands(curation_id, created_at, id);

CREATE FUNCTION reject_curation_selection_command_mutation()
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
    RAISE EXCEPTION 'CURATION_SELECTION_COMMAND_IMMUTABLE'
        USING
            ERRCODE='23514',
            CONSTRAINT='curation_selection_command_immutable';
END
$$;

CREATE TRIGGER curation_selection_command_immutable
BEFORE UPDATE OR DELETE ON curation_selection_commands
FOR EACH ROW
EXECUTE FUNCTION reject_curation_selection_command_mutation();
