-- Once a reply Action exists, restore a matching database backup or deploy a
-- forward fix. Do not erase conversation history to make an older binary boot.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM curation_actions WHERE action_type='RESPONSE') THEN
  RAISE EXCEPTION 'CURATION_RESPONSE_DOWN_REQUIRES_NO_RESPONSE_ACTIONS';
 END IF;
END $$;
ALTER TABLE curation_actions DROP CONSTRAINT curation_actions_action_type_check;
ALTER TABLE curation_actions ADD CONSTRAINT curation_actions_action_type_check CHECK(action_type IN
 ('INTENT_NEXT_STEP','PLANNING_ADD_TARGETS','PLANNING_START_CURATING','CURATION_ADD_TARGETS','TARGET_RESEARCH_AGAIN','TARGET_REMOVE','CANDIDATE_INTERACTION','SELECTION_MUTATION','AUTO_START','BUDGET_CHANGE','CRITERIA_CHANGE','START_RESEARCH'));
