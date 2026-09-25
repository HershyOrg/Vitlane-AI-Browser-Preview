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

CREATE OR REPLACE FUNCTION curation_conversation_action() RETURNS trigger LANGUAGE plpgsql AS $$
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
DROP TABLE curation_thread_jobs;
DROP TABLE curation_threads;
DROP TABLE curation_control_modes;
