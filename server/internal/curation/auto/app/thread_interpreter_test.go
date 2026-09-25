package app

import (
	"context"
	"encoding/json"
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	i "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	id "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"testing"
)

type threadProvider struct {
	content string
	calls   int
	keys    []string
}

func (p *threadProvider) Kind() id.ProviderKind                   { return id.ProviderManaged }
func (p *threadProvider) Available(context.Context, string) error { return nil }
func (p *threadProvider) Complete(_ context.Context, r i.CompletionRequest) (i.CompletionResult, error) {
	p.calls++
	p.keys = append(p.keys, r.RequestKey)
	if r.SchemaName != ActionDecisionSchema {
		panic("wrong schema")
	}
	return i.CompletionResult{Content: p.content}, nil
}
func threadFixture() c.ThreadContext {
	amount := "200000"
	return c.ThreadContext{Thread: d.CurationThread{Request: "다시 찾아줘"}, Targets: []c.ThreadTarget{{ID: "keyboard", Title: "Keyboard", Researchable: true}, {ID: "headphones", Title: "Headphones", Researchable: true}}, Budget: d.BudgetLedger{SchemaVersion: d.BudgetSchema, Version: 1, ResearchVersion: 1, Enabled: true, Currency: "KRW", TotalAmount: &amount, Allocations: []d.TargetBudget{{TargetID: "keyboard", Quantity: 1, Amount: &amount}, {TargetID: "headphones", Quantity: 1, Amount: ptr("0")}}}}
}
func ptr(s string) *string { return &s }
func TestThreadAutoSingleTargetNeverCallsModel(t *testing.T) {
	for _, request := range []string{"Try again", "Find better options", "Research this again", "다시 찾아줘"} {
		ctx := threadFixture()
		ctx.Targets = ctx.Targets[:1]
		ctx.Thread.Request = request
		p := &threadProvider{}
		out, err := (ThreadInterpreter{Provider: p}).Interpret(context.Background(), ctx)
		if err != nil || p.calls != 0 || len(out.Actions) != 1 || out.Actions[0].TargetID != "keyboard" || len(out.Decisions) != 4 {
			t.Fatalf("%s: %+v %v calls=%d", request, out, err, p.calls)
		}
	}
}
func TestThreadAutoQuantityStaysQuantityAndRejectsUnknownTarget(t *testing.T) {
	ctx := threadFixture()
	ctx.Thread.Request = "Find 2 cheaper keyboard options"
	for _, target := range []string{"keyboard", "invented"} {
		p := &threadProvider{content: `{"plan":{"actions":[{"kind":"RESEARCH_AGAIN","targetId":"` + target + `","instruction":"Find 2 cheaper keyboard options"}],"conditions":[],"budget":null},"question":null}`}
		out, err := (ThreadInterpreter{Provider: p}).Interpret(context.Background(), ctx)
		if p.calls != 1 {
			t.Fatal("expected exactly one bounded call")
		}
		if target == "keyboard" {
			if err != nil || out.Actions[0].TargetID != "keyboard" {
				t.Fatalf("%+v %v", out, err)
			}
		} else if err == nil {
			t.Fatal("invented target accepted")
		}
	}
}
func TestThreadAutoBudgetRequiresEvidenceAndNeverIgnoresExplicitCap(t *testing.T) {
	ctx := threadFixture()
	for _, request := range []string{"20만원 언더로 다시 찾아줘", "예산은 제한 없음"} {
		ctx.Thread.Request = request
		_, err := compileThreadPlan(ctx, primitiveProposal{Actions: []primitiveAction{{Kind: "RESEARCH_AGAIN", TargetID: "keyboard"}}}, false)
		if err == nil {
			t.Fatalf("cap silently ignored: %s", request)
		}
	}
	ctx.Thread.Request = "좋은 키보드 다시 찾아줘"
	_, err := compileThreadPlan(ctx, primitiveProposal{Budget: &budgetProposal{Evidence: "좋은 키보드", Command: d.BudgetCommand{Kind: "DISABLE"}}}, false)
	if err == nil {
		t.Fatal("unsolicited budget mutation accepted")
	}
	ctx.Thread.Request = "20만원 언더로 다시 찾아줘"
	out, err := compileThreadPlan(ctx, primitiveProposal{Budget: &budgetProposal{Evidence: "20만원 언더", Command: d.BudgetCommand{Kind: "SET_TOTAL", Currency: "KRW", TotalAmount: ptr("200000"), AllocationMode: "PROPORTIONAL"}}, Actions: []primitiveAction{{Kind: "RESEARCH_AGAIN", TargetID: "keyboard"}}}, false)
	if err != nil || len(out.Actions) != 2 || out.Actions[0].Type != "BUDGET_CHANGE" {
		t.Fatalf("%+v %v", out, err)
	}
}
func TestThreadQuestionPreservesEachExecutablePlanAndItsDecisions(t *testing.T) {
	ctx := threadFixture()
	p := &threadProvider{content: `{"plan":{"actions":[],"conditions":[],"budget":null},"question":{"prompt":"어느 상품인가요?","options":[{"label":"키보드","plan":{"actions":[{"kind":"RESEARCH_AGAIN","targetId":"keyboard","instruction":"다시 찾아줘"}],"conditions":[],"budget":null}},{"label":"헤드폰","plan":{"actions":[{"kind":"RESEARCH_AGAIN","targetId":"headphones","instruction":"다시 찾아줘"}],"conditions":[],"budget":null}}]}}`}
	out, err := (ThreadInterpreter{Provider: p}).Interpret(context.Background(), ctx)
	if err != nil || out.Question == nil || len(out.Actions) != 0 {
		t.Fatalf("%+v %v", out, err)
	}
	for _, o := range out.Question.Options {
		if len(o.Decisions) != 4 || len(o.Actions) != 1 || o.Actions[0].TargetLabel == "" {
			t.Fatalf("option lost audit: %+v", o)
		}
	}
	raw, _ := json.Marshal(out.Question)
	var restored d.ThreadQuestion
	if json.Unmarshal(raw, &restored) != nil || len(restored.Options[0].Decisions) != 4 {
		t.Fatal("question is not durable")
	}
}

func TestBudgetOnlyDecisionCannotDeleteExistingCriteria(t *testing.T) {
	input := threadFixture()
	input.Thread.Request = "예산을 15만원 이하로 바꿔줘. 조사는 하지 마."
	old := d.TargetCriteriaSetV1{Axes: []d.ResearchAxis{{AxisID: "design"}, {AxisID: "price"}}}
	input.Targets[0].Criteria = &old
	_, err := compileThreadPlan(input, primitiveProposal{
		Budget:     &budgetProposal{Evidence: "15만원 이하", Command: d.BudgetCommand{Kind: "SET_TOTAL", Currency: "KRW", TotalAmount: ptr("150000"), AllocationMode: "PROPORTIONAL"}},
		Conditions: []conditionProposal{{TargetID: "keyboard", Evidence: "예산을 15만원 이하로 바꿔줘", Criteria: d.TargetCriteriaSetV1{Axes: old.Axes[:1]}}},
	}, false)
	if err == nil {
		t.Fatal("budget request deleted a comparison criterion")
	}
}

func TestSelectionAnswerCanCorrectEarlierBudget(t *testing.T) {
	input := threadFixture()
	input.Thread.Request = "20만원 언더로 다시 찾아줘"
	for _, answer := range []string{"아니, 30만원 이하로 해줘", "예산 제한 없음으로 해줘"} {
		input.Answers = []d.ThreadAnswer{{Text: answer}}
		command := d.BudgetCommand{Kind: "SET_TOTAL", Currency: "KRW", TotalAmount: ptr("300000"), AllocationMode: "PROPORTIONAL"}
		if answer == "예산 제한 없음으로 해줘" {
			command = d.BudgetCommand{Kind: "DISABLE"}
		}
		_, err := compileThreadPlan(input, primitiveProposal{Budget: &budgetProposal{Evidence: answer, Command: command}}, false)
		if err != nil {
			t.Fatalf("latest budget answer was ignored: %v", err)
		}
	}
}

func TestExplicitPreservationDoesNotPreallocateANewTarget(t *testing.T) {
	for _, request := range []string{"헤드폰도 구매할 상품으로 추가해줘. 기존 전체 예산은 유지해.", "Add headphones. Keep the existing total budget."} {
		ctx := threadFixture()
		ctx.Thread.Request = request
		out, err := compileThreadPlan(ctx, primitiveProposal{Actions: []primitiveAction{{Kind: "ADD_TARGET", Instruction: "headphones"}}, Budget: &budgetProposal{Evidence: request, Command: d.BudgetCommand{Kind: "ENABLE"}}}, false)
		if err != nil || len(out.Actions) != 1 || out.Actions[0].Type != "CURATION_ADD_TARGETS" || out.Decisions[0].ReasonCode != "EXPLICIT_BUDGET_PRESERVED" {
			t.Fatalf("%+v %v", out, err)
		}
	}
	for _, request := range []string{"전체 예산은 유지하되 키보드 배분을 바꿔줘", "Keep budget. Set headphone allocation to $50", "기존 전체 예산은 유지해. 20만원 언더로 찾아줘"} {
		if explicitlyPreservesBudget(request) {
			t.Fatalf("mixed constraint erased: %s", request)
		}
	}
}

func TestInterpretationAttemptKeysAndTargetDecisionLabels(t *testing.T) {
	input := threadFixture()
	input.ActionID = "action"
	input.InputRevision = 2
	p := &threadProvider{content: `{"plan":{"actions":[{"kind":"RESEARCH_AGAIN","targetId":"keyboard","instruction":"try"}],"conditions":[],"budget":null},"question":null}`}
	for _, attempt := range []string{"first", "first", "second"} {
		input.AttemptID = attempt
		out, e := (ThreadInterpreter{Provider: p}).Interpret(context.Background(), input)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, d := range out.Decisions {
			if d.Kind == "TARGET" {
				found = d.TargetID == "keyboard" && d.TargetLabel == "Keyboard"
			}
		}
		if !found {
			t.Fatalf("target label missing: %+v", out.Decisions)
		}
	}
	if p.keys[0] != p.keys[1] || p.keys[0] == p.keys[2] {
		t.Fatalf("attempt keys: %+v", p.keys)
	}
}

func TestThreadCombinationRequestOnlyWritesAResponse(t *testing.T) {
	input := threadFixture()
	input.Thread.Request = "이 두 상품의 조합과 활용법을 추천해줘. 새 조사는 하지마."
	provider := &threadProvider{content: `{"answer":true,"combination":true,"plan":{"actions":[],"conditions":[],"budget":null},"question":null}`}
	result, err := (ThreadInterpreter{Provider: provider}).Interpret(context.Background(), input)
	if err != nil || len(result.Actions) != 1 || result.Actions[0].Type != d.CurationActionResponse || result.Actions[0].Instruction != "COMBINATION" {
		t.Fatal(result, err)
	}
	provider.content = `{"answer":false,"combination":true,"plan":{"actions":[],"conditions":[],"budget":null},"question":null}`
	if _, err = (ThreadInterpreter{Provider: provider}).Interpret(context.Background(), input); err == nil {
		t.Fatal("combination without answer accepted")
	}
}
