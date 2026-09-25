CREATE TABLE research_catalog_observations (
    id UUID PRIMARY KEY,
    research_round_id UUID NOT NULL REFERENCES research_rounds(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_capability_id UUID REFERENCES agent_capabilities(id) ON DELETE RESTRICT,
    agent_grant_id UUID REFERENCES agent_grants(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL CHECK (provider = 'SHOPIFY_UCP_GLOBAL'),
    protocol_version TEXT NOT NULL,
    product_id TEXT NOT NULL,
    variant_id TEXT NOT NULL,
    seller_id TEXT NOT NULL,
    seller_name TEXT NOT NULL,
    seller_domain TEXT NOT NULL,
    product_url TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    image_url TEXT NOT NULL DEFAULT '',
    price_amount NUMERIC(36, 18) NOT NULL CHECK (price_amount > 0),
    price_currency CHAR(3) NOT NULL,
    available BOOLEAN NOT NULL,
    payload_hash TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > observed_at),
    CHECK ((agent_capability_id IS NULL) <> (agent_grant_id IS NULL))
);

CREATE INDEX research_catalog_observations_round_access_idx
    ON research_catalog_observations(
        research_round_id, user_id, agent_capability_id, agent_grant_id, expires_at
    );
