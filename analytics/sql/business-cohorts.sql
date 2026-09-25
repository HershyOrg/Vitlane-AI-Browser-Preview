-- PostgreSQL. $1 = observation cutoff (exclusive timestamptz), $2 excluded UUIDs.
-- Business activity retention, not all-user visit retention. Asia/Seoul dates.
WITH activity AS (
 SELECT user_id, created_at AS at FROM curations
 UNION ALL SELECT user_id, created_at FROM curation_conversation_requests
 UNION ALL SELECT user_id, issued_at FROM agency_orders
), days AS (
 SELECT DISTINCT user_id, (at AT TIME ZONE 'Asia/Seoul')::date AS day
 FROM activity WHERE at < $1::timestamptz AND NOT (user_id = ANY($2::uuid[]))
), first_day AS (
 SELECT user_id, min(day) AS day FROM days GROUP BY user_id
), per_user AS (
 SELECT f.user_id, f.day,
 bool_or(d.day BETWEEN f.day + 7 AND f.day + 13) AS returned_w1,
 bool_or(d.day BETWEEN f.day + 30 AND f.day + 59) AS returned_d30_59
 FROM first_day f JOIN days d USING(user_id) GROUP BY f.user_id, f.day
)
SELECT date_trunc('month', day)::date AS cohort_month,
 count(*) AS cohort_accounts,
 count(*) FILTER (WHERE day + 14 <= ($1::timestamptz AT TIME ZONE 'Asia/Seoul')::date) AS w1_eligible,
 count(*) FILTER (WHERE day + 14 <= ($1::timestamptz AT TIME ZONE 'Asia/Seoul')::date AND returned_w1) AS w1_returned,
 count(*) FILTER (WHERE day + 60 <= ($1::timestamptz AT TIME ZONE 'Asia/Seoul')::date) AS d30_59_eligible,
 count(*) FILTER (WHERE day + 60 <= ($1::timestamptz AT TIME ZONE 'Asia/Seoul')::date AND returned_d30_59) AS d30_59_returned
FROM per_user GROUP BY 1 ORDER BY 1;
