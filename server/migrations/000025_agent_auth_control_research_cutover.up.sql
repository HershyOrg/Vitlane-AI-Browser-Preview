-- Research submissions and catalog observations move from reusable AgentGrant
-- provenance to one exact AgentControl WorkOrder. Legacy grant provenance stays
-- readable during the vertical cutover, but every new row must choose exactly
-- one provenance model.

ALTER TABLE agent_work_orders
    ADD CONSTRAINT agent_work_orders_research_identity_key
        UNIQUE (id, research_round_id);

ALTER TABLE research_submissions
    ALTER COLUMN agent_grant_id DROP NOT NULL,
    ADD COLUMN agent_work_order_id UUID,
    ADD CONSTRAINT research_submissions_agent_work_order_fkey
        FOREIGN KEY (agent_work_order_id, research_round_id)
        REFERENCES agent_work_orders(id, research_round_id)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT research_submissions_agent_control_provenance_check
        CHECK (num_nonnulls(agent_grant_id, agent_work_order_id) = 1);

CREATE UNIQUE INDEX research_submissions_work_order_idempotency_idx
    ON research_submissions(
        research_round_id,
        agent_work_order_id,
        client_submission_id
    )
    WHERE agent_work_order_id IS NOT NULL;

ALTER TABLE research_catalog_observations
    ALTER COLUMN agent_grant_id DROP NOT NULL,
    ADD COLUMN agent_work_order_id UUID,
    ADD CONSTRAINT research_catalog_observations_agent_work_order_fkey
        FOREIGN KEY (agent_work_order_id, research_round_id)
        REFERENCES agent_work_orders(id, research_round_id)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT research_catalog_observations_agent_control_provenance_check
        CHECK (num_nonnulls(agent_grant_id, agent_work_order_id) = 1);

CREATE INDEX research_catalog_observations_work_order_access_idx
    ON research_catalog_observations(
        research_round_id,
        user_id,
        agent_work_order_id,
        expires_at
    )
    WHERE agent_work_order_id IS NOT NULL;
