-- Additive recovery state. Existing observations and user selections are retained.
CREATE TABLE research_feed_checkpoints (
 provider text PRIMARY KEY, version bigint NOT NULL DEFAULT 0,
 last_id bigint NOT NULL DEFAULT 0 CHECK(last_id>=0),
 head_id bigint NOT NULL DEFAULT 0 CHECK(head_id>=0),
 before_id bigint NOT NULL DEFAULT 0 CHECK(before_id>=0)
);
INSERT INTO research_feed_checkpoints(provider,last_id)
SELECT 'TELEGRAM_JIRUM',COALESCE(max(split_part(external_id,'/',2)::bigint),0)
FROM research_feed_products WHERE provider='TELEGRAM_JIRUM' AND external_id ~ '^jirum/[0-9]{1,15}$';
ALTER TABLE research_feed_products ADD COLUMN link_url text NOT NULL DEFAULT '';
CREATE TABLE research_feed_links (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), provider text NOT NULL, url text NOT NULL,
 status text NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','RUNNING','RESOLVED','FAILED','EXPIRED')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 4),
 next_at timestamptz NOT NULL DEFAULT now(), lease_until timestamptz, token uuid,
 expires_at timestamptz NOT NULL, product_ref jsonb, reason text NOT NULL DEFAULT '',
 UNIQUE(provider,url)
);
CREATE INDEX research_feed_links_due ON research_feed_links(provider,next_at) WHERE status IN ('PENDING','RUNNING');
INSERT INTO research_feed_leases(name) VALUES ('TELEGRAM_JIRUM:links');
ALTER TABLE research_catalog_api_calls DROP CONSTRAINT research_catalog_api_calls_operation_check;
ALTER TABLE research_catalog_api_calls ADD CONSTRAINT research_catalog_api_calls_operation_check
 CHECK(operation IN ('SEARCH','DETAIL','USAGE','FEED','FEED_PAGE','LINK_RESOLVE'));
