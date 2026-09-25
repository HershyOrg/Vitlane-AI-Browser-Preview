-- A Thread's last Action may write its natural-language reply: the comment
-- after a research that added candidates, or the answer to a question
-- (ADR-0086). The reply lives in the Action's execution document, so only the
-- closed action type set changes here.
ALTER TABLE curation_actions DROP CONSTRAINT curation_actions_action_type_check;
ALTER TABLE curation_actions ADD CONSTRAINT curation_actions_action_type_check CHECK(action_type IN
 ('INTENT_NEXT_STEP','PLANNING_ADD_TARGETS','PLANNING_START_CURATING','CURATION_ADD_TARGETS','TARGET_RESEARCH_AGAIN','TARGET_REMOVE','CANDIDATE_INTERACTION','SELECTION_MUTATION','AUTO_START','BUDGET_CHANGE','CRITERIA_CHANGE','START_RESEARCH','RESPONSE'));
