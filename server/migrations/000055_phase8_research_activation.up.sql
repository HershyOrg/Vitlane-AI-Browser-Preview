-- Phase 8 Step 1 R6 activation state.
--
-- Shopify catalog display facts are deliberately absent from the candidate
-- pool tables. Only stable locators and user decisions are durable; current
-- title/price/availability/media are read from Shopify when rendered or when
-- an AgencyOrder preparation preview is requested.

CREATE TABLE phase8_research_pools (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    curation_id UUID NOT NULL,
    plan_target_id UUID NOT NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    expand_ordinal INTEGER NOT NULL DEFAULT 0 CHECK (expand_ordinal >= 0),
    latest_mode TEXT CHECK (latest_mode IN ('REPLACE', 'APPEND')),
    latest_duration_milliseconds BIGINT NOT NULL DEFAULT 0
        CHECK (latest_duration_milliseconds >= 0),
    latest_shopify_calls INTEGER NOT NULL DEFAULT 0
        CHECK (latest_shopify_calls >= 0),
    latest_rate_remaining INTEGER NOT NULL DEFAULT 0
        CHECK (latest_rate_remaining >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, curation_id, plan_target_id),
    CONSTRAINT phase8_research_pools_curation_fkey
        FOREIGN KEY (user_id, curation_id)
        REFERENCES curations(user_id, id) ON DELETE CASCADE,
    CONSTRAINT phase8_research_pools_target_fkey
        FOREIGN KEY (user_id, plan_target_id, curation_id)
        REFERENCES plan_targets(user_id, id, curation_id) ON DELETE CASCADE
);

-- A response-scoped Shopify read cannot hold a PostgreSQL transaction open.
-- This command row is the short durable reservation between preflight and
-- finalize. An opaque fencing token plus a bounded lease lets a later
-- preflight reclaim a crashed provider read without allowing the late result
-- to mutate CandidatePool state. A normal provider fault deletes only its
-- matching RUNNING reservation.
CREATE TABLE phase8_research_pool_commands (
    user_id UUID NOT NULL,
    curation_id UUID NOT NULL,
    plan_target_id UUID NOT NULL,
    idempotency_key TEXT NOT NULL CHECK (
        char_length(btrim(idempotency_key)) BETWEEN 1 AND 200
    ),
    request_hash TEXT NOT NULL CHECK (
        request_hash ~ '^0x[0-9a-f]{64}$'
    ),
    expected_pool_version BIGINT NOT NULL CHECK (expected_pool_version >= 0),
    mode TEXT NOT NULL CHECK (mode IN ('REPLACE', 'APPEND')),
    status TEXT NOT NULL CHECK (status IN ('RUNNING', 'COMPLETED')),
    fencing_token TEXT NOT NULL CHECK (
        fencing_token ~ '^[0-9a-f]{64}$'
    ),
    lease_expires_at TIMESTAMPTZ NOT NULL,
    result_pool_version BIGINT,
    result_expand_ordinal INTEGER,
    result_duration_milliseconds BIGINT,
    result_shopify_calls INTEGER,
    result_rate_remaining INTEGER,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, idempotency_key),
    CONSTRAINT phase8_research_pool_commands_target_fkey
        FOREIGN KEY (user_id, plan_target_id, curation_id)
        REFERENCES plan_targets(user_id, id, curation_id) ON DELETE CASCADE,
    CONSTRAINT phase8_research_pool_commands_lease_window_check CHECK (
        lease_expires_at > created_at
    ),
    CONSTRAINT phase8_research_pool_commands_result_shape_check CHECK (
        (
            status='RUNNING'
            AND result_pool_version IS NULL
            AND result_expand_ordinal IS NULL
            AND result_duration_milliseconds IS NULL
            AND result_shopify_calls IS NULL
            AND result_rate_remaining IS NULL
            AND completed_at IS NULL
        )
        OR
        (
            status='COMPLETED'
            AND result_pool_version > expected_pool_version
            AND result_expand_ordinal >= 0
            AND result_duration_milliseconds >= 0
            AND result_shopify_calls >= 0
            AND result_rate_remaining >= 0
            AND completed_at IS NOT NULL
        )
    )
);

CREATE UNIQUE INDEX phase8_research_pool_commands_one_running_target_idx
    ON phase8_research_pool_commands(user_id, curation_id, plan_target_id)
    WHERE status='RUNNING';

CREATE TABLE phase8_research_candidates (
    user_id UUID NOT NULL,
    curation_id UUID NOT NULL,
    plan_target_id UUID NOT NULL,
    candidate_id TEXT NOT NULL CHECK (
        char_length(btrim(candidate_id)) BETWEEN 1 AND 512
    ),
    provider_product_id TEXT NOT NULL CHECK (
        char_length(btrim(provider_product_id)) BETWEEN 1 AND 512
    ),
    source_kind TEXT NOT NULL CHECK (source_kind='SHOPIFY_LIVE'),
    identity_key TEXT NOT NULL CHECK (
        char_length(btrim(identity_key)) BETWEEN 1 AND 1024
    ),
    locator_kind TEXT NOT NULL CHECK (
        locator_kind IN ('PRODUCT_URL', 'MERCHANT_VARIANT')
    ),
    product_url TEXT,
    variant_id TEXT,
    seller_domain TEXT,
    intent_point_snapshot TEXT NOT NULL DEFAULT '' CHECK (
        char_length(intent_point_snapshot) <= 1000
    ),
    feature_lines_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (
        jsonb_typeof(feature_lines_snapshot)='array'
        AND jsonb_array_length(feature_lines_snapshot) <= 20
    ),
    specification_lines_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (
        jsonb_typeof(specification_lines_snapshot)='array'
        AND jsonb_array_length(specification_lines_snapshot) <= 20
    ),
    visible BOOLEAN NOT NULL DEFAULT true,
    display_order INTEGER NOT NULL CHECK (display_order >= 0),
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, curation_id, plan_target_id, candidate_id),
    UNIQUE (user_id, curation_id, candidate_id),
    UNIQUE (user_id, curation_id, plan_target_id, identity_key),
    CONSTRAINT phase8_research_candidates_pool_fkey
        FOREIGN KEY (user_id, curation_id, plan_target_id)
        REFERENCES phase8_research_pools(user_id, curation_id, plan_target_id)
        ON DELETE CASCADE,
    CONSTRAINT phase8_research_candidates_locator_check CHECK (
        (
            locator_kind='PRODUCT_URL'
            AND product_url IS NOT NULL
            AND char_length(btrim(product_url)) BETWEEN 1 AND 2048
            AND variant_id IS NULL
            AND seller_domain IS NULL
        )
        OR
        (
            locator_kind='MERCHANT_VARIANT'
            AND product_url IS NULL
            AND variant_id IS NOT NULL
            AND char_length(btrim(variant_id)) BETWEEN 1 AND 512
            AND seller_domain IS NOT NULL
            AND char_length(btrim(seller_domain)) BETWEEN 1 AND 255
        )
    )
);

CREATE INDEX phase8_research_candidates_visible_idx
    ON phase8_research_candidates(
        user_id, curation_id, plan_target_id, visible, display_order
    );

-- App preflight provides a provider-call-0 capacity failure. This trigger is
-- the final PostgreSQL invariant and serializes direct/concurrent inserts on
-- the owning pool row. Existing identities remain refreshable at 50.
CREATE FUNCTION phase8_guard_candidate_capacity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    candidate_count INTEGER;
BEGIN
    IF EXISTS (
        SELECT 1
        FROM phase8_research_candidates candidate
        WHERE candidate.user_id=NEW.user_id
          AND candidate.curation_id=NEW.curation_id
          AND candidate.plan_target_id=NEW.plan_target_id
          AND candidate.identity_key=NEW.identity_key
    ) THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM phase8_research_pools pool
    WHERE pool.user_id=NEW.user_id
      AND pool.curation_id=NEW.curation_id
      AND pool.plan_target_id=NEW.plan_target_id
    FOR UPDATE;

    SELECT count(*)
    INTO candidate_count
    FROM phase8_research_candidates candidate
    WHERE candidate.user_id=NEW.user_id
      AND candidate.curation_id=NEW.curation_id
      AND candidate.plan_target_id=NEW.plan_target_id;

    IF candidate_count >= 50 THEN
        RAISE EXCEPTION 'TARGET_CANDIDATE_LIMIT_REACHED'
            USING
                ERRCODE='23514',
                CONSTRAINT='phase8_research_candidates_capacity_guard';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER phase8_research_candidates_capacity_guard
BEFORE INSERT ON phase8_research_candidates
FOR EACH ROW
EXECUTE FUNCTION phase8_guard_candidate_capacity();

-- A liked Variant is written only with its Variant interaction. Binding the
-- account read model to the active Candidate prevents a detached legacy writer
-- from creating an orphan preference outside that atomic command.
ALTER TABLE phase8_liked_variants
    ADD CONSTRAINT phase8_liked_variants_candidate_fkey
    FOREIGN KEY (user_id, curation_id, candidate_id)
    REFERENCES phase8_research_candidates(user_id, curation_id, candidate_id)
    ON DELETE CASCADE;

CREATE TABLE phase8_candidate_configurations (
    user_id UUID NOT NULL,
    curation_id UUID NOT NULL,
    plan_target_id UUID NOT NULL,
    candidate_id TEXT NOT NULL,
    variant_id TEXT NOT NULL CHECK (
        char_length(btrim(variant_id)) BETWEEN 1 AND 512
    ),
    selected_options JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(selected_options)='array'),
    observed_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, curation_id, candidate_id),
    CONSTRAINT phase8_candidate_configurations_candidate_fkey
        FOREIGN KEY (user_id, curation_id, plan_target_id, candidate_id)
        REFERENCES phase8_research_candidates(
            user_id, curation_id, plan_target_id, candidate_id
        ) ON DELETE CASCADE
);

CREATE TABLE phase8_variant_interactions (
    user_id UUID NOT NULL,
    curation_id UUID NOT NULL,
    plan_target_id UUID NOT NULL,
    candidate_id TEXT NOT NULL,
    variant_id TEXT NOT NULL CHECK (
        char_length(btrim(variant_id)) BETWEEN 1 AND 512
    ),
    pinned BOOLEAN NOT NULL DEFAULT false,
    sentiment TEXT NOT NULL DEFAULT 'NONE'
        CHECK (sentiment IN ('NONE', 'LIKE', 'DISLIKE')),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, curation_id, candidate_id, variant_id),
    CONSTRAINT phase8_variant_interactions_candidate_fkey
        FOREIGN KEY (user_id, curation_id, plan_target_id, candidate_id)
        REFERENCES phase8_research_candidates(
            user_id, curation_id, plan_target_id, candidate_id
        ) ON DELETE CASCADE
);

CREATE TABLE phase8_cart_views (
    user_id UUID NOT NULL,
    curation_id UUID NOT NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, curation_id),
    CONSTRAINT phase8_cart_views_curation_fkey
        FOREIGN KEY (user_id, curation_id)
        REFERENCES curations(user_id, id) ON DELETE CASCADE
);

CREATE TABLE phase8_cart_items (
    user_id UUID NOT NULL,
    curation_id UUID NOT NULL,
    cart_item_id TEXT NOT NULL CHECK (
        char_length(btrim(cart_item_id)) BETWEEN 1 AND 1024
    ),
    plan_target_id UUID NOT NULL,
    candidate_id TEXT NOT NULL,
    product_title_snapshot TEXT NOT NULL CHECK (
        char_length(btrim(product_title_snapshot)) BETWEEN 1 AND 240
    ),
    product_url TEXT,
    merchant_name_snapshot TEXT NOT NULL DEFAULT ''
        CHECK (char_length(merchant_name_snapshot) <= 240),
    seller_domain TEXT,
    intent_point_snapshot TEXT NOT NULL DEFAULT ''
        CHECK (char_length(intent_point_snapshot) <= 1000),
    variant_id TEXT NOT NULL CHECK (
        char_length(btrim(variant_id)) BETWEEN 1 AND 512
    ),
    variant_title_snapshot TEXT NOT NULL CHECK (
        char_length(btrim(variant_title_snapshot)) BETWEEN 1 AND 240
    ),
    selected_options JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(selected_options)='array'),
    preview_price_minor BIGINT NOT NULL CHECK (preview_price_minor >= 0),
    preview_currency CHAR(3) NOT NULL CHECK (
        preview_currency = upper(preview_currency)
    ),
    quantity INTEGER NOT NULL CHECK (quantity BETWEEN 1 AND 99),
    observed_at TIMESTAMPTZ NOT NULL,
    added_at TIMESTAMPTZ NOT NULL CHECK (added_at >= observed_at),
    PRIMARY KEY (user_id, curation_id, cart_item_id),
    UNIQUE (user_id, curation_id, candidate_id, variant_id),
    CONSTRAINT phase8_cart_items_view_fkey
        FOREIGN KEY (user_id, curation_id)
        REFERENCES phase8_cart_views(user_id, curation_id) ON DELETE CASCADE,
    CONSTRAINT phase8_cart_items_candidate_fkey
        FOREIGN KEY (user_id, curation_id, plan_target_id, candidate_id)
        REFERENCES phase8_research_candidates(
            user_id, curation_id, plan_target_id, candidate_id
        ) ON DELETE RESTRICT,
    CONSTRAINT phase8_cart_items_product_url_check CHECK (
        product_url IS NULL OR char_length(btrim(product_url)) BETWEEN 1 AND 2048
    ),
    CONSTRAINT phase8_cart_items_seller_domain_check CHECK (
        seller_domain IS NULL OR char_length(btrim(seller_domain)) BETWEEN 1 AND 255
    )
);

CREATE INDEX phase8_cart_items_order_idx
    ON phase8_cart_items(user_id, curation_id, added_at, cart_item_id);
