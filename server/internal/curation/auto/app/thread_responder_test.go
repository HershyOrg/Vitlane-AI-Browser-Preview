package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	c "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	i "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	id "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type replyProvider struct {
	body    string
	request i.CompletionRequest
	calls   int
}

func (p *replyProvider) Kind() id.ProviderKind                   { return id.ProviderManaged }
func (p *replyProvider) Available(context.Context, string) error { return nil }
func (p *replyProvider) Complete(_ context.Context, r i.CompletionRequest) (i.CompletionResult, error) {
	p.calls++
	p.request = r
	raw, _ := json.Marshal(map[string]string{"body": p.body})
	return i.CompletionResult{Content: string(raw)}, nil
}

func replyContext(kind string) c.ResponseContext {
	price := int64(168000)
	return c.ResponseContext{Kind: kind, Locale: "ko-KR", Request: "뭐가 나아?", UserID: "user", ActionID: "action", JobID: "job", AttemptID: "attempt", InputRevision: 1,
		Targets:    []c.ResponseTarget{{Title: "만년필", Candidates: []c.ResponseCandidate{{Ref: "c1", CandidateID: "p-1", Title: "Pilot Custom 74", PriceMinor: &price, Price: &c.ResponsePrice{MinimumMinor: price, MaximumMinor: price, Currency: "KRW", Basis: "OBSERVED"}}}}},
		References: []d.ResponseReference{{Ref: "c1", CandidateID: "p-1", TargetID: "pens", Title: "Pilot Custom 74"}}}
}

func TestResponderSendsSavedFactsAndStoresOnlyValidatedText(t *testing.T) {
	p := &replyProvider{body: "[[c1]] 후보가 입문용으로 부담이 적어요. 실제 필기감은 확인하지 못했어요."}
	now := time.Date(2026, 9, 21, 6, 0, 0, 0, time.UTC)
	out, err := (ThreadResponder{Provider: p, DefaultModel: "default", Now: func() time.Time { return now }}).Respond(context.Background(), replyContext(d.ResponseKindComment))
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != d.ResponseKindComment || out.ModelKey != "default" || !out.CreatedAt.Equal(now) || len(out.References) != 1 || out.References[0].CandidateID != "p-1" {
		t.Fatalf("unexpected response %+v", out)
	}
	if p.request.SchemaName != ThreadResponseSchema || p.request.RequestKey != "curation-response:action:1:attempt" || p.request.JobID != "job" || p.request.AttemptID != "attempt" {
		t.Fatalf("the call must be attributed to its Job and Attempt: %+v", p.request)
	}
	if !strings.Contains(p.request.SystemPrompt, "Both comments and answers may use the supplied prices") || strings.Contains(p.request.UserPrompt, "p-1") {
		t.Fatalf("a comment receives prices and never sees internal candidate ids: %s", p.request.UserPrompt)
	}
}

func TestResponderRejectsUnknownReferencesAndAllowsPrices(t *testing.T) {
	p := &replyProvider{body: "[[c9]]는 ₩150,000 정도예요."}
	_, err := (ThreadResponder{Provider: p}).Respond(context.Background(), replyContext(d.ResponseKindAnswer))
	f, ok := fault.As(err)
	if !ok || f.Reason != "THREAD_RESPONSE_REFERENCE_UNKNOWN" || f.Retryable || p.calls != 1 {
		t.Fatalf("want one call and a final rejection, got %v calls=%d", err, p.calls)
	}
	p.body = "[[c1]]는 ₩168,000이에요."
	if _, err = (ThreadResponder{Provider: p}).Respond(context.Background(), replyContext(d.ResponseKindAnswer)); err != nil {
		t.Fatalf("a given price may be quoted: %v", err)
	}
	if !strings.Contains(p.request.SystemPrompt, "asked a question") {
		t.Fatal("an answer uses the answer prompt")
	}
}

func TestInterpreterRoutesAQuestionToTheReplyAction(t *testing.T) {
	ctx := threadFixture()
	ctx.Thread.Request = "LAMY 2000이랑 Custom 74 중에 입문자한테 뭐가 나아?"
	p := &threadProvider{content: `{"answer":true,"plan":{"actions":[],"conditions":[],"budget":null},"question":null}`}
	out, err := (ThreadInterpreter{Provider: p}).Interpret(context.Background(), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Actions) != 1 || out.Actions[0].Type != d.CurationActionResponse || out.Actions[0].Instruction != d.ResponseKindAnswer || out.Question != nil {
		t.Fatalf("a question runs the reply Action alone: %+v", out)
	}
	if err = out.ValidatePlan(); err != nil {
		t.Fatalf("the answer plan must be registrable: %v", err)
	}
	if len(out.Decisions) != 4 || out.Decisions[0].Result != "ANSWER" || out.Decisions[0].ReasonCode != "QUESTION_ANSWERED" {
		t.Fatalf("the record must say why nothing was researched: %+v", out.Decisions)
	}
	p.content = `{"answer":true,"plan":{"actions":[{"kind":"ADD_TARGET","targetId":"","instruction":"ink"}],"conditions":[],"budget":null},"question":null}`
	if _, err = (ThreadInterpreter{Provider: p}).Interpret(context.Background(), ctx); err == nil {
		t.Fatal("an answer that also plans work is rejected")
	}
}
