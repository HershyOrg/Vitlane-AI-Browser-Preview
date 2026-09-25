DELETE FROM research_catalog_api_quota WHERE source IN ('APIFY_MUSINSA','APIFY_29CM','APIFY_GMARKET');
DELETE FROM research_catalog_source_control WHERE source IN ('APIFY_MUSINSA','APIFY_29CM','APIFY_GMARKET');
DROP TABLE research_actor_runs;
