package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	c "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	pp "github.com/vitlane/vitlane/server/internal/curation/planning/infra/postgres"
	sa "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	sp "github.com/vitlane/vitlane/server/internal/curation/research/session/infra/postgres"
	shared "github.com/vitlane/vitlane/server/internal/shared/app"
	dbp "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// responseTestResponder records what a reply call was allowed to see and
// answers with a body the domain validator accepts.
type responseTestResponder struct {
	mu    sync.Mutex
	seen  []c.ResponseContext
	fails bool
}

func (r *responseTestResponder) Respond(_ context.Context, in c.ResponseContext) (d.ActionResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, in)
	if r.fails {
		return d.ActionResponse{}, errors.New("model unavailable")
	}
	body := "저장된 정보로는 답하기 어려워요."
	if len(in.References) > 0 {
		body = "[[" + in.References[0].Ref + "]] 후보가 기준에 가장 잘 맞았어요."
	}
	return d.NewActionResponse(in.Kind, body, in.Locale, "test-model", in.References, time.Now().UTC())
}
func (r *responseTestResponder) last() c.ResponseContext {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[len(r.seen)-1]
}

type responseTestSource struct{ beforeRead func(context.Context) }

func (source responseTestSource) SavedCandidates(ctx context.Context, _, _ string, targets []string) (map[string][]c.SavedCandidate, error) {
	if source.beforeRead != nil {
		source.beforeRead(ctx)
	}
	price := int64(15000)
	out := map[string][]c.SavedCandidate{}
	for _, target := range targets {
		out[target] = []c.SavedCandidate{{CandidateID: "shopify:chair-1", Title: "Quiet Chair", Source: "SHOPIFY", PriceMinor: &price, Price: &c.ResponsePrice{MinimumMinor: price, MaximumMinor: price, Currency: "USD", Basis: "PRODUCT_RANGE"}, Currency: "USD", Assessed: true, TotalScore: 82, IntentPoint: "조용한 의자예요."}}
	}
	return out, nil
}

// responseTestPrimitives stands in for the Intelligence side: research
// "finishes" at once with a saved Round outcome, and a model Job is a goroutine.
type responseTestPrimitives struct {
	t        *testing.T
	database *dbp.Database
	service  *c.ThreadService
	ids      shared.UUIDGenerator
	session  string
	target   string
	plan     string
	finished chan error
}

func (p *responseTestPrimitives) exec(ctx context.Context, query string, args ...any) error {
	_, err := p.database.Queryer(ctx).ExecContext(ctx, query, args...)
	if err != nil {
		p.t.Logf("fixture statement failed: %v\n%s", err, query)
	}
	return err
}
func (p *responseTestPrimitives) ResearchThreadTarget(ctx context.Context, t d.CurationThread, a d.CurationAction, _ c.ThreadTarget) error {
	round, job := p.ids.NewID(), p.ids.NewID()
	if err := p.exec(ctx, `INSERT INTO research_rounds(id,shopping_session_id,user_id,round_number,context_schema,context_version,context_hash,context_snapshot,status,created_at,completed_at)
		VALUES($1,$2,$3,1,'vitlane.research-context.v1',1,'thread-response','{}'::jsonb,'RESULTS_READY',now()-interval '1 minute',now()+interval '1 minute')`, round, p.session, t.UserID); err != nil {
		return err
	}
	if err := p.exec(ctx, `INSERT INTO intelligence_jobs(id,user_id,curation_id,curation_action_id,plan_id,target_kind,research_round_id,provider,model_key,status,created_at,updated_at,completed_at)
		VALUES($1,$2,$3,$4,$5,'RESEARCH_ROUND',$6,'MANAGED','gpt-5.6-luna','SUCCEEDED',now(),now(),now())`, job, t.UserID, t.CurationID, a.ID, p.plan, round); err != nil {
		return err
	}
	if err := p.exec(ctx, `INSERT INTO curation_thread_jobs(job_id,thread_id,action_id) VALUES($1,$2,$3)`, job, t.ID, a.ID); err != nil {
		return err
	}
	coverage, _ := json.Marshal([]d.ResearchSourceFact{{Source: "ELEVENST", Status: "SUCCEEDED", CandidateCount: 2}, {Source: "COUPANG", Status: "SKIPPED", ReasonCode: "CATALOG_API_RATE_LIMITED"}})
	if err := p.exec(ctx, `INSERT INTO research_round_outcomes(round_id,user_id,curation_id,plan_target_id,country,mode,observed_count,duplicate_count,rejected_count,admitted_count,evaluated_count,unevaluated_count,source_coverage,completed_at)
		VALUES($1,$2,$3,$4,'KR','APPEND',9,3,1,2,2,0,$5::jsonb,now())`, round, t.UserID, t.CurationID, p.target, string(coverage)); err != nil {
		return err
	}
	if err := p.exec(ctx, `INSERT INTO phase8_research_pools(user_id,curation_id,plan_target_id,version,latest_mode,created_at,updated_at,source_coverage,provider_country,provider_query)
		VALUES($1,$2,$3,1,'APPEND',now(),now(),$4::jsonb,'KR','조용한 의자') ON CONFLICT DO NOTHING`, t.UserID, t.CurationID, p.target, string(coverage)); err != nil {
		return err
	}
	for n, id := range []string{"chair-1", "chair-2"} {
		if err := p.exec(ctx, `INSERT INTO phase8_research_candidates(user_id,curation_id,plan_target_id,candidate_id,provider_product_id,source_kind,identity_key,locator_kind,variant_id,seller_domain,display_order,first_seen_at,last_seen_at)
			VALUES($1,$2,$3,$4,$5,'SHOPIFY_LIVE',$5,'MERCHANT_VARIANT',$6,'shop.example.com',$7,now(),now())`, t.UserID, t.CurationID, p.target, "candidate-"+id, "shopify:"+id, id, n); err != nil {
			return err
		}
	}
	return nil
}
func (*responseTestPrimitives) StartThreadResearch(context.Context, d.CurationThread, string) error {
	return nil
}
func (*responseTestPrimitives) CancelThreadAction(context.Context, string, string) error { return nil }
func (p *responseTestPrimitives) StartInterpretation(_ context.Context, t d.CurationThread, a d.CurationAction) error {
	go func() {
		time.Sleep(50 * time.Millisecond)
		err := p.service.RunActionInterpretation(c.WithThreadExecution(context.Background(), t.ID, a.ID), t.UserID, t.CurationID, a.ID, a.ID, a.ID, a.InputRevision)
		if p.finished != nil {
			p.finished <- err
		}
	}()
	return nil
}

func TestThreadWritesOneReplyAfterResearchAndAnswersAQuestionWithoutWork(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	const (
		user     = "50000000-0000-4000-8000-000000000001"
		plan     = "51000000-0000-4000-8000-000000000002"
		curation = "52000000-0000-4000-8000-000000000002"
		target   = "53000000-0000-4000-8000-000000000001"
		session  = "54000000-0000-4000-8000-000000000001"
	)
	if _, err = database.DB.ExecContext(ctx, `UPDATE shopping_sessions SET status='REVIEWING' WHERE id=$1`, session); err != nil {
		t.Fatal(err)
	}
	if _, err = database.DB.ExecContext(ctx, `INSERT INTO curation_budgets(curation_id,enabled,currency,version,research_version,allocations,initial_materialized) VALUES($1,true,'USD',1,1,jsonb_build_array(jsonb_build_object('targetId',$2::text,'quantity',1,'amount','200.00')),true)`, curation, target); err != nil {
		t.Fatal(err)
	}
	ids := shared.UUIDGenerator{}
	clock := shared.SystemClock{}
	repo := NewRepository(database, pp.NewRepository(database))
	core := c.NewService(repo, sa.NewService(sp.NewRepository(database), clock, ids), database, clock, ids, slog.New(slog.NewTextHandler(io.Discard, nil)))
	interpreter := &threadTestInterpreter{make(chan c.ThreadContext, 3), make(chan c.ThreadInterpretation, 3)}
	primitives := &responseTestPrimitives{t: t, database: database, ids: ids, session: session, target: target, plan: plan, finished: make(chan error, 16)}
	service := c.NewThreadService(core, repo, interpreter, primitives)
	responder := &responseTestResponder{}
	service.SetResponder(responder, responseTestSource{})
	primitives.service = service
	runctx, stop := context.WithCancel(ctx)
	defer stop()
	go service.Run(runctx)
	wait := func(id string, predicate func(d.CurationThread) bool) d.CurationThread {
		t.Helper()
		for n := 0; n < 240; n++ {
			out, e := repo.ReadThread(ctx, user, id)
			if e != nil {
				t.Fatal(e)
			}
			if predicate(out) {
				return out
			}
			time.Sleep(50 * time.Millisecond)
		}
		out, _ := repo.ReadThread(ctx, user, id)
		t.Fatalf("thread did not converge: %+v", out)
		return d.CurationThread{}
	}

	// A manual research that adds candidates ends with one comment.
	research, err := service.Submit(ctx, user, "", curation, c.SubmitThreadInput{ID: ids.NewID(), Request: "", ExpectedCurationVersion: 2, Kind: "RESEARCH_AGAIN", TargetID: target})
	if err != nil {
		t.Fatal(err)
	}
	done := wait(research.ID, func(v d.CurationThread) bool { return !v.Active() })
	select {
	case <-primitives.finished:
	case <-ctx.Done():
		t.Fatal("comment job did not finish")
	}
	if done.Status != "SUCCEEDED" || len(done.Actions) != 2 {
		t.Fatalf("research thread=%+v", done)
	}
	job := done.Actions[0].Jobs[0]
	if job.Facts == nil || job.Facts.Observed != 9 || job.Facts.Duplicates != 3 || job.Facts.Rejected != 1 || job.Facts.Admitted != 2 || job.Facts.Evaluated != 2 ||
		len(job.Facts.Sources) != 2 || job.Facts.Sources[1].Source != "COUPANG" || job.Facts.Sources[1].ReasonCode != "CATALOG_API_RATE_LIMITED" || job.Facts.RoundID == "" {
		t.Fatalf("the receipt must carry the Round outcome: %+v", job.Facts)
	}
	reply := done.Actions[1]
	if reply.Type != d.CurationActionResponse || reply.Instruction != d.ResponseKindComment || reply.Status != "SUCCEEDED" || reply.GeneratedByActionID != done.Actions[0].ID ||
		reply.Response == nil || reply.Response.Kind != d.ResponseKindComment || len(reply.Response.References) != 1 || reply.Response.References[0].CandidateID != "shopify:chair-1" {
		t.Fatalf("comment action=%+v", reply)
	}
	seen := responder.last()
	if seen.Kind != d.ResponseKindComment || len(seen.Targets) != 1 || !seen.Targets[0].Researched || seen.Targets[0].Added != 2 || seen.Targets[0].Facts == nil ||
		seen.Targets[0].UnitBudget != "$200.00" || seen.Targets[0].Candidates[0].Budget != "WITHIN" || seen.Targets[0].Candidates[0].Price == nil || len(seen.History) != 0 {
		t.Fatalf("a comment sees the researched product, its facts and listing prices: %+v", seen)
	}
	var stored string
	if err = database.DB.QueryRowContext(ctx, `SELECT execution->'response'->>'kind' FROM curation_actions WHERE id=$1 AND action_type='RESPONSE' AND status='SUCCEEDED'`, reply.ID).Scan(&stored); err != nil || stored != d.ResponseKindComment {
		t.Fatalf("the reply is stored on its Action row: %q %v", stored, err)
	}

	// A question runs no primitive: interpretation, then the answer.
	question, err := service.Submit(ctx, user, "", curation, c.SubmitThreadInput{ID: ids.NewID(), Request: "둘 중에 뭐가 더 조용해?", ExpectedCurationVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-interpreter.calls:
	case <-ctx.Done():
		t.Fatal("interpreter not called")
	}
	interpreter.replies <- c.ThreadInterpretation{Actions: []d.CurationAction{{Type: d.CurationActionResponse, Instruction: d.ResponseKindAnswer, InputRevision: 1}},
		Decisions: []d.ActionDecision{{Kind: "ACTION", Result: "ANSWER", Source: "MANAGED", ReasonCode: "QUESTION_ANSWERED"}}}
	answered := wait(question.ID, func(v d.CurationThread) bool { return !v.Active() })
	for n := 0; n < 2; n++ {
		select {
		case <-primitives.finished:
		case <-ctx.Done():
			t.Fatal("question jobs did not finish")
		}
	}
	if answered.Status != "SUCCEEDED" || len(answered.Actions) != 2 || answered.Actions[1].Type != d.CurationActionResponse || answered.Actions[1].Response == nil || answered.Actions[1].Response.Kind != d.ResponseKindAnswer {
		t.Fatalf("answer thread=%+v", answered)
	}
	seen = responder.last()
	if seen.Kind != d.ResponseKindAnswer || len(seen.Targets) != 1 || seen.Targets[0].Candidates[0].Price == nil || seen.Targets[0].Candidates[0].Price.MinimumMinor != 15000 || len(seen.History) != 1 || seen.History[0].Request != "[button] TARGET_RESEARCH_AGAIN 업무용 의자" || seen.History[0].Reply != "Quiet Chair 후보가 기준에 가장 잘 맞았어요." {
		t.Fatalf("an answer sees saved prices and the earlier turn: %+v", seen)
	}
	budget, err := core.Budget(ctx, user, curation)
	if err != nil || budget.Version != 1 {
		t.Fatalf("a question must not change settings: %+v %v", budget, err)
	}

	// Provider I/O holds no curation lock: cancellation completes while lookup is blocked.
	entered, release := make(chan struct{}), make(chan struct{})
	service.SetResponder(responder, responseTestSource{beforeRead: func(context.Context) { close(entered); <-release }})
	cancelled, err := service.Submit(ctx, user, "", curation, c.SubmitThreadInput{ID: ids.NewID(), Request: "현재 가격은?", ExpectedCurationVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	<-interpreter.calls
	interpreter.replies <- c.ThreadInterpretation{Actions: []d.CurationAction{{Type: d.CurationActionResponse, Instruction: d.ResponseKindAnswer, InputRevision: 1}}}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("lookup was not entered")
	}
	responder.mu.Lock()
	callsBefore := len(responder.seen)
	responder.mu.Unlock()
	cancelCtx, cancelTimeout := context.WithTimeout(ctx, 2*time.Second)
	_, cancelErr := service.Cancel(cancelCtx, user, curation, cancelled.ID)
	cancelTimeout()
	close(release)
	if cancelErr != nil {
		t.Fatalf("lookup held the curation lock: %v", cancelErr)
	}
	// Both AUTO_START and RESPONSE jobs finish; the latter rejects its stale snapshot.
	for n := 0; n < 2; n++ {
		select {
		case <-primitives.finished:
		case <-ctx.Done():
			t.Fatal("cancelled response did not finish")
		}
	}
	stopped := wait(cancelled.ID, func(v d.CurationThread) bool { return !v.Active() })
	responder.mu.Lock()
	callsAfter := len(responder.seen)
	responder.mu.Unlock()
	if stopped.Status != "CANCELLED" || stopped.ResponseAction().Response != nil || callsBefore != callsAfter {
		t.Fatalf("cancelled hydration must not call the model or save a reply: %+v", stopped)
	}
}
