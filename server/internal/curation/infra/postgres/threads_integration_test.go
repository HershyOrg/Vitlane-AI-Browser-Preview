package postgres

import (
	"context"
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	cv "github.com/vitlane/vitlane/server/internal/curation/conversation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	ip "github.com/vitlane/vitlane/server/internal/curation/intelligence/infra/postgres"
	pp "github.com/vitlane/vitlane/server/internal/curation/planning/infra/postgres"
	sa "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	sp "github.com/vitlane/vitlane/server/internal/curation/research/session/infra/postgres"
	shared "github.com/vitlane/vitlane/server/internal/shared/app"
	dbp "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

type threadTestInterpreter struct {
	calls   chan c.ThreadContext
	replies chan c.ThreadInterpretation
}

func (i *threadTestInterpreter) Interpret(ctx context.Context, in c.ThreadContext) (c.ThreadInterpretation, error) {
	select {
	case i.calls <- in:
	case <-ctx.Done():
		return c.ThreadInterpretation{}, ctx.Err()
	}
	out := <-i.replies
	return out, nil
}

type threadTestPrimitives struct{ service *c.ThreadService }

func (threadTestPrimitives) ResearchThreadTarget(context.Context, d.CurationThread, d.CurationAction, c.ThreadTarget) error {
	return d.ErrTargetNotFound
}
func (threadTestPrimitives) StartThreadResearch(context.Context, d.CurationThread, string) error {
	return nil
}
func (threadTestPrimitives) CancelThreadAction(context.Context, string, string) error { return nil }
func TestCurationThreadSelectionCancellationAndPrimitiveAtomicity(t *testing.T) {
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
	user, curation, target := "50000000-0000-4000-8000-000000000001", "52000000-0000-4000-8000-000000000002", "53000000-0000-4000-8000-000000000001"
	if _, err = database.DB.ExecContext(ctx, `INSERT INTO curation_budgets(curation_id,enabled,currency,version,research_version,allocations,initial_materialized) VALUES($1,true,'USD',1,1,jsonb_build_array(jsonb_build_object('targetId',$2::text,'quantity',1,'amount','200.00')),true)`, curation, target); err != nil {
		t.Fatal(err)
	}
	ids := shared.UUIDGenerator{}
	clock := shared.SystemClock{}
	repo := NewRepository(database, pp.NewRepository(database))
	core := c.NewService(repo, sa.NewService(sp.NewRepository(database), clock, ids), database, clock, ids, slog.New(slog.NewTextHandler(io.Discard, nil)))
	interpreter := &threadTestInterpreter{make(chan c.ThreadContext, 3), make(chan c.ThreadInterpretation, 3)}
	primitives := &threadTestPrimitives{}
	service := c.NewThreadService(core, repo, interpreter, primitives)
	primitives.service = service
	read := func(id string) d.CurationThread {
		t.Helper()
		out, e := repo.ReadThread(ctx, user, id)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	wait := func(id string, predicate func(d.CurationThread) bool) d.CurationThread {
		t.Helper()
		for n := 0; n < 160; n++ {
			out := read(id)
			if predicate(out) {
				return out
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("thread did not converge: %+v", read(id))
		return d.CurationThread{}
	}
	input := c.SubmitThreadInput{ID: ids.NewID(), Request: "의자 예산을 바꿔줘", ExpectedCurationVersion: 2}
	initial, err := service.Submit(ctx, user, "", curation, input)
	if err != nil {
		t.Fatal(err)
	}
	// The new request owns one legacy conversation row; old manual/follow-up intake
	// cannot enter while interpretation has no child job yet.
	var conversationCount int
	if e := database.DB.QueryRowContext(ctx, `SELECT count(*) FROM curation_conversation_requests WHERE id=$1 AND mode='THREAD'`, initial.ID).Scan(&conversationCount); e != nil || conversationCount != 1 {
		t.Fatalf("conversation intake=%d %v", conversationCount, e)
	}
	if e := database.WithinTransaction(ctx, func(tx context.Context) error {
		_, e := repo.AdmitManual(tx, cv.RequestInput{UserID: user, CurationID: curation, ClientRequestID: ids.NewID(), Mode: "RESEARCH_AGAIN", Request: "retry"})
		return e
	}); e == nil {
		t.Fatal("legacy intake bypassed interpreting thread")
	}
	if initial.Status != "INTERPRETING" {
		t.Fatal(initial.Status)
	}
	if _, err = service.Submit(ctx, user, "", curation, input); err != nil {
		t.Fatalf("request replay: %v", err)
	}
	conflicting := input
	conflicting.ID = ids.NewID()
	if _, err = service.Submit(ctx, user, "", curation, conflicting); err == nil {
		t.Fatal("parallel mutation admitted during interpretation")
	}
	counter := ip.NewRepository(database, ids)
	counter.EnableThreads()
	if n, e := counter.CountActiveActions(ctx, user); e != nil || n != 1 {
		t.Fatalf("thread count=%d %v", n, e)
	}
	if _, err = service.ChangeMode(ctx, user, curation, d.ControlMode{Mode: "MANUAL", Version: 1}); err == nil {
		t.Fatal("mode mutation during interpretation")
	}
	if _, err = service.ChangeMode(ctx, user, "52000000-0000-4000-8000-000000000001", d.ControlMode{Mode: "MANUAL", Version: 1}); err != nil {
		t.Fatal(err)
	}
	current, err := service.Mode(ctx, user, curation)
	if err != nil || current.Mode != "AUTO" {
		t.Fatal("another curation changed this mode")
	}
	runctx, stop := context.WithCancel(ctx)
	defer stop()
	go service.Run(runctx)
	select {
	case <-interpreter.calls:
	case <-ctx.Done():
		t.Fatal("interpreter not called")
	}
	amount := "125.00"
	command := d.BudgetCommand{Kind: "SET_TARGET", TargetID: target, Amount: &amount}
	plan := []d.CurationAction{{Type: "BUDGET_CHANGE", Budget: &command}, {Type: "TARGET_RESEARCH_AGAIN", TargetID: target}}
	interpreter.replies <- c.ThreadInterpretation{Question: &d.ThreadQuestion{Prompt: "어느 예산인가요?", Options: []d.ThreadOption{{Label: "의자", Actions: plan, Decisions: []d.ActionDecision{{Kind: "BUDGET", Result: "SET_TARGET", Source: "MANAGED", Evidence: "예산", ReasonCode: "TEST"}}}, {Label: "다시 설명", Actions: plan, Decisions: []d.ActionDecision{{Kind: "BUDGET", Result: "SET_TARGET", Source: "MANAGED", ReasonCode: "TEST"}}}}}}
	awaiting := wait(initial.ID, func(v d.CurationThread) bool { return v.Status == "WAITING_SELECTION" })
	if awaiting.Actions == nil || len(awaiting.Actions) != 1 {
		t.Fatal("question executed work")
	}
	if err = database.WithinTransaction(ctx, func(tx context.Context) error { return repo.GuardThreadMutation(tx, user, curation) }); err == nil {
		t.Fatal("selection wait did not block mutations")
	}
	answer := d.ThreadAnswer{Revision: awaiting.Revision, QuestionID: awaiting.Actions[0].Question.ID, OptionID: awaiting.Actions[0].Question.Options[0].ID}
	selected, err := service.Answer(ctx, user, curation, initial.ID, answer)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Actions[0].Questions) != 1 || selected.Actions[0].Questions[0].ID != answer.QuestionID {
		t.Fatal("question history lost")
	}
	if len(selected.Actions[0].Decisions) < 2 || selected.Actions[0].Decisions[0].Kind != "BUDGET" || len(selected.Actions[0].Decisions[0].ActionIDs) != 1 {
		t.Fatalf("lost selected decision: %+v", selected.Actions[0].Decisions)
	}
	if _, err = service.Answer(ctx, user, curation, initial.ID, answer); err != nil {
		t.Fatal("answer replay failed", err)
	}
	wrong := answer
	wrong.OptionID = awaiting.Actions[0].Question.Options[1].ID
	if _, err = service.Answer(ctx, user, curation, initial.ID, wrong); err == nil {
		t.Fatal("stale answer accepted")
	}
	partial := wait(initial.ID, func(v d.CurationThread) bool { return len(v.Actions) > 1 && v.Actions[1].Status == "SUCCEEDED" })
	if partial.Status != "RUNNING" || len(partial.Actions[1].Effects) != 1 {
		t.Fatalf("no per-primitive commit: %+v", partial)
	}
	stopped, err := service.Cancel(ctx, user, curation, initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Actions[1].Status != "SUCCEEDED" || stopped.Actions[2].Status != "SKIPPED" {
		t.Fatal("cancel lost completed result")
	}
	budget, err := core.Budget(ctx, user, curation)
	if err != nil || budget.TotalAmount == nil || *budget.TotalAmount != "125.00" {
		t.Fatalf("budget rollback crossed primitive boundary: %+v %v", budget, err)
	}
	if _, err = service.Cancel(ctx, user, curation, initial.ID); err != nil {
		t.Fatal("cancel replay", err)
	}
	var responseReady bool
	if e := database.DB.QueryRowContext(ctx, `SELECT response_ready FROM curation_conversation_requests WHERE id=$1`, initial.ID).Scan(&responseReady); e != nil || !responseReady {
		t.Fatalf("budget-only thread left conversation unfinished: %v %v", responseReady, e)
	}
	// Cancellation while provider is still returning: no late plan or side effect.
	input.ID = ids.NewID()
	late, err := service.Submit(ctx, user, "", curation, input)
	if err != nil {
		t.Fatal(err)
	}
	<-interpreter.calls
	if _, err = service.Cancel(ctx, user, curation, late.ID); err != nil {
		t.Fatal(err)
	}
	interpreter.replies <- c.ThreadInterpretation{Actions: plan}
	time.Sleep(150 * time.Millisecond)
	if read(late.ID).Status != "CANCELLED" {
		t.Fatal("late AI result reopened request")
	}
	// A rejected primitive must release the curation, preserving prior receipts.
	input.ID = ids.NewID()
	failed, err := service.Submit(ctx, user, "", curation, input)
	if err != nil {
		t.Fatal(err)
	}
	<-interpreter.calls
	invalid := command
	invalid.TargetID = ids.NewID()
	interpreter.replies <- c.ThreadInterpretation{Actions: []d.CurationAction{{Type: "BUDGET_CHANGE", Budget: &invalid}}}
	terminal := wait(failed.ID, func(v d.CurationThread) bool { return v.Status == "FAILED" })
	if terminal.Actions[1].Status != "FAILED" {
		t.Fatal("primitive failure not recorded")
	}
	if n, e := counter.CountActiveActions(ctx, user); e != nil || n != 0 {
		t.Fatalf("terminal requests occupy slots: %d %v", n, e)
	}
	budget, _ = core.Budget(ctx, user, curation)
	if *budget.TotalAmount != "125.00" {
		t.Fatal("failed primitive leaked a change")
	}
	// Empty feedback is preserved across locales and bypasses Auto interpretation.
	manual, err := service.Submit(ctx, user, "", curation, c.SubmitThreadInput{ID: ids.NewID(), Request: "", ExpectedCurationVersion: 2, Kind: "RESEARCH_AGAIN", TargetID: target})
	if err != nil || manual.Origin != "MANUAL" || manual.Status != "RUNNING" || manual.Request != "" || manual.Actions[0].Instruction != "" {
		t.Fatalf("manual blank feedback=%+v %v", manual, err)
	}
	if _, err = service.Cancel(ctx, user, curation, manual.ID); err != nil {
		t.Fatal(err)
	}

}

func (p *threadTestPrimitives) StartInterpretation(ctx context.Context, t d.CurationThread, a d.CurationAction) error {
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = p.service.RunActionInterpretation(c.WithThreadExecution(context.Background(), t.ID, a.ID), t.UserID, t.CurationID, a.ID, a.ID, a.ID, a.InputRevision)
	}()
	return nil
}

// seedCurationThreadFixtureRows seeds a small Curation graph for the thread
// integration test: a planning Curation, a curating one with a Target and a
// READY Session, and an archived one.
func seedCurationThreadFixtureRows(
	t *testing.T,
	ctx context.Context,
	database *dbp.Database,
) {
	t.Helper()
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO users(id,status,created_at,updated_at) VALUES
			('50000000-0000-4000-8000-000000000001','ACTIVE',
			 '2026-08-01T00:00:00Z','2026-08-01T00:00:00Z');
		INSERT INTO shopping_plans(
			id,user_id,original_intent,plan_mode,execution_mode,
			budget_amount,budget_currency,country,city,created_at,
			agent_mode,model_key
		) VALUES
			('51000000-0000-4000-8000-000000000001',
			 '50000000-0000-4000-8000-000000000001','계획 중인 조명',
			 'SINGLE','EXPERIMENT',100,'USD','KR','서울',
			 '2026-08-01T01:00:00Z','MANAGED','gpt-5.6-luna'),
			('51000000-0000-4000-8000-000000000002',
			 '50000000-0000-4000-8000-000000000001','검토가 필요한 의자',
			 'SINGLE','EXPERIMENT',200,'USD','KR','서울',
			 '2026-08-02T01:00:00Z','MANAGED','gpt-5.6-luna'),
			('51000000-0000-4000-8000-000000000003',
			 '50000000-0000-4000-8000-000000000001','보관한 책상',
			 'SINGLE','EXPERIMENT',300,'USD','KR','서울',
			 '2026-08-03T01:00:00Z','MANAGED','gpt-5.6-luna');
		INSERT INTO curations(
			user_id,version,created_at,updated_at,id,shopping_plan_id,
			phase,archived_at,archived_by_user_id
		) VALUES
			('50000000-0000-4000-8000-000000000001',1,
			 '2026-08-01T01:00:00Z','2026-08-01T02:00:00Z',
			 '52000000-0000-4000-8000-000000000001',
			 '51000000-0000-4000-8000-000000000001','PLANNING',NULL,NULL),
			('50000000-0000-4000-8000-000000000001',2,
			 '2026-08-02T01:00:00Z','2026-08-04T02:00:00Z',
			 '52000000-0000-4000-8000-000000000002',
			 '51000000-0000-4000-8000-000000000002','CURATING',NULL,NULL),
			('50000000-0000-4000-8000-000000000001',3,
			 '2026-08-03T01:00:00Z','2026-08-03T03:00:00Z',
			 '52000000-0000-4000-8000-000000000003',
			 '51000000-0000-4000-8000-000000000003','CURATING',
			 '2026-08-03T03:00:00Z','50000000-0000-4000-8000-000000000001');
		INSERT INTO plan_targets(
			id,plan_id,title,normalized_intent,category,allocated_amount,
			allocated_currency,country,city,url_mode,order_index,target_hash,
			version,created_at,updated_at,curation_id,user_id
		) VALUES (
			'53000000-0000-4000-8000-000000000001',
			'51000000-0000-4000-8000-000000000002','업무용 의자',
			'집중을 돕는 업무용 의자','chair',200,'USD','KR','서울','NONE',
			1,'phase66-target',1,'2026-08-02T01:01:00Z',
			'2026-08-02T01:01:00Z','52000000-0000-4000-8000-000000000002',
			'50000000-0000-4000-8000-000000000001'
		);
		INSERT INTO shopping_sessions(
			id,plan_target_id,user_id,target_snapshot,research_scope_snapshot,
			status,version,created_at,updated_at
		) VALUES (
			'54000000-0000-4000-8000-000000000001',
			'53000000-0000-4000-8000-000000000001',
			'50000000-0000-4000-8000-000000000001','{}'::jsonb,'{}'::jsonb,
			'READY',1,'2026-08-02T01:02:00Z','2026-08-02T01:02:00Z'
		);
	`); err != nil {
		t.Fatal(err)
	}
}
