-- The legacy ShoppingCart rows, membership, and command history were
-- intentionally destroyed. Recreating empty tables would falsely imply a
-- reversible data migration, so rollback is restore-only.
DO $$
BEGIN
    RAISE EXCEPTION
        '000037_intent_curation_legacy_cart_cleanup is irreversible; restore the approved pre-cutover database backup together with the previous application image before traffic resumes'
        USING ERRCODE='55000';
END
$$;
