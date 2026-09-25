-- PayPal Live Pilot runtime deny overlay. Static deployment flags remain the
-- upper bound; this row defaults every new Live side effect to killed.
CREATE TABLE ordering_live_control (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    version BIGINT NOT NULL CHECK (version > 0),
    order_issue_killed BOOLEAN NOT NULL,
    paypal_money_killed BOOLEAN NOT NULL,
    merchant_effect_killed BOOLEAN NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL,
    changed_by TEXT NOT NULL,
    reason TEXT NOT NULL
);

INSERT INTO ordering_live_control(
    singleton, version, order_issue_killed, paypal_money_killed,
    merchant_effect_killed, changed_at, changed_by, reason
) VALUES (TRUE, 1, TRUE, TRUE, TRUE, NOW(), 'SYSTEM_MIGRATION',
          'FAIL_CLOSED_INITIAL_STATE');

CREATE TABLE ordering_live_control_audits (
    audit_id UUID PRIMARY KEY,
    action TEXT NOT NULL CHECK (action IN ('KILL', 'REACTIVATE')),
    -- Rejected attempts preserve the submitted scope for audit; successful
    -- mutations are restricted by the application domain parser.
    scope TEXT NOT NULL,
    actor_user_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    expected_version BIGINT NOT NULL,
    observed_version BIGINT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('SUCCEEDED', 'REJECTED')),
    reason_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX ordering_live_control_audits_created_idx
    ON ordering_live_control_audits(created_at DESC);
