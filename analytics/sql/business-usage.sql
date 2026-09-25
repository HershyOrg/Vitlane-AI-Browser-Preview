-- PostgreSQL. Parameters: $1 inclusive start, $2 exclusive end (timestamptz),
-- $3 excluded test/operator user UUIDs. No analytics consent/identifiers involved.
-- Operational facts remain available for refused/unobserved users.
WITH activity AS (
 SELECT user_id, created_at AS at FROM curations
 UNION ALL SELECT user_id, created_at FROM curation_conversation_requests
 UNION ALL SELECT user_id, issued_at FROM agency_orders
), observed AS (
 SELECT * FROM activity WHERE at >= $1::timestamptz AND at < $2::timestamptz
 AND NOT (user_id = ANY($3::uuid[]))
)
SELECT
 (SELECT count(DISTINCT user_id) FROM observed) AS business_active_accounts,
 (SELECT count(*) FROM curations WHERE created_at >= $1 AND created_at < $2
   AND NOT (user_id = ANY($3::uuid[]))) AS curations_created,
 (SELECT count(*) FROM curation_conversation_requests WHERE created_at >= $1 AND created_at < $2
   AND NOT (user_id = ANY($3::uuid[]))) AS curation_requests,
 (SELECT count(*) FROM agency_orders WHERE issued_at >= $1 AND issued_at < $2
   AND NOT (user_id = ANY($3::uuid[]))) AS agency_orders_issued;
