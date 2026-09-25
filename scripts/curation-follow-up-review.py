#!/usr/bin/env python3
"""Create a fresh, actionable proposal in the dedicated local Step 5 fixture DB.

This is synthetic message setup, not evidence of model proposal quality. Existing
candidate assessments and previously answered messages are never rewritten.
"""
import argparse
import json
import re
import subprocess
from uuid import UUID, uuid4

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--database", required=True)
parser.add_argument("--curation-id", required=True)
parser.add_argument("--synthetic", action="store_true", required=True)
args = parser.parse_args()
if not re.fullmatch(r"vitlane_curation_step5_[a-z]+", args.database):
    parser.error("Only a dedicated local Step 5 fixture database is allowed")
cid = str(UUID(args.curation_id))
rid, mid = str(uuid4()), str(uuid4())
uid = "e5000000-0000-4000-8000-000000000101"

def sql(query):
    return subprocess.check_output(
        ["docker", "exec", "-i", "vitlane-curation-step4-review-pg", "psql",
         "-U", "vitlane", "-d", args.database, "-At", "-v", "ON_ERROR_STOP=1"],
        input=query, text=True,
    ).strip()

sql(f"""
BEGIN;
DO $$
BEGIN
 PERFORM 1 FROM curations WHERE id='{cid}' AND user_id='{uid}' AND archived_at IS NULL FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'Fixture Curation not found'; END IF;
 IF EXISTS(SELECT 1 FROM intelligence_jobs WHERE curation_id='{cid}' AND status IN ('PENDING','RUNNING'))
 OR EXISTS(SELECT 1 FROM curation_conversation_requests WHERE curation_id='{cid}' AND (NOT response_ready OR generating))
 THEN RAISE EXCEPTION 'Wait for this fixture Curation to finish'; END IF;
 IF NOT EXISTS(SELECT 1 FROM plan_targets t JOIN shopping_sessions s ON s.plan_target_id=t.id
 JOIN curation_target_criteria c ON c.target_id=t.id WHERE t.curation_id='{cid}' AND t.removed_at IS NULL)
 THEN RAISE EXCEPTION 'Researchable fixture target required'; END IF;
END $$;
INSERT INTO curation_conversation_requests(id,user_id,curation_id,mode,body,content_locale,request_hash,status,response_ready)
VALUES('{rid}','{uid}','{cid}','REVIEW_FIXTURE','후속 제의 버튼 검수용 예시','ko-KR','{rid}','COMPLETE',true);
INSERT INTO curation_conversations(curation_id,version,latest_request_id) VALUES('{cid}',1,'{rid}')
ON CONFLICT(curation_id) DO UPDATE SET version=curation_conversations.version+1,latest_request_id=EXCLUDED.latest_request_id;
UPDATE curation_follow_ups SET status='SUPERSEDED',version=version+1 WHERE curation_id='{cid}' AND status='PENDING';
INSERT INTO curation_follow_ups(id,user_id,curation_id,response_id,kind,status,content,payload,fingerprint)
SELECT '{mid}','{uid}','{cid}','{rid}','PROPOSAL','PENDING',
 jsonb_build_object('code','REVIEW_RESEARCH_AGAIN','body',
 '현재 기준으로 ' || COALESCE(c.criteria->'subject'->>'label',t.title) || ' 후보를 다시 조사해 볼까요?',
 'locale','ko-KR','targetTitle',t.title),
 jsonb_build_object('kind','RESEARCH_AGAIN','targetId',t.id,'sessionId',s.id,'sessionVersion',s.version,
 'criteriaVersion',c.version,'feedback',''),curation_follow_up_context('{cid}',t.id)
FROM plan_targets t JOIN shopping_sessions s ON s.plan_target_id=t.id
JOIN curation_target_criteria c ON c.target_id=t.id
WHERE t.curation_id='{cid}' AND t.removed_at IS NULL ORDER BY t.order_index LIMIT 1;
COMMIT;
""")
print(json.dumps({"curationId": cid, "messageId": mid, "status": "PENDING", "synthetic": True}))
