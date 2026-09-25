-- Preserve premature GIWA receipt evidence before the Owner repairs it.
-- Payment, authorization, chain transactions and receipt rows are not rewritten
-- by this migration. A durable observation asks the reducer to reconsider only
-- the archived orders; the Owner still requires exact canonical finality.
INSERT INTO order_process_execution_history(source,identity,agency_order_id,evidence)
SELECT 'receipt',r.id::text,r.agency_order_id,to_jsonb(r)
FROM agency_order_receipts r
WHERE r.payment_rail='GIWA' AND (
    (r.terminal_state<>'REFUNDED_ALL' AND
        COALESCE(r.payload->'payment'->>'state','')<>'COMPLETED')
    OR NOT EXISTS (
        SELECT 1 FROM jsonb_array_elements(CASE
            WHEN jsonb_typeof(r.payload->'transactions')='array' THEN r.payload->'transactions'
            ELSE '[]'::jsonb END) tx
        WHERE lower(tx->>'txHash')=lower(r.terminal_tx_hash)
          AND tx->>'state'='FINALIZED'
          AND ((r.terminal_state='REFUNDED_ALL' AND tx->>'purpose' IN ('REFUND_PARTIAL','REFUND'))
            OR (r.terminal_state<>'REFUNDED_ALL' AND tx->>'purpose'='COMPLETE'))
    )
)
ON CONFLICT(agency_order_id,source,identity) DO NOTHING;

DO $$ DECLARE item record; BEGIN
    FOR item IN
        SELECT p.id,p.agency_order_id,p.state
        FROM settlement_payments p
        JOIN order_process_execution_history h ON h.agency_order_id=p.agency_order_id
        WHERE h.source='receipt'
        ORDER BY p.agency_order_id
    LOOP
        PERFORM pg_advisory_xact_lock(hashtextextended('order_process_events:'||item.agency_order_id::text,0));
        INSERT INTO order_process_events(agency_order_id,seq,source,type,payload,dedup_key,occurred_at,recorded_at)
        SELECT item.agency_order_id,
            COALESCE((SELECT max(seq) FROM order_process_events WHERE agency_order_id=item.agency_order_id),0)+1,
            'SETTLEMENT','settlement.state_changed.v1',
            jsonb_build_object('settlementPaymentId',item.id,'state',item.state),
            'migration100:receipt-finality:'||item.agency_order_id::text,
            clock_timestamp(),clock_timestamp()
        ON CONFLICT(dedup_key) DO NOTHING;
    END LOOP;
END $$;
