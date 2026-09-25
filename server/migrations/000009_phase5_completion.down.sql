DROP INDEX IF EXISTS idx_purchases_supersedes;
DROP INDEX IF EXISTS idx_purchases_active_candidate_quantity;

ALTER TABLE purchases
    ADD CONSTRAINT purchases_user_id_shopping_session_id_candidate_id_quantity_key
    UNIQUE (user_id, shopping_session_id, candidate_id, quantity);

ALTER TABLE purchases
    DROP CONSTRAINT IF EXISTS purchases_supersedes_purchase_id_key;

ALTER TABLE purchases
    DROP COLUMN IF EXISTS supersedes_purchase_id;
