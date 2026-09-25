ALTER TABLE research_external_purchase_records DROP CONSTRAINT research_external_purchase_records_pkey;
ALTER TABLE research_external_purchase_records ALTER COLUMN asin DROP NOT NULL;
ALTER TABLE research_external_purchase_records DROP CONSTRAINT research_external_purchase_records_source_check;
ALTER TABLE research_external_purchase_records DROP CONSTRAINT research_external_purchase_records_marketplace_check;
ALTER TABLE research_external_purchase_records ADD COLUMN product_id TEXT;
ALTER TABLE research_external_purchase_records ADD CONSTRAINT external_purchase_subject_check CHECK (
 (source='AMAZON' AND marketplace='US' AND asin IS NOT NULL AND product_id IS NULL) OR
 (source IN ('COUPANG','ELEVENST') AND marketplace='KR' AND asin IS NULL AND product_id IS NOT NULL AND product_id ~ '^[1-9][0-9]{0,19}$')
);
ALTER TABLE research_external_purchase_records ADD CONSTRAINT external_purchase_variant_identity UNIQUE(user_id,curation_id,source,marketplace,asin);
ALTER TABLE research_external_purchase_records ADD CONSTRAINT external_purchase_product_identity UNIQUE(user_id,curation_id,source,marketplace,product_id);
