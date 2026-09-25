DROP TABLE IF EXISTS research_provider_account_usage;
DROP TABLE research_route_decisions;
ALTER TABLE research_actor_runs DROP COLUMN reserved_micros;
ALTER TABLE plan_targets DROP COLUMN product_vertical;
