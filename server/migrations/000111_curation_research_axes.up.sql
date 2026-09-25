CREATE TABLE curation_target_criteria (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 curation_id uuid NOT NULL REFERENCES curations(id) ON DELETE CASCADE,
 target_id uuid NOT NULL REFERENCES plan_targets(id) ON DELETE CASCADE,
 version bigint NOT NULL CHECK(version > 0),
 criteria jsonb NOT NULL,
 PRIMARY KEY(user_id,curation_id,target_id)
);
CREATE TABLE curation_criteria_commands (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 curation_id uuid NOT NULL REFERENCES curations(id) ON DELETE CASCADE,
 target_id uuid NOT NULL REFERENCES plan_targets(id) ON DELETE CASCADE,
 request_key text NOT NULL,
 request_hash bytea NOT NULL,
 result jsonb NOT NULL,
 PRIMARY KEY(user_id,curation_id,target_id,request_key)
);
CREATE TABLE curation_generation_locales (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 content_locale text NOT NULL CHECK(content_locale IN ('ko-KR','en-US')),
 PRIMARY KEY(user_id,request_id)
);
CREATE TABLE curation_criteria_axis_meanings (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 curation_id uuid NOT NULL REFERENCES curations(id) ON DELETE CASCADE,
 target_id uuid NOT NULL REFERENCES plan_targets(id) ON DELETE CASCADE,
 axis_id text NOT NULL,
 definition text NOT NULL,
 uses_price boolean NOT NULL,
 uses_visual_evidence boolean NOT NULL,
 PRIMARY KEY(user_id,curation_id,target_id,axis_id)
);
ALTER TABLE phase8_research_candidates ADD COLUMN axis_assessment jsonb;
CREATE TABLE research_criteria_checkpoints (
 round_id uuid PRIMARY KEY REFERENCES research_rounds(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 execution jsonb NOT NULL
);
CREATE TABLE research_source_progress (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 curation_id uuid NOT NULL REFERENCES curations(id) ON DELETE CASCADE,
 target_id uuid NOT NULL REFERENCES plan_targets(id) ON DELETE CASCADE,
 fingerprint text NOT NULL,
 progress jsonb NOT NULL,
 PRIMARY KEY(user_id,curation_id,target_id,fingerprint)
);

ALTER TABLE phase8_research_pools ADD COLUMN discovery_outcome jsonb;
