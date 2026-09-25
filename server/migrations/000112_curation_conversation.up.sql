-- Durable Curation conversation. Research/Planning remain the execution owners.
CREATE TABLE curation_conversations (
 curation_id uuid PRIMARY KEY REFERENCES curations(id) ON DELETE CASCADE,
 version bigint NOT NULL DEFAULT 0,
 latest_request_id uuid
);
CREATE TABLE curation_conversation_requests (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 curation_id uuid NOT NULL REFERENCES curations(id) ON DELETE CASCADE,
 mode text NOT NULL,
 body text NOT NULL DEFAULT '',
 content_locale text NOT NULL DEFAULT 'ko-KR' CHECK(content_locale IN ('ko-KR','en-US')),
 request_hash text NOT NULL,
 status text NOT NULL CHECK(status IN ('RESOLVING','DISPATCHED','COMPLETE')),
 action_id uuid,
 result_code text NOT NULL DEFAULT '',
 response_ready boolean NOT NULL DEFAULT false,
 terminal_jobs jsonb,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX curation_conversation_requests_pending ON curation_conversation_requests(curation_id) WHERE NOT response_ready;
CREATE TABLE curation_follow_ups (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 curation_id uuid NOT NULL REFERENCES curations(id) ON DELETE CASCADE,
 response_id uuid NOT NULL REFERENCES curation_conversation_requests(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK(kind IN ('RESULT','PROPOSAL','ERROR','CLARIFICATION','NOTICE')),
 status text NOT NULL CHECK(status IN ('PENDING','ACCEPTED','DISMISSED','ACKNOWLEDGED','SUPERSEDED')),
 version bigint NOT NULL DEFAULT 1,
 content jsonb NOT NULL,
 payload jsonb,
 fingerprint text NOT NULL DEFAULT '',
 response_request_id uuid,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX curation_follow_ups_one_proposal ON curation_follow_ups(response_id) WHERE kind='PROPOSAL';
CREATE INDEX curation_follow_ups_pending ON curation_follow_ups(curation_id) WHERE status='PENDING';
ALTER TABLE curation_auto_resolutions ADD COLUMN request_text text NOT NULL DEFAULT '';
ALTER TABLE curation_auto_resolutions ADD COLUMN expected_conversation_version bigint;

-- All entry points use the same Curation lock and same per-user admission lock.
CREATE FUNCTION curation_admit_request(uid uuid, cid uuid, rid uuid, input_mode text,
 input_body text, input_hash text, input_status text, input_expected_version bigint DEFAULT NULL) RETURNS void LANGUAGE plpgsql AS $$
DECLARE existing curation_conversation_requests; own_version bigint;
BEGIN
 PERFORM 1 FROM curations WHERE id=cid AND user_id=uid AND archived_at IS NULL FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'CURATION_NOT_FOUND'; END IF;
 SELECT * INTO existing FROM curation_conversation_requests WHERE id=rid;
 IF FOUND THEN
  IF existing.user_id<>uid OR existing.curation_id<>cid OR existing.request_hash<>input_hash THEN
   RAISE EXCEPTION 'CONVERSATION_IDEMPOTENCY_CONFLICT';
  END IF;
  RETURN;
 END IF;
 IF input_expected_version IS NOT NULL AND input_expected_version<>COALESCE((SELECT version FROM curation_conversations WHERE curation_id=cid),0) THEN RAISE EXCEPTION 'CONVERSATION_VERSION_CONFLICT'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('intelligence.action-admission:' || uid::text,0));
 IF EXISTS(SELECT 1 FROM curation_conversation_requests WHERE curation_id=cid AND status='RESOLVING') THEN
  RAISE EXCEPTION 'CURATION_ACTION_IN_PROGRESS';
 END IF;
 IF input_status='RESOLVING' THEN
  IF EXISTS(SELECT 1 FROM intelligence_jobs WHERE curation_id=cid AND status IN ('PENDING','RUNNING')) THEN
   RAISE EXCEPTION 'CURATION_ACTION_IN_PROGRESS';
  END IF;
  IF (SELECT count(*) FROM (
    SELECT curation_action_id FROM intelligence_jobs WHERE user_id=uid AND status IN ('PENDING','RUNNING') GROUP BY curation_action_id
    UNION ALL SELECT id FROM curation_conversation_requests WHERE user_id=uid AND status='RESOLVING'
   ) slots)>=3 THEN RAISE EXCEPTION 'TOO_MANY_ACTIVE_ACTIONS'; END IF;
 END IF;
 -- Freeze terminal job facts before a retry changes the same Job row.
 UPDATE curation_conversation_requests cr SET terminal_jobs=(
  SELECT COALESCE(jsonb_agg(jsonb_build_object('id',j.id,'status',j.status,'reason',COALESCE(j.failure_code,''),'retryable',j.retryable,'title',COALESCE(t.title,''),'round',j.research_round_id)),'[]'::jsonb)
  FROM intelligence_jobs j LEFT JOIN research_rounds rr ON rr.id=j.research_round_id LEFT JOIN shopping_sessions ss ON ss.id=rr.shopping_session_id LEFT JOIN plan_targets t ON t.id=ss.plan_target_id
  WHERE j.curation_id=cid AND (j.created_at>=cr.created_at OR (j.curation_action_id=cr.action_id AND j.updated_at>=cr.created_at))
 ) WHERE cr.id=(SELECT latest_request_id FROM curation_conversations WHERE curation_id=cid) AND cr.terminal_jobs IS NULL AND NOT cr.response_ready;
 INSERT INTO curation_conversation_requests(id,user_id,curation_id,mode,body,request_hash,status,action_id,content_locale)
 VALUES(rid,uid,cid,input_mode,input_body,input_hash,input_status,CASE WHEN input_status='DISPATCHED' THEN rid ELSE NULL END,COALESCE((SELECT ui_locale FROM account_user_preferences WHERE user_id=uid),'ko-KR'));
 INSERT INTO curation_conversations(curation_id,version,latest_request_id) VALUES(cid,1,rid)
 ON CONFLICT(curation_id) DO UPDATE SET version=curation_conversations.version+1,latest_request_id=rid;
 UPDATE curation_follow_ups SET status='SUPERSEDED',version=version+1 WHERE curation_id=cid AND status='PENDING';
END $$;

-- Exact actions are recorded in their owner's transaction. Server continuation
-- keeps the original request; a replay never closes a newer message.
CREATE FUNCTION curation_conversation_action() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.action_type NOT IN ('INTENT_NEXT_STEP','PLANNING_ADD_TARGETS','CURATION_ADD_TARGETS','TARGET_RESEARCH_AGAIN','PLANNING_START_CURATING') THEN RETURN NEW; END IF;
 IF NEW.action_type='PLANNING_START_CURATING' AND EXISTS(SELECT 1 FROM intelligence_jobs WHERE curation_id=NEW.curation_id AND status IN ('PENDING','RUNNING')) THEN RETURN NEW; END IF;
 IF EXISTS(SELECT 1 FROM curation_conversation_requests WHERE id=NEW.id AND user_id=NEW.actor_user_id AND curation_id=NEW.curation_id) THEN
  UPDATE curation_conversation_requests SET status='DISPATCHED',action_id=NEW.id WHERE id=NEW.id;
 ELSE
  PERFORM curation_admit_request(NEW.actor_user_id,NEW.curation_id,NEW.id,'MANUAL','',encode(NEW.request_hash,'hex'),'DISPATCHED');
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER curation_conversation_action AFTER INSERT ON curation_actions FOR EACH ROW EXECUTE FUNCTION curation_conversation_action();

CREATE FUNCTION curation_conversation_auto() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM curation_admit_request(NEW.user_id,NEW.curation_id,NEW.id,'AUTO',NEW.request_text,NEW.request_hash,'RESOLVING',NEW.expected_conversation_version);
  IF NOT EXISTS(SELECT 1 FROM curations WHERE id=NEW.curation_id AND version=NEW.expected_curation_version) THEN RAISE EXCEPTION 'VERSION_CONFLICT'; END IF;
 ELSIF NEW.status='NEEDS_SELECTION' THEN
  UPDATE curation_conversation_requests SET status='COMPLETE',result_code=NEW.reason_code WHERE id=NEW.id;
 ELSIF NEW.status='EXECUTED' THEN
  UPDATE curation_conversation_requests SET status='DISPATCHED',action_id=NEW.id WHERE id=NEW.id;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER curation_conversation_auto AFTER INSERT OR UPDATE OF status ON curation_auto_resolutions FOR EACH ROW EXECUTE FUNCTION curation_conversation_auto();

-- Context fingerprint excludes view currency, visibility, sorting and locale.
CREATE FUNCTION curation_follow_up_context(cid uuid, tid uuid) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT md5(jsonb_build_object('country',c.research_country,'criteria',tc.criteria,
 'removed',t.removed_at,'budget',jsonb_build_object('enabled',b.enabled,'currency',b.currency,'allocation',(SELECT value FROM jsonb_array_elements(b.allocations) value WHERE value->>'targetId'=tid::text)))::text)
 FROM curations c JOIN plan_targets t ON t.curation_id=c.id AND t.id=tid
 LEFT JOIN curation_target_criteria tc ON tc.curation_id=c.id AND tc.target_id=t.id
 LEFT JOIN curation_budgets b ON b.curation_id=c.id WHERE c.id=cid
$$;

-- No old completed response gets a new proposal. Active pipelines and failed
-- jobs remain recoverable after rollout, grouped once per Curation.
INSERT INTO curation_conversation_requests(id,user_id,curation_id,mode,request_hash,status,action_id,created_at)
 SELECT DISTINCT ON (curation_id) id,user_id,curation_id,'RECOVERED',id::text,'DISPATCHED',curation_action_id,created_at
 FROM intelligence_jobs WHERE status IN ('PENDING','RUNNING','FAILED') ORDER BY curation_id,created_at DESC;
INSERT INTO curation_conversations(curation_id,version,latest_request_id)
 SELECT curation_id,1,id FROM curation_conversation_requests;
ALTER TABLE curation_conversation_requests ADD COLUMN generating boolean NOT NULL DEFAULT false;
ALTER TABLE curation_conversation_requests ADD COLUMN generation_started_at timestamptz;
CREATE FUNCTION curation_conversation_retry() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rid uuid;
BEGIN
 IF OLD.status='FAILED' AND NEW.status='PENDING' THEN
  rid:=COALESCE(NULLIF(current_setting('vitlane.conversation_request',true),'')::uuid,md5(NEW.id::text || ':retry:' || NEW.attempt_count::text)::uuid);
  IF NOT EXISTS(SELECT 1 FROM curation_conversation_requests WHERE id=rid AND user_id=NEW.user_id AND curation_id=NEW.curation_id AND mode='RETRY' AND status='RESOLVING') THEN
   PERFORM curation_admit_request(NEW.user_id,NEW.curation_id,rid,'RETRY','',rid::text,'RESOLVING');
  END IF;
  UPDATE curation_conversation_requests SET status='DISPATCHED',action_id=NEW.curation_action_id WHERE id=rid;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER curation_conversation_retry BEFORE UPDATE OF status ON intelligence_jobs FOR EACH ROW EXECUTE FUNCTION curation_conversation_retry();
CREATE FUNCTION curation_invalidate_follow_ups() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cid uuid;
BEGIN
 IF TG_TABLE_NAME='curations' THEN cid:=NEW.id; ELSE cid:=NEW.curation_id; END IF;
 UPDATE curation_follow_ups SET status='SUPERSEDED',version=version+1
 WHERE curation_id=cid AND kind='PROPOSAL' AND status='PENDING'
 AND fingerprint IS DISTINCT FROM curation_follow_up_context(cid,(payload->>'targetId')::uuid);
 RETURN NEW;
END $$;
CREATE TRIGGER curation_follow_up_criteria AFTER UPDATE ON curation_target_criteria FOR EACH ROW EXECUTE FUNCTION curation_invalidate_follow_ups();
CREATE TRIGGER curation_follow_up_country AFTER UPDATE OF research_country ON curations FOR EACH ROW EXECUTE FUNCTION curation_invalidate_follow_ups();
CREATE TRIGGER curation_follow_up_budget AFTER UPDATE ON curation_budgets FOR EACH ROW EXECUTE FUNCTION curation_invalidate_follow_ups();
CREATE TRIGGER curation_follow_up_target AFTER UPDATE OF removed_at ON plan_targets FOR EACH ROW EXECUTE FUNCTION curation_invalidate_follow_ups();

CREATE UNIQUE INDEX curation_follow_up_response_key ON curation_follow_ups(response_request_id) WHERE response_request_id IS NOT NULL;
