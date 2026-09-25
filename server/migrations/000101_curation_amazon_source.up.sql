-- Curation enhancement Step 1. Existing Shopify IDs and references are untouched.
ALTER TABLE phase8_research_candidates DROP CONSTRAINT phase8_research_candidates_source_kind_check;
ALTER TABLE phase8_research_candidates ADD CONSTRAINT phase8_research_candidates_source_kind_check
 CHECK (source_kind IN ('SHOPIFY_LIVE','AMAZON'));
ALTER TABLE phase8_research_candidates ADD CONSTRAINT phase8_amazon_reference_check CHECK (
 source_kind <> 'AMAZON' OR (
  provider_product_id ~ '^amazon:US:[A-Z0-9]{10}$'
  AND identity_key=provider_product_id AND locator_kind='PRODUCT_URL'
  AND product_url='https://www.amazon.com/dp/' || right(provider_product_id,10)
 )
);

CREATE TABLE research_catalog_api_quota (
 source TEXT PRIMARY KEY CHECK(source='AMAZON'),
 quota_limit BIGINT CHECK(quota_limit>=0),
 provider_used BIGINT CHECK(provider_used>=0),
 provider_remaining BIGINT CHECK(provider_remaining>=0),
 reset_at TIMESTAMPTZ,
 observed_at TIMESTAMPTZ,
 baseline_at TIMESTAMPTZ,
 is_free BOOLEAN,
 refresh_failure TEXT NOT NULL DEFAULT ''
);
INSERT INTO research_catalog_api_quota(source) VALUES ('AMAZON');
CREATE TABLE research_catalog_api_calls (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 source TEXT NOT NULL REFERENCES research_catalog_api_quota(source),
 operation TEXT NOT NULL CHECK(operation IN ('SEARCH','DETAIL')),
 outcome TEXT NOT NULL DEFAULT 'RUNNING',
 billable BOOLEAN NOT NULL DEFAULT true,
 started_at TIMESTAMPTZ NOT NULL,
 completed_at TIMESTAMPTZ
);
CREATE INDEX research_catalog_api_calls_time_idx ON research_catalog_api_calls(source,started_at);

-- Defense in depth: even a direct Cart write cannot introduce an external source.
CREATE FUNCTION phase8_guard_checkout_source() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM phase8_research_candidates c
  WHERE c.user_id=NEW.user_id AND c.curation_id=NEW.curation_id
   AND c.plan_target_id=NEW.plan_target_id AND c.candidate_id=NEW.candidate_id
   AND c.source_kind='SHOPIFY_LIVE'
 ) THEN
  RAISE EXCEPTION 'EXTERNAL_PRODUCT_CART_FORBIDDEN' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER phase8_cart_source_guard BEFORE INSERT OR UPDATE ON phase8_cart_items
 FOR EACH ROW EXECUTE FUNCTION phase8_guard_checkout_source();

DROP INDEX phase8_research_candidates_provider_product_identity_idx;
CREATE UNIQUE INDEX phase8_research_candidates_provider_product_identity_idx
 ON phase8_research_candidates(user_id,curation_id,plan_target_id,source_kind,provider_product_id);
ALTER TABLE phase8_research_pools ADD COLUMN source_coverage JSONB NOT NULL DEFAULT '[]';
ALTER TABLE phase8_candidate_configurations ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK(version>0);
CREATE TABLE research_amazon_relations (
 token_hash TEXT PRIMARY KEY,
 user_id UUID NOT NULL,curation_id UUID NOT NULL,candidate_id TEXT NOT NULL,
 anchor_asin TEXT NOT NULL, variants JSONB NOT NULL,
 relation_status TEXT NOT NULL,truncated BOOLEAN NOT NULL,
 observed_at TIMESTAMPTZ NOT NULL,expires_at TIMESTAMPTZ NOT NULL,
 FOREIGN KEY(user_id,curation_id,candidate_id) REFERENCES phase8_research_candidates(user_id,curation_id,candidate_id) ON DELETE CASCADE
);
CREATE INDEX research_amazon_relations_expiry ON research_amazon_relations(expires_at);
CREATE TABLE research_purchase_feedback (
 user_id UUID NOT NULL,curation_id UUID NOT NULL,
 version BIGINT NOT NULL DEFAULT 0 CHECK(version>=0),
 PRIMARY KEY(user_id,curation_id),
 FOREIGN KEY(user_id,curation_id) REFERENCES curations(user_id,id) ON DELETE CASCADE
);
CREATE TABLE research_external_purchase_records (
 user_id UUID NOT NULL,curation_id UUID NOT NULL,
 source TEXT NOT NULL CHECK(source='AMAZON'),marketplace TEXT NOT NULL CHECK(marketplace='US'),
 asin TEXT NOT NULL CHECK(asin ~ '^[A-Z0-9]{10}$'),
 candidate_id TEXT NOT NULL,checked BOOLEAN NOT NULL,version BIGINT NOT NULL CHECK(version>0),
 recorded_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(user_id,curation_id,source,marketplace,asin),
 FOREIGN KEY(user_id,curation_id) REFERENCES curations(user_id,id) ON DELETE CASCADE
);
CREATE TABLE research_purchase_commands (
 user_id UUID NOT NULL,curation_id UUID NOT NULL,idempotency_key TEXT NOT NULL,
 command_hash TEXT NOT NULL,result JSONB NOT NULL,created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(user_id,curation_id,idempotency_key),
 FOREIGN KEY(user_id,curation_id) REFERENCES curations(user_id,id) ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION phase8_guard_candidate_capacity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    candidate_count INTEGER;
BEGIN
    IF EXISTS (
        SELECT 1
        FROM phase8_research_candidates candidate
        WHERE candidate.user_id=NEW.user_id
          AND candidate.curation_id=NEW.curation_id
          AND candidate.plan_target_id=NEW.plan_target_id
          AND candidate.provider_product_id=NEW.provider_product_id
          AND candidate.source_kind=NEW.source_kind
    ) THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM phase8_research_pools pool
    WHERE pool.user_id=NEW.user_id
      AND pool.curation_id=NEW.curation_id
      AND pool.plan_target_id=NEW.plan_target_id
    FOR UPDATE;

    SELECT count(*)
    INTO candidate_count
    FROM phase8_research_candidates candidate
    WHERE candidate.user_id=NEW.user_id
      AND candidate.curation_id=NEW.curation_id
      AND candidate.plan_target_id=NEW.plan_target_id;

    IF candidate_count >= 50 THEN
        RAISE EXCEPTION 'TARGET_CANDIDATE_LIMIT_REACHED'
            USING
                ERRCODE='23514',
                CONSTRAINT='phase8_research_candidates_capacity_guard';
    END IF;
    RETURN NEW;
END;
$$;
