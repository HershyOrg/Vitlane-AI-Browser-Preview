-- Once Actions exist, restore a matching database backup or deploy a forward fix.
-- Do not erase execution history to make an older binary boot.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM curation_threads) OR EXISTS(SELECT 1 FROM intelligence_jobs WHERE target_kind='ACTION_INTERPRETATION') THEN
  RAISE EXCEPTION 'CURATION_ACTION_DOWN_REQUIRES_EMPTY_THREADS';
 END IF;
END $$;
DROP TRIGGER curation_thread_action_storage ON curation_threads;
DROP FUNCTION curation_store_thread_actions();
DROP TRIGGER intelligence_attempt_execution_owner ON intelligence_attempts;
DROP TRIGGER intelligence_job_execution_owner ON intelligence_jobs;
DROP FUNCTION intelligence_fix_execution_owner();
ALTER TABLE intelligence_attempts DROP COLUMN curation_action_id;
ALTER TABLE curation_thread_jobs DROP CONSTRAINT curation_thread_jobs_action_id_fkey;
DROP INDEX curation_thread_job_action;
ALTER TABLE curation_thread_jobs ADD COLUMN step_id uuid NOT NULL;
CREATE INDEX curation_thread_job_parent ON curation_thread_jobs(thread_id,step_id);
ALTER TABLE intelligence_jobs DROP CONSTRAINT intelligence_jobs_target_check;
ALTER TABLE intelligence_jobs DROP CONSTRAINT intelligence_jobs_target_kind_check;
ALTER TABLE intelligence_jobs DROP COLUMN interpretation_action_id, DROP COLUMN interpretation_revision, DROP COLUMN execution_action_id;
ALTER TABLE intelligence_jobs ADD CONSTRAINT intelligence_jobs_target_kind_check CHECK(target_kind IN('PLANNING_TASK','RESEARCH_ROUND'));
ALTER TABLE intelligence_jobs ADD CONSTRAINT intelligence_jobs_target_check CHECK(
 (target_kind='PLANNING_TASK' AND planning_task_id IS NOT NULL AND research_round_id IS NULL) OR
 (target_kind='RESEARCH_ROUND' AND research_round_id IS NOT NULL AND planning_task_id IS NULL));
ALTER TABLE curation_actions DROP COLUMN thread_id, DROP COLUMN sequence, DROP COLUMN status, DROP COLUMN execution;
ALTER TABLE curation_actions DROP CONSTRAINT curation_actions_action_type_check;
ALTER TABLE curation_actions ADD CONSTRAINT curation_actions_action_type_check CHECK(action_type IN
 ('INTENT_NEXT_STEP','PLANNING_ADD_TARGETS','PLANNING_START_CURATING','CURATION_ADD_TARGETS','TARGET_RESEARCH_AGAIN','TARGET_REMOVE','CANDIDATE_INTERACTION','SELECTION_MUTATION'));
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

CREATE OR REPLACE FUNCTION curation_conversation_retry() RETURNS trigger LANGUAGE plpgsql AS $$
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
