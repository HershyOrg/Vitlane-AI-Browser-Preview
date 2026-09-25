-- Structural rollback only, and only partially. ADR-0038 approved this cutover
-- as irreversible: the agent runtime rows are gone and no down migration can
-- invent them. The 19 dropped tables are not recreated here either — an empty
-- shell would let an older binary start against a database that cannot serve
-- it, which is worse than refusing. A production rollback is a restore from the
-- pre-migration backup.
--
-- What this does undo is the two changes that would otherwise reject writes an
-- older binary still makes.

ALTER TABLE research_rounds
    ADD COLUMN current_agent_grant_id UUID;
ALTER TABLE planning_tasks
    ADD COLUMN current_agent_grant_id UUID;

ALTER TABLE curation_actions
    DROP CONSTRAINT curation_actions_effect_kind_check;
UPDATE curation_actions SET effect_kind='AGENT_WORK' WHERE effect_kind='INTELLIGENCE';
ALTER TABLE curation_actions
    ADD CONSTRAINT curation_actions_effect_kind_check CHECK (
        effect_kind IN ('NONE', 'AGENT_WORK', 'PURCHASE_THREAD'));

-- The 000045 audit goes; the 000030 audit it did not create stays.
DROP TABLE IF EXISTS agent_runtime_cutover_audits;
