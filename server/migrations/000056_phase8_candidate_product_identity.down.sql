DROP INDEX IF EXISTS phase8_research_candidates_provider_product_identity_idx;

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

-- Rollback restores the 000055 locator-key shape without deleting or merging
-- legitimate distinct Shopify products that currently share one locator. The
-- earliest durable row keeps the exact 000055 locator key so its old app can
-- refresh it; each sibling receives a disjoint Candidate-ID alias that still
-- satisfies the 000055 opaque identity_key contract and its 1024-byte bound.
WITH locator_identity AS (
    SELECT
        candidate.user_id,
        candidate.curation_id,
        candidate.plan_target_id,
        candidate.candidate_id,
        CASE candidate.locator_kind
            WHEN 'PRODUCT_URL' THEN
                'url:' || lower(btrim(candidate.product_url))
            WHEN 'MERCHANT_VARIANT' THEN
                'variant:' || lower(btrim(candidate.seller_domain))
                || ':' || btrim(candidate.variant_id)
        END AS locator_identity_key,
        row_number() OVER (
            PARTITION BY candidate.user_id, candidate.curation_id,
                         candidate.plan_target_id,
                         CASE candidate.locator_kind
                             WHEN 'PRODUCT_URL' THEN
                                 'url:' || lower(btrim(candidate.product_url))
                             WHEN 'MERCHANT_VARIANT' THEN
                                 'variant:' || lower(btrim(candidate.seller_domain))
                                 || ':' || btrim(candidate.variant_id)
                         END
            ORDER BY candidate.first_seen_at, candidate.display_order,
                     candidate.candidate_id
        ) AS locator_rank
    FROM phase8_research_candidates candidate
)
UPDATE phase8_research_candidates candidate
SET identity_key=CASE
    WHEN identity.locator_rank=1 THEN identity.locator_identity_key
    ELSE 'rollback-candidate:' || candidate.candidate_id
END
FROM locator_identity identity
WHERE candidate.user_id=identity.user_id
  AND candidate.curation_id=identity.curation_id
  AND candidate.plan_target_id=identity.plan_target_id
  AND candidate.candidate_id=identity.candidate_id;

DROP FUNCTION IF EXISTS phase8_shopify_product_identity_key(TEXT);
