-- Keep Amazon's existing control, quota and call history. The legacy source
-- column now keys the API product; customer-facing platform identity is separate.
ALTER TABLE research_catalog_source_control DROP CONSTRAINT research_catalog_source_control_source_check;
ALTER TABLE research_catalog_source_control ADD CONSTRAINT research_catalog_source_control_source_check CHECK(source IN ('AMAZON','OWN_PRODUCT','OWN_WEB','NAVER_WEBKR','SERP_GOOGLE','ELEVENST_HTML'));
ALTER TABLE research_catalog_api_quota DROP CONSTRAINT research_catalog_api_quota_source_check;
ALTER TABLE research_catalog_api_quota ADD CONSTRAINT research_catalog_api_quota_source_check CHECK(source IN ('AMAZON','OWN_PRODUCT','OWN_WEB','NAVER_WEBKR','SERP_GOOGLE','ELEVENST_HTML'));
INSERT INTO research_catalog_source_control(source,enabled) VALUES ('OWN_PRODUCT',true),('OWN_WEB',false),('NAVER_WEBKR',true),('SERP_GOOGLE',false),('ELEVENST_HTML',true);
INSERT INTO research_catalog_api_quota(source) SELECT source FROM research_catalog_source_control WHERE source<>'AMAZON';
ALTER TABLE research_catalog_api_calls DROP CONSTRAINT research_catalog_api_calls_operation_check;
ALTER TABLE research_catalog_api_calls ADD CONSTRAINT research_catalog_api_calls_operation_check CHECK(operation IN ('SEARCH','DETAIL','USAGE'));
ALTER TABLE research_catalog_api_calls ADD COLUMN http_status INTEGER;
ALTER TABLE research_catalog_api_calls ADD COLUMN retry_after_seconds INTEGER;
