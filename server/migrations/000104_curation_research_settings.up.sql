CREATE TABLE account_user_preferences (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0),
    ui_locale TEXT CHECK (ui_locale IN ('ko-KR','en-US')),
    preferred_currency TEXT CHECK (preferred_currency IN ('KRW','USD')),
    research_country TEXT CHECK (research_country IN ('KR','US')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- NULL preserves the original market of existing curations. This is a seed,
-- not a live binding to another curation or to the account's latest setting.
ALTER TABLE curations ADD COLUMN research_country TEXT CHECK (research_country IN ('KR','US'));
ALTER TABLE curations ADD COLUMN research_settings_version BIGINT NOT NULL DEFAULT 0 CHECK (research_settings_version >= 0);
