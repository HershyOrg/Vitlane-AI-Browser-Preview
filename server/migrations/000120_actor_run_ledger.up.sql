CREATE TABLE research_actor_runs (
 run_key TEXT PRIMARY KEY CHECK(char_length(btrim(run_key)) BETWEEN 1 AND 200),
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 api_id TEXT NOT NULL CHECK(api_id ~ '^[A-Z][A-Z0-9_]{1,31}$'),
 source TEXT NOT NULL CHECK(source ~ '^[A-Z][A-Z0-9_]{1,31}$'),
 provider_run_id TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK(status IN ('RUNNING','SUCCEEDED','FAILED','ABORTED')),
 item_count INTEGER NOT NULL DEFAULT 0 CHECK(item_count >= 0),
 cost_micros BIGINT NOT NULL DEFAULT 0 CHECK(cost_micros >= 0),
 started_at TIMESTAMPTZ NOT NULL,
 finished_at TIMESTAMPTZ,
 CHECK(finished_at IS NULL OR finished_at >= started_at)
);
CREATE INDEX research_actor_runs_started_at_idx ON research_actor_runs(started_at DESC);

-- Paid Actor paths start Off. The owner turns them on per mall in the
-- operator screen once the Apify plan has the credits they want to spend.
INSERT INTO research_catalog_source_control(source,enabled) VALUES
 ('APIFY_MUSINSA',false),('APIFY_29CM',false),('APIFY_GMARKET',false)
ON CONFLICT (source) DO NOTHING;
INSERT INTO research_catalog_api_quota(source)
 SELECT source FROM research_catalog_source_control
 WHERE source IN ('APIFY_MUSINSA','APIFY_29CM','APIFY_GMARKET')
ON CONFLICT (source) DO NOTHING;
