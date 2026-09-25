-- The Shopify catalog (UCP search and lookup) joins the operator's API call
-- ledger. Until now its calls were counted only per response and in a
-- process-local rate window, so the API usage page showed neither how often
-- Shopify was called nor how those calls failed. The row starts On: the ledger
-- records every call and honours the operator switch; the binding rate guard
-- stays the in-process window configured for catalog research.
INSERT INTO research_catalog_source_control(source,enabled)
 VALUES ('SHOPIFY_UCP',true)
 ON CONFLICT (source) DO NOTHING;
INSERT INTO research_catalog_api_quota(source)
 SELECT source FROM research_catalog_source_control
 WHERE source='SHOPIFY_UCP'
 ON CONFLICT (source) DO NOTHING;
