-- The expanded operation constraint is retained for historical call receipts.
DELETE FROM research_feed_leases WHERE name='TELEGRAM_JIRUM:links';
DROP TABLE research_feed_links;
ALTER TABLE research_feed_products DROP COLUMN link_url;
DROP TABLE research_feed_checkpoints;
