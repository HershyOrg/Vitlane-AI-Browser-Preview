-- The TEST-only legacy REFUND command rows are intentionally deleted and the
-- application no longer contains that runtime path. Reintroducing an empty
-- schema branch would provide a false rollback.
DO $$
BEGIN
    RAISE EXCEPTION '000075 GIWA legacy refund command cutover is irreversible';
END
$$;
