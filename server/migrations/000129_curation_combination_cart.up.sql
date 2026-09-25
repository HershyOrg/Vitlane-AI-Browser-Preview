-- User-confirmed Cart command receipts, not cached catalog observations.
CREATE TABLE curation_combination_cart_commands (
 user_id uuid NOT NULL,
 curation_id uuid NOT NULL REFERENCES curations(id) ON DELETE CASCADE,
 id uuid NOT NULL,
 request_hash text NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(user_id,id)
);
CREATE INDEX curation_combination_cart_curation_idx ON curation_combination_cart_commands(curation_id);
