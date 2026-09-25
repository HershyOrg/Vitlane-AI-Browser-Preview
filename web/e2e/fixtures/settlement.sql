-- Phase 5 browser E2E prerequisite: one authenticated user with two immutable,
-- researched candidates. Payments, wallet/KYC records, fulfillment and
-- receipts are intentionally not seeded; the browser and workers must create them.
BEGIN;
SET CONSTRAINTS ALL DEFERRED;
SET LOCAL vitlane.account_reset = 'on';

-- The fixed Phase 5 operator is a dedicated E2E identity. Reset every
-- application-owned row for that identity so failed or repeated Phase 4 runs
-- cannot leak liked Candidates, Plans or checkout policy state into this run.
-- Merchant registry, migrations and every non-E2E user remain untouched.
-- ADR-0054: legacy Purchase/Fulfillment/PII-audit table은 migration 72에서
-- 삭제됐다. 이 seed는 현재 schema(AgencyOrder graph)만 정리한다.
DELETE FROM shipping_snapshots
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM shipping_profiles
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM kyc_evidence_observations
WHERE user_id IN (
  'e5000000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000009'
);
DELETE FROM kyc_provider_operations
WHERE user_id IN (
  'e5000000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000009'
);
DELETE FROM kyc_verification_audit_events
WHERE case_id IN (
  SELECT id FROM kyc_verification_cases
  WHERE user_id IN (
    'e5000000-0000-4000-8000-000000000001',
    'e5000000-0000-4000-8000-000000000009'
  )
);
DELETE FROM kyc_credentials
WHERE user_id IN (
  'e5000000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000009'
);
DELETE FROM kyc_verification_cases
WHERE user_id IN (
  'e5000000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000009'
);
DELETE FROM wallet_ownership_proofs
WHERE user_id IN (
  'e5000000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000009'
);
DELETE FROM wallet_registration_attempts
WHERE user_id IN (
  'e5000000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000009'
);
DELETE FROM buyer_profiles
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM user_policy_acceptances
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM wallets
WHERE user_id IN (
  'e5000000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000009'
);

UPDATE shopping_sessions
SET current_research_round_id = NULL
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
UPDATE research_rounds
SET result_submission_id = NULL
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM curation_selection_commands
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM curation_selections
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM candidate_configurations
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM research_feedback
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM research_catalog_observations
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM candidates
WHERE shopping_session_id IN (
  SELECT id FROM shopping_sessions
  WHERE user_id = 'e5000000-0000-4000-8000-000000000001'
);
DELETE FROM research_submissions
WHERE research_round_id IN (
  SELECT id FROM research_rounds
  WHERE user_id = 'e5000000-0000-4000-8000-000000000001'
);
UPDATE plan_targets
SET created_by_curation_run_id = NULL
WHERE plan_id IN (
  SELECT id FROM shopping_plans
  WHERE user_id = 'e5000000-0000-4000-8000-000000000001'
);
DELETE FROM curation_runs
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM planning_proposals
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';

-- Thread job links point at Jobs, so they go before the Jobs (migration 000114).
DELETE FROM curation_thread_jobs
WHERE thread_id IN (SELECT id FROM curation_threads WHERE user_id = 'e5000000-0000-4000-8000-000000000001');
-- Jobs are held by RESTRICT from the product results deleted above, so they
-- can only go once those are gone and before the Rounds they point at.
DELETE FROM intelligence_steps
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM intelligence_attempts
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM intelligence_jobs
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
-- Curation deletion cascades through PlanTargets. Remove the Research graph
-- that still points at those Targets before removing the Curation aggregate.
DELETE FROM research_rounds
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM planning_tasks
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM shopping_sessions
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM curation_actions
WHERE actor_user_id = 'e5000000-0000-4000-8000-000000000001';
-- Threads and control modes reference the Curation (migration 000114); Actions
-- reference Threads, so they were removed just above.
DELETE FROM curation_threads
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM curation_control_modes
WHERE curation_id IN (SELECT id FROM curations WHERE user_id = 'e5000000-0000-4000-8000-000000000001');
DELETE FROM curations
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM plan_creation_requests
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM shopping_plans
WHERE user_id = 'e5000000-0000-4000-8000-000000000001';
DELETE FROM auth_sessions
WHERE user_id IN (
  'e5000000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000009'
);

-- Users are stable auth roots. Refresh identities after the application reset
-- instead of deleting rows that are referenced by audit history.
INSERT INTO users(id, status, email, display_name, created_at, updated_at)
VALUES (
  'e5000000-0000-4000-8000-000000000001',
  'ACTIVE',
  'operator@example.com',
  'Vitlane Local Review',
  now(),
  now()
)
ON CONFLICT (id) DO UPDATE
SET status = EXCLUDED.status,
    email = EXCLUDED.email,
    display_name = EXCLUDED.display_name,
    updated_at = EXCLUDED.updated_at;

INSERT INTO users(id, status, email, display_name, created_at, updated_at)
VALUES (
  'e5000000-0000-4000-8000-000000000009',
  'ACTIVE',
  'phase5-user@example.com',
  'Phase 5 Non-operator',
  now(),
  now()
)
ON CONFLICT (id) DO UPDATE
SET status = EXCLUDED.status,
    email = EXCLUDED.email,
    display_name = EXCLUDED.display_name,
    updated_at = EXCLUDED.updated_at;

INSERT INTO shopping_plans(
  id, user_id, original_intent, plan_mode, execution_mode,
  budget_amount, budget_currency, country, city, created_at
) VALUES (
  'e5000000-0000-4000-8000-000000000002',
  'e5000000-0000-4000-8000-000000000001',
  'Amazon US에서 60달러 안으로 테스트 상품과 보조 항목 조사',
  'AUTO',
  'EXPERIMENT',
  60,
  'USD',
  'US',
  'Seattle',
  now()
);

INSERT INTO curations(
  id, shopping_plan_id, user_id, phase, version,
  created_at, updated_at
) VALUES (
  'e5000000-0000-4000-8000-000000000011',
  'e5000000-0000-4000-8000-000000000002',
  'e5000000-0000-4000-8000-000000000001',
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
    'e5000000-0000-4000-8000-000000000021',
    'e5000000-0000-4000-8000-000000000011',
    'e5000000-0000-4000-8000-000000000001',
    'INTENT_NEXT_STEP',
    'HAVING_INTENT',
    'PLANNING',
    'INTENT',
    NULL,
    'NONE',
    'SHOPPING_PLAN',
    'e5000000-0000-4000-8000-000000000002',
    1,
    sha256(convert_to('phase5-intent-next-step', 'UTF8')),
    now() - interval '3 seconds'
  ),
  (
    'e5000000-0000-4000-8000-000000000022',
    'e5000000-0000-4000-8000-000000000011',
    'e5000000-0000-4000-8000-000000000001',
    'PLANNING_ADD_TARGETS',
    'PLANNING',
    NULL,
    'TARGET_LIST',
    NULL,
    'INTELLIGENCE',
    'CURATION_RUN_REQUEST',
    'e5000000-0000-4000-8000-000000000022',
    1,
    sha256(convert_to('phase5-planning-add-targets', 'UTF8')),
    now() - interval '2 seconds'
  ),
  (
    'e5000000-0000-4000-8000-000000000023',
    'e5000000-0000-4000-8000-000000000011',
    'e5000000-0000-4000-8000-000000000001',
    'PLANNING_START_CURATING',
    'PLANNING',
    'CURATING',
    'CURATION',
    'e5000000-0000-4000-8000-000000000011',
    'INTELLIGENCE',
    'RESEARCH_START_REQUEST',
    'e5000000-0000-4000-8000-000000000023',
    2,
    sha256(convert_to('phase5-planning-next-step', 'UTF8')),
    now() - interval '1 second'
  );

INSERT INTO plan_targets(
  id, plan_id, curation_id, user_id, title, normalized_intent, category,
  allocated_amount, allocated_currency, country, city,
  url_mode, order_index, confirmed_at, target_hash, version,
  created_at, updated_at
) VALUES (
  'e5000000-0000-4000-8000-000000000003',
  'e5000000-0000-4000-8000-000000000002',
  'e5000000-0000-4000-8000-000000000011',
  'e5000000-0000-4000-8000-000000000001',
  'Phase 5 E2E Test Product',
  'Amazon US 50 USD test product',
  'test-product',
  50,
  'USD',
  'US',
  'Seattle',
  'EXACT_PRODUCT',
  0,
  now(),
  'phase5-e2e-target-hash',
  1,
  now(),
  now()
);

INSERT INTO plan_targets(
  id, plan_id, curation_id, user_id, title, normalized_intent, category,
  allocated_amount, allocated_currency, country, city,
  url_mode, order_index, confirmed_at, target_hash, version,
  created_at, updated_at
) VALUES (
  'e5000000-0000-4000-8000-000000000013',
  'e5000000-0000-4000-8000-000000000002',
  'e5000000-0000-4000-8000-000000000011',
  'e5000000-0000-4000-8000-000000000001',
  'Phase 5 Unselected Product',
  '구매하지 않고 명시적으로 제외할 두 번째 상품군',
  'unselected-test-product',
  10,
  'USD',
  'US',
  'Seattle',
  'NONE',
  1,
  now(),
  'phase5-e2e-unselected-target-hash',
  1,
  now(),
  now()
);

INSERT INTO shopping_sessions(
  id, plan_target_id, user_id, target_snapshot, research_scope_snapshot,
  status, version, created_at, updated_at
) VALUES (
  'e5000000-0000-4000-8000-000000000004',
  'e5000000-0000-4000-8000-000000000003',
  'e5000000-0000-4000-8000-000000000001',
  '{"id":"e5000000-0000-4000-8000-000000000003","planId":"e5000000-0000-4000-8000-000000000002","curationId":"e5000000-0000-4000-8000-000000000011","title":"Phase 5 E2E Test Product","normalizedIntent":"Amazon US 50 USD test product","category":"test-product","allocatedBudget":{"amount":"50","currency":"USD"},"country":"US","city":"Seattle","urlMode":"EXACT_PRODUCT","referenceUrl":"https://www.amazon.com/dp/PHASE5E2E"}'::jsonb,
  '{"budget":{"amount":"50","currency":"USD"},"country":"US","city":"Seattle","allowedItems":[],"blockedItems":[]}'::jsonb,
  'REVIEWING',
  4,
  now(),
  now()
);

INSERT INTO shopping_sessions(
  id, plan_target_id, user_id, target_snapshot, research_scope_snapshot,
  status, version, created_at, updated_at
) VALUES (
  'e5000000-0000-4000-8000-000000000014',
  'e5000000-0000-4000-8000-000000000013',
  'e5000000-0000-4000-8000-000000000001',
  '{"id":"e5000000-0000-4000-8000-000000000013","planId":"e5000000-0000-4000-8000-000000000002","curationId":"e5000000-0000-4000-8000-000000000011","title":"Phase 5 Unselected Product","normalizedIntent":"구매하지 않고 명시적으로 제외할 두 번째 상품군","category":"unselected-test-product","allocatedBudget":{"amount":"10","currency":"USD"},"country":"US","city":"Seattle","urlMode":"NONE"}'::jsonb,
  '{"budget":{"amount":"10","currency":"USD"},"country":"US","city":"Seattle","allowedItems":[],"blockedItems":[]}'::jsonb,
  'READY',
  1,
  now(),
  now()
);

INSERT INTO research_rounds(
  id, shopping_session_id, user_id, round_number,
  context_schema, context_version, context_hash, context_snapshot,
  status, created_at, completed_at
) VALUES (
  'e5000000-0000-4000-8000-000000000006',
  'e5000000-0000-4000-8000-000000000004',
  'e5000000-0000-4000-8000-000000000001',
  1,
  'vitlane.research-context.v1',
  1,
  'phase5-e2e-context-hash',
  '{}'::jsonb,
  'RESULTS_READY',
  now(),
  now()
);

-- The Round's own IntelligenceJob. It carries the PLANNING_START_CURATING
-- action that opened the Round, which is the lineage the workspace projects.
INSERT INTO intelligence_jobs(
  id, user_id, curation_id, curation_action_id, plan_id,
  target_kind, research_round_id, provider, model_key,
  status, attempt_count, created_at, updated_at, completed_at
) VALUES (
  'e5000000-0000-4000-8000-000000000005',
  'e5000000-0000-4000-8000-000000000001',
  'e5000000-0000-4000-8000-000000000011',
  'e5000000-0000-4000-8000-000000000023',
  'e5000000-0000-4000-8000-000000000002',
  'RESEARCH_ROUND',
  'e5000000-0000-4000-8000-000000000006',
  'MANAGED',
  'gpt-5-nano',
  'SUCCEEDED',
  1,
  now(),
  now(),
  now()
);

INSERT INTO research_submissions(
  id, research_round_id, intelligence_job_id, client_submission_id,
  schema_version, context_version, context_hash, submission_hash,
  payload, outcome, validation_status, submitted_at
) VALUES (
  'e5000000-0000-4000-8000-000000000007',
  'e5000000-0000-4000-8000-000000000006',
  'e5000000-0000-4000-8000-000000000005',
  'phase5-e2e-submission',
  'vitlane.research-submission.v3',
  1,
  'phase5-e2e-context-hash',
  'phase5-e2e-submission-hash',
  '{}'::jsonb,
  'RESULTS',
  'VALID',
  now()
);

INSERT INTO candidates(
  id, research_submission_id, shopping_session_id,
  product_url, merchant_domain, category, name, description, image_url,
  price_amount, price_currency, evidence, observed_at,
  order_support, variant_discovery, orderability, eligibility,
  candidate_hash_schema, candidate_hash,
  order_index, created_at
) VALUES (
  'e5000000-0000-4000-8000-000000000008',
  'e5000000-0000-4000-8000-000000000007',
  'e5000000-0000-4000-8000-000000000004',
  'https://www.amazon.com/dp/PHASE5E2E',
  'amazon.com',
  'test-product',
  'Phase 5 E2E Test Product',
  '실제 주문을 만들지 않는 결합 E2E 후보',
  '',
  50,
  'USD',
  '{"summary":"Phase 5 E2E fixture","matchedCriteria":["50 USD"],"tradeoffs":["shipping and tax not quoted"],"sourceUrls":["https://www.amazon.com/dp/PHASE5E2E"]}'::jsonb,
  now(),
  'UNKNOWN',
  '{"schemaVersion":"vitlane.variant-discovery.v1","status":"OBSERVED_PARTIAL","fields":[{"key":"color","label":"Color","inputKind":"ENUM_OR_VALUE","required":true,"knownValues":[{"value":"black","label":"Black"}],"source":"AGENT_OBSERVATION","discoveryStatus":"OBSERVED_PARTIAL"}],"providerVariantRefs":[],"observedAt":"2026-07-25T00:00:00Z","evidence":{"summary":"Phase 5 E2E fixture","sourceUrls":["https://www.amazon.com/dp/PHASE5E2E"]}}'::jsonb,
  '{"schemaVersion":"vitlane.orderability.v1","providerKind":"GENERIC_WEB","executionMode":"MANUAL_MERCHANT_ORDER","externalEffect":"SIMULATED","liveOrderability":"UNVERIFIED","settlementStatus":"SUPPORTED","status":"OPTIONS_REQUIRED","reasonCodes":["VARIANT_CONFIGURATION_REQUIRED"]}'::jsonb,
  '{"eligible":true,"reasonCodes":[]}'::jsonb,
  'vitlane.candidate.v3',
  'phase5-e2e-candidate-hash',
  0,
  now()
);

INSERT INTO candidate_configurations(
  id, shopping_session_id, candidate_id, user_id,
  schema_version, fields, selections, confirms_no_options,
  configuration_hash, created_at
) VALUES (
  'e5000000-0000-4000-8000-000000000010',
  'e5000000-0000-4000-8000-000000000004',
  'e5000000-0000-4000-8000-000000000008',
  'e5000000-0000-4000-8000-000000000001',
  'vitlane.candidate-configuration.v1',
  '[{"key":"color","label":"Color","inputKind":"ENUM_OR_VALUE","required":true,"knownValues":[{"value":"black","label":"Black"}],"source":"AGENT_OBSERVATION","discoveryStatus":"OBSERVED_PARTIAL"}]'::jsonb,
  '[{"key":"color","label":"Color","value":"black","valueLabel":"Black","source":"USER_CONFIRMED"}]'::jsonb,
  false,
  'phase5-e2e-configuration-hash',
  now()
);

UPDATE research_rounds
SET result_submission_id = 'e5000000-0000-4000-8000-000000000007'
WHERE id = 'e5000000-0000-4000-8000-000000000006';

UPDATE shopping_sessions
SET current_research_round_id = 'e5000000-0000-4000-8000-000000000006'
WHERE id = 'e5000000-0000-4000-8000-000000000004';

-- A separate researched item drives the application refund path without changing
-- the successful purchase/cart assertions above.
INSERT INTO shopping_plans(
  id, user_id, original_intent, plan_mode, execution_mode,
  budget_amount, budget_currency, country, city, created_at
) VALUES (
  'e5010000-0000-4000-8000-000000000002',
  'e5000000-0000-4000-8000-000000000001',
  'Amazon US에서 40달러 환불 테스트 상품 조사',
  'SINGLE',
  'EXPERIMENT',
  40,
  'USD',
  'US',
  'Seattle',
  now()
);

INSERT INTO curations(
  id, shopping_plan_id, user_id, phase, version,
  created_at, updated_at
) VALUES (
  'e5010000-0000-4000-8000-000000000011',
  'e5010000-0000-4000-8000-000000000002',
  'e5000000-0000-4000-8000-000000000001',
  'CURATING',
  2,
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
    'e5010000-0000-4000-8000-000000000021',
    'e5010000-0000-4000-8000-000000000011',
    'e5000000-0000-4000-8000-000000000001',
    'INTENT_NEXT_STEP',
    'HAVING_INTENT',
    'PLANNING',
    'INTENT',
    NULL,
    'NONE',
    'SHOPPING_PLAN',
    'e5010000-0000-4000-8000-000000000002',
    1,
    sha256(convert_to('phase5-refund-intent-next-step', 'UTF8')),
    now() - interval '2 seconds'
  ),
  (
    'e5010000-0000-4000-8000-000000000022',
    'e5010000-0000-4000-8000-000000000011',
    'e5000000-0000-4000-8000-000000000001',
    'PLANNING_START_CURATING',
    'PLANNING',
    'CURATING',
    'CURATION',
    'e5010000-0000-4000-8000-000000000011',
    'INTELLIGENCE',
    'RESEARCH_START_REQUEST',
    'e5010000-0000-4000-8000-000000000022',
    1,
    sha256(convert_to('phase5-refund-planning-next-step', 'UTF8')),
    now() - interval '1 second'
  );

INSERT INTO plan_targets(
  id, plan_id, curation_id, user_id, title, normalized_intent, category,
  allocated_amount, allocated_currency, country, city,
  url_mode, order_index, confirmed_at, target_hash, version,
  created_at, updated_at
) VALUES (
  'e5010000-0000-4000-8000-000000000003',
  'e5010000-0000-4000-8000-000000000002',
  'e5010000-0000-4000-8000-000000000011',
  'e5000000-0000-4000-8000-000000000001',
  'Phase 5 Refund Test Product',
  'Amazon US 40 USD refund test product',
  'test-product',
  40,
  'USD',
  'US',
  'Seattle',
  'EXACT_PRODUCT',
  0,
  now(),
  'phase5-refund-target-hash',
  1,
  now(),
  now()
);

INSERT INTO shopping_sessions(
  id, plan_target_id, user_id, target_snapshot, research_scope_snapshot,
  status, version, created_at, updated_at
) VALUES (
  'e5010000-0000-4000-8000-000000000004',
  'e5010000-0000-4000-8000-000000000003',
  'e5000000-0000-4000-8000-000000000001',
  '{"id":"e5010000-0000-4000-8000-000000000003","planId":"e5010000-0000-4000-8000-000000000002","curationId":"e5010000-0000-4000-8000-000000000011","title":"Phase 5 Refund Test Product","normalizedIntent":"Amazon US 40 USD refund test product","category":"test-product","allocatedBudget":{"amount":"40","currency":"USD"},"country":"US","city":"Seattle","urlMode":"EXACT_PRODUCT","referenceUrl":"https://www.amazon.com/dp/PHASE5REFUND"}'::jsonb,
  '{"budget":{"amount":"40","currency":"USD"},"country":"US","city":"Seattle","allowedItems":[],"blockedItems":[]}'::jsonb,
  'REVIEWING',
  4,
  now(),
  now()
);

INSERT INTO research_rounds(
  id, shopping_session_id, user_id, round_number,
  context_schema, context_version, context_hash, context_snapshot,
  status, created_at, completed_at
) VALUES (
  'e5010000-0000-4000-8000-000000000006',
  'e5010000-0000-4000-8000-000000000004',
  'e5000000-0000-4000-8000-000000000001',
  1,
  'vitlane.research-context.v1',
  1,
  'phase5-refund-context-hash',
  '{}'::jsonb,
  'RESULTS_READY',
  now(),
  now()
);

INSERT INTO intelligence_jobs(
  id, user_id, curation_id, curation_action_id, plan_id,
  target_kind, research_round_id, provider, model_key,
  status, attempt_count, created_at, updated_at, completed_at
) VALUES (
  'e5010000-0000-4000-8000-000000000005',
  'e5000000-0000-4000-8000-000000000001',
  'e5010000-0000-4000-8000-000000000011',
  'e5010000-0000-4000-8000-000000000022',
  'e5010000-0000-4000-8000-000000000002',
  'RESEARCH_ROUND',
  'e5010000-0000-4000-8000-000000000006',
  'MANAGED',
  'gpt-5-nano',
  'SUCCEEDED',
  1,
  now(),
  now(),
  now()
);

INSERT INTO research_submissions(
  id, research_round_id, intelligence_job_id, client_submission_id,
  schema_version, context_version, context_hash, submission_hash,
  payload, outcome, validation_status, submitted_at
) VALUES (
  'e5010000-0000-4000-8000-000000000007',
  'e5010000-0000-4000-8000-000000000006',
  'e5010000-0000-4000-8000-000000000005',
  'phase5-refund-submission',
  'vitlane.research-submission.v3',
  1,
  'phase5-refund-context-hash',
  'phase5-refund-submission-hash',
  '{}'::jsonb,
  'RESULTS',
  'VALID',
  now()
);

INSERT INTO candidates(
  id, research_submission_id, shopping_session_id,
  product_url, merchant_domain, category, name, description, image_url,
  price_amount, price_currency, evidence, observed_at,
  order_support, variant_discovery, orderability, eligibility,
  candidate_hash_schema, candidate_hash,
  order_index, created_at
) VALUES (
  'e5010000-0000-4000-8000-000000000008',
  'e5010000-0000-4000-8000-000000000007',
  'e5010000-0000-4000-8000-000000000004',
  'https://www.amazon.com/dp/PHASE5REFUND',
  'amazon.com',
  'test-product',
  'Phase 5 Refund Test Product',
  '실제 주문 없이 application refund를 검증하는 결합 E2E 후보',
  '',
  40,
  'USD',
  '{"summary":"Phase 5 refund E2E fixture","matchedCriteria":["40 USD"],"tradeoffs":["shipping and tax not quoted"],"sourceUrls":["https://www.amazon.com/dp/PHASE5REFUND"]}'::jsonb,
  now(),
  'UNKNOWN',
  '{"schemaVersion":"vitlane.variant-discovery.v1","status":"OBSERVED_PARTIAL","fields":[{"key":"color","label":"Color","inputKind":"ENUM_OR_VALUE","required":true,"knownValues":[{"value":"black","label":"Black"}],"source":"AGENT_OBSERVATION","discoveryStatus":"OBSERVED_PARTIAL"}],"providerVariantRefs":[],"observedAt":"2026-07-25T00:00:00Z","evidence":{"summary":"Phase 5 refund E2E fixture","sourceUrls":["https://www.amazon.com/dp/PHASE5REFUND"]}}'::jsonb,
  '{"schemaVersion":"vitlane.orderability.v1","providerKind":"GENERIC_WEB","executionMode":"MANUAL_MERCHANT_ORDER","externalEffect":"SIMULATED","liveOrderability":"UNVERIFIED","settlementStatus":"SUPPORTED","status":"OPTIONS_REQUIRED","reasonCodes":["VARIANT_CONFIGURATION_REQUIRED"]}'::jsonb,
  '{"eligible":true,"reasonCodes":[]}'::jsonb,
  'vitlane.candidate.v3',
  'phase5-refund-candidate-hash',
  0,
  now()
);

INSERT INTO candidate_configurations(
  id, shopping_session_id, candidate_id, user_id,
  schema_version, fields, selections, confirms_no_options,
  configuration_hash, created_at
) VALUES (
  'e5010000-0000-4000-8000-000000000010',
  'e5010000-0000-4000-8000-000000000004',
  'e5010000-0000-4000-8000-000000000008',
  'e5000000-0000-4000-8000-000000000001',
  'vitlane.candidate-configuration.v1',
  '[{"key":"color","label":"Color","inputKind":"ENUM_OR_VALUE","required":true,"knownValues":[{"value":"black","label":"Black"}],"source":"AGENT_OBSERVATION","discoveryStatus":"OBSERVED_PARTIAL"}]'::jsonb,
  '[{"key":"color","label":"Color","value":"black","valueLabel":"Black","source":"USER_CONFIRMED"}]'::jsonb,
  false,
  'phase5-refund-configuration-hash',
  now()
);

UPDATE research_rounds
SET result_submission_id = 'e5010000-0000-4000-8000-000000000007'
WHERE id = 'e5010000-0000-4000-8000-000000000006';

UPDATE shopping_sessions
SET current_research_round_id = 'e5010000-0000-4000-8000-000000000006'
WHERE id = 'e5010000-0000-4000-8000-000000000004';

INSERT INTO curation_selections(
  id, user_id, curation_id, plan_target_id, shopping_session_id,
  candidate_id, candidate_configuration_id,
  candidate_configuration_hash, quantity, version,
  selected_at, updated_at
) VALUES
  (
    'e5000000-0000-4000-8000-000000000012',
    'e5000000-0000-4000-8000-000000000001',
    'e5000000-0000-4000-8000-000000000011',
    'e5000000-0000-4000-8000-000000000003',
    'e5000000-0000-4000-8000-000000000004',
    'e5000000-0000-4000-8000-000000000008',
    'e5000000-0000-4000-8000-000000000010',
    'phase5-e2e-configuration-hash',
    1,
    1,
    now(),
    now()
  ),
  (
    'e5010000-0000-4000-8000-000000000012',
    'e5000000-0000-4000-8000-000000000001',
    'e5010000-0000-4000-8000-000000000011',
    'e5010000-0000-4000-8000-000000000003',
    'e5010000-0000-4000-8000-000000000004',
    'e5010000-0000-4000-8000-000000000008',
    'e5010000-0000-4000-8000-000000000010',
    'phase5-refund-configuration-hash',
    1,
    1,
    now(),
    now()
  );

-- Human review profiles stay separate from the automated E2E graph. The local
-- review reset endpoint clears their user-owned data at each harness start.
INSERT INTO users(id, status, email, display_name, created_at, updated_at)
VALUES
  (
    'e5000000-0000-4000-8000-000000000101',
    'ACTIVE',
    'local-empty-user@example.com',
    '빈 일반 사용자',
    now(),
    now()
  ),
  (
    'e5000000-0000-4000-8000-000000000102',
    'ACTIVE',
    'local-empty-operator@example.com',
    '빈 운영자 사용자',
    now(),
    now()
  ),
  (
    'e5000000-0000-4000-8000-000000000103',
    'ACTIVE',
    'local-multi-product@example.com',
    '다중 상품 구매 시나리오',
    now(),
    now()
  )
ON CONFLICT (id) DO UPDATE
SET status=EXCLUDED.status,
    email=EXCLUDED.email,
    display_name=EXCLUDED.display_name,
    updated_at=EXCLUDED.updated_at;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM shopping_sessions AS session
    JOIN plan_targets AS target ON target.id=session.plan_target_id
    WHERE target.plan_id IN (
      'e5000000-0000-4000-8000-000000000002',
      'e5010000-0000-4000-8000-000000000002'
    )
      AND (
        session.target_snapshot->>'id' <> target.id::text
        OR session.target_snapshot->>'planId' <> target.plan_id::text
      )
  ) THEN
    RAISE EXCEPTION 'Phase 5 fixture target snapshot identity mismatch';
  END IF;
END $$;

-- SQL fixtures bypass the application creator and are loaded after migrations.
-- Preserve their historical prices as provenance without enabling budgets.
INSERT INTO curation_budgets(curation_id, currency, allocations)
SELECT c.id, p.budget_currency,
  COALESCE((SELECT jsonb_agg(jsonb_build_object('targetId',t.id,'quantity',1,'amount',NULL) ORDER BY t.order_index)
    FROM plan_targets t WHERE t.curation_id=c.id AND t.removed_at IS NULL),'[]'::jsonb)
FROM curations c JOIN shopping_plans p ON p.id=c.shopping_plan_id
WHERE c.id IN ('e5000000-0000-4000-8000-000000000011','e5010000-0000-4000-8000-000000000011');

COMMIT;
