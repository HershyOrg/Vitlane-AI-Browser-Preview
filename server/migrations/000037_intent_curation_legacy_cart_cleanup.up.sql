-- CurationSelection is now the sole stored selection model and CartView is a
-- status-free projection. The legacy per-plan ShoppingCart aggregate cannot be
-- kept in parallel because its trigger would recreate state for every new
-- immutable ShoppingPlan.
--
-- Keep the migration itself fail-closed as a last line of defense. The deploy
-- runbook performs the same check under writer freeze, but a manual migration
-- must not bypass unresolved payment, chain, refund or outbox responsibility.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM settlement_payments
        WHERE state IN (
            'AUTHORIZED', 'AWAITING_ALLOWANCE', 'PAYMENT_SUBMITTED',
            'SUBMISSION_UNKNOWN', 'SAFE', 'FINALIZED',
            'COMPLETION_SUBMITTED', 'REFUND_PENDING'
        )
    )
    OR EXISTS (
        SELECT 1
        FROM chain_transactions
        WHERE state NOT IN ('FINALIZED', 'FAILED')
    )
    OR EXISTS (
        SELECT 1
        FROM settlement_command_outbox
        WHERE state <> 'FINALIZED'
    )
    OR EXISTS (
        SELECT 1
        FROM purchases
        WHERE status = 'REFUND_PENDING'
    ) THEN
        RAISE EXCEPTION 'INTENT_CURATION_FINANCIAL_RESPONSIBILITY_ACTIVE'
            USING
                ERRCODE = '55000',
                HINT = 'freeze writers, resolve escrow/chain/refund/outbox responsibility, then rerun the approved cutover';
    END IF;
END
$$;

DROP TRIGGER IF EXISTS shopping_plans_create_cart ON shopping_plans;
DROP FUNCTION IF EXISTS vitlane_create_plan_cart();

-- Drop children before their parent. This is an intentional hard cutover:
-- legacy cart commands and membership are not migrated into Selection history.
DROP TABLE IF EXISTS shopping_cart_commands;
DROP TABLE IF EXISTS shopping_cart_items;
DROP TABLE IF EXISTS shopping_carts;
