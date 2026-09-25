CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('ACTIVE')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS wallets (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    address TEXT NOT NULL,
    chain_id TEXT NOT NULL,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, chain_id, address)
);

CREATE TABLE IF NOT EXISTS shopping_plans (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    original_intent TEXT NOT NULL,
    plan_mode TEXT NOT NULL CHECK (plan_mode IN ('SINGLE_PRODUCT', 'MULTI_PRODUCT')),
    execution_mode TEXT NOT NULL CHECK (execution_mode IN ('EXPERIMENT', 'LIVE')),
    budget_amount NUMERIC(36, 18) NOT NULL CHECK (budget_amount > 0),
    budget_currency CHAR(3) NOT NULL,
    country CHAR(2) NOT NULL,
    city TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS plan_targets (
    id UUID PRIMARY KEY,
    plan_id UUID NOT NULL REFERENCES shopping_plans(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    normalized_intent TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    allocated_amount NUMERIC(36, 18) NOT NULL CHECK (allocated_amount > 0),
    allocated_currency CHAR(3) NOT NULL,
    country CHAR(2) NOT NULL,
    city TEXT NOT NULL DEFAULT '',
    allowed_items TEXT[] NOT NULL DEFAULT '{}',
    blocked_items TEXT[] NOT NULL DEFAULT '{}',
    min_price_amount NUMERIC(36, 18),
    max_price_amount NUMERIC(36, 18),
    price_currency CHAR(3),
    reference_url TEXT NOT NULL DEFAULT '',
    url_mode TEXT NOT NULL CHECK (url_mode IN ('NONE', 'REFERENCE', 'EXACT_PRODUCT')),
    order_index INTEGER NOT NULL,
    confirmed_at TIMESTAMPTZ,
    target_hash TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (plan_id, order_index),
    CHECK (
        (min_price_amount IS NULL AND max_price_amount IS NULL AND price_currency IS NULL)
        OR price_currency IS NOT NULL
    ),
    CHECK (min_price_amount IS NULL OR min_price_amount >= 0),
    CHECK (max_price_amount IS NULL OR max_price_amount >= 0),
    CHECK (min_price_amount IS NULL OR max_price_amount IS NULL OR min_price_amount <= max_price_amount)
);

CREATE TABLE IF NOT EXISTS shopping_sessions (
    id UUID PRIMARY KEY,
    plan_target_id UUID NOT NULL REFERENCES plan_targets(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_snapshot JSONB NOT NULL,
    research_scope_snapshot JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('READY')),
    version BIGINT NOT NULL CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (plan_target_id)
);

CREATE INDEX IF NOT EXISTS idx_shopping_plans_user_id ON shopping_plans(user_id);
CREATE INDEX IF NOT EXISTS idx_shopping_sessions_user_id ON shopping_sessions(user_id);
