DROP INDEX IF EXISTS research_external_purchase_records_account_idx;
ALTER TABLE research_external_purchase_records DROP CONSTRAINT external_purchase_snapshot_check;
ALTER TABLE research_external_purchase_records
 DROP COLUMN product_title,
 DROP COLUMN variant_title,
 DROP COLUMN merchant,
 DROP COLUMN price_minor,
 DROP COLUMN price_unknown,
 DROP COLUMN currency,
 DROP COLUMN snapshot_at;
