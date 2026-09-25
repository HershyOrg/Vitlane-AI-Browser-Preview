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
  'e5100000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000103',
  '여행과 업무를 위한 전자제품 3개를 비교하고 장바구니로 구매',
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
  'e5100000-0000-4000-8000-000000000002',
  'e5100000-0000-4000-8000-000000000001',
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
    'e5100000-0000-4000-8000-000000000091',
    'e5100000-0000-4000-8000-000000000002',
    'e5000000-0000-4000-8000-000000000103',
    'INTENT_NEXT_STEP',
    'HAVING_INTENT',
    'PLANNING',
    'INTENT',
    NULL,
    'NONE',
    'SHOPPING_PLAN',
    'e5100000-0000-4000-8000-000000000001',
    1,
    sha256(convert_to('local-review-intent-next-step', 'UTF8')),
    now() - interval '3 seconds'
  ),
  (
    'e5100000-0000-4000-8000-000000000092',
    'e5100000-0000-4000-8000-000000000002',
    'e5000000-0000-4000-8000-000000000103',
    'PLANNING_ADD_TARGETS',
    'PLANNING',
    NULL,
    'TARGET_LIST',
    NULL,
    'INTELLIGENCE',
    'CURATION_RUN_REQUEST',
    'e5100000-0000-4000-8000-000000000092',
    1,
    sha256(convert_to('local-review-planning-add-targets', 'UTF8')),
    now() - interval '2 seconds'
  ),
  (
    'e5100000-0000-4000-8000-000000000093',
    'e5100000-0000-4000-8000-000000000002',
    'e5000000-0000-4000-8000-000000000103',
    'PLANNING_START_CURATING',
    'PLANNING',
    'CURATING',
    'CURATION',
    'e5100000-0000-4000-8000-000000000002',
    'INTELLIGENCE',
    'RESEARCH_START_REQUEST',
    'e5100000-0000-4000-8000-000000000093',
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
    'e5100000-0000-4000-8000-000000000011',
    'e5100000-0000-4000-8000-000000000001',
    'e5100000-0000-4000-8000-000000000002',
    'e5000000-0000-4000-8000-000000000103',
    '노이즈 캔슬링 헤드폰',
    'black over-ear noise cancelling headphones',
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
    'e5100000-0000-4000-8000-000000000012',
    'e5100000-0000-4000-8000-000000000001',
    'e5100000-0000-4000-8000-000000000002',
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
    'e5100000-0000-4000-8000-000000000013',
    'e5100000-0000-4000-8000-000000000001',
    'e5100000-0000-4000-8000-000000000002',
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
    'e5100000-0000-4000-8000-000000000021',
    'e5100000-0000-4000-8000-000000000011',
    'e5000000-0000-4000-8000-000000000103',
    '{"id":"e5100000-0000-4000-8000-000000000011","planId":"e5100000-0000-4000-8000-000000000001","curationId":"e5100000-0000-4000-8000-000000000002","title":"노이즈 캔슬링 헤드폰","normalizedIntent":"black over-ear noise cancelling headphones","category":"audio","allocatedBudget":{"amount":"460","currency":"USD"},"country":"US","city":"Seattle","urlMode":"REFERENCE"}'::jsonb,
    '{"budget":{"amount":"460","currency":"USD"},"country":"US","city":"Seattle","allowedItems":[],"blockedItems":[]}'::jsonb,
    'REVIEWING',
    4,
    now(),
    now()
  ),
  (
    'e5100000-0000-4000-8000-000000000022',
    'e5100000-0000-4000-8000-000000000012',
    'e5000000-0000-4000-8000-000000000103',
    '{"id":"e5100000-0000-4000-8000-000000000012","planId":"e5100000-0000-4000-8000-000000000001","curationId":"e5100000-0000-4000-8000-000000000002","title":"휴대용 전자책 리더","normalizedIntent":"black 16GB glare-free ebook reader","category":"reader","allocatedBudget":{"amount":"180","currency":"USD"},"country":"US","city":"Seattle","urlMode":"REFERENCE"}'::jsonb,
    '{"budget":{"amount":"180","currency":"USD"},"country":"US","city":"Seattle","allowedItems":[],"blockedItems":[]}'::jsonb,
    'REVIEWING',
    4,
    now(),
    now()
  ),
  (
    'e5100000-0000-4000-8000-000000000023',
    'e5100000-0000-4000-8000-000000000013',
    'e5000000-0000-4000-8000-000000000103',
    '{"id":"e5100000-0000-4000-8000-000000000013","planId":"e5100000-0000-4000-8000-000000000001","curationId":"e5100000-0000-4000-8000-000000000002","title":"여행용 멀티 어댑터","normalizedIntent":"compact universal travel adapter","category":"travel-accessory","allocatedBudget":{"amount":"60","currency":"USD"},"country":"US","city":"Seattle","urlMode":"REFERENCE"}'::jsonb,
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
  ('e5100000-0000-4000-8000-000000000031','e5100000-0000-4000-8000-000000000021','e5000000-0000-4000-8000-000000000103',1,'vitlane.research-context.v1',1,'local-context-headphones','{}'::jsonb,'RESULTS_READY',now(),now()),
  ('e5100000-0000-4000-8000-000000000032','e5100000-0000-4000-8000-000000000022','e5000000-0000-4000-8000-000000000103',1,'vitlane.research-context.v1',1,'local-context-reader','{}'::jsonb,'RESULTS_READY',now(),now()),
  ('e5100000-0000-4000-8000-000000000033','e5100000-0000-4000-8000-000000000023','e5000000-0000-4000-8000-000000000103',1,'vitlane.research-context.v1',1,'local-context-adapter','{}'::jsonb,'RESULTS_READY',now(),now());

-- One Job per Round, all sharing the PLANNING_START_CURATING action that
-- opened them. That shared lineage is what replaced the single grant covering
-- three sessions.
INSERT INTO intelligence_jobs(
  id, user_id, curation_id, curation_action_id, plan_id,
  target_kind, research_round_id, provider, model_key,
  status, attempt_count, created_at, updated_at, completed_at
) VALUES
  ('e5100000-0000-4000-8000-000000000034','e5000000-0000-4000-8000-000000000103','e5100000-0000-4000-8000-000000000002','e5100000-0000-4000-8000-000000000093','e5100000-0000-4000-8000-000000000001','RESEARCH_ROUND','e5100000-0000-4000-8000-000000000031','MANAGED','gpt-5-nano','SUCCEEDED',1,now(),now(),now()),
  ('e5100000-0000-4000-8000-000000000035','e5000000-0000-4000-8000-000000000103','e5100000-0000-4000-8000-000000000002','e5100000-0000-4000-8000-000000000093','e5100000-0000-4000-8000-000000000001','RESEARCH_ROUND','e5100000-0000-4000-8000-000000000032','MANAGED','gpt-5-nano','SUCCEEDED',1,now(),now(),now()),
  ('e5100000-0000-4000-8000-000000000036','e5000000-0000-4000-8000-000000000103','e5100000-0000-4000-8000-000000000002','e5100000-0000-4000-8000-000000000093','e5100000-0000-4000-8000-000000000001','RESEARCH_ROUND','e5100000-0000-4000-8000-000000000033','MANAGED','gpt-5-nano','SUCCEEDED',1,now(),now(),now());

-- Research data-plane state is intentionally not seeded here. The fixture
-- preserves only the lifecycle control plane (Round + Job); Phase 8
-- CandidatePool rows are created by the actual Shopify command during review.

UPDATE shopping_sessions AS session
SET current_research_round_id = mapping.round_id
FROM (
  VALUES
    ('e5100000-0000-4000-8000-000000000021'::uuid,'e5100000-0000-4000-8000-000000000031'::uuid),
    ('e5100000-0000-4000-8000-000000000022'::uuid,'e5100000-0000-4000-8000-000000000032'::uuid),
    ('e5100000-0000-4000-8000-000000000023'::uuid,'e5100000-0000-4000-8000-000000000033'::uuid)
) AS mapping(session_id, round_id)
WHERE session.id = mapping.session_id;

-- Human review needs a non-empty server-level time series. These rows are
-- deterministic TEST data for the dedicated local-review database only; they
-- do not identify a user and are replaced each time this fixture is applied.
DELETE FROM managed_runner_usage_daily
WHERE scope = 'SERVER'
  AND scope_id = 'SERVER'
  AND usage_date BETWEEN CURRENT_DATE - 29 AND CURRENT_DATE;

INSERT INTO managed_runner_usage_daily(
  usage_date, scope, scope_id, reserved_micros, settled_micros,
  request_count, input_tokens, output_tokens, updated_at
)
SELECT
  day::date,
  'SERVER',
  'SERVER',
  CASE WHEN day_offset = 29 THEN 180000 ELSE 0 END,
  240000 + (day_offset % 6) * 135000,
  5 + (day_offset % 8),
  18000 + day_offset * 1250 + (day_offset % 4) * 3100,
  5200 + day_offset * 420 + (day_offset % 3) * 1700,
  now()
FROM generate_series(
  CURRENT_DATE - 29,
  CURRENT_DATE,
  interval '1 day'
) WITH ORDINALITY AS series(day, ordinal)
CROSS JOIN LATERAL (SELECT ordinal - 1 AS day_offset) AS position;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM shopping_sessions AS session
    JOIN plan_targets AS target ON target.id=session.plan_target_id
    WHERE target.plan_id='e5100000-0000-4000-8000-000000000001'
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
WHERE c.id='e5100000-0000-4000-8000-000000000002';

COMMIT;
