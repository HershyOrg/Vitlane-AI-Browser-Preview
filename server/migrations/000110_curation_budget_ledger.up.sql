-- One current ledger per Curation. total_amount is deliberately not stored.
ALTER TABLE shopping_plans DROP CONSTRAINT shopping_plans_budget_amount_check;
ALTER TABLE shopping_plans ADD CONSTRAINT shopping_plans_budget_amount_check CHECK (budget_amount >= 0);
ALTER TABLE plan_targets DROP CONSTRAINT plan_targets_allocated_amount_check;
ALTER TABLE plan_targets ADD CONSTRAINT plan_targets_allocated_amount_check CHECK (allocated_amount >= 0);

CREATE TABLE curation_budgets (
    curation_id UUID PRIMARY KEY REFERENCES curations(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    currency TEXT NOT NULL CHECK (currency IN ('KRW','USD')),
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    research_version BIGINT NOT NULL DEFAULT 0 CHECK (research_version >= 0),
    allocations JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(allocations) = 'array'),
    initial_request JSONB,
    initial_materialized BOOLEAN NOT NULL DEFAULT TRUE
);

-- Previous hidden plan amounts are provenance, never an opt-in to budgets.
INSERT INTO curation_budgets(curation_id,currency,allocations)
SELECT c.id,CASE WHEN p.budget_currency='USD' THEN 'USD' ELSE 'KRW' END,
  COALESCE((SELECT jsonb_agg(jsonb_build_object('targetId',t.id,'quantity',1,'amount',NULL) ORDER BY t.order_index)
    FROM plan_targets t WHERE t.curation_id=c.id AND t.removed_at IS NULL),'[]'::jsonb)
FROM curations c JOIN shopping_plans p ON p.id=c.shopping_plan_id;

CREATE TABLE curation_budget_commands (
    user_id UUID NOT NULL REFERENCES users(id),
    command_id UUID NOT NULL,
    curation_id UUID NOT NULL REFERENCES curations(id) ON DELETE CASCADE,
    request_hash TEXT NOT NULL,
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(user_id,command_id)
);

CREATE TABLE curation_budget_history (
    curation_id UUID NOT NULL REFERENCES curations(id) ON DELETE CASCADE,
    version BIGINT NOT NULL,
    reason TEXT NOT NULL,
    snapshot JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(curation_id,version)
);

ALTER TABLE phase8_research_pool_commands ADD COLUMN budget_snapshot JSONB;
