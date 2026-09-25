-- Account purchase-check list (ADR-0075). The snapshot is what the user saw when
-- they marked the purchase: mutable self-report context, never a catalog, price
-- or order authority. Amazon rows recorded before this migration keep no
-- snapshot; the subject (ASIN) and its product page remain known.
ALTER TABLE research_external_purchase_records
 ADD COLUMN product_title TEXT CHECK (product_title IS NULL OR char_length(btrim(product_title)) BETWEEN 1 AND 2000),
 ADD COLUMN variant_title TEXT CHECK (variant_title IS NULL OR char_length(btrim(variant_title)) BETWEEN 1 AND 240),
 ADD COLUMN merchant TEXT CHECK (merchant IS NULL OR char_length(btrim(merchant)) BETWEEN 1 AND 240),
 ADD COLUMN price_minor BIGINT CHECK (price_minor IS NULL OR price_minor >= 0),
 ADD COLUMN price_unknown BOOLEAN,
 ADD COLUMN currency TEXT CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
 ADD COLUMN snapshot_at TIMESTAMPTZ;
ALTER TABLE research_external_purchase_records ADD CONSTRAINT external_purchase_snapshot_check CHECK (
 (snapshot_at IS NULL AND product_title IS NULL AND variant_title IS NULL AND merchant IS NULL
  AND price_minor IS NULL AND price_unknown IS NULL AND currency IS NULL)
 OR (snapshot_at IS NOT NULL AND product_title IS NOT NULL AND price_unknown IS NOT NULL
  AND ((price_unknown AND price_minor IS NULL) OR (NOT price_unknown AND price_minor IS NOT NULL AND currency IS NOT NULL)))
);
CREATE INDEX research_external_purchase_records_account_idx
 ON research_external_purchase_records(user_id, recorded_at DESC) WHERE checked;
-- Korean product rows are backfilled from the saved product observation the
-- server already owns. The snapshot time is the recording time of the check.
UPDATE research_external_purchase_records r SET
 product_title=left(btrim(c.external_observation->>'title'),2000),
 merchant=CASE WHEN c.external_observation->'seller'->>'kind'='KNOWN'
  AND char_length(btrim(COALESCE(c.external_observation->'seller'->>'name',''))) BETWEEN 1 AND 240
  THEN btrim(c.external_observation->'seller'->>'name') END,
 price_unknown=NOT (c.external_observation->'price'->>'kind'='OBSERVED'
  AND (c.external_observation->'price'->>'amountMinor') IS NOT NULL
  AND c.external_observation->'price'->>'currency' ~ '^[A-Z]{3}$'),
 price_minor=CASE WHEN c.external_observation->'price'->>'kind'='OBSERVED'
  AND (c.external_observation->'price'->>'amountMinor') IS NOT NULL
  AND c.external_observation->'price'->>'currency' ~ '^[A-Z]{3}$'
  THEN (c.external_observation->'price'->>'amountMinor')::bigint END,
 currency=CASE WHEN c.external_observation->'price'->>'kind'='OBSERVED'
  AND (c.external_observation->'price'->>'amountMinor') IS NOT NULL
  AND c.external_observation->'price'->>'currency' ~ '^[A-Z]{3}$'
  THEN c.external_observation->'price'->>'currency' END,
 snapshot_at=r.recorded_at
FROM phase8_research_candidates c
WHERE r.product_id IS NOT NULL AND r.snapshot_at IS NULL
 AND c.user_id=r.user_id AND c.curation_id=r.curation_id AND c.candidate_id=r.candidate_id
 AND c.external_observation IS NOT NULL
 AND char_length(btrim(COALESCE(c.external_observation->>'title','')))>=1
 AND COALESCE((c.external_observation->'price'->>'amountMinor')::bigint,0)>=0;
