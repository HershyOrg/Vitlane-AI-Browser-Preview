-- Planning proposals move to exact AgentControl WorkOrder provenance.
-- Legacy AgentGrant rows remain readable while old tools compile, but every
-- proposal chooses exactly one provenance model.

ALTER TABLE agent_work_orders
    ADD CONSTRAINT agent_work_orders_planning_identity_key
        UNIQUE (id, planning_task_id, user_id);

ALTER TABLE planning_proposals
    ALTER COLUMN agent_grant_id DROP NOT NULL,
    ADD COLUMN agent_work_order_id UUID,
    ADD CONSTRAINT planning_proposals_agent_work_order_fkey
        FOREIGN KEY (agent_work_order_id, task_id, user_id)
        REFERENCES agent_work_orders(id, planning_task_id, user_id)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT planning_proposals_agent_control_provenance_check
        CHECK (num_nonnulls(agent_grant_id, agent_work_order_id) = 1);

CREATE UNIQUE INDEX planning_proposals_work_order_idempotency_idx
    ON planning_proposals(
        task_id,
        agent_work_order_id,
        client_proposal_id
    )
    WHERE agent_work_order_id IS NOT NULL;
