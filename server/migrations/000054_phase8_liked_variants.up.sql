-- Phase 8 user preference: a liked Variant is durable account state. The row
-- keeps the user's selected locator and the small display snapshot they liked;
-- it is never treated as current price, availability, or checkout authority.
CREATE TABLE phase8_liked_variants (
    user_id UUID NOT NULL,
    curation_id UUID NOT NULL,
    candidate_id TEXT NOT NULL CHECK (
        char_length(btrim(candidate_id)) BETWEEN 1 AND 512
    ),
    variant_id TEXT NOT NULL CHECK (
        char_length(btrim(variant_id)) BETWEEN 1 AND 512
    ),
    product_title TEXT NOT NULL CHECK (
        char_length(btrim(product_title)) BETWEEN 1 AND 240
    ),
    variant_title TEXT NOT NULL CHECK (
        char_length(btrim(variant_title)) BETWEEN 1 AND 240
    ),
    product_url TEXT CHECK (
        product_url IS NULL OR char_length(product_url) BETWEEN 1 AND 2048
    ),
    merchant TEXT NOT NULL CHECK (
        char_length(btrim(merchant)) BETWEEN 1 AND 240
    ),
    price_minor BIGINT NOT NULL CHECK (price_minor >= 0),
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    target_title TEXT NOT NULL CHECK (
        char_length(btrim(target_title)) BETWEEN 1 AND 240
    ),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, candidate_id, variant_id),
    CONSTRAINT phase8_liked_variants_curation_fkey
        FOREIGN KEY (user_id, curation_id)
        REFERENCES curations(user_id, id) ON DELETE CASCADE
);

CREATE INDEX phase8_liked_variants_user_updated_idx
    ON phase8_liked_variants (user_id, updated_at DESC);
