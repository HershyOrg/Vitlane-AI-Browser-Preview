-- Durable owner-bound journal for an on-device merchant browser run. A run
-- records approvals and observations only; it does not authorize credentials,
-- OTP, payment details or a merchant's final purchase action.
CREATE TABLE browser_runs (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    curation_id UUID NOT NULL,
    candidate_id TEXT NOT NULL,
    product_url TEXT NOT NULL CHECK (
        char_length(btrim(product_url)) BETWEEN 1 AND 2048
        AND product_url LIKE 'https://%'
    ),
    merchant_origin TEXT NOT NULL CHECK (
        char_length(btrim(merchant_origin)) BETWEEN 1 AND 512
        AND merchant_origin LIKE 'https://%'
    ),
    merchant_host TEXT NOT NULL CHECK (
        char_length(btrim(merchant_host)) BETWEEN 1 AND 255
    ),
    state TEXT NOT NULL CHECK (state IN (
        'AWAITING_NAVIGATION_APPROVAL', 'NAVIGATION_APPROVED',
        'AWAITING_PREPARATION_APPROVAL', 'PREPARATION_APPROVED',
        'USER_CONTROL', 'RESUME_REQUIRES_OBSERVATION', 'PAUSED',
        'READY_FOR_USER_PAYMENT', 'COMPLETED', 'PARTIAL', 'FAILED',
        'RESULT_UNKNOWN', 'CANCELLED'
    )),
    control_owner TEXT NOT NULL CHECK (control_owner IN ('NONE', 'AGENT', 'USER')),
    version BIGINT NOT NULL CHECK (version > 0),
    navigation_approved_at TIMESTAMPTZ,
    plan_revision BIGINT CHECK (plan_revision > 0),
    quote_digest TEXT CHECK (quote_digest ~ '^sha256:[0-9a-f]{64}$'),
    allowed_preparation_steps JSONB CHECK (
        jsonb_typeof(allowed_preparation_steps)='array'
        AND jsonb_array_length(allowed_preparation_steps) BETWEEN 1 AND 4
        AND allowed_preparation_steps <@ '["ADD_TO_CART","OPEN_CHECKOUT_REVIEW","SELECT_VARIANT","SET_QUANTITY"]'::jsonb
    ),
    price_ceiling_minor BIGINT CHECK (price_ceiling_minor > 0),
    price_currency TEXT CHECK (price_currency ~ '^[A-Z]{3}$'),
    preparation_approved_at TIMESTAMPTZ,
    latest_observation_revision BIGINT NOT NULL DEFAULT 0 CHECK (latest_observation_revision >= 0),
    latest_observation_kind TEXT CHECK (latest_observation_kind IN (
        'PUBLIC_PRODUCT', 'SIGN_IN_REQUIRED', 'CHECKOUT_REVIEW', 'RESULT'
    )),
    latest_observation_origin TEXT,
    latest_page_identity_digest TEXT CHECK (
        latest_page_identity_digest ~ '^sha256:[0-9a-f]{64}$'
    ),
	latest_session_state_hint TEXT CHECK (
		latest_session_state_hint IN ('authenticated','anonymous','unknown')
	),
    latest_observed_at TIMESTAMPTZ,
    handoff_reason TEXT CHECK (handoff_reason IN (
        'SIGN_IN_REQUIRED', 'VERIFICATION_REQUIRED',
        'UNSUPPORTED_INTERACTION', 'USER_REQUESTED', 'FINAL_PAYMENT_REQUIRED'
    )),
    resume_state TEXT CHECK (resume_state IN (
        'NAVIGATION_APPROVED', 'AWAITING_PREPARATION_APPROVAL', 'PREPARATION_APPROVED'
    )),
    resume_after_observation_revision BIGINT NOT NULL DEFAULT 0
        CHECK (resume_after_observation_revision >= 0),
    fresh_observation_required BOOLEAN NOT NULL DEFAULT false,
    result_outcome TEXT CHECK (result_outcome IN ('SUCCEEDED','PARTIAL','FAILED','UNKNOWN')),
    result_evidence_source TEXT CHECK (result_evidence_source IN ('MERCHANT_OBSERVED','USER_REPORTED')),
    result_evidence_digest TEXT CHECK (result_evidence_digest ~ '^sha256:[0-9a-f]{64}$'),
    result_observation_revision BIGINT CHECK (result_observation_revision > 0),
    result_verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    terminal_at TIMESTAMPTZ,
    CONSTRAINT browser_runs_candidate_fkey
        FOREIGN KEY (user_id, curation_id, candidate_id)
        REFERENCES phase8_research_candidates(user_id, curation_id, candidate_id)
        ON DELETE CASCADE,
    CONSTRAINT browser_runs_preparation_shape_check CHECK (
        (plan_revision IS NULL AND quote_digest IS NULL
            AND allowed_preparation_steps IS NULL AND price_ceiling_minor IS NULL
            AND price_currency IS NULL AND preparation_approved_at IS NULL)
        OR
        (plan_revision IS NOT NULL AND quote_digest IS NOT NULL
            AND allowed_preparation_steps IS NOT NULL AND price_ceiling_minor IS NOT NULL
            AND price_currency IS NOT NULL AND preparation_approved_at IS NOT NULL)
    ),
    CONSTRAINT browser_runs_observation_shape_check CHECK (
        (latest_observation_revision=0 AND latest_observation_kind IS NULL
            AND latest_observation_origin IS NULL AND latest_page_identity_digest IS NULL
			AND latest_session_state_hint IS NULL
            AND latest_observed_at IS NULL)
        OR
        (latest_observation_revision>0 AND latest_observation_kind IS NOT NULL
            AND latest_observation_origin IS NOT NULL AND latest_page_identity_digest IS NOT NULL
			AND latest_session_state_hint IS NOT NULL
            AND latest_observed_at IS NOT NULL)
    ),
    CONSTRAINT browser_runs_result_shape_check CHECK (
        (result_outcome IS NULL AND result_evidence_source IS NULL
            AND result_evidence_digest IS NULL AND result_observation_revision IS NULL
            AND result_verified_at IS NULL)
        OR
        (result_outcome IS NOT NULL AND result_evidence_source='USER_REPORTED'
            AND result_evidence_digest IS NULL AND result_observation_revision IS NULL
            AND result_verified_at IS NOT NULL)
        OR
        (result_outcome IS NOT NULL AND result_evidence_source='MERCHANT_OBSERVED'
            AND result_evidence_digest IS NOT NULL AND result_observation_revision IS NOT NULL
            AND result_verified_at IS NOT NULL)
    ),
    CONSTRAINT browser_runs_terminal_shape_check CHECK (
        (state IN ('COMPLETED','PARTIAL','FAILED','RESULT_UNKNOWN','CANCELLED')) =
        (terminal_at IS NOT NULL)
    ),
    CONSTRAINT browser_runs_fresh_observation_check CHECK (
        (state='RESUME_REQUIRES_OBSERVATION') = fresh_observation_required
    )
);

CREATE INDEX browser_runs_owner_updated_idx
    ON browser_runs(user_id, updated_at DESC, id);

CREATE TABLE browser_run_commands (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    run_id UUID NOT NULL REFERENCES browser_runs(id) ON DELETE CASCADE,
    command_type TEXT NOT NULL CHECK (command_type IN (
        'CREATE', 'APPROVE_NAVIGATION', 'APPROVE_PREPARATION',
        'RECORD_OBSERVATION', 'REQUIRE_HANDOFF', 'TAKE_OVER', 'PAUSE',
        'REQUEST_RESUME', 'CANCEL', 'VERIFY_RESULT'
    )),
    idempotency_key TEXT NOT NULL CHECK (
        char_length(btrim(idempotency_key)) BETWEEN 8 AND 200
    ),
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^0x[0-9a-f]{64}$'),
    response_version BIGINT NOT NULL CHECK (response_version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, idempotency_key)
);

CREATE TABLE browser_run_events (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    run_id UUID NOT NULL REFERENCES browser_runs(id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    event_type TEXT NOT NULL CHECK (
        char_length(btrim(event_type)) BETWEEN 1 AND 100
    ),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (run_id, sequence)
);
