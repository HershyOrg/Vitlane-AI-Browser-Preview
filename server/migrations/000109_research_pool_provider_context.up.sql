-- Existing deterministic expansion must keep the last search provider context.
-- Next-round settings may already differ while old results remain visible.
ALTER TABLE phase8_research_pools ADD COLUMN provider_country TEXT CHECK (provider_country IN ('KR','US'));
ALTER TABLE phase8_research_pools ADD COLUMN provider_query TEXT CHECK (length(provider_query) BETWEEN 1 AND 2000);
