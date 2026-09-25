CREATE TABLE research_product_reactions (
    user_id UUID NOT NULL,
    curation_id UUID NOT NULL,
    plan_target_id UUID NOT NULL,
    candidate_id TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('COUPANG', 'ELEVENST')),
    marketplace TEXT NOT NULL CHECK (marketplace = 'KR'),
    product_id TEXT NOT NULL CHECK (product_id ~ '^[1-9][0-9]{0,19}$'),
    pinned BOOLEAN NOT NULL,
    sentiment TEXT NOT NULL CHECK (sentiment IN ('NONE', 'LIKE', 'DISLIKE')),
    version BIGINT NOT NULL CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL,
    liked_snapshot JSONB,
    PRIMARY KEY (user_id, curation_id, candidate_id),
    UNIQUE (user_id, curation_id, plan_target_id, source, marketplace, product_id),
    FOREIGN KEY (user_id, curation_id, plan_target_id, candidate_id)
        REFERENCES phase8_research_candidates(user_id, curation_id, plan_target_id, candidate_id) ON DELETE CASCADE,
    CHECK (((sentiment = 'LIKE' AND liked_snapshot IS NOT NULL
        AND jsonb_typeof(liked_snapshot) = 'object'
        AND liked_snapshot->'productRef'->>'source' = source
        AND liked_snapshot->'productRef'->>'marketplace' = marketplace
        AND liked_snapshot->'productRef'->>'productId' = product_id)
        OR (sentiment <> 'LIKE' AND liked_snapshot IS NULL)) IS TRUE)
);
CREATE INDEX research_product_reactions_liked ON research_product_reactions(user_id, updated_at DESC) WHERE sentiment = 'LIKE';
