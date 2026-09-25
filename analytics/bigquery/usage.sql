-- GoogleSQL. @start_date inclusive, @end_date exclusive (Asia/Seoul).
-- Daily export only; prefer event_key, with legacy server event_id fallback.
-- gtag.js consumes event_id without exporting it. GA4 does not promise universal dedup.
WITH events AS (
 SELECT event_timestamp,event_name,user_id,user_pseudo_id,
 COALESCE(
   (SELECT value.string_value FROM UNNEST(event_params) WHERE key='event_key'),
   (SELECT value.string_value FROM UNNEST(event_params) WHERE key='event_id')
 ) AS event_id,
 (SELECT value.int_value FROM UNNEST(event_params) WHERE key='schema_version') AS schema_version,
 (SELECT value.string_value FROM UNNEST(event_params) WHERE key='action') AS action
 FROM `YOUR_PROJECT.analytics_PROPERTY.events_*`
 WHERE REGEXP_CONTAINS(_TABLE_SUFFIX,r'^\d{8}$')
 AND _TABLE_SUFFIX BETWEEN FORMAT_DATE('%Y%m%d',@start_date) AND FORMAT_DATE('%Y%m%d',DATE_SUB(@end_date,INTERVAL 1 DAY))
), dedup AS (
 SELECT * FROM events WHERE schema_version=1 AND event_id IS NOT NULL
 QUALIFY ROW_NUMBER() OVER(PARTITION BY event_name,event_id ORDER BY event_timestamp)=1
)
SELECT
 COUNT(DISTINCT IF(event_name='page_view', user_pseudo_id, NULL)) AS observed_browsers,
 COUNT(DISTINCT IF(
 event_name IN ('curation_created','research_results_viewed','candidate_viewed','external_merchant_opened','order_sheet_viewed','agency_order_issued')
 OR (event_name='candidate_reacted' AND action IN ('LIKE','DISLIKE','NONE'))
 OR (event_name='external_purchase_reported' AND action='REPORTED'),
 NULLIF(user_id,''),NULL)) AS observed_active_accounts,
 COUNTIF(event_name='curation_created') AS observed_curations_created,
 COUNTIF(event_name='agency_order_issued') AS observed_agency_orders_issued
FROM dedup;
