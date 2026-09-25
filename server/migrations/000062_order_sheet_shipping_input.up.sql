ALTER TABLE shipping_snapshots
    ALTER COLUMN source_profile_id DROP NOT NULL,
    ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'ACCOUNT_PROFILE';

ALTER TABLE shipping_snapshots
    ADD CONSTRAINT shipping_snapshots_source_kind_check CHECK (
        (source_kind = 'ACCOUNT_PROFILE' AND source_profile_id IS NOT NULL)
        OR (source_kind = 'ORDER_SHEET_INPUT' AND source_profile_id IS NULL)
    );

CREATE INDEX idx_shipping_snapshots_order_sheet_input
    ON shipping_snapshots(user_id, created_at DESC)
    WHERE source_kind = 'ORDER_SHEET_INPUT';
