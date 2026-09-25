DELETE FROM research_catalog_api_calls WHERE source IN ('KURLY_JSON','ZIGZAG_HTML','LOTTEON_HTML','DAISOMALL_HTML');
DELETE FROM research_catalog_api_quota WHERE source IN ('KURLY_JSON','ZIGZAG_HTML','LOTTEON_HTML','DAISOMALL_HTML');
DELETE FROM research_catalog_source_control_audit WHERE source IN ('KURLY_JSON','ZIGZAG_HTML','LOTTEON_HTML','DAISOMALL_HTML');
DELETE FROM research_catalog_source_control WHERE source IN ('KURLY_JSON','ZIGZAG_HTML','LOTTEON_HTML','DAISOMALL_HTML');
ALTER TABLE research_catalog_api_quota DROP CONSTRAINT research_catalog_api_quota_source_check;
ALTER TABLE research_catalog_api_quota ADD CONSTRAINT research_catalog_api_quota_source_check
 CHECK (source IN ('AMAZON','OWN_PRODUCT','OWN_WEB','NAVER_WEBKR','SERP_GOOGLE','ELEVENST_HTML'));
ALTER TABLE research_catalog_source_control DROP CONSTRAINT research_catalog_source_control_source_check;
ALTER TABLE research_catalog_source_control ADD CONSTRAINT research_catalog_source_control_source_check
 CHECK (source IN ('AMAZON','OWN_PRODUCT','OWN_WEB','NAVER_WEBKR','SERP_GOOGLE','ELEVENST_HTML'));
