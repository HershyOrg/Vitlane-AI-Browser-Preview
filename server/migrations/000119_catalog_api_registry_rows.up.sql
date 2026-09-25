-- Mall public-page and public-JSON API products join the operator control
-- ledger. The API id set is now owned by the Research app definitions, so the
-- schema keeps only the id grammar. New rows start On: they carry no
-- credential and are bounded by their local caps; operators can switch them
-- off from the API usage page.
ALTER TABLE research_catalog_source_control DROP CONSTRAINT research_catalog_source_control_source_check;
ALTER TABLE research_catalog_source_control ADD CONSTRAINT research_catalog_source_control_source_check
 CHECK (source ~ '^[A-Z][A-Z0-9_]{1,31}$');
ALTER TABLE research_catalog_api_quota DROP CONSTRAINT research_catalog_api_quota_source_check;
ALTER TABLE research_catalog_api_quota ADD CONSTRAINT research_catalog_api_quota_source_check
 CHECK (source ~ '^[A-Z][A-Z0-9_]{1,31}$');
INSERT INTO research_catalog_source_control(source,enabled)
 VALUES ('KURLY_JSON',true),('ZIGZAG_HTML',true),('LOTTEON_HTML',true),('DAISOMALL_HTML',true)
 ON CONFLICT (source) DO NOTHING;
INSERT INTO research_catalog_api_quota(source)
 SELECT source FROM research_catalog_source_control
 WHERE source IN ('KURLY_JSON','ZIGZAG_HTML','LOTTEON_HTML','DAISOMALL_HTML')
 ON CONFLICT (source) DO NOTHING;
