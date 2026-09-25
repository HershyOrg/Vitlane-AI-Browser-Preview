-- Anonymous, ephemeral merchant-page discovery through the separately
-- authenticated Vitlane Browser Fork. The row starts On, but the runtime is
-- configured only when BROWSER_PRODUCT_SEARCH_ENABLED and its bridge pairing
-- token are present. Source identity remains the merchant platform.
INSERT INTO research_catalog_source_control(source,enabled)
 VALUES ('BROWSER_MERCHANT',true)
 ON CONFLICT (source) DO NOTHING;
INSERT INTO research_catalog_api_quota(source)
 SELECT source FROM research_catalog_source_control
 WHERE source='BROWSER_MERCHANT'
 ON CONFLICT (source) DO NOTHING;
