-- PayPal AUTHORIZE + merchant-order funding clean cut.
--
-- This migration is intentionally forward-only.  The capture-first and
-- monetary unit-slice models cannot be truthfully backfilled into a single
-- full-order PayPal authorization plus immutable merchant-order allocations.
-- Operators must archive and explicitly reset the Sandbox/TEST commerce
-- graph before applying it.  The migration never deletes retained commerce
-- facts and refuses to cross any live/real-money or unresolved effect.

DO $cutover_guard$
BEGIN
    IF EXISTS (
        SELECT 1 FROM agency_orders
        WHERE economic_effect='REAL_MONEY' OR provider_environment='LIVE'
    ) OR EXISTS (
        SELECT 1 FROM payment_customer_payments
        WHERE economic_effect='REAL_MONEY' OR provider_environment='LIVE'
    ) OR EXISTS (
        SELECT 1 FROM payment_funds_receipts
        WHERE provider_environment='LIVE'
    ) OR EXISTS (
        SELECT 1 FROM payment_paypal_dispute_cases
        WHERE environment='LIVE'
    ) OR EXISTS (
        SELECT 1 FROM payment_paypal_webhook_inbox
        WHERE environment='LIVE'
    ) THEN
        RAISE EXCEPTION
            'cannot apply MO funding clean cut while REAL_MONEY/LIVE facts exist'
            USING ERRCODE='55000';
    END IF;

    IF EXISTS (
        SELECT 1 FROM payment_external_operations
        WHERE state IN ('SENT','UNKNOWN')
    ) THEN
        RAISE EXCEPTION
            'cannot apply MO funding clean cut with SENT/UNKNOWN external operations'
            USING ERRCODE='55000';
    END IF;

    IF EXISTS (
        SELECT 1 FROM payment_paypal_dispute_cases WHERE state='OPEN'
    ) THEN
        RAISE EXCEPTION
            'cannot apply MO funding clean cut with an open PayPal dispute'
            USING ERRCODE='55000';
    END IF;

    IF EXISTS (
        SELECT 1 FROM payment_customer_payments
        WHERE state IN (
            'CREATED','ACTION_REQUIRED','PROCESSING','OUTCOME_UNKNOWN',
            'CAPTURED_NONCONFORMING'
        )
    ) OR EXISTS (
        SELECT 1 FROM payment_customer_refunds
        WHERE state IN ('APPROVED','EXECUTION_PENDING')
    ) OR EXISTS (
        SELECT 1 FROM payment_customer_refund_attempts
        WHERE state IN (
            'PREPARED','SUBMISSION_PENDING','PROCESSING','OUTCOME_UNKNOWN'
        )
    ) OR EXISTS (
        SELECT 1 FROM settlement_payments
        WHERE state NOT IN ('COMPLETED','REFUNDED','FAILED')
    ) OR EXISTS (
        SELECT 1 FROM settlement_command_outbox
        WHERE state IN ('PLANNED','NONCE_RESERVED','SIGNED','BROADCAST')
    ) OR EXISTS (
        SELECT 1 FROM merchant_orders
        WHERE state NOT IN ('PLACED','FAILED','CANCELLED')
    ) OR EXISTS (
        SELECT 1 FROM merchant_payments
        WHERE state NOT IN ('SUCCEEDED','FAILED')
    ) OR EXISTS (
        SELECT 1 FROM procurement_effect_locks
        WHERE state IN ('STARTED','OUTCOME_UNKNOWN')
    ) THEN
        RAISE EXCEPTION
            'cannot apply MO funding clean cut with nonterminal money or merchant effects'
            USING ERRCODE='55000';
    END IF;

    -- Even terminal Sandbox/TEST rows are capture-first facts.  A migration
    -- must not fabricate per-MO fee allocations or PayPal authorizations for
    -- them, and must not silently erase them.  Archive/reset is an explicit
    -- operational precondition.
    IF EXISTS (SELECT 1 FROM agency_orders)
       OR EXISTS (SELECT 1 FROM payment_external_operations) THEN
        RAISE EXCEPTION
            'MO funding clean cut requires an archived and empty legacy commerce graph'
            USING ERRCODE='55000';
    END IF;
END
$cutover_guard$;

-- The empty-graph guard above makes it safe to remove the pre-v1
-- authorization compatibility shape without fabricating customer approval.
-- Migration history stays intact, while every authorization accepted by the
-- current schema is the v1 customer-approved manual purchase scope.
DROP TRIGGER IF EXISTS trg_procurement_authorization_legacy_insert
    ON agency_order_procurement_authorizations;
DROP FUNCTION IF EXISTS reject_legacy_procurement_authorization_insert();

DO $authorization_clean_cut$
DECLARE
    constraint_name TEXT;
BEGIN
    FOR constraint_name IN
        SELECT constraints.conname
        FROM pg_constraint constraints
        WHERE constraints.conrelid =
                  'agency_order_procurement_authorizations'::regclass
          AND constraints.contype = 'c'
          AND pg_get_constraintdef(constraints.oid) LIKE '%authorization_kind%'
    LOOP
        EXECUTE format(
            'ALTER TABLE agency_order_procurement_authorizations DROP CONSTRAINT %I',
            constraint_name
        );
    END LOOP;
END
$authorization_clean_cut$;

ALTER TABLE agency_order_procurement_authorizations
    ALTER COLUMN locale SET NOT NULL,
    ADD CONSTRAINT agency_order_procurement_authorizations_current_kind_check
        CHECK (authorization_kind = 'MANUAL_OPERATOR_PURCHASE'),
    ADD CONSTRAINT agency_order_procurement_authorizations_current_payload_kind_check
        CHECK (payload->>'kind' IS NOT DISTINCT FROM authorization_kind),
    ADD CONSTRAINT agency_order_procurement_authorizations_current_payload_check
        CHECK ((
            locale IN ('en-US','ko-KR')
            AND copy_version = 'procurement-authorization.v1'
            AND jsonb_typeof(payload->'customerApproval')
                IS NOT DISTINCT FROM 'object'
            AND payload#>>'{customerApproval,locale}' IS NOT DISTINCT FROM locale
            AND payload#>>'{customerApproval,copyVersion}'
                IS NOT DISTINCT FROM copy_version
            AND (payload#>>'{customerApproval,agencyConsent}')::boolean IS TRUE
            AND (payload#>>'{customerApproval,privacyConsent}')::boolean IS TRUE
            AND char_length(COALESCE(
                payload#>>'{customerApproval,orderMessage}', ''
            )) <= 500
            AND char_length(COALESCE(
                payload#>>'{customerApproval,deliveryMessage}', ''
            )) <= 500
        ) IS TRUE);

-- Immutable issuance-time economic allocation: one row per merchant checkout
-- and, after planning, exactly one MerchantOrder.
CREATE TABLE agency_order_mo_allocations (
    id UUID PRIMARY KEY,
    agency_order_id UUID NOT NULL REFERENCES agency_orders(id) ON DELETE RESTRICT,
    checkout_ordinal INTEGER NOT NULL CHECK (checkout_ordinal > 0),
    merchant_id TEXT NOT NULL CHECK (char_length(BTRIM(merchant_id)) BETWEEN 1 AND 255),
    shop_domain TEXT NOT NULL CHECK (char_length(BTRIM(shop_domain)) BETWEEN 1 AND 255),
    pass_through_minor BIGINT NOT NULL CHECK (pass_through_minor > 0),
    fee_variable_minor BIGINT NOT NULL CHECK (fee_variable_minor >= 0),
    fee_fixed_minor BIGINT NOT NULL CHECK (fee_fixed_minor >= 0),
    fee_total_minor BIGINT NOT NULL CHECK (
        fee_total_minor = fee_variable_minor + fee_fixed_minor
    ),
    customer_gross_minor BIGINT NOT NULL CHECK (
        customer_gross_minor = pass_through_minor + fee_total_minor
    ),
    currency TEXT NOT NULL CHECK (currency='USD'),
    fee_policy_version TEXT NOT NULL CHECK (
        char_length(BTRIM(fee_policy_version)) BETWEEN 1 AND 128
    ),
    allocation_hash TEXT NOT NULL UNIQUE CHECK (allocation_hash ~ '^0x[0-9a-f]{64}$'),
    execution_profile_hash TEXT NOT NULL CHECK (
        execution_profile_hash ~ '^0x[0-9a-f]{64}$'
    ),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (agency_order_id, checkout_ordinal),
    UNIQUE (id, agency_order_id),
    UNIQUE (
        id, agency_order_id, customer_gross_minor, currency,
        execution_profile_hash
    ),
    FOREIGN KEY (agency_order_id, execution_profile_hash)
        REFERENCES agency_orders(id, execution_profile_hash) ON DELETE RESTRICT
);

CREATE FUNCTION reject_agency_order_mo_allocation_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
	IF TG_OP='DELETE'
	   AND current_setting('vitlane.account_reset', true)='on' THEN
		RETURN OLD;
	END IF;
    RAISE EXCEPTION 'agency order MO allocation is immutable'
        USING ERRCODE='23000';
END;
$$;

CREATE TRIGGER trg_agency_order_mo_allocation_immutable
BEFORE UPDATE OR DELETE ON agency_order_mo_allocations
FOR EACH ROW EXECUTE FUNCTION reject_agency_order_mo_allocation_change();

-- A Sandbox operator enters the same complete evidence shape that Live will
-- require, but the stored kind makes it TEST evidence and never a claim that a
-- real merchant effect occurred.  The clean-cut guard guarantees there are no
-- historical MerchantOrders that need fabricated evidence.
ALTER TABLE merchant_orders
    DROP CONSTRAINT merchant_orders_check2,
    ADD COLUMN placement_evidence_kind TEXT,
    ADD COLUMN placement_receipt_safe_ref TEXT,
    ADD COLUMN placement_actual_amount_minor BIGINT,
    ADD COLUMN placement_evidence_source TEXT,
    ADD COLUMN placement_evidence_hash TEXT,
    ADD COLUMN placement_observed_at TIMESTAMPTZ,
    ADD COLUMN placement_recorded_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    ADD COLUMN placement_recorded_at TIMESTAMPTZ,
    ADD CONSTRAINT merchant_orders_placement_evidence_kind_check CHECK (
        placement_evidence_kind IS NULL OR placement_evidence_kind IN (
            'SANDBOX_TEST_EVIDENCE','LIVE_MERCHANT_EFFECT_EVIDENCE'
        )
    ),
    ADD CONSTRAINT merchant_orders_placement_evidence_source_check CHECK (
        placement_evidence_source IS NULL OR placement_evidence_source IN (
            'OPERATOR_OBSERVATION','MERCHANT_PAGE','MERCHANT_POLICY','RECEIPT','OTHER'
        )
    ),
    ADD CONSTRAINT merchant_orders_placement_evidence_shape_check CHECK (
        (
            placement_evidence_kind IS NULL
            AND external_order_ref IS NULL
            AND placement_receipt_safe_ref IS NULL
            AND placement_actual_amount_minor IS NULL
            AND placement_evidence_source IS NULL
            AND placement_evidence_hash IS NULL
            AND placement_observed_at IS NULL
            AND placement_recorded_by_user_id IS NULL
            AND placement_recorded_at IS NULL
        ) OR (
            state='PLACED'
            AND external_order_ref IS NOT NULL
            AND char_length(BTRIM(external_order_ref)) BETWEEN 1 AND 255
            AND placement_receipt_safe_ref IS NOT NULL
            AND char_length(BTRIM(placement_receipt_safe_ref)) BETWEEN 1 AND 512
            AND placement_actual_amount_minor IS NOT NULL
            AND placement_actual_amount_minor > 0
            AND placement_evidence_source IS NOT NULL
            AND placement_evidence_hash IS NOT NULL
            AND char_length(BTRIM(placement_evidence_hash)) BETWEEN 16 AND 256
            AND placement_observed_at IS NOT NULL
            AND placement_recorded_by_user_id IS NOT NULL
            AND placement_recorded_at IS NOT NULL
            AND (
                (execution_mode='SIMULATED_NO_EFFECT'
                 AND placement_evidence_kind='SANDBOX_TEST_EVIDENCE')
                OR
                (execution_mode='LIVE_MERCHANT_EFFECT'
                 AND placement_evidence_kind='LIVE_MERCHANT_EFFECT_EVIDENCE')
            )
        )
    );

CREATE OR REPLACE FUNCTION reject_merchant_order_placement_evidence_change()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.placement_evidence_kind IS NOT NULL AND ROW(
        NEW.external_order_ref,
        NEW.placement_evidence_kind,
        NEW.placement_receipt_safe_ref,
        NEW.placement_actual_amount_minor,
        NEW.placement_evidence_source,
        NEW.placement_evidence_hash,
        NEW.placement_observed_at,
        NEW.placement_recorded_by_user_id,
        NEW.placement_recorded_at,
        NEW.result_hash
    ) IS DISTINCT FROM ROW(
        OLD.external_order_ref,
        OLD.placement_evidence_kind,
        OLD.placement_receipt_safe_ref,
        OLD.placement_actual_amount_minor,
        OLD.placement_evidence_source,
        OLD.placement_evidence_hash,
        OLD.placement_observed_at,
        OLD.placement_recorded_by_user_id,
        OLD.placement_recorded_at,
        OLD.result_hash
    ) THEN
        RAISE EXCEPTION 'MerchantOrder placement evidence is immutable after recording';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_merchant_order_placement_evidence_immutable
BEFORE UPDATE ON merchant_orders
FOR EACH ROW EXECUTE FUNCTION reject_merchant_order_placement_evidence_change();

-- CustomerPayment remains the rail-neutral aggregate.  Authorization and
-- partial-capture progress are explicit states rather than a whole-order
-- CAPTURE success shortcut.
ALTER TABLE payment_customer_payments
    DROP CONSTRAINT payment_customer_payments_state_check;
ALTER TABLE payment_customer_payments
    ADD CONSTRAINT payment_customer_payments_state_check CHECK (state IN (
        'CREATED','ACTION_REQUIRED','PROCESSING','OUTCOME_UNKNOWN',
		'AUTHORIZED','PARTIALLY_CAPTURED','CAPTURED','CLOSED',
		'FAILED','ABANDONED','EXPIRED','SUPERSEDED'
    )),
    ADD CONSTRAINT payment_customer_payments_authorization_identity_unique UNIQUE (
        id, agency_order_id, rail, provider_environment, amount_minor,
        currency, execution_profile_hash
    ),
    ADD CONSTRAINT payment_customer_payments_order_profile_unique UNIQUE (
        id, agency_order_id, execution_profile_hash
    ),
    ADD CONSTRAINT payment_customer_payments_funding_identity_unique UNIQUE (
        id, agency_order_id, rail, provider_environment, execution_profile_hash
    );

DROP INDEX idx_payment_customer_payments_open;
CREATE UNIQUE INDEX idx_payment_customer_payments_open
    ON payment_customer_payments(agency_order_id)
    WHERE state NOT IN ('FAILED','ABANDONED','EXPIRED','SUPERSEDED','CLOSED');

ALTER TABLE payment_paypal_attempts
    DROP CONSTRAINT payment_paypal_attempts_state_check;
ALTER TABLE payment_paypal_attempts
    ADD CONSTRAINT payment_paypal_attempts_state_check CHECK (state IN (
        'ORDER_PREPARED','CANCELLED_BEFORE_CREATE','ORDER_CREATE_SUBMITTED',
        'ORDER_CREATE_UNKNOWN','ORDER_CREATE_FAILED','PAYER_ACTION_REQUIRED',
        'PAYER_APPROVED','APPROVAL_REVERSED','CANCELLED_BY_USER','EXPIRED',
        'SUPERSEDED_BEFORE_AUTHORIZE','ABANDONED_BEFORE_AUTHORIZE',
        'AUTHORIZE_SUBMITTED','AUTHORIZE_PENDING','AUTHORIZE_OUTCOME_UNKNOWN',
        'AUTHORIZE_COMPLETED','AUTHORIZE_DECLINED','AUTHORIZE_FAILED'
    )),
    ADD CONSTRAINT payment_paypal_attempts_id_payment_unique
        UNIQUE (id, customer_payment_id);

DROP INDEX idx_payment_paypal_attempts_capture;
ALTER TABLE payment_paypal_attempts DROP COLUMN capture_id;

-- A PayPal order has one purchase unit and one authorization for the full
-- customer payment.  All MO funding positions below reference this same row;
-- captures are partial executions against that authorization.
CREATE TABLE payment_paypal_authorizations (
    id UUID PRIMARY KEY,
    customer_payment_id UUID NOT NULL UNIQUE,
    agency_order_id UUID NOT NULL UNIQUE,
    paypal_attempt_id UUID NOT NULL UNIQUE,
    rail TEXT NOT NULL CHECK (rail='PAYPAL'),
    provider_environment TEXT NOT NULL CHECK (
        provider_environment IN ('SANDBOX','LIVE')
    ),
    paypal_order_id TEXT NOT NULL,
	payee_merchant_id TEXT NOT NULL,
    paypal_authorization_id TEXT,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency='USD'),
    execution_profile_hash TEXT NOT NULL CHECK (
        execution_profile_hash ~ '^0x[0-9a-f]{64}$'
    ),
    state TEXT NOT NULL CHECK (state IN (
        'ORDER_CREATED','PAYER_ACTION_REQUIRED','PAYER_APPROVED',
        'AUTHORIZE_PENDING','AUTHORIZE_UNKNOWN','AUTHORIZED',
        'PARTIALLY_CAPTURED','CAPTURED','VOIDED','DENIED','EXPIRED','FAILED'
    )),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    authorized_at TIMESTAMPTZ,
    honor_refreshed_at TIMESTAMPTZ NOT NULL,
    reauthorization_count INTEGER NOT NULL DEFAULT 0 CHECK (reauthorization_count >= 0),
    terminal_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (provider_environment, paypal_order_id),
    UNIQUE (provider_environment, paypal_authorization_id),
    UNIQUE (
        id, customer_payment_id, agency_order_id, provider_environment,
        execution_profile_hash
    ),
    FOREIGN KEY (
        customer_payment_id, agency_order_id, rail, provider_environment,
        amount_minor, currency, execution_profile_hash
    ) REFERENCES payment_customer_payments(
        id, agency_order_id, rail, provider_environment, amount_minor,
        currency, execution_profile_hash
    ) ON DELETE RESTRICT,
    FOREIGN KEY (paypal_attempt_id, customer_payment_id)
        REFERENCES payment_paypal_attempts(id, customer_payment_id) ON DELETE RESTRICT,
    CHECK (
        state NOT IN ('AUTHORIZED','PARTIALLY_CAPTURED','CAPTURED','VOIDED')
        OR (paypal_authorization_id IS NOT NULL AND authorized_at IS NOT NULL)
    ),
    CHECK (authorized_at IS NULL OR honor_refreshed_at >= authorized_at),
    CHECK (
        (state IN ('CAPTURED','VOIDED','DENIED','EXPIRED','FAILED'))
        = (terminal_at IS NOT NULL)
    )
);

-- One current funding position per immutable allocation.  PayPal positions
-- share the full-order authorization; GIWA positions point to no PayPal fact
-- and represent allocation of an already finalized TEST prepayment.
CREATE TABLE payment_mo_funding_positions (
    id UUID PRIMARY KEY,
    allocation_id UUID NOT NULL UNIQUE,
    agency_order_id UUID NOT NULL,
    customer_payment_id UUID NOT NULL,
    paypal_authorization_id UUID REFERENCES payment_paypal_authorizations(id)
        ON DELETE RESTRICT,
    rail TEXT NOT NULL CHECK (rail IN ('PAYPAL','GIWA')),
    source TEXT NOT NULL CHECK (source IN ('PAYPAL_AUTHORIZATION','GIWA_PREPAID')),
    provider_environment TEXT NOT NULL CHECK (
        provider_environment IN ('SANDBOX','LIVE','TESTNET')
    ),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency='USD'),
    execution_profile_hash TEXT NOT NULL CHECK (
        execution_profile_hash ~ '^0x[0-9a-f]{64}$'
    ),
    state TEXT NOT NULL CHECK (state IN (
        'AVAILABLE','ACTIVATION_PENDING','ACTIVATION_UNKNOWN','ACTIVE',
        'RELEASE_PENDING','RELEASE_UNKNOWN','RELEASED','FAILED'
    )),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    available_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (
        id, allocation_id, agency_order_id, customer_payment_id, rail,
        provider_environment, amount_minor, currency, execution_profile_hash
    ),
    UNIQUE (
        id, allocation_id, agency_order_id, customer_payment_id,
        paypal_authorization_id, provider_environment, amount_minor, currency,
        execution_profile_hash
    ),
    FOREIGN KEY (
        allocation_id, agency_order_id, amount_minor, currency,
        execution_profile_hash
    ) REFERENCES agency_order_mo_allocations(
        id, agency_order_id, customer_gross_minor, currency,
        execution_profile_hash
    ) ON DELETE RESTRICT,
    FOREIGN KEY (
        customer_payment_id, agency_order_id, rail, provider_environment,
        execution_profile_hash
    ) REFERENCES payment_customer_payments(
        id, agency_order_id, rail, provider_environment, execution_profile_hash
    ) ON DELETE RESTRICT,
    FOREIGN KEY (
        paypal_authorization_id, customer_payment_id, agency_order_id,
        provider_environment, execution_profile_hash
    ) REFERENCES payment_paypal_authorizations(
        id, customer_payment_id, agency_order_id, provider_environment,
        execution_profile_hash
    ) ON DELETE RESTRICT,
    CHECK (
        (rail='PAYPAL' AND source='PAYPAL_AUTHORIZATION'
         AND provider_environment IN ('SANDBOX','LIVE')
         AND paypal_authorization_id IS NOT NULL)
        OR
        (rail='GIWA' AND source='GIWA_PREPAID'
         AND provider_environment='TESTNET'
         AND paypal_authorization_id IS NULL)
    ),
    CHECK ((state='RELEASED') = (released_at IS NOT NULL))
);

-- Presence of a row is the accepted MO capture fact.  GIWA does not mint a
-- synthetic per-MO capture receipt; its existing finalized-pay receipt remains
-- the truthful whole-order cash receipt.
CREATE TABLE payment_mo_cash_receipts (
    id UUID PRIMARY KEY,
    funding_position_id UUID NOT NULL UNIQUE,
    allocation_id UUID NOT NULL UNIQUE,
    agency_order_id UUID NOT NULL,
    customer_payment_id UUID NOT NULL,
    paypal_authorization_id UUID NOT NULL,
    provider_environment TEXT NOT NULL CHECK (
        provider_environment IN ('SANDBOX','LIVE')
    ),
    kind TEXT NOT NULL CHECK (kind='PAYPAL_CAPTURE'),
    provider_capture_id TEXT NOT NULL,
    gross_minor BIGINT NOT NULL CHECK (gross_minor > 0),
    economics_reconciled BOOLEAN NOT NULL,
    processor_fee_minor BIGINT CHECK (
        processor_fee_minor IS NULL OR processor_fee_minor >= 0
    ),
    net_receivable_minor BIGINT CHECK (
        net_receivable_minor IS NULL OR net_receivable_minor >= 0
    ),
    currency TEXT NOT NULL CHECK (currency='USD'),
    execution_profile_hash TEXT NOT NULL CHECK (
        execution_profile_hash ~ '^0x[0-9a-f]{64}$'
    ),
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (provider_environment, provider_capture_id),
    UNIQUE (
        id, customer_payment_id, agency_order_id, provider_environment,
        provider_capture_id
    ),
    FOREIGN KEY (
        funding_position_id, allocation_id, agency_order_id,
        customer_payment_id, paypal_authorization_id, provider_environment,
        gross_minor, currency, execution_profile_hash
    ) REFERENCES payment_mo_funding_positions(
        id, allocation_id, agency_order_id, customer_payment_id,
        paypal_authorization_id, provider_environment, amount_minor, currency,
        execution_profile_hash
    ) ON DELETE RESTRICT,
    CHECK (
        (economics_reconciled
         AND processor_fee_minor IS NOT NULL
         AND net_receivable_minor IS NOT NULL
         AND gross_minor - processor_fee_minor = net_receivable_minor)
        OR
        (NOT economics_reconciled
         AND processor_fee_minor IS NULL
         AND net_receivable_minor IS NULL)
    )
);

CREATE FUNCTION reject_payment_mo_cash_receipt_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
	IF TG_OP='DELETE'
	   AND current_setting('vitlane.account_reset', true)='on' THEN
		RETURN OLD;
	END IF;
    RAISE EXCEPTION 'MO cash receipt is immutable'
        USING ERRCODE='23000';
END;
$$;

CREATE TRIGGER trg_payment_mo_cash_receipt_immutable
BEFORE UPDATE OR DELETE ON payment_mo_cash_receipts
FOR EACH ROW EXECUTE FUNCTION reject_payment_mo_cash_receipt_change();

-- A compensation always covers the full immutable MO customer gross.  The
-- action differs by rail/funding milestone, while the customer amount does not.
CREATE TABLE payment_mo_compensations (
    id UUID PRIMARY KEY,
    allocation_id UUID NOT NULL UNIQUE,
    funding_position_id UUID NOT NULL UNIQUE,
    agency_order_id UUID NOT NULL,
    customer_payment_id UUID NOT NULL,
    rail TEXT NOT NULL CHECK (rail IN ('PAYPAL','GIWA')),
    provider_environment TEXT NOT NULL CHECK (
        provider_environment IN ('SANDBOX','LIVE','TESTNET')
    ),
    action TEXT NOT NULL CHECK (action IN ('VOID','REFUND','TVIT_REFUND')),
    cause TEXT NOT NULL CHECK (cause IN (
        'CUSTOMER_CANCEL_PRE_EFFECT','CUSTOMER_REFUND_POST_EFFECT',
        'PROCUREMENT_FAILURE','DELIVERY_EXCEPTION','DELAY_RULE'
    )),
    state TEXT NOT NULL CHECK (state IN (
        'APPROVED','EXECUTION_PENDING','OUTCOME_UNKNOWN','SUCCEEDED','FAILED'
    )),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency='USD'),
    execution_profile_hash TEXT NOT NULL CHECK (
        execution_profile_hash ~ '^0x[0-9a-f]{64}$'
    ),
    provider_resource_id TEXT,
    idempotency_key TEXT NOT NULL UNIQUE,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    approved_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    support_notified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (id, provider_environment),
    FOREIGN KEY (
        funding_position_id, allocation_id, agency_order_id,
        customer_payment_id, rail, provider_environment, amount_minor,
        currency, execution_profile_hash
    ) REFERENCES payment_mo_funding_positions(
        id, allocation_id, agency_order_id, customer_payment_id, rail,
        provider_environment, amount_minor, currency, execution_profile_hash
    ) ON DELETE RESTRICT,
    CHECK (
        (rail='PAYPAL' AND provider_environment IN ('SANDBOX','LIVE')
         AND action IN ('VOID','REFUND'))
        OR
        (rail='GIWA' AND provider_environment='TESTNET' AND action='TVIT_REFUND')
    ),
    CHECK ((state IN ('SUCCEEDED','FAILED')) = (completed_at IS NOT NULL))
);

CREATE INDEX idx_payment_mo_compensations_support_pending
ON payment_mo_compensations(completed_at, id)
WHERE state='SUCCEEDED' AND support_notified_at IS NULL;

-- A PayPal refund/void or GIWA refund resource can settle only one immutable
-- MO compensation. This also makes a stale same-gross provider identity fail
-- closed at the database boundary.
CREATE UNIQUE INDEX idx_payment_mo_compensations_provider_resource
ON payment_mo_compensations(provider_environment, provider_resource_id)
WHERE provider_resource_id IS NOT NULL;

CREATE FUNCTION enforce_payment_mo_compensation_action()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    has_cash_receipt BOOLEAN;
BEGIN
    SELECT EXISTS (
        SELECT 1 FROM payment_mo_cash_receipts receipt
        WHERE receipt.funding_position_id=NEW.funding_position_id
    ) INTO has_cash_receipt;

    IF NEW.action='VOID' AND has_cash_receipt THEN
        RAISE EXCEPTION 'captured MO funding cannot be voided'
            USING ERRCODE='23514';
    ELSIF NEW.action='REFUND' AND NOT has_cash_receipt THEN
        RAISE EXCEPTION 'uncaptured MO funding cannot be refunded'
            USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_payment_mo_compensation_action
BEFORE INSERT OR UPDATE OF funding_position_id, action
ON payment_mo_compensations
FOR EACH ROW EXECUTE FUNCTION enforce_payment_mo_compensation_action();

-- Durable operation vocabulary for authorize, partial capture and full-MO
-- compensation effects.
ALTER TABLE payment_external_operations
    DROP CONSTRAINT payment_external_operations_purpose_check,
    DROP CONSTRAINT payment_external_operations_owner_kind_check;
ALTER TABLE payment_external_operations
    ADD CONSTRAINT payment_external_operations_purpose_check CHECK (purpose IN (
        'PAYPAL_ORDER_CREATE','PAYPAL_AUTHORIZE','PAYPAL_MO_CAPTURE',
        'PAYPAL_AUTH_VOID','PAYPAL_REAUTHORIZE','PAYPAL_MO_REFUND','TVIT_REFUND'
    )),
    ADD CONSTRAINT payment_external_operations_owner_kind_check CHECK (owner_kind IN (
        'PAYPAL_ATTEMPT','PAYPAL_AUTHORIZATION','MO_FUNDING_POSITION',
        'MO_COMPENSATION'
    )),
    ADD CONSTRAINT payment_external_operations_owner_identity_unique
        UNIQUE (id, owner_kind, owner_id);

-- A manual refund adoption is a GET-verified identity recovery for one expired
-- SENT/UNKNOWN operation. It never authorizes a new POST or request key.
CREATE TABLE payment_paypal_refund_adoptions (
    id UUID PRIMARY KEY,
    compensation_id UUID NOT NULL,
    operation_id UUID NOT NULL UNIQUE,
    operation_owner_kind TEXT NOT NULL DEFAULT 'MO_COMPENSATION'
        CHECK (operation_owner_kind='MO_COMPENSATION'),
    provider_environment TEXT NOT NULL CHECK (
        provider_environment IN ('SANDBOX','LIVE')
    ),
    provider_refund_id TEXT NOT NULL CHECK (
        char_length(BTRIM(provider_refund_id)) BETWEEN 1 AND 255
    ),
    provider_status TEXT NOT NULL CHECK (
        provider_status IN ('COMPLETED','PENDING','FAILED','CANCELLED')
    ),
    outcome_state TEXT NOT NULL CHECK (
        outcome_state IN ('OUTCOME_UNKNOWN','SUCCEEDED','FAILED')
    ),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency='USD'),
    parent_capture_id TEXT NOT NULL CHECK (
        char_length(BTRIM(parent_capture_id)) BETWEEN 1 AND 255
    ),
    invoice_id TEXT NOT NULL CHECK (
        char_length(BTRIM(invoice_id)) BETWEEN 1 AND 127
    ),
    operation_first_sent_at TIMESTAMPTZ NOT NULL,
    operation_idempotency_deadline TIMESTAMPTZ NOT NULL,
    operator_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    public_rationale TEXT NOT NULL CHECK (
        char_length(BTRIM(public_rationale)) BETWEEN 1 AND 2000
    ),
    evidence_source TEXT NOT NULL CHECK (evidence_source IN (
        'PAYPAL_DASHBOARD','PAYPAL_API','PAYPAL_WEBHOOK','OTHER'
    )),
    evidence_hash TEXT NOT NULL CHECK (evidence_hash ~ '^0x[0-9a-f]{64}$'),
    observed_at TIMESTAMPTZ NOT NULL,
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^0x[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (provider_environment, provider_refund_id),
    FOREIGN KEY (compensation_id, provider_environment)
        REFERENCES payment_mo_compensations(id, provider_environment)
        ON DELETE RESTRICT,
    FOREIGN KEY (operation_id, operation_owner_kind, compensation_id)
        REFERENCES payment_external_operations(id, owner_kind, owner_id)
        ON DELETE RESTRICT,
    CHECK (
        (provider_status='COMPLETED' AND outcome_state='SUCCEEDED')
        OR (provider_status IN ('FAILED','CANCELLED') AND outcome_state='FAILED')
        OR (provider_status='PENDING' AND outcome_state='OUTCOME_UNKNOWN')
    ),
    CHECK (operation_first_sent_at <= operation_idempotency_deadline),
    CHECK (created_at >= operation_idempotency_deadline),
    CHECK (observed_at <= created_at + INTERVAL '5 minutes')
);

CREATE FUNCTION reject_payment_paypal_refund_adoption_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP='DELETE'
       AND current_setting('vitlane.account_reset', true)='on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'PayPal refund adoption evidence is immutable'
        USING ERRCODE='23000';
END;
$$;

CREATE TRIGGER trg_payment_paypal_refund_adoption_immutable
BEFORE UPDATE OR DELETE ON payment_paypal_refund_adoptions
FOR EACH ROW EXECUTE FUNCTION reject_payment_paypal_refund_adoption_change();

-- Each successful refresh is append-only evidence. The current provider
-- authorization id on payment_paypal_authorizations advances to the newest
-- generation while authorized_at continues to anchor the original 29-day
-- validity window.
CREATE TABLE payment_paypal_reauthorizations (
    id UUID PRIMARY KEY,
    paypal_authorization_id UUID NOT NULL
        REFERENCES payment_paypal_authorizations(id) ON DELETE RESTRICT,
    operation_id UUID NOT NULL UNIQUE
        REFERENCES payment_external_operations(id) ON DELETE RESTRICT,
    provider_environment TEXT NOT NULL CHECK (
        provider_environment IN ('SANDBOX','LIVE')
    ),
    previous_provider_authorization_id TEXT NOT NULL,
    provider_authorization_id TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency='USD'),
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (provider_environment, provider_authorization_id),
    CHECK (provider_authorization_id <> previous_provider_authorization_id)
);

CREATE FUNCTION reject_payment_paypal_reauthorization_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP='DELETE'
       AND current_setting('vitlane.account_reset', true)='on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'PayPal reauthorization evidence is immutable'
        USING ERRCODE='23000';
END;
$$;

CREATE TRIGGER trg_payment_paypal_reauthorization_immutable
BEFORE UPDATE OR DELETE ON payment_paypal_reauthorizations
FOR EACH ROW EXECUTE FUNCTION reject_payment_paypal_reauthorization_change();

-- A resource-less PAYPAL_REAUTHORIZE operation can be repaired only after its
-- original retry deadline by binding one operator-supplied authorization ID.
-- Payment verifies that ID with a fresh GET; this append-only row preserves
-- every stored and provider fact used by that decision.
CREATE TABLE payment_paypal_reauthorization_adoptions (
    id UUID PRIMARY KEY,
    merchant_order_id UUID NOT NULL
        REFERENCES merchant_orders(id) ON DELETE RESTRICT,
    target_funding_position_id UUID NOT NULL
        REFERENCES payment_mo_funding_positions(id) ON DELETE RESTRICT,
    paypal_authorization_id UUID NOT NULL
        REFERENCES payment_paypal_authorizations(id) ON DELETE RESTRICT,
    operation_id UUID NOT NULL UNIQUE,
    operation_owner_kind TEXT NOT NULL DEFAULT 'PAYPAL_AUTHORIZATION'
        CHECK (operation_owner_kind='PAYPAL_AUTHORIZATION'),
    provider_environment TEXT NOT NULL CHECK (
        provider_environment IN ('SANDBOX','LIVE')
    ),
    previous_provider_authorization_id TEXT NOT NULL CHECK (
        char_length(BTRIM(previous_provider_authorization_id)) BETWEEN 1 AND 255
    ),
    provider_authorization_id TEXT NOT NULL CHECK (
        char_length(BTRIM(provider_authorization_id)) BETWEEN 1 AND 255
    ),
    provider_status TEXT NOT NULL CHECK (
        char_length(BTRIM(provider_status)) BETWEEN 1 AND 64
    ),
    operation_state TEXT NOT NULL CHECK (
        operation_state IN ('SUCCEEDED','FAILED','UNKNOWN')
    ),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL CHECK (currency='USD'),
    paypal_order_id TEXT NOT NULL CHECK (
        char_length(BTRIM(paypal_order_id)) BETWEEN 1 AND 255
    ),
    payee_merchant_id TEXT NOT NULL CHECK (
        char_length(BTRIM(payee_merchant_id)) BETWEEN 1 AND 255
    ),
    provider_created_at TIMESTAMPTZ NOT NULL,
    actor_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    evidence_source TEXT NOT NULL CHECK (evidence_source IN (
        'PAYPAL_DASHBOARD','PAYPAL_SUPPORT','OTHER'
    )),
    evidence_hash TEXT NOT NULL CHECK (evidence_hash ~ '^0x[0-9a-f]{64}$'),
    internal_note TEXT CHECK (char_length(internal_note) <= 4000),
    observed_at TIMESTAMPTZ NOT NULL,
    operation_first_sent_at TIMESTAMPTZ NOT NULL,
    operation_idempotency_deadline TIMESTAMPTZ NOT NULL,
    original_authorized_at TIMESTAMPTZ NOT NULL,
    idempotency_key_hash TEXT NOT NULL UNIQUE CHECK (
        idempotency_key_hash ~ '^0x[0-9a-f]{64}$'
    ),
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^0x[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (provider_environment, provider_authorization_id),
    FOREIGN KEY (
        operation_id, operation_owner_kind, paypal_authorization_id
    ) REFERENCES payment_external_operations(id, owner_kind, owner_id)
        ON DELETE RESTRICT,
    CHECK (provider_authorization_id <> previous_provider_authorization_id),
    CHECK (
        (provider_status='CREATED' AND operation_state='SUCCEEDED')
        OR (provider_status IN ('DENIED','VOIDED','EXPIRED')
            AND operation_state='FAILED')
        OR (provider_status NOT IN ('CREATED','DENIED','VOIDED','EXPIRED')
            AND operation_state='UNKNOWN')
    ),
    CHECK (operation_first_sent_at >= original_authorized_at),
    CHECK (operation_first_sent_at <= operation_idempotency_deadline),
    CHECK (created_at >= operation_idempotency_deadline),
    CHECK (provider_created_at >= operation_first_sent_at),
    CHECK (
        provider_created_at < original_authorized_at + INTERVAL '29 days'
    ),
    CHECK (observed_at >= provider_created_at),
    CHECK (observed_at <= created_at + INTERVAL '5 minutes')
);

CREATE FUNCTION reject_payment_paypal_reauthorization_adoption_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP='DELETE'
       AND current_setting('vitlane.account_reset', true)='on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'PayPal reauthorization adoption evidence is immutable'
        USING ERRCODE='23000';
END;
$$;

CREATE TRIGGER trg_payment_paypal_reauthorization_adoption_immutable
BEFORE UPDATE OR DELETE ON payment_paypal_reauthorization_adoptions
FOR EACH ROW EXECUTE FUNCTION reject_payment_paypal_reauthorization_adoption_change();

-- Procurement can plan from a PayPal authorization/customer payment before
-- any MO capture exists.  GIWA keeps the existing accepted-receipt source.
ALTER TABLE procurement_manifests
    ALTER COLUMN funds_receipt_id DROP NOT NULL,
    ADD COLUMN customer_payment_id UUID,
    ADD CONSTRAINT procurement_manifests_customer_payment_fk FOREIGN KEY (
        customer_payment_id, agency_order_id, execution_profile_hash
    ) REFERENCES payment_customer_payments(
        id, agency_order_id, execution_profile_hash
    ) ON DELETE RESTRICT,
    ADD CONSTRAINT procurement_manifests_one_planning_source CHECK (
        (funds_receipt_id IS NOT NULL)::integer
        + (customer_payment_id IS NOT NULL)::integer = 1
    );

CREATE UNIQUE INDEX idx_procurement_manifests_customer_payment
    ON procurement_manifests(customer_payment_id)
    WHERE customer_payment_id IS NOT NULL;

-- A MerchantOrder is the operational projection of exactly one immutable MO
-- allocation.  The precondition above guarantees there are no legacy rows for
-- which this fact would have to be invented.
ALTER TABLE merchant_orders
    ADD COLUMN allocation_id UUID NOT NULL,
    ADD CONSTRAINT merchant_orders_allocation_unique UNIQUE (allocation_id),
    ADD CONSTRAINT merchant_orders_id_allocation_order_unique UNIQUE (
        id, allocation_id, agency_order_id
    ),
    ADD CONSTRAINT merchant_orders_allocation_order_fk FOREIGN KEY (
        allocation_id, agency_order_id
    ) REFERENCES agency_order_mo_allocations(id, agency_order_id) ON DELETE RESTRICT;

-- Physical units are retained only for logistics identity. Monetary refund
-- progress is represented by the MO compensation row, never a unit status.
ALTER TABLE merchant_order_units
    DROP CONSTRAINT merchant_order_units_disposition_check,
    ADD CONSTRAINT merchant_order_units_disposition_check CHECK (
        disposition IN ('PENDING','NO_PAYMENT_EFFECT')
    );

-- BeginMerchantEffect first owns a funding activation lock.  Seller purchase
-- may reach STARTED only after that position is FUNDED.
ALTER TABLE procurement_effect_locks
    DROP CONSTRAINT procurement_effect_locks_state_check,
    DROP CONSTRAINT procurement_effect_locks_check;
ALTER TABLE procurement_effect_locks
    ADD COLUMN funding_position_id UUID NOT NULL UNIQUE
        REFERENCES payment_mo_funding_positions(id) ON DELETE RESTRICT,
    ADD COLUMN funding_state TEXT NOT NULL CHECK (funding_state IN (
        'FUNDING_PENDING','FUNDING_UNKNOWN','FUNDED','RELEASED','FAILED'
    )),
    ADD COLUMN funding_requested_at TIMESTAMPTZ NOT NULL,
    ADD COLUMN funding_resolved_at TIMESTAMPTZ,
    ADD CONSTRAINT procurement_effect_locks_state_check CHECK (state IN (
        'FUNDING_PENDING','FUNDING_UNKNOWN','STARTED','PLACED','FAILED','OUTCOME_UNKNOWN'
    )),
    ADD CONSTRAINT procurement_effect_locks_resolution_check CHECK (
        (state IN ('FUNDING_PENDING','FUNDING_UNKNOWN','STARTED') AND resolved_at IS NULL)
        OR
        (state IN ('PLACED','FAILED','OUTCOME_UNKNOWN') AND resolved_at IS NOT NULL)
    ),
    ADD CONSTRAINT procurement_effect_locks_funding_resolution_check CHECK (
        (funding_state IN ('FUNDING_PENDING','FUNDING_UNKNOWN')
         AND funding_resolved_at IS NULL)
        OR
        (funding_state IN ('FUNDED','RELEASED','FAILED')
         AND funding_resolved_at IS NOT NULL)
    ),
    ADD CONSTRAINT procurement_effect_locks_funding_before_effect_check CHECK (
        (state='FUNDING_PENDING' AND funding_state='FUNDING_PENDING')
        OR (state='FUNDING_UNKNOWN' AND funding_state='FUNDING_UNKNOWN')
        OR (state IN ('STARTED','PLACED','OUTCOME_UNKNOWN') AND funding_state='FUNDED')
        OR (state='FAILED' AND funding_state IN ('FUNDED','FAILED'))
    );

CREATE FUNCTION enforce_procurement_effect_funding_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM merchant_orders merchant_order
        JOIN payment_mo_funding_positions funding
          ON funding.allocation_id=merchant_order.allocation_id
         AND funding.agency_order_id=merchant_order.agency_order_id
        WHERE merchant_order.id=NEW.merchant_order_id
          AND funding.id=NEW.funding_position_id
    ) THEN
        RAISE EXCEPTION 'merchant effect funding position belongs to another MO'
            USING ERRCODE='23503';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_procurement_effect_funding_owner
BEFORE INSERT OR UPDATE OF merchant_order_id, funding_position_id
ON procurement_effect_locks
FOR EACH ROW EXECUTE FUNCTION enforce_procurement_effect_funding_owner();

-- A terminal funding activation failure is a Procurement-owned projection:
-- the exact MO/task are failed and the operator-visible reason is retained in
-- the same audit stream as the successful merchant-effect start.
ALTER TABLE agency_order_execution_audits
    DROP CONSTRAINT agency_order_execution_audits_action_check;
ALTER TABLE agency_order_execution_audits
    ADD CONSTRAINT agency_order_execution_audits_action_check CHECK (action IN (
        'PAYMENT_FINALIZED','OPERATOR_ASSIGNED','MERCHANT_ORDER_ACCEPTED',
        'FULFILLMENT_FAILED','SIBLING_CANCELLED','PROCUREMENT_PLANNED',
        'CUSTOMER_CANCELLED','MERCHANT_ORDER_PLACED',
        'RECOVERY_CREATED','RECOVERY_RECORDED','RECOVERY_WAIVED','RECOVERY_DELETED',
        'PROCUREMENT_DECISION_RECORDED','PROCUREMENT_REQUEST_CREATED',
        'PROCUREMENT_REQUEST_RESOLVED','MERCHANT_EFFECT_STARTED',
		'MERCHANT_EFFECT_FUNDING_FAILED','MERCHANT_EFFECT_FUNDING_REASSIGNED',
		'MERCHANT_EFFECT_CUSTOMER_STOP_AFTER_FUNDING'
    ));

-- Customer cancellation and refund-review intent now target one planned
-- MerchantOrder and its immutable economic allocation.
ALTER TABLE agency_order_cancellations
    DROP CONSTRAINT agency_order_cancellations_agency_order_id_key,
    DROP CONSTRAINT agency_order_cancellations_refund_basis_check,
    ADD COLUMN allocation_id UUID NOT NULL,
    ADD COLUMN merchant_order_id UUID NOT NULL,
    ADD CONSTRAINT agency_order_cancellations_allocation_unique UNIQUE (allocation_id),
    ADD CONSTRAINT agency_order_cancellations_allocation_order_fk FOREIGN KEY (
        allocation_id, agency_order_id
    ) REFERENCES agency_order_mo_allocations(id, agency_order_id) ON DELETE RESTRICT,
    ADD CONSTRAINT agency_order_cancellations_merchant_target_fk FOREIGN KEY (
        merchant_order_id, allocation_id, agency_order_id
    ) REFERENCES merchant_orders(id, allocation_id, agency_order_id) ON DELETE RESTRICT,
    ADD CONSTRAINT agency_order_cancellations_refund_basis_check
        CHECK (refund_basis='GROSS');

ALTER TABLE agency_order_refund_requests
    DROP CONSTRAINT agency_order_refund_requests_reason_check,
    DROP CONSTRAINT refund_requests_review_contract_check,
    DROP CONSTRAINT refund_requests_public_rationale_check,
    DROP COLUMN reason,
    DROP COLUMN review_contract_version,
    ALTER COLUMN public_rationale SET NOT NULL,
    ADD COLUMN allocation_id UUID NOT NULL,
    ADD COLUMN merchant_order_id UUID NOT NULL,
    ADD COLUMN decision TEXT CHECK (decision IN ('APPROVED','REJECTED')),
    ADD COLUMN decision_public_rationale TEXT CHECK (
        decision_public_rationale IS NULL
        OR char_length(BTRIM(decision_public_rationale)) BETWEEN 1 AND 2000
    ),
    ADD COLUMN internal_note TEXT CHECK (
        internal_note IS NULL OR char_length(internal_note) <= 4000
    ),
    ADD COLUMN decided_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    ADD COLUMN decided_at TIMESTAMPTZ,
    ADD CONSTRAINT agency_order_refund_requests_public_rationale_check CHECK (
        char_length(BTRIM(public_rationale)) BETWEEN 1 AND 500
    ),
    ADD CONSTRAINT agency_order_refund_requests_allocation_order_fk FOREIGN KEY (
        allocation_id, agency_order_id
    ) REFERENCES agency_order_mo_allocations(id, agency_order_id) ON DELETE RESTRICT,
    ADD CONSTRAINT agency_order_refund_requests_merchant_target_fk FOREIGN KEY (
        merchant_order_id, allocation_id, agency_order_id
    ) REFERENCES merchant_orders(id, allocation_id, agency_order_id) ON DELETE RESTRICT,
    ADD CONSTRAINT agency_order_refund_requests_decision_shape_check CHECK (
        (state <> 'RESOLVED' AND decision IS NULL
         AND decision_public_rationale IS NULL AND decided_by_user_id IS NULL
         AND decided_at IS NULL)
        OR
        (state='RESOLVED' AND decision IS NOT NULL
         AND decision_public_rationale IS NOT NULL
         AND decided_by_user_id IS NOT NULL AND decided_at IS NOT NULL)
    );

CREATE UNIQUE INDEX idx_agency_order_refund_requests_open_allocation
    ON agency_order_refund_requests(allocation_id)
    WHERE state IN ('REQUESTED','REVIEWING');

-- Reuse the proven GIWA nonce/sign/broadcast outbox for one whole-MO TEST
-- refund, but replace its legacy CustomerRefund owner with MOCompensation.
DROP INDEX idx_settlement_command_customer_refund;
ALTER TABLE settlement_command_outbox
    DROP CONSTRAINT settlement_command_outbox_partial_shape_check,
    DROP COLUMN customer_refund_id,
    ADD COLUMN mo_compensation_id UUID
        REFERENCES payment_mo_compensations(id) ON DELETE RESTRICT,
    ADD CONSTRAINT settlement_command_outbox_partial_shape_check CHECK (
        (
            purpose='COMPLETE'
            AND mo_compensation_id IS NULL
            AND pass_through_part IS NULL
            AND fee_part IS NULL
            AND refund_key IS NULL
        )
        OR
        (
            purpose='REFUND_PARTIAL'
            AND mo_compensation_id IS NOT NULL
            AND pass_through_part IS NOT NULL
            AND fee_part IS NOT NULL
            AND (pass_through_part > 0 OR fee_part > 0)
            AND refund_key ~ '^0x[0-9a-f]{64}$'
            AND fulfillment_hash IS NULL
        )
    );

-- One compensation may need more than one chain attempt after a definitive
-- revert. Historical tx->MO identity remains append-only so a late canonical
-- event can never fall through to the whole-order refund path. Only one
-- nonterminal command may exist at a time; every attempt shares the same
-- contract refund_key.
DROP INDEX idx_settlement_command_refund_key;
CREATE INDEX idx_settlement_command_refund_key
    ON settlement_command_outbox(refund_key)
    WHERE refund_key IS NOT NULL;
CREATE UNIQUE INDEX idx_settlement_command_mo_compensation
    ON settlement_command_outbox(mo_compensation_id)
    WHERE mo_compensation_id IS NOT NULL
      AND state IN ('PLANNED','NONCE_RESERVED','SIGNED','BROADCAST');

-- The old attempt/claim refund ledger cannot coexist truthfully with the new
-- one-compensation-per-allocation model. The cutover guard guarantees emptiness.
DROP TABLE payment_refund_slice_claims;
DROP TABLE payment_customer_refund_attempts;
DROP TABLE payment_customer_refunds;

-- PayPal disputes now identify the exact MO partial capture receipt.
ALTER TABLE payment_paypal_dispute_cases
    DROP COLUMN funds_receipt_id,
    ADD COLUMN mo_cash_receipt_id UUID NOT NULL,
    ADD CONSTRAINT payment_paypal_dispute_cases_mo_receipt_fk FOREIGN KEY (
        mo_cash_receipt_id, customer_payment_id, agency_order_id, environment,
        capture_id
    ) REFERENCES payment_mo_cash_receipts(
        id, customer_payment_id, agency_order_id, provider_environment,
        provider_capture_id
    ) ON DELETE RESTRICT;

-- Retire monetary unit slices in explicit dependency order.  Physical units
-- remain for logistics identity only.  Dependencies are removed explicitly.
DROP TABLE agency_order_refund_request_items;
ALTER TABLE merchant_order_units DROP COLUMN slice_id;
ALTER TABLE logistics_expected_units DROP COLUMN slice_id;
DROP TABLE agency_order_refund_slices;

-- Keep idx_payment_funds_receipts_accepted: payment_funds_receipts remains the
-- whole-order GIWA receipt table and still permits at most one accepted receipt
-- per customer payment. PayPal MO captures live in payment_mo_cash_receipts and
-- therefore do not require weakening that independent money-truth invariant.
