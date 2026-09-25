DROP TABLE IF EXISTS phase8_cart_items;
DROP TABLE IF EXISTS phase8_cart_views;
DROP TABLE IF EXISTS phase8_variant_interactions;
DROP TABLE IF EXISTS phase8_candidate_configurations;
ALTER TABLE IF EXISTS phase8_liked_variants
    DROP CONSTRAINT IF EXISTS phase8_liked_variants_candidate_fkey;
DROP TRIGGER IF EXISTS phase8_research_candidates_capacity_guard
    ON phase8_research_candidates;
DROP FUNCTION IF EXISTS phase8_guard_candidate_capacity();
DROP TABLE IF EXISTS phase8_research_candidates;
DROP TABLE IF EXISTS phase8_research_pool_commands;
DROP TABLE IF EXISTS phase8_research_pools;
