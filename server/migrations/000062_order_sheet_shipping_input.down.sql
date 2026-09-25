DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM shipping_snapshots
        WHERE source_kind = 'ORDER_SHEET_INPUT' OR source_profile_id IS NULL
    ) THEN
        RAISE EXCEPTION 'cannot remove OrderSheet shipping input while direct snapshots exist';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_shipping_snapshots_order_sheet_input;

ALTER TABLE shipping_snapshots
    DROP CONSTRAINT shipping_snapshots_source_kind_check,
    DROP COLUMN source_kind,
    ALTER COLUMN source_profile_id SET NOT NULL;
