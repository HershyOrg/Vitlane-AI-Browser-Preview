-- Live-switch readiness: manual Procurement judgment and customer requests.
--
-- A MANUAL_OPERATOR_PURCHASE v1 ProcurementAuthorization is an immutable
-- customer-approved scope (owned by AgencyOrder). The migration-only
-- LEGACY_NO_REAL_VALUE_V0 kind is explicitly non-v1 evidence and is accepted
-- only for no-real-value simulated orders. These tables deliberately do not
-- model a Shopify checkout
-- session or perform a post-payment preflight.  Operators record their manual
-- judgment, and material new conditions are answered in the existing single
-- customer conversation before a technical merchant-effect lock can begin.

CREATE TABLE procurement_decision_records (
    id UUID PRIMARY KEY,
    merchant_order_id UUID NOT NULL REFERENCES merchant_orders(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    task_id UUID NOT NULL REFERENCES merchant_order_execution_tasks(id) ON DELETE RESTRICT,
    decision TEXT NOT NULL CHECK (decision IN (
        'WITHIN_AUTHORIZATION','IMMATERIAL_VARIANCE',
        'MATERIAL_NEW_CONDITION','UNABLE_TO_PURCHASE'
    )),
    public_rationale TEXT NOT NULL CHECK (char_length(public_rationale) BETWEEN 8 AND 2000),
    internal_note TEXT CHECK (internal_note IS NULL OR char_length(internal_note) <= 4000),
    observed_condition TEXT NOT NULL CHECK (char_length(observed_condition) BETWEEN 1 AND 2000),
    evidence_source TEXT NOT NULL CHECK (evidence_source IN (
        'OPERATOR_OBSERVATION','MERCHANT_PAGE','MERCHANT_POLICY','RECEIPT','OTHER'
    )),
    evidence_hash TEXT NOT NULL CHECK (char_length(evidence_hash) BETWEEN 16 AND 256),
    observed_at TIMESTAMPTZ NOT NULL,
    decided_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    task_version BIGINT NOT NULL CHECK (task_version > 0),
    authorization_hash TEXT NOT NULL,
    execution_profile_hash TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_procurement_decisions_order_created
    ON procurement_decision_records(merchant_order_id, created_at DESC, id DESC);

CREATE TABLE procurement_customer_requests (
    id UUID PRIMARY KEY,
    merchant_order_id UUID NOT NULL REFERENCES merchant_orders(id) ON DELETE RESTRICT,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('INFORMATION','CONSENT')),
    prompt TEXT NOT NULL CHECK (char_length(prompt) BETWEEN 1 AND 2000),
    response_type TEXT NOT NULL CHECK (response_type IN (
        'TEXT','SINGLE_CHOICE','BOOLEAN_CONSENT'
    )),
    response_options JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(response_options) = 'array'),
    public_context TEXT NOT NULL CHECK (char_length(public_context) BETWEEN 1 AND 2000),
    state TEXT NOT NULL CHECK (state IN (
        'PENDING','ANSWERED','DECLINED','FAILED_NO_RESPONSE','CANCELLED'
    )),
    response JSONB,
    requested_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    requested_at TIMESTAMPTZ NOT NULL,
    due_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    resolved_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    resolution_reason TEXT CHECK (resolution_reason IS NULL OR char_length(resolution_reason) <= 2000),
    source_decision_id UUID NOT NULL REFERENCES procurement_decision_records(id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL UNIQUE,
    resolution_idempotency_key TEXT UNIQUE,
    resolution_request_hash TEXT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (due_at > requested_at),
    CHECK ((state = 'PENDING' AND resolved_at IS NULL)
        OR (state <> 'PENDING' AND resolved_at IS NOT NULL)),
    CHECK ((state = 'ANSWERED' AND response IS NOT NULL)
        OR (state <> 'ANSWERED'))
);

CREATE UNIQUE INDEX idx_procurement_customer_requests_open
    ON procurement_customer_requests(merchant_order_id)
    WHERE state = 'PENDING';
CREATE INDEX idx_procurement_customer_requests_user
    ON procurement_customer_requests(user_id, requested_at DESC, id DESC);
CREATE INDEX idx_procurement_customer_requests_due
    ON procurement_customer_requests(due_at, id)
    WHERE state = 'PENDING';

-- BeginMerchantEffect is a narrow technical lock.  It records which immutable
-- authorization/profile and which operator decision opened the effect; it does
-- not evaluate current merchant terms or call Shopify.
CREATE TABLE procurement_effect_locks (
    merchant_order_id UUID PRIMARY KEY REFERENCES merchant_orders(id) ON DELETE RESTRICT,
    task_id UUID NOT NULL REFERENCES merchant_order_execution_tasks(id) ON DELETE RESTRICT,
    decision_record_id UUID NOT NULL REFERENCES procurement_decision_records(id) ON DELETE RESTRICT,
    authorization_hash TEXT NOT NULL,
    execution_profile_hash TEXT NOT NULL,
    operator_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN ('STARTED','PLACED','FAILED','OUTCOME_UNKNOWN')),
    idempotency_key TEXT NOT NULL UNIQUE,
    started_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    CHECK ((state = 'STARTED' AND resolved_at IS NULL)
        OR (state <> 'STARTED' AND resolved_at IS NOT NULL))
);

ALTER TABLE agency_order_execution_audits
    DROP CONSTRAINT agency_order_execution_audits_action_check;
ALTER TABLE agency_order_execution_audits
    ADD CONSTRAINT agency_order_execution_audits_action_check CHECK (action IN (
        'PAYMENT_FINALIZED','OPERATOR_ASSIGNED','MERCHANT_ORDER_ACCEPTED',
        'FULFILLMENT_FAILED','SIBLING_CANCELLED','PROCUREMENT_PLANNED',
        'CUSTOMER_CANCELLED','MERCHANT_ORDER_PLACED',
        'RECOVERY_CREATED','RECOVERY_RECORDED','RECOVERY_WAIVED','RECOVERY_DELETED',
        'PROCUREMENT_DECISION_RECORDED','PROCUREMENT_REQUEST_CREATED',
        'PROCUREMENT_REQUEST_RESOLVED','MERCHANT_EFFECT_STARTED'
    ));
