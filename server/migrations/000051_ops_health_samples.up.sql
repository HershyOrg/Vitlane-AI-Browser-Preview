-- ADR-0040 §6 supplement: five-minute ops health snapshots for the operator
-- dashboard's time-series view. First-party table with a 35-day retention
-- the sampler enforces on every insert; it stores PII-free aggregates only
-- (the same document the ops health readback serves) and no alerting ever
-- reads it, so losing this table loses charts, never detection.
CREATE TABLE ops_health_samples (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sampled_at TIMESTAMPTZ NOT NULL,
    report JSONB NOT NULL
);

CREATE INDEX ops_health_samples_sampled_at_idx
    ON ops_health_samples (sampled_at);
