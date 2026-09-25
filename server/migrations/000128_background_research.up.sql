-- Shared passive feed, independent of foreground threads; PostgreSQL is the bus.
CREATE TABLE research_taxonomy (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton), version bigint NOT NULL DEFAULT 1,
 categories jsonb NOT NULL, revised_at timestamptz NOT NULL DEFAULT now(),
 review_after timestamptz NOT NULL DEFAULT now()+interval '1 day'
);
INSERT INTO research_taxonomy(categories) VALUES ('[{"id":"general","label":"General","definition":"Products that do not fit another category"},{"id":"electronics","label":"Electronics","definition":"Computers, electronics and electronic accessories"},{"id":"fashion","label":"Fashion","definition":"Clothes, bags, shoes and wearable accessories"},{"id":"beauty","label":"Beauty","definition":"Cosmetics and personal care"},{"id":"food","label":"Food","definition":"Food and beverages"},{"id":"living","label":"Living","definition":"Home, household and furniture"}]');
CREATE TABLE research_subscriptions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL,
 curation_id uuid NOT NULL, target_id uuid NOT NULL, proposal_id uuid NOT NULL UNIQUE REFERENCES curation_follow_ups(id) ON DELETE CASCADE,
 terms jsonb NOT NULL, terms_hash text NOT NULL,
 status text NOT NULL DEFAULT 'ACTIVE' CHECK(status IN ('ACTIVE','EXPIRED','CANCELLED','TARGET_REMOVED','ARCHIVED')),
 expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 taxonomy_version bigint NOT NULL DEFAULT 0, categories text[] NOT NULL DEFAULT '{}',
 FOREIGN KEY(user_id,target_id,curation_id) REFERENCES plan_targets(user_id,id,curation_id) ON DELETE CASCADE
);
CREATE INDEX research_subscriptions_route ON research_subscriptions USING gin(categories) WHERE status='ACTIVE';
CREATE INDEX research_subscriptions_active ON research_subscriptions(curation_id,expires_at) WHERE status='ACTIVE';
CREATE UNIQUE INDEX research_subscriptions_duplicate ON research_subscriptions(curation_id,target_id,terms_hash) WHERE status='ACTIVE';
CREATE TABLE research_feed_products (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), provider text NOT NULL, external_id text NOT NULL, identity text NOT NULL,
 product jsonb NOT NULL, country text NOT NULL, expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 taxonomy_version bigint NOT NULL DEFAULT 0, categories text[] NOT NULL DEFAULT '{}',
 routed_version bigint NOT NULL DEFAULT 0, route_after uuid,
 UNIQUE(provider,external_id)
);
CREATE INDEX research_feed_products_ready ON research_feed_products(taxonomy_version,created_at);
CREATE TABLE research_feed_leases (
 name text PRIMARY KEY, next_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO research_feed_leases(name) VALUES ('TELEGRAM_JIRUM'),('AMAZON'),('classify'),('route'),('taxonomy');
UPDATE research_feed_leases SET next_at=now()+interval '1 day' WHERE name='taxonomy';
CREATE TABLE research_match_jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), deal_id uuid NOT NULL REFERENCES research_feed_products(id) ON DELETE CASCADE,
 terms_hash text NOT NULL, terms jsonb NOT NULL,
 status text NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','RUNNING','MATCH','NO_MATCH','FAILED')),
 lease_until timestamptz, token uuid, attempts int NOT NULL DEFAULT 0, reason text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(deal_id,terms_hash)
);
CREATE INDEX research_match_jobs_pending ON research_match_jobs(status,lease_until);
CREATE TABLE research_match_recipients (
 job_id uuid NOT NULL REFERENCES research_match_jobs(id) ON DELETE CASCADE,
 subscription_id uuid NOT NULL REFERENCES research_subscriptions(id) ON DELETE CASCADE, PRIMARY KEY(job_id,subscription_id)
);
CREATE TABLE research_findings (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL, curation_id uuid NOT NULL,
 target_id uuid NOT NULL, subscription_id uuid NOT NULL REFERENCES research_subscriptions(id) ON DELETE CASCADE,
 identity text NOT NULL, product jsonb NOT NULL, reason text NOT NULL,
 status text NOT NULL DEFAULT 'NEW' CHECK(status IN ('NEW','HIDDEN','ADDED')),
 candidate_id text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(curation_id,identity),
 FOREIGN KEY(user_id,target_id,curation_id) REFERENCES plan_targets(user_id,id,curation_id) ON DELETE CASCADE
);
CREATE INDEX research_findings_curation ON research_findings(curation_id,created_at);
CREATE TABLE curation_product_notices (
 curation_id uuid PRIMARY KEY REFERENCES curations(id) ON DELETE CASCADE, new_products boolean NOT NULL DEFAULT false
);
CREATE TABLE curation_visible_leases (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, client_id uuid NOT NULL,
 curation_id uuid NOT NULL REFERENCES curations(id) ON DELETE CASCADE, visible_until timestamptz NOT NULL,
 PRIMARY KEY(user_id,client_id)
);
CREATE INDEX curation_visible_leases_curation ON curation_visible_leases(curation_id,visible_until);
-- All proposal kinds share one pending slot.
CREATE UNIQUE INDEX curation_follow_ups_one_pending_proposal ON curation_follow_ups(curation_id)
 WHERE kind='PROPOSAL' AND status='PENDING';
ALTER TABLE research_catalog_api_calls DROP CONSTRAINT research_catalog_api_calls_operation_check;
ALTER TABLE research_catalog_api_calls ADD CONSTRAINT research_catalog_api_calls_operation_check CHECK(operation IN ('SEARCH','DETAIL','USAGE','FEED'));
INSERT INTO research_catalog_source_control(source,enabled) VALUES ('TELEGRAM_JIRUM',true);
INSERT INTO research_catalog_api_quota(source) VALUES ('TELEGRAM_JIRUM');
-- NULL user means a shared server task, charged to SERVER only, never an arbitrary subscriber.
ALTER TABLE managed_runner_reservations ALTER COLUMN user_id DROP NOT NULL;
CREATE FUNCTION research_subscription_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.terms IS DISTINCT FROM OLD.terms OR NEW.expires_at<>OLD.expires_at OR NEW.terms_hash<>OLD.terms_hash OR
 NEW.user_id<>OLD.user_id OR NEW.curation_id<>OLD.curation_id OR NEW.target_id<>OLD.target_id OR NEW.proposal_id<>OLD.proposal_id THEN
 RAISE EXCEPTION 'RESEARCH_SUBSCRIPTION_IMMUTABLE'; END IF;
 IF OLD.status<>'ACTIVE' AND NEW.status<>OLD.status THEN RAISE EXCEPTION 'RESEARCH_SUBSCRIPTION_TERMINAL'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER research_subscription_immutable BEFORE UPDATE ON research_subscriptions FOR EACH ROW EXECUTE FUNCTION research_subscription_immutable();
CREATE FUNCTION curation_new_product_notice() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cid uuid;
BEGIN
 cid:=NEW.curation_id;
 -- Serialize entry/visible leases and product delivery on the same short lock.
 PERFORM 1 FROM curations WHERE id=cid FOR UPDATE;
 IF TG_TABLE_NAME='phase8_research_candidates' THEN
 IF EXISTS (SELECT 1 FROM research_findings WHERE curation_id=cid AND identity=NEW.identity_key) THEN RETURN NEW; END IF;
 END IF;
 INSERT INTO curation_product_notices(curation_id,new_products)
 VALUES(cid,NOT EXISTS(SELECT 1 FROM curation_visible_leases WHERE curation_id=cid AND visible_until>now()))
 ON CONFLICT(curation_id) DO UPDATE SET new_products=curation_product_notices.new_products OR EXCLUDED.new_products;
 RETURN NEW;
END $$;
CREATE TRIGGER research_finding_notice AFTER INSERT ON research_findings FOR EACH ROW EXECUTE FUNCTION curation_new_product_notice();
CREATE TRIGGER research_candidate_notice AFTER INSERT ON phase8_research_candidates FOR EACH ROW EXECUTE FUNCTION curation_new_product_notice();

-- A bounded finding evaluation occupies the same foreground admission budget.
-- Subscription delivery itself never occupies this budget.
CREATE FUNCTION research_finding_foreground_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE uid uuid; cid uuid; finding boolean; busy boolean; slots integer;
BEGIN
 uid:=NEW.user_id; cid:=NEW.curation_id;
 finding:=TG_TABLE_NAME='phase8_research_pool_commands';
 IF finding THEN
  IF NEW.idempotency_key NOT LIKE 'finding:%' THEN RETURN NEW; END IF;
 END IF;
 PERFORM 1 FROM curations WHERE id=cid FOR UPDATE;
 PERFORM pg_advisory_xact_lock(hashtextextended('intelligence.action-admission:'||uid::text,0));
 SELECT EXISTS(SELECT 1 FROM phase8_research_pool_commands WHERE curation_id=cid AND status='RUNNING' AND lease_expires_at>now() AND idempotency_key LIKE 'finding:%') INTO busy;
 IF busy THEN RAISE EXCEPTION 'CURATION_ACTION_IN_PROGRESS'; END IF;
 IF finding AND (EXISTS(SELECT 1 FROM curation_threads WHERE curation_id=cid AND status IN ('INTERPRETING','WAITING_SELECTION','RUNNING'))
 OR EXISTS(SELECT 1 FROM intelligence_jobs WHERE curation_id=cid AND status IN ('PENDING','RUNNING'))
 OR EXISTS(SELECT 1 FROM curation_conversation_requests WHERE curation_id=cid AND status='RESOLVING')) THEN
 RAISE EXCEPTION 'CURATION_ACTION_IN_PROGRESS'; END IF;
 SELECT count(*) INTO slots FROM (
 SELECT curation_id FROM curation_threads WHERE user_id=uid AND status IN ('INTERPRETING','WAITING_SELECTION','RUNNING')
 UNION SELECT curation_id FROM intelligence_jobs WHERE user_id=uid AND status IN ('PENDING','RUNNING')
 UNION SELECT curation_id FROM phase8_research_pool_commands WHERE user_id=uid AND status='RUNNING' AND lease_expires_at>now() AND idempotency_key LIKE 'finding:%'
 ) active;
 IF slots>=3 AND NOT EXISTS(SELECT 1 FROM curation_threads WHERE user_id=uid AND curation_id=cid AND status IN ('INTERPRETING','WAITING_SELECTION','RUNNING')) AND NOT EXISTS(SELECT 1 FROM intelligence_jobs WHERE user_id=uid AND curation_id=cid AND status IN ('PENDING','RUNNING')) THEN RAISE EXCEPTION 'TOO_MANY_ACTIVE_ACTIONS'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER research_finding_foreground_guard BEFORE INSERT ON phase8_research_pool_commands FOR EACH ROW EXECUTE FUNCTION research_finding_foreground_guard();
CREATE TRIGGER curation_thread_finding_guard BEFORE INSERT ON curation_threads FOR EACH ROW EXECUTE FUNCTION research_finding_foreground_guard();
CREATE TRIGGER intelligence_job_finding_guard BEFORE INSERT ON intelligence_jobs FOR EACH ROW EXECUTE FUNCTION research_finding_foreground_guard();
