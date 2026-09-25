ALTER TABLE research_actor_runs ADD COLUMN reserved_micros bigint NOT NULL DEFAULT 0 CHECK(reserved_micros>=0);
UPDATE research_actor_runs SET reserved_micros=CASE WHEN api_id='APIFY_GMARKET' THEN 10000 ELSE 25000 END WHERE status='RUNNING';
CREATE TABLE research_provider_account_usage(
 account_id text NOT NULL,
 period_start timestamptz NOT NULL,
 used_micros bigint NOT NULL CHECK(used_micros>=0),
 PRIMARY KEY(account_id,period_start)
);
INSERT INTO research_provider_account_usage(account_id,period_start,used_micros)
 SELECT 'apify',date_trunc('month',started_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
 SUM(CASE WHEN status='RUNNING' OR provider_run_id='' THEN GREATEST(cost_micros,reserved_micros) ELSE cost_micros END)
 FROM research_actor_runs GROUP BY 2;
CREATE TABLE research_route_decisions(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 attempt_key text NOT NULL,
 route_id text NOT NULL,
 policy_version text NOT NULL,
 product_vertical text NOT NULL,
 api_ids jsonb NOT NULL,
 pressure double precision NOT NULL CHECK(pressure>=0 AND pressure<=1),
 weight double precision NOT NULL CHECK(weight>=0),
 decision text NOT NULL,
 reason text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL,
 UNIQUE(user_id,attempt_key,route_id)
);
CREATE INDEX research_route_decisions_created ON research_route_decisions(created_at);

ALTER TABLE plan_targets ADD COLUMN product_vertical text NOT NULL DEFAULT '' CHECK(product_vertical IN ('','GENERAL','FASHION','BEAUTY','FOOD','LIVING','ELECTRONICS'));
