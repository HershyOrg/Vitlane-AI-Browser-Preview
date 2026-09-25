-- One durable row per completed research Round. The pool keeps only the latest
-- coverage per Target, so operators could not see per-Round source outcomes,
-- admitted counts or (from the partial-evaluation change onward) how many
-- observed products were actually evaluated. Failed Rounds keep their reason on
-- research_rounds; this table records completions only.
CREATE TABLE research_round_outcomes (
    round_id UUID PRIMARY KEY REFERENCES research_rounds(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    curation_id UUID NOT NULL,
    plan_target_id UUID NOT NULL,
    attempt_id UUID,
    country TEXT NOT NULL CHECK (country IN ('KR', 'US')),
    mode TEXT NOT NULL CHECK (mode IN ('REPLACE', 'APPEND')),
    observed_count INTEGER NOT NULL CHECK (observed_count >= 0),
    duplicate_count INTEGER NOT NULL CHECK (duplicate_count >= 0),
    rejected_count INTEGER NOT NULL CHECK (rejected_count >= 0),
    admitted_count INTEGER NOT NULL CHECK (admitted_count >= 0),
    evaluated_count INTEGER NOT NULL CHECK (evaluated_count >= 0),
    unevaluated_count INTEGER NOT NULL CHECK (unevaluated_count >= 0),
    source_coverage JSONB NOT NULL DEFAULT '[]'
        CHECK (jsonb_typeof(source_coverage) = 'array'),
    duration_milliseconds BIGINT NOT NULL DEFAULT 0
        CHECK (duration_milliseconds >= 0),
    completed_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX research_round_outcomes_completed_idx
    ON research_round_outcomes(completed_at DESC);
