ALTER TABLE managed_runner_usage_daily
    ADD COLUMN input_tokens BIGINT NOT NULL DEFAULT 0
        CHECK (input_tokens >= 0),
    ADD COLUMN output_tokens BIGINT NOT NULL DEFAULT 0
        CHECK (output_tokens >= 0);
