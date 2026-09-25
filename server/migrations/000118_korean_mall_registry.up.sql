-- Korean malls are a registry in Research domain code; the customer-facing
-- Source is any registered mall, not the two hard-coded ones. The identity
-- checks stay: the saved observation names the same product the row claims and
-- the outbound URL is the observation's canonical URL. Per-mall URL templates
-- and id grammars now belong to the registry, not to this schema.
ALTER TABLE phase8_research_candidates DROP CONSTRAINT phase8_research_candidates_source_kind_check;
ALTER TABLE phase8_research_candidates ADD CONSTRAINT phase8_research_candidates_source_kind_check
 CHECK (source_kind IN ('SHOPIFY_LIVE','AMAZON') OR source_kind ~ '^[A-Z][A-Z0-9_]{1,31}$');
ALTER TABLE phase8_research_candidates DROP CONSTRAINT phase8_external_product_reference_check;
ALTER TABLE phase8_research_candidates ADD CONSTRAINT phase8_external_product_reference_check CHECK ((
 (source_kind IN ('SHOPIFY_LIVE','AMAZON') AND external_observation IS NULL) OR
 (source_kind NOT IN ('SHOPIFY_LIVE','AMAZON') AND external_observation IS NOT NULL
  AND jsonb_typeof(external_observation)='object'
  AND external_observation->'productRef'->>'productId' ~ '^[A-Za-z0-9][A-Za-z0-9/_-]{0,79}$'
  AND provider_product_id=lower(source_kind)||':KR:'||(external_observation->'productRef'->>'productId')
  AND identity_key=provider_product_id AND locator_kind='PRODUCT_URL'
  AND external_observation->'productRef'->>'source'=source_kind
  AND external_observation->'productRef'->>'marketplace'='KR'
  AND external_observation->>'schemaVersion'='vitlane.external-product-observation.v1'
  AND external_observation->>'priceScope'='PRODUCT'
  AND product_url ~ '^https://'
  AND external_observation->>'productUrl'=product_url)
) IS TRUE);

ALTER TABLE research_external_purchase_records DROP CONSTRAINT external_purchase_subject_check;
ALTER TABLE research_external_purchase_records ADD CONSTRAINT external_purchase_subject_check CHECK (
 (source='AMAZON' AND marketplace='US' AND asin IS NOT NULL AND product_id IS NULL) OR
 (source NOT IN ('AMAZON','SHOPIFY') AND source ~ '^[A-Z][A-Z0-9_]{1,31}$' AND marketplace='KR'
  AND asin IS NULL AND product_id IS NOT NULL AND product_id ~ '^[A-Za-z0-9][A-Za-z0-9/_-]{0,79}$')
);

ALTER TABLE research_product_reactions DROP CONSTRAINT research_product_reactions_source_check;
ALTER TABLE research_product_reactions DROP CONSTRAINT research_product_reactions_product_id_check;
ALTER TABLE research_product_reactions ADD CONSTRAINT research_product_reactions_source_check
 CHECK (source NOT IN ('AMAZON','SHOPIFY') AND source ~ '^[A-Z][A-Z0-9_]{1,31}$');
ALTER TABLE research_product_reactions ADD CONSTRAINT research_product_reactions_product_id_check
 CHECK (product_id ~ '^[A-Za-z0-9][A-Za-z0-9/_-]{0,79}$');
