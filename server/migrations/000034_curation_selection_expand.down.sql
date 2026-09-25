DROP TRIGGER IF EXISTS curation_selection_command_immutable
    ON curation_selection_commands;
DROP FUNCTION IF EXISTS reject_curation_selection_command_mutation();
DROP TABLE IF EXISTS curation_selection_commands;

DROP TRIGGER IF EXISTS curation_selection_immutable
    ON curation_selections;
DROP FUNCTION IF EXISTS enforce_curation_selection_immutability();
DROP TABLE IF EXISTS curation_selections;

DROP FUNCTION IF EXISTS curation_selection_snapshot_hash_v1(
    UUID, UUID, UUID, UUID, UUID, UUID, TEXT, BIGINT, BIGINT
);

ALTER TABLE candidate_configurations
    DROP CONSTRAINT IF EXISTS
        candidate_configurations_selection_lineage_unique,
    DROP CONSTRAINT IF EXISTS
        candidate_configurations_selection_identity_unique;

ALTER TABLE shopping_sessions
    DROP CONSTRAINT IF EXISTS shopping_sessions_user_target_fkey,
    DROP CONSTRAINT IF EXISTS shopping_sessions_user_id_id_target_unique;
