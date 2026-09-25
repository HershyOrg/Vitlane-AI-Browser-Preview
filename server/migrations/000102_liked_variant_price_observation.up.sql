-- Saved reactions are valid even when the product price is unobserved.
ALTER TABLE phase8_liked_variants ADD COLUMN price_unknown BOOLEAN NOT NULL DEFAULT false;
