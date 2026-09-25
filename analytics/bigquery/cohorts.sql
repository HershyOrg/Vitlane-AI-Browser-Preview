-- GoogleSQL. Replace YOUR_PROJECT.analytics_PROPERTY.
-- @as_of DATE is an exclusive Asia/Seoul cutoff. @export_start DATE is the first
-- COMPLETE daily-export date. Never read events_intraday or infer earlier history.
-- This query measures CONSENTED OBSERVED users only; user_id is an HMAC pseudonym.
WITH raw AS (
 SELECT event_timestamp, event_name, user_id, user_pseudo_id,
 COALESCE(
   (SELECT value.string_value FROM UNNEST(event_params) WHERE key='event_key'),
   (SELECT value.string_value FROM UNNEST(event_params) WHERE key='event_id')
 ) AS event_id,
 (SELECT value.int_value FROM UNNEST(event_params) WHERE key='schema_version') AS schema_version,
 (SELECT value.string_value FROM UNNEST(event_params) WHERE key='action') AS action
 FROM `YOUR_PROJECT.analytics_PROPERTY.events_*`
 WHERE REGEXP_CONTAINS(_TABLE_SUFFIX, r'^\d{8}$')
 AND _TABLE_SUFFIX BETWEEN FORMAT_DATE('%Y%m%d', @export_start) AND FORMAT_DATE('%Y%m%d', DATE_SUB(@as_of, INTERVAL 1 DAY))
), dedup AS (
 SELECT * FROM raw WHERE schema_version=1 AND event_id IS NOT NULL
 QUALIFY ROW_NUMBER() OVER(PARTITION BY event_name, event_id ORDER BY event_timestamp)=1
), qualified AS (
 SELECT user_id, event_name, TIMESTAMP_MICROS(event_timestamp) AS occurred_at,
 DATE(TIMESTAMP_MICROS(event_timestamp),'Asia/Seoul') AS day
 FROM dedup WHERE user_id IS NOT NULL AND user_id != ''
 AND (event_name IN ('curation_created','research_results_viewed','candidate_viewed','external_merchant_opened','order_sheet_viewed','agency_order_issued')
 OR (event_name='candidate_reacted' AND action IN ('LIKE','DISLIKE','NONE'))
 OR (event_name='external_purchase_reported' AND action='REPORTED'))
), first_seen AS (
 SELECT user_id, MIN(day) AS day FROM qualified GROUP BY user_id
), users AS (
 SELECT f.user_id, f.day,
 COUNTIF(q.day BETWEEN DATE_ADD(f.day,INTERVAL 7 DAY) AND DATE_ADD(f.day,INTERVAL 13 DAY)) > 0 AS w1,
 COUNTIF(q.day BETWEEN DATE_ADD(f.day,INTERVAL 30 DAY) AND DATE_ADD(f.day,INTERVAL 59 DAY)) > 0 AS d30_59
 FROM first_seen f JOIN qualified q USING(user_id) GROUP BY f.user_id,f.day
)
SELECT DATE_TRUNC(day,MONTH) AS observed_cohort_month, COUNT(*) AS observed_accounts,
 COUNTIF(DATE_ADD(day,INTERVAL 14 DAY)<=@as_of) AS w1_eligible,
 COUNTIF(DATE_ADD(day,INTERVAL 14 DAY)<=@as_of AND w1) AS w1_returned,
 COUNTIF(DATE_ADD(day,INTERVAL 60 DAY)<=@as_of) AS d30_59_eligible,
 COUNTIF(DATE_ADD(day,INTERVAL 60 DAY)<=@as_of AND d30_59) AS d30_59_returned
FROM users GROUP BY 1 ORDER BY 1;
