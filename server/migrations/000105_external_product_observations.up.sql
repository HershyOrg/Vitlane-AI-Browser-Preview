ALTER TABLE phase8_research_candidates DROP CONSTRAINT phase8_research_candidates_source_kind_check;
ALTER TABLE phase8_research_candidates ADD CONSTRAINT phase8_research_candidates_source_kind_check
 CHECK (source_kind IN ('SHOPIFY_LIVE','AMAZON','COUPANG','ELEVENST'));
-- The saved, dated product observation supports external browsing without
-- spending another provider call or pretending an unknown Variant exists.
ALTER TABLE phase8_research_candidates ADD COLUMN external_observation JSONB;
ALTER TABLE phase8_research_candidates ADD CONSTRAINT phase8_external_product_reference_check CHECK ((
 (source_kind NOT IN ('COUPANG','ELEVENST') AND external_observation IS NULL) OR
 (source_kind IN ('COUPANG','ELEVENST') AND external_observation IS NOT NULL
  AND jsonb_typeof(external_observation)='object'
  AND provider_product_id ~ '^(coupang|elevenst):KR:[1-9][0-9]{0,19}$'
  AND provider_product_id=lower(source_kind)||':KR:'||(external_observation->'productRef'->>'productId')
  AND identity_key=provider_product_id AND locator_kind='PRODUCT_URL'
  AND external_observation->'productRef'->>'source'=source_kind
  AND product_url=CASE source_kind WHEN 'COUPANG' THEN 'https://www.coupang.com/vp/products/' ELSE 'https://www.11st.co.kr/products/' END || split_part(provider_product_id,':',3)
  AND external_observation->>'schemaVersion'='vitlane.external-product-observation.v1'
  AND external_observation->'productRef'->>'marketplace'='KR'
  AND external_observation->>'priceScope'='PRODUCT'
  AND external_observation->>'productUrl'=product_url)
) IS TRUE);
