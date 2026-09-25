#!/usr/bin/env python3
"""Development UI fixture only: add ordinary research beside existing live Step 7 findings.

Fixed local container/database/curation; no provider calls, no cost records, no reset.
The four sample catalog products and the completed reply are synthetic. Existing
Telegram findings, their real assessments and accepted subscriptions are preserved.
"""
import copy
import json
import subprocess
from datetime import datetime, timedelta

CID = "e5700000-0000-4000-8000-000000000002"
UID = "e5000000-0000-4000-8000-000000000103"
PLAN = "e5700000-0000-4000-8000-000000000001"
PSQL = ["docker", "exec", "-i", "vitlane-local-review-postgres", "psql", "-v", "ON_ERROR_STOP=1", "-U", "vitlane", "-d", "vitlane_curation_step7", "-At"]

def query(sql):
    return subprocess.check_output(PSQL, input=sql, text=True).strip()

def literal(value):
    return "'" + str(value).replace("'", "''") + "'"

def js(value):
    return literal(json.dumps(value, ensure_ascii=False)) + "::jsonb"

state = json.loads(query(f"SELECT json_build_object('owner',user_id,'createdAt',created_at) FROM curations WHERE id='{CID}'"))
assert state["owner"] == UID, "Dedicated Step 7 fixture owner required"
criteria = json.loads(query(f"SELECT criteria FROM curation_target_criteria WHERE curation_id='{CID}' ORDER BY target_id LIMIT 1"))
stamp = datetime.fromisoformat(state["createdAt"]) - timedelta(minutes=10)
time = stamp.isoformat()
targets = ["e5700000-0000-4000-8000-0000000000" + n for n in ["11", "12", "13"]]
rows = [
    (0, "9700000001", "실리콘 조리도구 5종 세트", 24900, 91),
    (0, "9700000002", "스테인리스 믹싱볼 3종 세트", 18900, 87),
    (1, "9700000003", "6인치 전자책 리더 · 16GB", 159000, 88),
    (2, "9700000004", "USB-C 35W 여행용 멀티 어댑터", 32900, 86),
]
labels = ["주방용품", "휴대용 전자책 리더", "여행용 멀티 어댑터"]
commands = ["BEGIN;", f"SELECT id FROM curations WHERE id='{CID}' AND user_id='{UID}' FOR UPDATE;"]
for target in targets:
    commands.append(f"INSERT INTO phase8_research_pools(user_id,curation_id,plan_target_id,version,expand_ordinal,created_at,updated_at) VALUES('{UID}','{CID}','{target}',1,1,{literal(time)},{literal(time)}) ON CONFLICT DO NOTHING;")
for order, (idx, product, title, price, score) in enumerate(rows):
    target = targets[idx]
    ref = {"source": "COUPANG", "marketplace": "KR", "productId": product}
    url = f"https://www.coupang.com/vp/products/{product}"
    observation = {"schemaVersion": "vitlane.external-product-observation.v1", "productRef": ref, "productUrl": url,
                   "title": title, "description": "UI 검수용 가상 상품입니다. 실제 판매 상품이나 가격이 아닙니다.",
                   "price": {"kind": "OBSERVED", "currency": "KRW", "amountMinor": price}, "priceScope": "PRODUCT", "seller": {"kind": "UNKNOWN"}, "observedAt": time,
                   "provenance": {"apiProvider": "DEV_REVIEW_FIXTURE", "apiProduct": "UI review fixture", "country": "KR", "queryLanguage": "ko", "discoveryChannel": "CATALOG_SEARCH"}}
    c = copy.deepcopy(criteria); c["subject"]["label"] = labels[idx]
    assessment = {"schemaVersion": "vitlane.axis-assessment.v1", "criteria": c, "weights": [100], "scores": [{"axisId": "usefulness", "scorePercent": score, "basis": "PROVIDED", "factIds": ["name"], "explanation": "UI 검수용 예시 평가입니다. 실제 AI 평가가 아닙니다."}], "totalScore": score, "totalBasisPoints": score * 100, "contentLocale": "ko-KR", "roundId": "e5700000-0000-4000-8000-00000000003" + str(idx + 1), "modelKey": "dev-ui-fixture", "createdAt": time}
    values = [UID, CID, target, "dev-step7-ordinary-" + product, "coupang:KR:" + product, "COUPANG", "coupang:KR:" + product, "PRODUCT_URL", url]
    commands.append("INSERT INTO phase8_research_candidates(user_id,curation_id,plan_target_id,candidate_id,provider_product_id,source_kind,identity_key,locator_kind,product_url,display_order,first_seen_at,last_seen_at,external_observation,axis_assessment) VALUES(" + ",".join(literal(v) for v in values) + f",{order+1},{literal(time)},{literal(time)},{js(observation)},{js(assessment)}) ON CONFLICT DO NOTHING;")

def action(n, kind, **extras):
    return {"id": "e5700000-0000-4000-8000-0000000000" + n, "type": kind, "status": "SUCCEEDED", "jobs": [], "effects": [], "decisions": [], "decisionIds": [], "questions": [], "answers": [], **extras}

jobs = [{"jobId": "e5700000-0000-4000-8000-00000000003" + str(i+4), "actionId": "e5700000-0000-4000-8000-000000000093", "kind": "RESEARCH_ROUND", "targetId": target, "status": "SUCCEEDED", "effects": [{"kind": "CANDIDATES_ADDED", "targetId": target, "count": 2 if i == 0 else 1}], "facts": {"roundId": "e5700000-0000-4000-8000-00000000003" + str(i+1), "observed": 2 if i == 0 else 1, "evaluated": 2 if i == 0 else 1, "admitted": 2 if i == 0 else 1, "rejected": 0, "duplicates": 0, "unevaluated": 0, "sources": [{"source": "COUPANG", "status": "SUCCEEDED", "candidateCount": 2 if i == 0 else 1}]}} for i, target in enumerate(targets)]
thread_id = "e5700000-0000-4000-8000-000000000093"
thread = {"schemaVersion": "vitlane.curation-thread.v2", "id": thread_id, "curationId": CID, "mode": "AUTO", "origin": "REQUEST", "request": "실용적인 주방용품을 비교해 줘. 전자책 리더와 여행용 어댑터도 함께 보고 싶어.", "expectedCurationVersion": 3, "revision": 1, "status": "SUCCEEDED", "targetLabels": dict(zip(targets, labels)), "createdAt": time, "updatedAt": time,
          "actions": [action("91", "INTENT_NEXT_STEP"), action("92", "PLANNING_ADD_TARGETS"), action("93", "PLANNING_START_CURATING", jobs=jobs), action("94", "RESPONSE", instruction="COMMENT", response={"schemaVersion": "vitlane.thread-response.v1", "kind": "COMMENT", "body": "주방용품은 매일 쓰기 좋은 조리도구 세트와 믹싱볼을 비교했어요. 전자책 리더는 휴대성, 어댑터는 USB-C 충전을 기준으로 골랐어요. 마음에 드는 상품을 열어 자세히 비교해 보세요.", "locale": "ko-KR", "references": [], "modelKey": "dev-ui-fixture", "createdAt": time})]}
commands.append(f"INSERT INTO curation_threads(id,user_id,curation_id,plan_id,status,request_hash,data,created_at,updated_at) VALUES('{thread_id}','{UID}','{CID}','{PLAN}','SUCCEEDED','step7-ui-fixture-v1',{js(thread)},{literal(time)},{literal(time)}) ON CONFLICT DO NOTHING;")
commands.append(f"UPDATE phase8_research_pools SET version=version+1 WHERE curation_id='{CID}';")
# The older lifecycle fixture predates the current Action subject/effect contract.
commands.append(f"UPDATE curation_actions SET effect_kind='INTELLIGENCE' WHERE id='e5700000-0000-4000-8000-000000000091' AND curation_id='{CID}';")
commands.append(f"UPDATE curation_actions SET subject_id='{CID}' WHERE id='e5700000-0000-4000-8000-000000000092' AND curation_id='{CID}';")
# Remove only the obsolete, empty result messages generated for the two old fixture actions.
commands.append(f"DELETE FROM curation_follow_ups WHERE curation_id='{CID}' AND kind='RESULT' AND response_id IN ('e5700000-0000-4000-8000-000000000091','e5700000-0000-4000-8000-000000000092');")
commands.append("COMMIT;")
query("\n".join(commands))
print("Prepared 4 synthetic ordinary candidates and one conversation reply; live findings/subscriptions preserved.")
