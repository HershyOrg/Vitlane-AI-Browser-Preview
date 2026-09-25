-- Amazon keeps the same kind of dated observation Korean products already keep,
-- with a narrower field set (ADR-0077): the saved ASIN, its title, an optional
-- observed price and the time it was seen. No image, description or seller.
ALTER TABLE phase8_research_candidates ADD COLUMN amazon_observation JSONB;
ALTER TABLE phase8_research_candidates ADD CONSTRAINT phase8_amazon_observation_check CHECK ((
 amazon_observation IS NULL OR (
  source_kind='AMAZON'
  AND jsonb_typeof(amazon_observation)='object'
  AND amazon_observation->>'schemaVersion'='vitlane.amazon-observation.v1'
  AND amazon_observation->'variantRef'->>'source'='AMAZON'
  AND amazon_observation->'variantRef'->>'marketplace'='US'
  AND amazon_observation->'variantRef'->>'asin' ~ '^[A-Z0-9]{10}$'
  AND amazon_observation->>'productUrl'='https://www.amazon.com/dp/' || (amazon_observation->'variantRef'->>'asin')
  AND char_length(btrim(COALESCE(amazon_observation->>'title',''))) BETWEEN 1 AND 2000
  AND (amazon_observation->>'observedAt') IS NOT NULL
  AND amazon_observation->'price'->>'kind' IN ('OBSERVED','UNKNOWN')
  AND NOT (amazon_observation ? 'imageUrl')
  AND NOT (amazon_observation ? 'description')
  AND NOT (amazon_observation ? 'seller')
 )
) IS TRUE);

-- Amazon purchase checks recorded before the snapshot columns existed can be
-- filled from the user's own liked snapshot for the same ASIN when there is
-- one. No provider call and no new content: it is a record the user already made.
UPDATE research_external_purchase_records record SET
 product_title=liked.product_title,
 variant_title=NULLIF(liked.variant_title,''),
 merchant=NULLIF(liked.merchant,''),
 price_unknown=liked.price_unknown,
 price_minor=CASE WHEN liked.price_unknown THEN NULL ELSE liked.price_minor END,
 currency=CASE WHEN liked.price_unknown THEN NULL ELSE liked.currency END,
 snapshot_at=record.recorded_at
FROM phase8_liked_variants liked
WHERE record.source='AMAZON' AND record.snapshot_at IS NULL AND record.asin IS NOT NULL
 AND liked.user_id=record.user_id AND liked.candidate_id=record.candidate_id
 AND liked.variant_id=record.asin
 AND char_length(btrim(liked.product_title)) BETWEEN 1 AND 2000;
