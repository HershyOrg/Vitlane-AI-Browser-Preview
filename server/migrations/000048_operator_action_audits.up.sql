-- ADR-0040 §10: sensitive operator actions carry an append-only audit with
-- the acting operator and the stated reason. Session revocation is the first
-- action; operator offboarding extends the CHECK in its own migration.
CREATE TABLE operator_action_audits (
    id UUID PRIMARY KEY,
    operator_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (action IN ('SESSION_REVOKE_ALL')),
    subject_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    reason_detail TEXT NOT NULL
        CHECK (char_length(reason_detail) BETWEEN 8 AND 500),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX operator_action_audits_created_idx
    ON operator_action_audits(created_at);
