CREATE TABLE plan_purchase_item_decisions (
    plan_id UUID NOT NULL REFERENCES shopping_plans(id) ON DELETE CASCADE,
    shopping_session_id UUID NOT NULL REFERENCES shopping_sessions(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    decision TEXT NOT NULL CHECK (decision IN ('PURCHASE', 'SKIP')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (plan_id, shopping_session_id)
);

CREATE INDEX idx_plan_purchase_item_decisions_user
    ON plan_purchase_item_decisions(user_id, updated_at DESC);

CREATE TABLE plan_purchase_closures (
    plan_id UUID PRIMARY KEY REFERENCES shopping_plans(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason TEXT NOT NULL CHECK (reason IN ('ALL_PURCHASED', 'USER_STOPPED')),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_plan_purchase_closures_user
    ON plan_purchase_closures(user_id, created_at DESC);
