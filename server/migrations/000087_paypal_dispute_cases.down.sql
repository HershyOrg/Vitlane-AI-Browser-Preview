-- A dispute case/action is financial operations evidence. Never make rollback
-- synonymous with deleting it; a populated release requires a forward fix.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payment_paypal_dispute_cases)
       OR EXISTS (SELECT 1 FROM payment_paypal_dispute_manual_actions) THEN
        RAISE EXCEPTION 'cannot rollback PayPal disputes with retained evidence'
            USING ERRCODE = '23000';
    END IF;
END;
$$;

DROP TABLE IF EXISTS payment_paypal_dispute_manual_actions;
DROP FUNCTION IF EXISTS reject_payment_paypal_dispute_action_change();
DROP TABLE IF EXISTS payment_paypal_dispute_cases;

ALTER TABLE payment_funds_receipts
    DROP CONSTRAINT IF EXISTS payment_funds_receipts_dispute_identity_unique;
