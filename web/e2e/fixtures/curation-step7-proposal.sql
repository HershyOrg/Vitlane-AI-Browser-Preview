-- Development-only; fresh dedicated Step 7 DB with development user 103.
-- Human-operated local review fixture. The owning user is reset by the server
-- before this file is applied. It starts with three completed lifecycle
-- Targets but no Research Candidate data; Phase 8 CandidatePool rows must be
-- created through the same Shopify-backed commands used by the application.
BEGIN;

INSERT INTO shopping_plans(
  id, user_id, original_intent, plan_mode, execution_mode,
  budget_amount, budget_currency, country, city,
  agent_mode, model_key, created_at
) VALUES (
  'e5700000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000103',
  '[Step 7 체험] 실용적인 주방용품 핫딜을 기다려요',
  'AUTO',
  'EXPERIMENT',
  700,
  'USD',
  'US',
  'Seattle',
  'MANAGED',
  'gpt-5-nano',
  now()
);

INSERT INTO curations(
  id, shopping_plan_id, user_id, phase, version,
  created_at, updated_at
) VALUES (
  'e5700000-0000-4000-8000-000000000002',
  'e5700000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000103',
  'CURATING',
  3,
  now(),
  now()
);

INSERT INTO curation_actions(
  id, curation_id, actor_user_id, action_type, phase_at_request,
  requested_transition_to, subject_type, subject_id, effect_kind,
  source_ref_type, source_ref_id, expected_curation_version,
  request_hash, created_at
) VALUES
  (
    'e5700000-0000-4000-8000-000000000091',
    'e5700000-0000-4000-8000-000000000002',
    'e5000000-0000-4000-8000-000000000103',
    'INTENT_NEXT_STEP',
    'HAVING_INTENT',
    'PLANNING',
    'INTENT',
    NULL,
    'NONE',
    'SHOPPING_PLAN',
    'e5700000-0000-4000-8000-000000000001',
    1,
    sha256(convert_to('local-review-intent-next-step', 'UTF8')),
    now() - interval '3 seconds'
  ),
  (
    'e5700000-0000-4000-8000-000000000092',
    'e5700000-0000-4000-8000-000000000002',
    'e5000000-0000-4000-8000-000000000103',
    'PLANNING_ADD_TARGETS',
    'PLANNING',
    NULL,
    'TARGET_LIST',
    NULL,
    'INTELLIGENCE',
    'CURATION_RUN_REQUEST',
    'e5700000-0000-4000-8000-000000000092',
    1,
    sha256(convert_to('local-review-planning-add-targets', 'UTF8')),
    now() - interval '2 seconds'
  ),
  (
    'e5700000-0000-4000-8000-000000000093',
    'e5700000-0000-4000-8000-000000000002',
    'e5000000-0000-4000-8000-000000000103',
    'PLANNING_START_CURATING',
    'PLANNING',
    'CURATING',
    'CURATION',
    'e5700000-0000-4000-8000-000000000002',
    'INTELLIGENCE',
    'RESEARCH_START_REQUEST',
    'e5700000-0000-4000-8000-000000000093',
    2,
    sha256(convert_to('local-review-planning-next-step', 'UTF8')),
    now() - interval '1 second'
  );

INSERT INTO plan_targets(
  id, plan_id, curation_id, user_id, title, normalized_intent, category,
  allocated_amount, allocated_currency, country, city,
  url_mode, order_index, confirmed_at, target_hash, target_hash_schema,
  version, created_at, updated_at
) VALUES
  (
    'e5700000-0000-4000-8000-000000000011',
    'e5700000-0000-4000-8000-000000000001',
    'e5700000-0000-4000-8000-000000000002',
    'e5000000-0000-4000-8000-000000000103',
    '주방용품',
    'everyday kitchen cookware',
    'audio',
    460,
    'USD',
    'US',
    'Seattle',
    'REFERENCE',
    0,
    now(),
    'local-target-headphones',
    'vitlane.plan-target.v1',
    1,
    now(),
    now()
  ),
  (
    'e5700000-0000-4000-8000-000000000012',
    'e5700000-0000-4000-8000-000000000001',
    'e5700000-0000-4000-8000-000000000002',
    'e5000000-0000-4000-8000-000000000103',
    '휴대용 전자책 리더',
    'black 16GB glare-free ebook reader',
    'reader',
    180,
    'USD',
    'US',
    'Seattle',
    'REFERENCE',
    1,
    now(),
    'local-target-reader',
    'vitlane.plan-target.v1',
    1,
    now(),
    now()
  ),
  (
    'e5700000-0000-4000-8000-000000000013',
    'e5700000-0000-4000-8000-000000000001',
    'e5700000-0000-4000-8000-000000000002',
    'e5000000-0000-4000-8000-000000000103',
    '여행용 멀티 어댑터',
    'compact universal travel adapter',
    'travel-accessory',
    60,
    'USD',
    'US',
    'Seattle',
    'REFERENCE',
    2,
    now(),
    'local-target-adapter',
    'vitlane.plan-target.v1',
    1,
    now(),
    now()
  );

INSERT INTO shopping_sessions(
  id, plan_target_id, user_id, target_snapshot, research_scope_snapshot,
  status, version, created_at, updated_at
) VALUES
  (
    'e5700000-0000-4000-8000-000000000021',
    'e5700000-0000-4000-8000-000000000011',
    'e5000000-0000-4000-8000-000000000103',
    '{"id":"e5700000-0000-4000-8000-000000000011","planId":"e5700000-0000-4000-8000-000000000001","curationId":"e5700000-0000-4000-8000-000000000002","title":"주방용품","normalizedIntent":"everyday kitchen cookware","category":"audio","allocatedBudget":{"amount":"460","currency":"USD"},"country":"US","city":"Seattle","urlMode":"REFERENCE"}'::jsonb,
    '{"budget":{"amount":"460","currency":"USD"},"country":"US","city":"Seattle","allowedItems":[],"blockedItems":[]}'::jsonb,
    'REVIEWING',
    4,
    now(),
    now()
  ),
  (
    'e5700000-0000-4000-8000-000000000022',
    'e5700000-0000-4000-8000-000000000012',
    'e5000000-0000-4000-8000-000000000103',
    '{"id":"e5700000-0000-4000-8000-000000000012","planId":"e5700000-0000-4000-8000-000000000001","curationId":"e5700000-0000-4000-8000-000000000002","title":"휴대용 전자책 리더","normalizedIntent":"black 16GB glare-free ebook reader","category":"reader","allocatedBudget":{"amount":"180","currency":"USD"},"country":"US","city":"Seattle","urlMode":"REFERENCE"}'::jsonb,
    '{"budget":{"amount":"180","currency":"USD"},"country":"US","city":"Seattle","allowedItems":[],"blockedItems":[]}'::jsonb,
    'REVIEWING',
    4,
    now(),
    now()
  ),
  (
    'e5700000-0000-4000-8000-000000000023',
    'e5700000-0000-4000-8000-000000000013',
    'e5000000-0000-4000-8000-000000000103',
    '{"id":"e5700000-0000-4000-8000-000000000013","planId":"e5700000-0000-4000-8000-000000000001","curationId":"e5700000-0000-4000-8000-000000000002","title":"여행용 멀티 어댑터","normalizedIntent":"compact universal travel adapter","category":"travel-accessory","allocatedBudget":{"amount":"60","currency":"USD"},"country":"US","city":"Seattle","urlMode":"REFERENCE"}'::jsonb,
    '{"budget":{"amount":"60","currency":"USD"},"country":"US","city":"Seattle","allowedItems":[],"blockedItems":[]}'::jsonb,
    'REVIEWING',
    4,
    now(),
    now()
  );

INSERT INTO research_rounds(
  id, shopping_session_id, user_id, round_number,
  context_schema, context_version, context_hash, context_snapshot,
  status, created_at, completed_at
) VALUES
  ('e5700000-0000-4000-8000-000000000031','e5700000-0000-4000-8000-000000000021','e5000000-0000-4000-8000-000000000103',1,'vitlane.research-context.v1',1,'local-context-headphones','{}'::jsonb,'RESULTS_READY',now(),now()),
  ('e5700000-0000-4000-8000-000000000032','e5700000-0000-4000-8000-000000000022','e5000000-0000-4000-8000-000000000103',1,'vitlane.research-context.v1',1,'local-context-reader','{}'::jsonb,'RESULTS_READY',now(),now()),
  ('e5700000-0000-4000-8000-000000000033','e5700000-0000-4000-8000-000000000023','e5000000-0000-4000-8000-000000000103',1,'vitlane.research-context.v1',1,'local-context-adapter','{}'::jsonb,'RESULTS_READY',now(),now());

-- One Job per Round, all sharing the PLANNING_START_CURATING action that
-- opened them. That shared lineage is what replaced the single grant covering
-- three sessions.
INSERT INTO intelligence_jobs(
  id, user_id, curation_id, curation_action_id, plan_id,
  target_kind, research_round_id, provider, model_key,
  status, attempt_count, created_at, updated_at, completed_at
) VALUES
  ('e5700000-0000-4000-8000-000000000034','e5000000-0000-4000-8000-000000000103','e5700000-0000-4000-8000-000000000002','e5700000-0000-4000-8000-000000000093','e5700000-0000-4000-8000-000000000001','RESEARCH_ROUND','e5700000-0000-4000-8000-000000000031','MANAGED','gpt-5-nano','SUCCEEDED',1,now(),now(),now()),
  ('e5700000-0000-4000-8000-000000000035','e5000000-0000-4000-8000-000000000103','e5700000-0000-4000-8000-000000000002','e5700000-0000-4000-8000-000000000093','e5700000-0000-4000-8000-000000000001','RESEARCH_ROUND','e5700000-0000-4000-8000-000000000032','MANAGED','gpt-5-nano','SUCCEEDED',1,now(),now(),now()),
  ('e5700000-0000-4000-8000-000000000036','e5000000-0000-4000-8000-000000000103','e5700000-0000-4000-8000-000000000002','e5700000-0000-4000-8000-000000000093','e5700000-0000-4000-8000-000000000001','RESEARCH_ROUND','e5700000-0000-4000-8000-000000000033','MANAGED','gpt-5-nano','SUCCEEDED',1,now(),now(),now());

-- Research data-plane state is intentionally not seeded here. The fixture
-- preserves only the lifecycle control plane (Round + Job); Phase 8
-- CandidatePool rows are created by the actual Shopify command during review.

UPDATE shopping_sessions AS session
SET current_research_round_id = mapping.round_id
FROM (
  VALUES
    ('e5700000-0000-4000-8000-000000000021'::uuid,'e5700000-0000-4000-8000-000000000031'::uuid),
    ('e5700000-0000-4000-8000-000000000022'::uuid,'e5700000-0000-4000-8000-000000000032'::uuid),
    ('e5700000-0000-4000-8000-000000000023'::uuid,'e5700000-0000-4000-8000-000000000033'::uuid)
) AS mapping(session_id, round_id)
WHERE session.id = mapping.session_id;

-- Step 7 keeps the real AI ledger; no synthetic historical cost rows.

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM shopping_sessions AS session
    JOIN plan_targets AS target ON target.id=session.plan_target_id
    WHERE target.plan_id='e5700000-0000-4000-8000-000000000001'
      AND (
        session.target_snapshot->>'id' <> target.id::text
        OR session.target_snapshot->>'planId' <> target.plan_id::text
      )
  ) THEN
    RAISE EXCEPTION 'Local review fixture target snapshot identity mismatch';
  END IF;
END $$;

-- SQL fixtures are inserted after migrations, so create the disabled ledger
-- that migration 000110 backfills for existing production Curations.
INSERT INTO curation_budgets(curation_id, currency, allocations)
SELECT c.id, p.budget_currency,
  COALESCE((SELECT jsonb_agg(jsonb_build_object('targetId',t.id,'quantity',1,'amount',NULL) ORDER BY t.order_index)
    FROM plan_targets t WHERE t.curation_id=c.id AND t.removed_at IS NULL),'[]'::jsonb)
FROM curations c JOIN shopping_plans p ON p.id=c.shopping_plan_id
WHERE c.id='e5700000-0000-4000-8000-000000000002';

COMMIT;

-- Development-only scenario; apply after local-review-multi-product.sql in a fresh Step 7 database.
BEGIN;
UPDATE curations SET research_country='KR' WHERE id='e5700000-0000-4000-8000-000000000002';
INSERT INTO curation_target_criteria(target_id,user_id,curation_id,version,criteria) VALUES('e5700000-0000-4000-8000-000000000011','e5000000-0000-4000-8000-000000000103','e5700000-0000-4000-8000-000000000002',1,'{"schemaVersion": "vitlane.target-criteria.v1", "version": 1, "subject": {"label": "주방용품", "productType": "kitchen cookware and household kitchen supplies"}, "axes": [{"axisId": "usefulness", "label": "일상 활용도", "definition": "일상적인 조리에 유용한 주방용품", "importance": 5, "origin": "REQUEST"}], "exclusions": []}');
INSERT INTO curation_conversation_requests(id,user_id,curation_id,mode,body,request_hash,status,response_ready)
VALUES('e5710000-0000-4000-8000-000000000001','e5000000-0000-4000-8000-000000000103','e5700000-0000-4000-8000-000000000002','FOLLOW_UP_ACCEPT','[개발용 시나리오] 3만 원 이하 주방용품 핫딜을 기다리고 싶어요','step7-review','COMPLETE',true);
INSERT INTO curation_conversations(curation_id,version,latest_request_id) VALUES('e5700000-0000-4000-8000-000000000002',1,'e5710000-0000-4000-8000-000000000001') ON CONFLICT(curation_id) DO UPDATE SET latest_request_id=EXCLUDED.latest_request_id;
INSERT INTO curation_follow_ups(id,user_id,curation_id,response_id,kind,status,content,payload,fingerprint)
VALUES('e5710000-0000-4000-8000-000000000001','e5000000-0000-4000-8000-000000000103','e5700000-0000-4000-8000-000000000002','e5710000-0000-4000-8000-000000000001','PROPOSAL','PENDING','{"code": "SUBSCRIBE_DEALS", "locale": "ko-KR", "targetTitle": "주방용품", "body": "앞으로 7일 동안 3만 원 이하의 주방용품 핫딜을 지켜볼까요? 새 상품을 발견하면 이 대화에서 보여드릴게요."}','{"kind": "SUBSCRIBE_DEALS", "targetId": "e5700000-0000-4000-8000-000000000011", "criteriaVersion": 1, "subscription": {"schemaVersion": "vitlane.research-subscription-terms.v1", "criteria": {"schemaVersion": "vitlane.target-criteria.v1", "version": 1, "subject": {"label": "주방용품", "productType": "kitchen cookware and household kitchen supplies"}, "axes": [{"axisId": "usefulness", "label": "일상 활용도", "definition": "일상적인 조리에 유용한 주방용품", "importance": 5, "origin": "REQUEST"}], "exclusions": []}, "country": "KR", "currency": "KRW", "maximumMinor": 30000, "keywords": ["주방용품"], "expiresAt": "2026-09-29T13:15:28.517398Z"}}',curation_follow_up_context('e5700000-0000-4000-8000-000000000002','e5700000-0000-4000-8000-000000000011'));
UPDATE curation_follow_ups SET payload=jsonb_set(payload,'{subscription,expiresAt}',to_jsonb(now()+interval '7 days')) WHERE id='e5710000-0000-4000-8000-000000000001';
COMMIT;