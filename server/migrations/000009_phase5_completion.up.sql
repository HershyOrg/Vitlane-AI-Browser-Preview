ALTER TABLE purchases
    ADD COLUMN supersedes_purchase_id UUID REFERENCES purchases(id) ON DELETE RESTRICT;

ALTER TABLE purchases
    ADD CONSTRAINT purchases_supersedes_purchase_id_key UNIQUE (supersedes_purchase_id);

ALTER TABLE purchases
    DROP CONSTRAINT purchases_user_id_shopping_session_id_candidate_id_quantity_key;

CREATE UNIQUE INDEX idx_purchases_active_candidate_quantity
    ON purchases(user_id, shopping_session_id, candidate_id, quantity)
    WHERE status <> 'SUPERSEDED';

CREATE INDEX idx_purchases_supersedes
    ON purchases(supersedes_purchase_id)
    WHERE supersedes_purchase_id IS NOT NULL;
