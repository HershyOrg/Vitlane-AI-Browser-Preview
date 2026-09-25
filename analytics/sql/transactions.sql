-- PostgreSQL. $1 inclusive start, $2 exclusive end, $3 excluded test/operator UUIDs.
-- All values are minor units, grouped by immutable profile and currency.
-- Authorization, self-reported purchases and issued payable amounts are NOT revenue.
WITH facts AS (
 SELECT o.payment_rail, o.provider_environment, o.economic_effect, o.merchant_execution_mode,
 o.currency::text AS currency, 'agency_orders_issued'::text AS metric,
 count(*)::bigint AS fact_count, sum(o.customer_payable_minor) AS amount_minor
 FROM agency_orders o
 WHERE o.issued_at >= $1::timestamptz AND o.issued_at < $2::timestamptz
 AND NOT (o.user_id = ANY($3::uuid[]))
 GROUP BY 1,2,3,4,5
 UNION ALL
 SELECT o.payment_rail, o.provider_environment, o.economic_effect, o.merchant_execution_mode,
 r.currency, 'paypal_captures', count(*), sum(r.gross_minor)
 FROM payment_mo_cash_receipts r JOIN agency_orders o ON o.id = r.agency_order_id
 WHERE r.occurred_at >= $1 AND r.occurred_at < $2 AND NOT (o.user_id = ANY($3::uuid[]))
 GROUP BY 1,2,3,4,5
 UNION ALL
 SELECT o.payment_rail, o.provider_environment, o.economic_effect, o.merchant_execution_mode,
 c.currency, 'completed_refunds', count(*), sum(c.amount_minor)
 FROM payment_mo_compensations c JOIN agency_orders o ON o.id = c.agency_order_id
 WHERE c.state = 'SUCCEEDED' AND c.action IN ('REFUND','TVIT_REFUND')
 AND c.completed_at >= $1 AND c.completed_at < $2 AND NOT (o.user_id = ANY($3::uuid[]))
 GROUP BY 1,2,3,4,5
 UNION ALL
 SELECT o.payment_rail, o.provider_environment, o.economic_effect, o.merchant_execution_mode,
 o.currency::text, 'issued_cohort_currently_placed_merchant_orders', count(*), NULL::numeric
 FROM merchant_orders m JOIN agency_orders o ON o.id = m.agency_order_id
 WHERE m.state = 'PLACED' AND o.issued_at >= $1 AND o.issued_at < $2
 AND NOT (o.user_id = ANY($3::uuid[]))
 GROUP BY 1,2,3,4,5
)
SELECT * FROM facts ORDER BY payment_rail, provider_environment, economic_effect, merchant_execution_mode, currency, metric;
