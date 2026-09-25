-- Fail closed if new independent Purchase rows cannot satisfy the legacy
-- one-active-per-session constraints. The migration runner wraps this file in
-- one transaction, so no provenance is removed when this preflight fails.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM purchase_origin_requests) THEN
        RAISE EXCEPTION
            '000034 rollback requires explicit preservation or removal of Purchase origin provenance';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM purchases
        WHERE status IN (
            'DRAFT',
            'AWAITING_USER_APPROVAL',
            'USER_APPROVED',
            'SETTLEMENT_PENDING',
            'FUNDED',
            'FULFILLMENT_PENDING',
            'REFUND_PENDING'
        )
        GROUP BY user_id, shopping_session_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION
            '000034 rollback requires manual resolution of multiple active purchases in one shopping session';
    END IF;
END
$$;

DROP TRIGGER IF EXISTS purchase_origin_request_item_immutable
    ON purchase_origin_request_items;
DROP FUNCTION IF EXISTS reject_purchase_origin_request_item_mutation();
DROP TRIGGER IF EXISTS purchase_origin_request_immutable
    ON purchase_origin_requests;
DROP FUNCTION IF EXISTS enforce_purchase_origin_request_immutability();

DROP TABLE IF EXISTS purchase_origin_request_items;
DROP TABLE IF EXISTS purchase_origin_requests;

ALTER TABLE curation_actions
    DROP CONSTRAINT IF EXISTS curation_actions_id_actor_curation_unique;

ALTER TABLE purchases
    DROP CONSTRAINT IF EXISTS purchases_origin_lineage_unique,
    DROP CONSTRAINT IF EXISTS purchases_user_id_id_unique;

CREATE UNIQUE INDEX idx_purchases_active_candidate_quantity_configuration
    ON purchases(
        user_id,
        shopping_session_id,
        candidate_id,
        quantity,
        configuration_hash
    )
    WHERE status IN (
        'DRAFT',
        'AWAITING_USER_APPROVAL',
        'USER_APPROVED',
        'SETTLEMENT_PENDING',
        'FUNDED',
        'FULFILLMENT_PENDING',
        'REFUND_PENDING'
    );

CREATE UNIQUE INDEX idx_purchases_one_active_per_session
    ON purchases(user_id, shopping_session_id)
    WHERE status IN (
        'DRAFT',
        'AWAITING_USER_APPROVAL',
        'USER_APPROVED',
        'SETTLEMENT_PENDING',
        'FUNDED',
        'FULFILLMENT_PENDING',
        'REFUND_PENDING'
    );
