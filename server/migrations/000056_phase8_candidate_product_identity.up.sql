-- Phase 8 Candidate is a product-level durable choice. Shopify's product ID
-- is the canonical identity; product URL and preview Variant are mutable
-- lookup locators and may change between otherwise identical search results.
CREATE FUNCTION phase8_shopify_product_identity_key(provider_product_id TEXT)
RETURNS TEXT
LANGUAGE sql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT 'shopify-product:' || btrim(provider_product_id)
$$;

-- 000055 was already exercised in local/test. Collapse any locator-keyed
-- duplicate rows before establishing the provider product invariant. The
-- earliest Candidate ID survives so saved UI references remain stable; child
-- rows are rebound to it, with the latest current-state row winning only where
-- two duplicates represented the same configuration/Variant.
CREATE TEMP TABLE phase8_candidate_identity_v56 ON COMMIT DROP AS
SELECT
    candidate.user_id,
    candidate.curation_id,
    candidate.plan_target_id,
    candidate.candidate_id AS old_candidate_id,
    first_value(candidate.candidate_id) OVER identity_partition
        AS survivor_candidate_id,
    phase8_shopify_product_identity_key(candidate.provider_product_id)
        AS canonical_identity_key,
    count(*) OVER identity_partition AS identity_count
FROM phase8_research_candidates candidate
WINDOW identity_partition AS (
    PARTITION BY candidate.user_id, candidate.curation_id,
                 candidate.plan_target_id, btrim(candidate.provider_product_id)
    ORDER BY candidate.first_seen_at, candidate.display_order,
             candidate.candidate_id
    ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
);

CREATE TEMP TABLE phase8_candidate_configurations_v56 ON COMMIT DROP AS
SELECT DISTINCT ON (
    configuration.user_id, configuration.curation_id,
    identity.survivor_candidate_id
)
    configuration.user_id,
    configuration.curation_id,
    configuration.plan_target_id,
    identity.survivor_candidate_id AS candidate_id,
    configuration.variant_id,
    configuration.selected_options,
    configuration.observed_at,
    configuration.updated_at
FROM phase8_candidate_configurations configuration
JOIN phase8_candidate_identity_v56 identity
  ON identity.user_id=configuration.user_id
 AND identity.curation_id=configuration.curation_id
 AND identity.plan_target_id=configuration.plan_target_id
 AND identity.old_candidate_id=configuration.candidate_id
WHERE identity.identity_count > 1
ORDER BY configuration.user_id, configuration.curation_id,
         identity.survivor_candidate_id,
         configuration.updated_at DESC, configuration.candidate_id;

CREATE TEMP TABLE phase8_variant_interactions_v56 ON COMMIT DROP AS
SELECT DISTINCT ON (
    interaction.user_id, interaction.curation_id,
    identity.survivor_candidate_id, interaction.variant_id
)
    interaction.user_id,
    interaction.curation_id,
    interaction.plan_target_id,
    interaction.candidate_id AS source_candidate_id,
    identity.survivor_candidate_id AS candidate_id,
    interaction.variant_id,
    interaction.pinned,
    CASE
        WHEN interaction.sentiment <> 'LIKE' THEN interaction.sentiment
        WHEN EXISTS (
            SELECT 1
            FROM phase8_liked_variants liked
            WHERE liked.user_id=interaction.user_id
              AND liked.curation_id=interaction.curation_id
              AND liked.candidate_id=interaction.candidate_id
              AND liked.variant_id=interaction.variant_id
        ) THEN 'LIKE'
        ELSE 'NONE'
    END AS sentiment,
    interaction.updated_at
FROM phase8_variant_interactions interaction
JOIN phase8_candidate_identity_v56 identity
  ON identity.user_id=interaction.user_id
 AND identity.curation_id=interaction.curation_id
 AND identity.plan_target_id=interaction.plan_target_id
 AND identity.old_candidate_id=interaction.candidate_id
WHERE identity.identity_count > 1
ORDER BY interaction.user_id, interaction.curation_id,
         identity.survivor_candidate_id, interaction.variant_id,
         interaction.updated_at DESC, interaction.candidate_id;

CREATE TEMP TABLE phase8_liked_variants_v56 ON COMMIT DROP AS
SELECT DISTINCT ON (
    liked.user_id, identity.survivor_candidate_id, liked.variant_id
)
    liked.user_id,
    liked.curation_id,
    identity.survivor_candidate_id AS candidate_id,
    liked.variant_id,
    liked.product_title,
    liked.variant_title,
    liked.product_url,
    liked.merchant,
    liked.price_minor,
    liked.currency,
    liked.target_title,
    liked.updated_at
FROM phase8_liked_variants liked
JOIN phase8_candidate_identity_v56 identity
  ON identity.user_id=liked.user_id
 AND identity.curation_id=liked.curation_id
 AND identity.old_candidate_id=liked.candidate_id
JOIN phase8_variant_interactions_v56 interaction
  ON interaction.user_id=liked.user_id
 AND interaction.curation_id=liked.curation_id
 AND interaction.source_candidate_id=liked.candidate_id
 AND interaction.variant_id=liked.variant_id
 AND interaction.sentiment='LIKE'
WHERE identity.identity_count > 1
ORDER BY liked.user_id, identity.survivor_candidate_id, liked.variant_id,
         liked.updated_at DESC, liked.candidate_id;

CREATE TEMP TABLE phase8_cart_items_v56 ON COMMIT DROP AS
SELECT DISTINCT ON (
    item.user_id, item.curation_id,
    identity.survivor_candidate_id, item.variant_id
)
    item.user_id,
    item.curation_id,
    item.cart_item_id,
    item.plan_target_id,
    identity.survivor_candidate_id AS candidate_id,
    item.product_title_snapshot,
    item.product_url,
    item.merchant_name_snapshot,
    item.seller_domain,
    item.intent_point_snapshot,
    item.variant_id,
    item.variant_title_snapshot,
    item.selected_options,
    item.preview_price_minor,
    item.preview_currency,
    item.quantity,
    item.observed_at,
    item.added_at
FROM phase8_cart_items item
JOIN phase8_candidate_identity_v56 identity
  ON identity.user_id=item.user_id
 AND identity.curation_id=item.curation_id
 AND identity.plan_target_id=item.plan_target_id
 AND identity.old_candidate_id=item.candidate_id
WHERE identity.identity_count > 1
ORDER BY item.user_id, item.curation_id,
         identity.survivor_candidate_id, item.variant_id,
         item.added_at DESC, item.cart_item_id;

DELETE FROM phase8_candidate_configurations configuration
USING phase8_candidate_identity_v56 identity
WHERE identity.identity_count > 1
  AND identity.user_id=configuration.user_id
  AND identity.curation_id=configuration.curation_id
  AND identity.plan_target_id=configuration.plan_target_id
  AND identity.old_candidate_id=configuration.candidate_id;

INSERT INTO phase8_candidate_configurations(
    user_id, curation_id, plan_target_id, candidate_id, variant_id,
    selected_options, observed_at, updated_at
)
SELECT user_id, curation_id, plan_target_id, candidate_id, variant_id,
       selected_options, observed_at, updated_at
FROM phase8_candidate_configurations_v56;

DELETE FROM phase8_variant_interactions interaction
USING phase8_candidate_identity_v56 identity
WHERE identity.identity_count > 1
  AND identity.user_id=interaction.user_id
  AND identity.curation_id=interaction.curation_id
  AND identity.plan_target_id=interaction.plan_target_id
  AND identity.old_candidate_id=interaction.candidate_id;

INSERT INTO phase8_variant_interactions(
    user_id, curation_id, plan_target_id, candidate_id, variant_id,
    pinned, sentiment, updated_at
)
SELECT user_id, curation_id, plan_target_id, candidate_id, variant_id,
       pinned, sentiment, updated_at
FROM phase8_variant_interactions_v56;

DELETE FROM phase8_liked_variants liked
USING phase8_candidate_identity_v56 identity
WHERE identity.identity_count > 1
  AND identity.user_id=liked.user_id
  AND identity.curation_id=liked.curation_id
  AND identity.old_candidate_id=liked.candidate_id;

INSERT INTO phase8_liked_variants(
    user_id, curation_id, candidate_id, variant_id, product_title,
    variant_title, product_url, merchant, price_minor, currency,
    target_title, updated_at
)
SELECT user_id, curation_id, candidate_id, variant_id, product_title,
       variant_title, product_url, merchant, price_minor, currency,
       target_title, updated_at
FROM phase8_liked_variants_v56;

DELETE FROM phase8_cart_items item
USING phase8_candidate_identity_v56 identity
WHERE identity.identity_count > 1
  AND identity.user_id=item.user_id
  AND identity.curation_id=item.curation_id
  AND identity.plan_target_id=item.plan_target_id
  AND identity.old_candidate_id=item.candidate_id;

INSERT INTO phase8_cart_items(
    user_id, curation_id, cart_item_id, plan_target_id, candidate_id,
    product_title_snapshot, product_url, merchant_name_snapshot,
    seller_domain, intent_point_snapshot, variant_id,
    variant_title_snapshot, selected_options, preview_price_minor,
    preview_currency, quantity, observed_at, added_at
)
SELECT user_id, curation_id, cart_item_id, plan_target_id, candidate_id,
       product_title_snapshot, product_url, merchant_name_snapshot,
       seller_domain, intent_point_snapshot, variant_id,
       variant_title_snapshot, selected_options, preview_price_minor,
       preview_currency, quantity, observed_at, added_at
FROM phase8_cart_items_v56;

WITH survivor_state AS (
    SELECT
        identity.user_id,
        identity.curation_id,
        identity.plan_target_id,
        identity.survivor_candidate_id,
        bool_or(candidate.visible) AS visible,
        min(candidate.display_order) AS display_order,
        min(candidate.first_seen_at) AS first_seen_at,
        max(candidate.last_seen_at) AS last_seen_at
    FROM phase8_candidate_identity_v56 identity
    JOIN phase8_research_candidates candidate
      ON candidate.user_id=identity.user_id
     AND candidate.curation_id=identity.curation_id
     AND candidate.plan_target_id=identity.plan_target_id
     AND candidate.candidate_id=identity.old_candidate_id
    WHERE identity.identity_count > 1
    GROUP BY identity.user_id, identity.curation_id,
             identity.plan_target_id, identity.survivor_candidate_id
), survivor_latest AS (
    SELECT DISTINCT ON (
        identity.user_id, identity.curation_id,
        identity.plan_target_id, identity.survivor_candidate_id
    )
        identity.user_id,
        identity.curation_id,
        identity.plan_target_id,
        identity.survivor_candidate_id,
        candidate.provider_product_id,
        candidate.source_kind,
        candidate.locator_kind,
        candidate.product_url,
        candidate.variant_id,
        candidate.seller_domain,
        candidate.intent_point_snapshot,
        candidate.feature_lines_snapshot,
        candidate.specification_lines_snapshot
    FROM phase8_candidate_identity_v56 identity
    JOIN phase8_research_candidates candidate
      ON candidate.user_id=identity.user_id
     AND candidate.curation_id=identity.curation_id
     AND candidate.plan_target_id=identity.plan_target_id
     AND candidate.candidate_id=identity.old_candidate_id
    WHERE identity.identity_count > 1
    ORDER BY identity.user_id, identity.curation_id,
             identity.plan_target_id, identity.survivor_candidate_id,
             candidate.last_seen_at DESC, candidate.candidate_id
)
UPDATE phase8_research_candidates candidate
SET provider_product_id=latest.provider_product_id,
    source_kind=latest.source_kind,
    locator_kind=latest.locator_kind,
    product_url=latest.product_url,
    variant_id=latest.variant_id,
    seller_domain=latest.seller_domain,
    intent_point_snapshot=latest.intent_point_snapshot,
    feature_lines_snapshot=latest.feature_lines_snapshot,
    specification_lines_snapshot=latest.specification_lines_snapshot,
    visible=state.visible,
    display_order=state.display_order,
    first_seen_at=state.first_seen_at,
    last_seen_at=state.last_seen_at
FROM survivor_state state
JOIN survivor_latest latest
  ON latest.user_id=state.user_id
 AND latest.curation_id=state.curation_id
 AND latest.plan_target_id=state.plan_target_id
 AND latest.survivor_candidate_id=state.survivor_candidate_id
WHERE candidate.user_id=state.user_id
  AND candidate.curation_id=state.curation_id
  AND candidate.plan_target_id=state.plan_target_id
  AND candidate.candidate_id=state.survivor_candidate_id;

DELETE FROM phase8_research_candidates candidate
USING phase8_candidate_identity_v56 identity
WHERE identity.identity_count > 1
  AND identity.old_candidate_id <> identity.survivor_candidate_id
  AND candidate.user_id=identity.user_id
  AND candidate.curation_id=identity.curation_id
  AND candidate.plan_target_id=identity.plan_target_id
  AND candidate.candidate_id=identity.old_candidate_id;

UPDATE phase8_research_candidates
SET provider_product_id=btrim(provider_product_id),
    identity_key=phase8_shopify_product_identity_key(provider_product_id);

CREATE UNIQUE INDEX phase8_research_candidates_provider_product_identity_idx
    ON phase8_research_candidates(
        user_id, curation_id, plan_target_id, provider_product_id
    );

-- Capacity checks use the same product-level identity as repository upsert.
-- A locator refresh of an existing product remains legal at the 50-row cap.
CREATE OR REPLACE FUNCTION phase8_guard_candidate_capacity()
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
          AND candidate.provider_product_id=NEW.provider_product_id
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
