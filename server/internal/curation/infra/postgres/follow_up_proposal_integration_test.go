package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	c "github.com/vitlane/vitlane/server/internal/curation/app"
	cv "github.com/vitlane/vitlane/server/internal/curation/conversation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	pp "github.com/vitlane/vitlane/server/internal/curation/planning/infra/postgres"
	rd "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sa "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	sp "github.com/vitlane/vitlane/server/internal/curation/research/session/infra/postgres"
	shared "github.com/vitlane/vitlane/server/internal/shared/app"
	dbp "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// A response's proposals read the Rounds it completed: the stored assessments
// (weak axis), the Round outcomes (few new candidates, call-limited malls) and
// the pool's last provider query. Accepting one runs its stored search feedback
// as a research-again Thread without a criteria interpretation first.
func TestFollowUpProposalsReadRoundOutcomesAndAcceptanceKeepsCriteria(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	database, err := dbp.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.Migrate(ctx, "../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	conn, err := database.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended('vitlane.integration_tests',0))`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended('vitlane.integration_tests',0))`)
	if _, err = database.DB.ExecContext(ctx, `TRUNCATE users CASCADE`); err != nil {
		t.Fatal(err)
	}
	seedCurationThreadFixtureRows(t, ctx, database)
	ids := shared.UUIDGenerator{}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := database.DB.ExecContext(ctx, query, args...); e != nil {
			t.Fatal(e)
		}
	}
	const (
		user     = "50000000-0000-4000-8000-000000000001"
		plan     = "51000000-0000-4000-8000-000000000002"
		curation = "52000000-0000-4000-8000-000000000002"
		chair    = "53000000-0000-4000-8000-000000000001"
		stand    = "53000000-0000-4000-8000-000000000002"
	)
	// Both Targets were researched: their Sessions are reviewing results.
	exec(`INSERT INTO plan_targets(id,plan_id,title,normalized_intent,category,allocated_amount,allocated_currency,country,city,url_mode,order_index,target_hash,version,created_at,updated_at,curation_id,user_id)
		VALUES($1,$2,'모니터 받침대','책상 위 모니터 받침대','desk',100,'USD','KR','서울','NONE',2,'follow-up-stand',1,'2026-08-02T01:03:00Z','2026-08-02T01:03:00Z',$3,$4)`, stand, plan, curation, user)
	exec(`INSERT INTO shopping_sessions(id,plan_target_id,user_id,target_snapshot,research_scope_snapshot,status,version,created_at,updated_at)
		VALUES('54000000-0000-4000-8000-000000000002',$1,$2,'{}'::jsonb,'{}'::jsonb,'REVIEWING',1,'2026-08-02T01:04:00Z','2026-08-02T01:04:00Z')`, stand, user)
	exec(`UPDATE shopping_sessions SET status='REVIEWING' WHERE id='54000000-0000-4000-8000-000000000001'`)
	exec(`INSERT INTO curation_budgets(curation_id,enabled,currency,version,research_version,allocations,initial_materialized) VALUES($1,true,'USD',1,1,jsonb_build_array(jsonb_build_object('targetId',$2::text,'quantity',1,'amount','200.00'),jsonb_build_object('targetId',$3::text,'quantity',1,'amount','100.00')),true)`, curation, chair, stand)
	criteria := map[string]d.TargetCriteriaSetV1{
		chair: {SchemaVersion: d.CriteriaSchema, Version: 1, Subject: d.ResearchSubject{Label: "업무용 의자", ProductType: "office chair"}, Axes: []d.ResearchAxis{{AxisID: "quiet", Label: "조용함", Definition: "움직일 때 소음이 적다", Importance: 4, Origin: "REQUEST"}}, Exclusions: []string{}},
		stand: {SchemaVersion: d.CriteriaSchema, Version: 1, Subject: d.ResearchSubject{Label: "모니터 받침대", ProductType: "monitor stand"}, Axes: []d.ResearchAxis{{AxisID: "sturdy", Label: "튼튼함", Definition: "흔들림이 적다", Importance: 4, Origin: "REQUEST"}}, Exclusions: []string{}},
	}
	for target, set := range criteria {
		raw, _ := json.Marshal(set)
		exec(`INSERT INTO curation_target_criteria(user_id,curation_id,target_id,version,criteria) VALUES($1,$2,$3,1,$4::jsonb)`, user, curation, target, string(raw))
	}

	// The request precedes the research it started.
	response := ids.NewID()
	exec(`SELECT curation_admit_request($1,$2,$3,'THREAD','의자와 받침대를 찾아줘','follow-up-request','DISPATCHED',NULL)`, user, curation, response)
	action := ids.NewID()
	exec(`INSERT INTO curation_actions(id,curation_id,actor_user_id,action_type,phase_at_request,subject_type,subject_id,effect_kind,source_ref_type,source_ref_id,expected_curation_version,request_hash,created_at)
		VALUES($1,$2,$3,'CANDIDATE_INTERACTION','CURATING','CURATION',$4,'INTELLIGENCE','TEST','follow-up-proposal',2,decode(repeat('ab',32),'hex'),now())`, action, curation, user, curation)
	type sourceCoverage struct {
		Source         string `json:"source"`
		Status         string `json:"status"`
		ReasonCode     string `json:"reasonCode,omitempty"`
		CandidateCount int    `json:"candidateCount"`
	}
	researched := func(target, session string, scores []int, observed, duplicates int, coverage []sourceCoverage, query string) {
		t.Helper()
		round, job := ids.NewID(), ids.NewID()
		exec(`INSERT INTO research_rounds(id,shopping_session_id,user_id,round_number,context_schema,context_version,context_hash,context_snapshot,status,created_at,completed_at)
			VALUES($1,$2,$3,1,'vitlane.research-context.v1',1,'follow-up-context','{}'::jsonb,'RESULTS_READY',now(),now())`, round, session, user)
		exec(`INSERT INTO intelligence_jobs(id,user_id,curation_id,curation_action_id,plan_id,target_kind,research_round_id,provider,model_key,status,created_at,updated_at,completed_at)
			VALUES($1,$2,$3,$4,$5,'RESEARCH_ROUND',$6,'MANAGED','gpt-5.6-luna','SUCCEEDED',now(),now(),now())`, job, user, curation, action, plan, round)
		raw, _ := json.Marshal(coverage)
		exec(`INSERT INTO research_round_outcomes(round_id,user_id,curation_id,plan_target_id,country,mode,observed_count,duplicate_count,rejected_count,admitted_count,evaluated_count,unevaluated_count,source_coverage,completed_at)
			VALUES($1,$2,$3,$4,'KR','APPEND',$5,$6,0,$7,$7,0,$8::jsonb,now())`, round, user, curation, target, observed, duplicates, len(scores), string(raw))
		exec(`INSERT INTO phase8_research_pools(user_id,curation_id,plan_target_id,version,latest_mode,created_at,updated_at,source_coverage,provider_country,provider_query)
			VALUES($1,$2,$3,1,'APPEND',now(),now(),$4::jsonb,'KR',$5)`, user, curation, target, string(raw), query)
		for n, score := range scores {
			assessment, _ := json.Marshal(rd.AxisAssessmentV1{SchemaVersion: "vitlane.axis-assessment.v1", Criteria: criteria[target], Weights: []int{100}, Scores: []rd.AxisScoreV1{{AxisID: criteria[target].Axes[0].AxisID, ScorePercent: score, Basis: "OBSERVED", Explanation: "통합 테스트 점수", FactIDs: []string{}}}, TotalScore: score, ContentLocale: "ko-KR", RoundID: round, ModelKey: "gpt-5.6-luna", CreatedAt: time.Now().UTC()})
			variant := fmt.Sprintf("%s-%d", target[len(target)-4:], n)
			exec(`INSERT INTO phase8_research_candidates(user_id,curation_id,plan_target_id,candidate_id,provider_product_id,source_kind,identity_key,locator_kind,variant_id,seller_domain,display_order,first_seen_at,last_seen_at,axis_assessment)
				VALUES($1,$2,$3,$4,$5,'SHOPIFY_LIVE',$5,'MERCHANT_VARIANT',$6,'shop.example.com',$7,now(),now(),$8::jsonb)`, user, curation, target, "candidate-"+variant, "shopify:"+variant, variant, n, string(assessment))
		}
	}
	// Chair: one of three new candidates scored 60 or more (not a low majority),
	// and Coupang was skipped on its per-minute limit.
	researched(chair, "54000000-0000-4000-8000-000000000001", []int{70, 55, 40}, 10, 2,
		[]sourceCoverage{{Source: "ELEVENST", Status: "SUCCEEDED", CandidateCount: 3}, {Source: "COUPANG", Status: "SKIPPED", ReasonCode: "CATALOG_API_RATE_LIMITED"}, {Source: "GMARKET", Status: "SKIPPED", ReasonCode: "CATALOG_API_DISABLED"}}, "저소음 사무용 의자")
	// Stand: the search mostly returned candidates it already had.
	researched(stand, "54000000-0000-4000-8000-000000000002", []int{90}, 8, 7,
		[]sourceCoverage{{Source: "ELEVENST", Status: "SUCCEEDED", CandidateCount: 1}}, "원목 모니터 받침대")

	// The request's reply could not be written (ADR-0086). Its Job stays FAILED for operators,
	// but it is not a research failure: no error is reported and the proposals below are still offered.
	replyAction, replyJob := ids.NewID(), ids.NewID()
	exec(`INSERT INTO curation_actions(id,curation_id,actor_user_id,action_type,phase_at_request,subject_type,subject_id,effect_kind,source_ref_type,source_ref_id,expected_curation_version,request_hash,created_at)
		VALUES($1,$2,$3,'RESPONSE','CURATING','CURATION',$4,'INTELLIGENCE','TEST','follow-up-reply',2,decode(repeat('cd',32),'hex'),now())`, replyAction, curation, user, curation)
	exec(`INSERT INTO intelligence_jobs(id,user_id,curation_id,curation_action_id,plan_id,target_kind,interpretation_action_id,interpretation_revision,provider,model_key,status,failure_code,retryable,created_at,updated_at,completed_at)
		VALUES($1,$2,$3,$4,$5,'ACTION_INTERPRETATION',$4,1,'MANAGED','gpt-5.6-luna','FAILED','THREAD_RESPONSE_PRICE_FORBIDDEN',false,now(),now(),now())`, replyJob, user, curation, replyAction, plan)

	repo := NewRepository(database, pp.NewRepository(database))
	generation, err := repo.ClaimResponse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if generation == nil || generation.ResponseID != response {
		t.Fatalf("claimed %+v", generation)
	}
	var reportedErrors int
	if err = database.DB.QueryRowContext(ctx, `SELECT count(*) FROM curation_follow_ups WHERE curation_id=$1 AND kind='ERROR'`, curation).Scan(&reportedErrors); err != nil || reportedErrors != 0 {
		t.Fatalf("a reply that was not written must not read as a failed research: errors=%d err=%v", reportedErrors, err)
	}
	codes := []string{}
	byCode := map[string]d.FollowUp{}
	for _, choice := range generation.Choices {
		codes = append(codes, choice.Content.Code+":"+choice.Payload.TargetID)
		byCode[choice.Content.Code] = choice
	}
	if strings.Join(codes, ",") != d.ProposalLowAxisFit+":"+chair+","+d.ProposalSourcesRateLimited+":"+chair+","+d.ProposalFewNewCandidates+":"+stand {
		t.Fatalf("choices=%v", codes)
	}
	weak := byCode[d.ProposalLowAxisFit]
	if weak.Content.Body != "새 후보 3개 중 ‘조용함’ 평가 60점 이상은 1개뿐이에요. ‘조용함’을 더 잘 충족하는 후보를 우선해 업무용 의자를 다시 찾아볼까요?" ||
		weak.Payload.Feedback != "‘조용함’ 기준을 더 잘 충족하는 후보를 우선해서 찾아줘." || weak.Payload.SessionID != "54000000-0000-4000-8000-000000000001" || weak.Payload.CriteriaVersion != 1 {
		t.Fatalf("weak axis=%+v %+v", weak.Content, weak.Payload)
	}
	limited := byCode[d.ProposalSourcesRateLimited]
	if limited.Content.Body != "쿠팡은 호출 한도로 이번에 확인하지 못했어요. 잠시 뒤 업무용 의자를 다시 찾아볼까요?" || limited.Payload.Feedback != "" ||
		limited.Content.AvailableAt == nil || time.Until(*limited.Content.AvailableAt) < 50*time.Second {
		t.Fatalf("rate limited=%+v %+v", limited.Content, limited.Payload)
	}
	few := byCode[d.ProposalFewNewCandidates]
	if few.Content.Body != "검색 결과 8개 중 7개가 이미 있던 후보라 새 후보는 1개였어요. 다른 표현과 브랜드로 모니터 받침대를 넓혀 다시 찾아볼까요?" ||
		few.Payload.Feedback != "이전 검색어와 다른 표현·브랜드·하위 종류로 넓혀서 찾아줘. 이전 검색어: 원목 모니터 받침대" || few.Payload.SessionID != "54000000-0000-4000-8000-000000000002" {
		t.Fatalf("few new=%+v %+v", few.Content, few.Payload)
	}
	var generating bool
	if err = database.DB.QueryRowContext(ctx, `SELECT generating FROM curation_conversation_requests WHERE id=$1`, response).Scan(&generating); err != nil || !generating {
		t.Fatalf("generating=%t err=%v", generating, err)
	}

	// The selection chose the few-new proposal; it waits for the user.
	if err = repo.FinishGeneration(ctx, generation, &few); err != nil {
		t.Fatal(err)
	}
	conversation, err := repo.Read(ctx, user, curation)
	if err != nil {
		t.Fatal(err)
	}
	var message d.FollowUp
	for _, m := range conversation.Messages {
		if m.Kind == "PROPOSAL" {
			message = m
		}
	}
	if message.Status != "PENDING" || message.Content.Code != d.ProposalFewNewCandidates || message.Content.Body != few.Content.Body {
		t.Fatalf("stored proposal=%+v", message)
	}

	clock := shared.SystemClock{}
	core := c.NewService(repo, sa.NewService(sp.NewRepository(database), clock, ids), database, clock, ids, slog.New(slog.NewTextHandler(io.Discard, nil)))
	threads := c.NewThreadService(core, repo, &threadTestInterpreter{}, &threadTestPrimitives{})
	service := &cv.Service{Threads: threads, Repository: repo, Tx: database}
	accept := ids.NewID()
	if err = service.Respond(ctx, cv.ResponseInput{UserID: user, AuthSessionID: "auth-session-1", CurationID: curation, MessageID: message.ID, Response: "ACCEPT", ClientRequestID: accept, ExpectedVersion: message.Version}); err != nil {
		t.Fatal(err)
	}
	thread, err := repo.ReadThread(ctx, user, accept)
	if err != nil {
		t.Fatal(err)
	}
	if thread.Request != few.Payload.Feedback || len(thread.Actions) != 1 {
		t.Fatalf("accepted thread request=%q actions=%+v", thread.Request, thread.Actions)
	}
	research := thread.Actions[0]
	if research.Type != d.CurationActionTargetResearchAgain || research.TargetID != stand || research.Instruction != few.Payload.Feedback || research.Status != "PENDING" {
		t.Fatalf("accepted research action=%+v", research)
	}
	if conversation, err = repo.Read(ctx, user, curation); err != nil {
		t.Fatal(err)
	}
	for _, m := range conversation.Messages {
		if m.ID == message.ID && m.Status != "ACCEPTED" {
			t.Fatalf("proposal after accept=%+v", m)
		}
	}
}
