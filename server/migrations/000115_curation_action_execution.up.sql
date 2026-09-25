-- Action is the durable command and receipt identity; Thread owns admission/order.
ALTER TABLE curation_actions DROP CONSTRAINT curation_actions_action_type_check;
ALTER TABLE curation_actions ADD CONSTRAINT curation_actions_action_type_check CHECK(action_type IN
 ('INTENT_NEXT_STEP','PLANNING_ADD_TARGETS','PLANNING_START_CURATING','CURATION_ADD_TARGETS','TARGET_RESEARCH_AGAIN','TARGET_REMOVE','CANDIDATE_INTERACTION','SELECTION_MUTATION','AUTO_START','BUDGET_CHANGE','CRITERIA_CHANGE','START_RESEARCH'));
ALTER TABLE curation_actions ADD COLUMN thread_id uuid REFERENCES curation_threads(id) DEFERRABLE INITIALLY DEFERRED,
 ADD COLUMN sequence integer, ADD COLUMN status text NOT NULL DEFAULT 'SUCCEEDED' CHECK(status IN('PENDING','RUNNING','WAITING_SELECTION','SUCCEEDED','FAILED','CANCELLED','SKIPPED')),
 ADD COLUMN execution jsonb NOT NULL DEFAULT '{}'::jsonb;
CREATE UNIQUE INDEX curation_action_thread_order ON curation_actions(thread_id,sequence) WHERE thread_id IS NOT NULL;
ALTER TABLE intelligence_jobs ADD COLUMN interpretation_action_id uuid REFERENCES curation_actions(id),
 ADD COLUMN interpretation_revision bigint,
 ADD COLUMN execution_action_id uuid REFERENCES curation_actions(id);
UPDATE intelligence_jobs SET execution_action_id=curation_action_id;
ALTER TABLE intelligence_jobs ALTER COLUMN execution_action_id SET NOT NULL;
ALTER TABLE intelligence_jobs DROP CONSTRAINT intelligence_jobs_target_kind_check;
ALTER TABLE intelligence_jobs ADD CONSTRAINT intelligence_jobs_target_kind_check CHECK(target_kind IN('PLANNING_TASK','RESEARCH_ROUND','ACTION_INTERPRETATION'));
ALTER TABLE intelligence_jobs DROP CONSTRAINT intelligence_jobs_target_check;
ALTER TABLE intelligence_jobs ADD CONSTRAINT intelligence_jobs_target_check CHECK(
 (target_kind='PLANNING_TASK')=(planning_task_id IS NOT NULL) AND
 (target_kind='RESEARCH_ROUND')=(research_round_id IS NOT NULL) AND
 (target_kind='ACTION_INTERPRETATION')=(interpretation_action_id IS NOT NULL) AND
 (target_kind<>'ACTION_INTERPRETATION' OR interpretation_revision>0));
CREATE UNIQUE INDEX intelligence_jobs_interpretation_target_key ON intelligence_jobs(interpretation_action_id,interpretation_revision) WHERE interpretation_action_id IS NOT NULL;
ALTER TABLE intelligence_attempts ADD COLUMN curation_action_id uuid REFERENCES curation_actions(id);
UPDATE intelligence_attempts a SET curation_action_id=j.curation_action_id FROM intelligence_jobs j WHERE j.id=a.job_id;
-- Preserve Job creation provenance while each Attempt fixes its execution owner.
CREATE FUNCTION intelligence_fix_execution_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='intelligence_jobs' THEN NEW.execution_action_id=COALESCE(NEW.execution_action_id,NEW.curation_action_id);
 ELSE SELECT execution_action_id INTO NEW.curation_action_id FROM intelligence_jobs WHERE id=NEW.job_id;
 END IF;RETURN NEW;
END $$;
CREATE TRIGGER intelligence_job_execution_owner BEFORE INSERT ON intelligence_jobs FOR EACH ROW EXECUTE FUNCTION intelligence_fix_execution_owner();
CREATE TRIGGER intelligence_attempt_execution_owner BEFORE INSERT ON intelligence_attempts FOR EACH ROW EXECUTE FUNCTION intelligence_fix_execution_owner();

-- Storage normalization only: the app decides transitions and ordered commands.
CREATE FUNCTION curation_store_thread_actions() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE a jsonb; old curation_actions; n integer:=0; typ text; phase text; target text; body jsonb; sid text; effect text;
BEGIN
 PERFORM set_config('vitlane.action_storage','true',true);
 FOR a IN SELECT value FROM jsonb_array_elements(COALESCE(NEW.data->'actions','[]'::jsonb)) LOOP
  typ=a->>'type';target=NULLIF(a->>'targetId','');
  SELECT * INTO old FROM curation_actions WHERE id=(a->>'id')::uuid FOR UPDATE;
  IF FOUND AND old.thread_id IS NOT NULL AND (old.thread_id<>NEW.id OR old.sequence<>n) THEN RAISE EXCEPTION 'CURATION_ACTION_IDENTITY_CHANGED';END IF;
  IF FOUND AND old.execution<>'{}'::jsonb THEN
   IF old.status<>a->>'status' AND NOT (
    old.status='PENDING' AND a->>'status' IN('RUNNING','SUCCEEDED','FAILED','CANCELLED','SKIPPED') OR
    old.status='RUNNING' AND a->>'status' IN('SUCCEEDED','FAILED','CANCELLED','WAITING_SELECTION') OR
    old.status='WAITING_SELECTION' AND a->>'status' IN('PENDING','SUCCEEDED','CANCELLED')) THEN RAISE EXCEPTION 'CURATION_ACTION_TRANSITION_INVALID';END IF;
   IF old.execution->'type' IS DISTINCT FROM a->'type' OR old.execution->'targetId' IS DISTINCT FROM a->'targetId' OR old.execution->'instruction' IS DISTINCT FROM a->'instruction' OR old.execution->'budget' IS DISTINCT FROM a->'budget' OR old.execution->'criteria' IS DISTINCT FROM a->'criteria' THEN RAISE EXCEPTION 'CURATION_ACTION_COMMAND_IMMUTABLE';END IF;
   IF old.status IN('SUCCEEDED','FAILED','CANCELLED','SKIPPED') THEN
    IF old.status<>a->>'status' THEN RAISE EXCEPTION 'CURATION_ACTION_RESULT_IMMUTABLE';END IF;
    -- Final receipts are frozen. Re-reading mutable Jobs must never rewrite them.
    n=n+1;CONTINUE;
   END IF;
  END IF;
  SELECT c.phase INTO phase FROM curations c WHERE c.id=NEW.curation_id;
  sid=CASE WHEN typ IN('CRITERIA_CHANGE','TARGET_RESEARCH_AGAIN','TARGET_REMOVE') THEN target ELSE NEW.curation_id::text END;effect=CASE WHEN typ IN('BUDGET_CHANGE','CRITERIA_CHANGE','TARGET_REMOVE','SELECTION_MUTATION') THEN 'NONE' ELSE 'INTELLIGENCE' END;
  IF typ='INTENT_NEXT_STEP' THEN phase='HAVING_INTENT';END IF;
  a=a||jsonb_build_object('threadId',NEW.id,'sequence',n,'curationId',NEW.curation_id,'actorUserId',NEW.user_id);
  INSERT INTO curation_actions(id,curation_id,actor_user_id,action_type,phase_at_request,subject_type,subject_id,effect_kind,source_ref_type,source_ref_id,expected_curation_version,request_hash,created_at,requested_transition_to)
  VALUES((a->>'id')::uuid,NEW.curation_id,NEW.user_id,typ,phase,CASE WHEN typ='INTENT_NEXT_STEP' THEN 'INTENT' WHEN typ='PLANNING_ADD_TARGETS' THEN 'TARGET_LIST' WHEN typ IN('CRITERIA_CHANGE','TARGET_RESEARCH_AGAIN','TARGET_REMOVE') THEN 'TARGET' ELSE 'CURATION' END,CASE WHEN typ='INTENT_NEXT_STEP' THEN NULL ELSE sid END,effect,'CURATION_THREAD',NEW.id::text,GREATEST(COALESCE((NEW.data->>'expectedCurationVersion')::bigint,1),1),decode(md5(a::text)||md5(a::text),'hex'),NEW.created_at,CASE WHEN typ='INTENT_NEXT_STEP' THEN 'PLANNING' WHEN typ='PLANNING_START_CURATING' THEN 'CURATING' ELSE NULL END)
  ON CONFLICT(id) DO NOTHING;
  UPDATE curation_actions SET thread_id=NEW.id,sequence=n,status=a->>'status',execution=a WHERE id=(a->>'id')::uuid;
  n=n+1;
 END LOOP;
 NEW.data=NEW.data-'actions';
 PERFORM set_config('vitlane.action_storage','false',true);
 RETURN NEW;
END $$;
CREATE TRIGGER curation_thread_action_storage BEFORE INSERT OR UPDATE ON curation_threads FOR EACH ROW EXECUTE FUNCTION curation_store_thread_actions();

CREATE OR REPLACE FUNCTION curation_conversation_action() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('vitlane.action_storage',true)='true' THEN RETURN NEW; END IF;
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

-- Recursively translate stored choices and decision references too. No provider call.
CREATE FUNCTION curation_migrate_action_json(v jsonb) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE k text; x jsonb; out jsonb; typ text;
BEGIN
 IF v IS NULL THEN RETURN NULL; END IF;
 IF jsonb_typeof(v)='array' THEN
  out='[]'::jsonb;
  FOR x IN SELECT value FROM jsonb_array_elements(v) LOOP out=out||jsonb_build_array(curation_migrate_action_json(x));END LOOP;
  RETURN out;
 ELSIF jsonb_typeof(v)<>'object' THEN RETURN v; END IF;
 out='{}'::jsonb;
 FOR k,x IN SELECT * FROM jsonb_each(v) LOOP
  out=out||jsonb_build_object(CASE k WHEN 'steps' THEN 'actions' WHEN 'stepIds' THEN 'actionIds' ELSE k END,curation_migrate_action_json(x));
 END LOOP;
 IF out->>'kind' IN('BUDGET','CRITERIA','ADD_TARGET','RESEARCH_AGAIN','PLANNING','START_RESEARCH') THEN
  typ=CASE out->>'kind' WHEN 'BUDGET' THEN 'BUDGET_CHANGE' WHEN 'CRITERIA' THEN 'CRITERIA_CHANGE' WHEN 'ADD_TARGET' THEN 'CURATION_ADD_TARGETS' WHEN 'RESEARCH_AGAIN' THEN 'TARGET_RESEARCH_AGAIN' WHEN 'PLANNING' THEN 'INTENT_NEXT_STEP' ELSE out->>'kind' END;
  out=(out-'kind'-'actionId')||jsonb_build_object('type',typ);
 END IF;
 RETURN out;
END $$;

-- Migrate prior Step receipts and pending commands without re-executing work.
DO $$
DECLARE t curation_threads; a jsonb; actions jsonb; typ text; autoid uuid; decisions jsonb;
BEGIN
 FOR t IN SELECT * FROM curation_threads ORDER BY created_at LOOP
  actions='[]'::jsonb;decisions=curation_migrate_action_json(COALESCE(t.data->'decisions','[]'::jsonb));
  IF t.data->>'origin'='REQUEST' THEN
   autoid=md5(t.id::text||':auto-start')::uuid;
   actions=actions||jsonb_build_array(jsonb_build_object('id',autoid,'type','AUTO_START','instruction',t.data->>'request','status',CASE WHEN t.status='INTERPRETING' AND t.data->>'interpretationStartedAt' IS NOT NULL THEN 'FAILED' WHEN t.status='INTERPRETING' THEN 'PENDING' WHEN t.status='WAITING_SELECTION' THEN 'WAITING_SELECTION' WHEN t.status='CANCELLED' THEN 'CANCELLED' WHEN t.status='FAILED' AND jsonb_array_length(COALESCE(t.data->'steps','[]'::jsonb))=0 THEN 'FAILED' ELSE 'SUCCEEDED' END,'reasonCode',CASE WHEN t.status='INTERPRETING' AND t.data->>'interpretationStartedAt' IS NOT NULL THEN 'AUTO_INTERPRETATION_INTERRUPTED' ELSE '' END,'inputRevision',GREATEST(t.revision,1),'decisions',decisions,'question',curation_migrate_action_json(t.data->'question'),'questions',curation_migrate_action_json(COALESCE(t.data->'questions','[]'::jsonb)),'answers',COALESCE(t.data->'answers','[]'::jsonb),'jobs','[]'::jsonb,'effects','[]'::jsonb));
  END IF;
  FOR a IN SELECT value FROM jsonb_array_elements(COALESCE(t.data->'steps','[]'::jsonb)) LOOP
   typ=CASE a->>'kind' WHEN 'BUDGET' THEN 'BUDGET_CHANGE' WHEN 'CRITERIA' THEN 'CRITERIA_CHANGE' WHEN 'ADD_TARGET' THEN 'CURATION_ADD_TARGETS' WHEN 'RESEARCH_AGAIN' THEN 'TARGET_RESEARCH_AGAIN' WHEN 'PLANNING' THEN 'INTENT_NEXT_STEP' ELSE a->>'kind' END;
   a=curation_migrate_action_json(a)||jsonb_build_object('type',typ,'decisions',CASE WHEN t.data->>'origin'='REQUEST' THEN '[]'::jsonb ELSE decisions END);
   actions=actions||jsonb_build_array(a);
  END LOOP;
  IF t.status='INTERPRETING' AND t.data->>'interpretationStartedAt' IS NOT NULL THEN
   UPDATE curation_threads SET status='FAILED' WHERE id=t.id;
   UPDATE curation_conversation_requests SET status='COMPLETE',response_ready=true WHERE id=t.id;
  END IF;
  UPDATE curation_threads SET data=(data-'steps'-'decisions'-'answers'-'questions'-'question'-'interpretationStartedAt')||jsonb_build_object('schemaVersion','vitlane.curation-thread.v2','actions',actions) WHERE id=t.id;
 END LOOP;
END $$;
DROP FUNCTION curation_migrate_action_json(jsonb);
UPDATE curation_thread_jobs SET action_id=step_id;
UPDATE intelligence_jobs j SET execution_action_id=l.action_id FROM curation_thread_jobs l WHERE l.job_id=j.id;
-- Keep the legacy column only through migration; runtime joins exclusively by Action.
ALTER TABLE curation_thread_jobs DROP COLUMN step_id;
ALTER TABLE curation_thread_jobs ADD FOREIGN KEY(action_id) REFERENCES curation_actions(id);
CREATE INDEX curation_thread_job_action ON curation_thread_jobs(thread_id,action_id);


CREATE OR REPLACE FUNCTION curation_conversation_retry() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rid uuid;
BEGIN
 IF current_setting('vitlane.automatic_retry',true)=NEW.id::text THEN RETURN NEW; END IF;
 IF OLD.status='FAILED' AND NEW.status='PENDING' THEN
  rid:=COALESCE(NULLIF(current_setting('vitlane.conversation_request',true),'')::uuid,md5(NEW.id::text || ':retry:' || NEW.attempt_count::text)::uuid);
  IF NOT EXISTS(SELECT 1 FROM curation_conversation_requests WHERE id=rid AND user_id=NEW.user_id AND curation_id=NEW.curation_id AND mode='RETRY' AND status='RESOLVING') THEN
   PERFORM curation_admit_request(NEW.user_id,NEW.curation_id,rid,'RETRY','',rid::text,'RESOLVING');
  END IF;
  UPDATE curation_conversation_requests SET status='DISPATCHED',action_id=NEW.curation_action_id WHERE id=rid;
 END IF;
 RETURN NEW;
END $$;
