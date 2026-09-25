-- The simulation-stage hard cutover intentionally destroys legacy rows and
-- schemas. Recreating empty Purchase/Fulfillment tables would provide a false
-- rollback while the application no longer contains those models.
DO $$
BEGIN
    RAISE EXCEPTION '000072 legacy commerce hard cutover is irreversible';
END
$$;
