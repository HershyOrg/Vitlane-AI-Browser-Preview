-- PayPal live-switch readiness: signed dispute webhooks are projected into an
-- environment-bound manual operations queue. No Disputes API write is made.

ALTER TABLE payment_funds_receipts
    ADD CONSTRAINT payment_funds_receipts_dispute_identity_unique UNIQUE (
        id, customer_payment_id, agency_order_id, provider_environment, capture_id
    );

CREATE TABLE payment_paypal_dispute_cases (
    id UUID PRIMARY KEY,
    environment TEXT NOT NULL CHECK (environment IN ('SANDBOX','LIVE')),
    dispute_id TEXT NOT NULL CHECK (
        char_length(dispute_id) BETWEEN 1 AND 255
        AND dispute_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]*$'
    ),
    agency_order_id UUID NOT NULL,
    customer_payment_id UUID NOT NULL,
    funds_receipt_id UUID NOT NULL,
    capture_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('OPEN','RESOLVED')),
    provider_status TEXT NOT NULL CHECK (provider_status IN (
        'UNKNOWN','OPEN','WAITING_FOR_SELLER_RESPONSE',
        'WAITING_FOR_BUYER_RESPONSE','UNDER_REVIEW','RESOLVED'
    )),
    outcome TEXT NOT NULL CHECK (outcome IN (
        'NONE','RESOLVED_BUYER_FAVOUR','RESOLVED_SELLER_FAVOUR',
        'RESOLVED_WITH_PAYOUT','CANCELED_BY_BUYER','ACCEPTED','DENIED'
    )),
    reason TEXT NOT NULL CHECK (
        char_length(reason) BETWEEN 1 AND 64 AND reason ~ '^[A-Z0-9_]+$'
    ),
    lifecycle_stage TEXT NOT NULL CHECK (lifecycle_stage IN (
        'UNKNOWN','INQUIRY','CHARGEBACK','PRE_ARBITRATION','ARBITRATION'
    )),
    seller_response_due_at TIMESTAMPTZ,
    latest_event_id TEXT NOT NULL,
    latest_event_type TEXT NOT NULL CHECK (latest_event_type IN (
        'CUSTOMER.DISPUTE.CREATED','CUSTOMER.DISPUTE.UPDATED',
        'CUSTOMER.DISPUTE.RESOLVED'
    )),
    opened_at TIMESTAMPTZ NOT NULL,
    last_observed_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (environment, dispute_id),
    FOREIGN KEY (
        funds_receipt_id, customer_payment_id, agency_order_id, environment, capture_id
    ) REFERENCES payment_funds_receipts (
        id, customer_payment_id, agency_order_id, provider_environment, capture_id
    ) ON DELETE RESTRICT,
    CHECK (
        (state='OPEN' AND outcome='NONE' AND resolved_at IS NULL)
        OR (state='RESOLVED' AND provider_status='RESOLVED' AND resolved_at IS NOT NULL)
    )
);

CREATE INDEX idx_payment_paypal_dispute_cases_operator_queue
    ON payment_paypal_dispute_cases(
        state, environment, seller_response_due_at, last_observed_at DESC
    );

CREATE TABLE payment_paypal_dispute_manual_actions (
    id UUID PRIMARY KEY,
    dispute_case_id UUID NOT NULL
        REFERENCES payment_paypal_dispute_cases(id) ON DELETE RESTRICT,
    action_kind TEXT NOT NULL CHECK (action_kind IN (
        'CASE_OBSERVED','MESSAGE_SENT','EVIDENCE_SUBMITTED','OFFER_MADE',
        'CLAIM_ACCEPTED','APPEAL_SUBMITTED','OTHER'
    )),
    external_reference TEXT NOT NULL CHECK (
        char_length(external_reference) BETWEEN 1 AND 255
        AND external_reference ~ '^[A-Za-z0-9][A-Za-z0-9._:-]*$'
    ),
    public_rationale TEXT NOT NULL CHECK (
        char_length(BTRIM(public_rationale)) BETWEEN 1 AND 2000
    ),
    internal_note TEXT NOT NULL DEFAULT '' CHECK (char_length(internal_note) <= 4000),
    actor_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    observed_provider_status TEXT CHECK (observed_provider_status IN (
        'OPEN','WAITING_FOR_SELLER_RESPONSE','WAITING_FOR_BUYER_RESPONSE',
        'UNDER_REVIEW','RESOLVED'
    )),
    observed_outcome TEXT CHECK (observed_outcome IN (
        'NONE','RESOLVED_BUYER_FAVOUR','RESOLVED_SELLER_FAVOUR',
        'RESOLVED_WITH_PAYOUT','CANCELED_BY_BUYER','ACCEPTED','DENIED'
    )),
    evidence_source TEXT NOT NULL CHECK (evidence_source IN (
        'PAYPAL_RESOLUTION_CENTER','PAYPAL_EMAIL','INTERNAL_ORDER_RECORD',
        'CARRIER','MERCHANT_RECEIPT','OTHER'
    )),
    evidence_hash TEXT NOT NULL CHECK (evidence_hash ~ '^0x[0-9a-f]{64}$'),
    observed_at TIMESTAMPTZ NOT NULL,
    idempotency_key_hash TEXT NOT NULL CHECK (idempotency_key_hash ~ '^0x[0-9a-f]{64}$'),
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^0x[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (dispute_case_id, idempotency_key_hash),
    CHECK (
        observed_outcome IS NULL
        OR observed_provider_status='RESOLVED'
    )
);

CREATE INDEX idx_payment_paypal_dispute_manual_actions_case
    ON payment_paypal_dispute_manual_actions(dispute_case_id, observed_at, created_at);

CREATE FUNCTION reject_payment_paypal_dispute_action_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- Append-only remains absolute for runtime writes. The development-only
    -- account reset sets this transaction-local guard while deleting the
    -- complete synthetic user graph.
    IF TG_OP = 'DELETE'
       AND current_setting('vitlane.account_reset', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'PayPal dispute manual actions are append-only'
        USING ERRCODE = '23000';
END;
$$;

CREATE TRIGGER trg_payment_paypal_dispute_actions_append_only
BEFORE UPDATE OR DELETE ON payment_paypal_dispute_manual_actions
FOR EACH ROW EXECUTE FUNCTION reject_payment_paypal_dispute_action_change();
