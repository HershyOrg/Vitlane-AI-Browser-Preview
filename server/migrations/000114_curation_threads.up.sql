-- One control mode per curation. Existing amounts and immutable inputs are retained.
CREATE TABLE curation_control_modes (
 curation_id uuid PRIMARY KEY REFERENCES curations(id),
 mode text NOT NULL CHECK (mode IN ('AUTO','MANUAL')),
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0)
);
INSERT INTO curation_control_modes(curation_id,mode) SELECT id,'AUTO' FROM curations;
CREATE TABLE curation_threads (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id),
 curation_id uuid NOT NULL REFERENCES curations(id),
 plan_id uuid NOT NULL REFERENCES shopping_plans(id),
 status text NOT NULL CHECK (status IN ('INTERPRETING','WAITING_SELECTION','RUNNING','SUCCEEDED','FAILED','CANCELLED')),
 request_hash text NOT NULL,
 revision bigint NOT NULL DEFAULT 1,
 data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX curation_one_active_thread ON curation_threads(curation_id)
 WHERE status IN ('INTERPRETING','WAITING_SELECTION','RUNNING');
CREATE INDEX curation_thread_owner ON curation_threads(user_id,curation_id,created_at DESC);
CREATE INDEX curation_thread_pending ON curation_threads(updated_at)
 WHERE status IN ('INTERPRETING','RUNNING');
CREATE TABLE curation_thread_jobs (
 job_id uuid PRIMARY KEY REFERENCES intelligence_jobs(id),
 thread_id uuid NOT NULL REFERENCES curation_threads(id),
 step_id uuid NOT NULL,
 action_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX curation_thread_job_parent ON curation_thread_jobs(thread_id,step_id);

-- Group legacy conversation reporting and follow-up generation by request thread.
CREATE OR REPLACE FUNCTION curation_admit_request(uid uuid, cid uuid, rid uuid, input_mode text,
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
  IF EXISTS(SELECT 1 FROM curation_threads WHERE curation_id=cid AND status IN ('INTERPRETING','WAITING_SELECTION','RUNNING')) THEN RAISE EXCEPTION 'CURATION_THREAD_ACTIVE'; END IF;
  IF EXISTS(SELECT 1 FROM intelligence_jobs WHERE curation_id=cid AND status IN ('PENDING','RUNNING')) THEN
   RAISE EXCEPTION 'CURATION_ACTION_IN_PROGRESS';
  END IF;
  IF (SELECT count(*) FROM (
    SELECT id FROM curation_threads WHERE user_id=uid AND status IN ('INTERPRETING','WAITING_SELECTION','RUNNING')
    UNION ALL SELECT j.curation_action_id FROM intelligence_jobs j LEFT JOIN curation_thread_jobs l ON l.job_id=j.id WHERE j.user_id=uid AND j.status IN ('PENDING','RUNNING') AND l.job_id IS NULL GROUP BY j.curation_action_id
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

CREATE OR REPLACE FUNCTION curation_conversation_action() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM curation_threads WHERE id::text=COALESCE(current_setting('vitlane.conversation_request',true),'') AND curation_id=NEW.curation_id) THEN RETURN NEW; END IF;
 IF NEW.action_type NOT IN ('INTENT_NEXT_STEP','PLANNING_ADD_TARGETS','CURATION_ADD_TARGETS','TARGET_RESEARCH_AGAIN','PLANNING_START_CURATING') THEN RETURN NEW; END IF;
 IF NEW.action_type='PLANNING_START_CURATING' AND EXISTS(SELECT 1 FROM intelligence_jobs WHERE curation_id=NEW.curation_id AND status IN ('PENDING','RUNNING')) THEN RETURN NEW; END IF;
 IF EXISTS(SELECT 1 FROM curation_conversation_requests WHERE id=NEW.id AND user_id=NEW.actor_user_id AND curation_id=NEW.curation_id) THEN
  UPDATE curation_conversation_requests SET status='DISPATCHED',action_id=NEW.id WHERE id=NEW.id;
 ELSE
  PERFORM curation_admit_request(NEW.actor_user_id,NEW.curation_id,NEW.id,'MANUAL','',encode(NEW.request_hash,'hex'),'DISPATCHED');
 END IF;
 RETURN NEW;
END $$;
