CREATE TABLE curation_auto_resolutions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    curation_id UUID NOT NULL REFERENCES curations(id) ON DELETE RESTRICT,
    plan_id UUID NOT NULL REFERENCES shopping_plans(id) ON DELETE RESTRICT,
    request_hash TEXT NOT NULL,
    target_snapshot_hash TEXT NOT NULL,
    expected_curation_version BIGINT NOT NULL CHECK (expected_curation_version > 0),
    status TEXT NOT NULL CHECK (
        status IN ('PENDING', 'RESOLVED', 'EXECUTED', 'NEEDS_SELECTION')
    ),
    decision TEXT CHECK (
        decision IS NULL OR decision IN (
            'ADD_TARGET', 'RESEARCH_AGAIN', 'NEEDS_SELECTION'
        )
    ),
    source TEXT CHECK (
        source IS NULL OR source IN ('DETERMINISTIC', 'MANAGED')
    ),
    target_id UUID REFERENCES plan_targets(id) ON DELETE RESTRICT,
    session_id UUID REFERENCES shopping_sessions(id) ON DELETE RESTRICT,
    session_version BIGINT CHECK (session_version IS NULL OR session_version > 0),
    reason_code TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT curation_auto_resolutions_hash_check CHECK (
        btrim(request_hash) <> '' AND btrim(target_snapshot_hash) <> ''
    ),
    CONSTRAINT curation_auto_resolutions_state_check CHECK (
        (status = 'PENDING' AND decision IS NULL AND source IS NULL
            AND target_id IS NULL AND session_id IS NULL
            AND session_version IS NULL AND reason_code IS NULL
            AND completed_at IS NULL)
        OR
        (status = 'NEEDS_SELECTION' AND decision = 'NEEDS_SELECTION'
            AND source IS NOT NULL AND target_id IS NULL
            AND session_id IS NULL AND session_version IS NULL
            AND reason_code IS NOT NULL AND btrim(reason_code) <> ''
            AND completed_at IS NULL)
        OR
        (status IN ('RESOLVED', 'EXECUTED')
            AND decision IN ('ADD_TARGET', 'RESEARCH_AGAIN')
            AND source IS NOT NULL AND reason_code IS NOT NULL
            AND btrim(reason_code) <> ''
            AND (
                (decision = 'ADD_TARGET' AND target_id IS NULL
                    AND session_id IS NULL AND session_version IS NULL)
                OR
                (decision = 'RESEARCH_AGAIN' AND target_id IS NOT NULL
                    AND session_id IS NOT NULL AND session_version IS NOT NULL)
            )
            AND ((status = 'EXECUTED') = (completed_at IS NOT NULL)))
    )
);

CREATE INDEX curation_auto_resolutions_curation_created_idx
    ON curation_auto_resolutions(curation_id, created_at DESC);

COMMENT ON TABLE curation_auto_resolutions IS
    'Server-owned @Auto decision and exact-action dispatch checkpoint. Original request text remains in the existing exact-action source; this table stores hashes and bounded decision metadata.';
