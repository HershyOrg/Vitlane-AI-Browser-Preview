-- Legacy rows are a deterministic projection of the pre-v1 AgencyOrder and
-- may be reconstructed by reapplying this migration. A v1 row contains new
-- customer consent evidence that the old schema cannot represent, so never
-- erase one during rollback.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM agency_order_procurement_authorizations
        WHERE authorization_kind='MANUAL_OPERATOR_PURCHASE'
    ) THEN
        RAISE EXCEPTION 'cannot rollback procurement authorization with retained v1 consent'
            USING ERRCODE = '23000';
    END IF;
END;
$$;

DROP TABLE IF EXISTS agency_order_procurement_authorizations;
DROP FUNCTION IF EXISTS reject_procurement_authorization_update();
DROP FUNCTION IF EXISTS reject_legacy_procurement_authorization_insert();
