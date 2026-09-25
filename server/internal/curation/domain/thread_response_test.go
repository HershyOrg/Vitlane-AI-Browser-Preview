package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func offeredReferences() []ResponseReference {
	return []ResponseReference{{Ref: "c1", CandidateID: "p-1", TargetID: "pens", Title: "LAMY 2000"}, {Ref: "c2", CandidateID: "p-2", TargetID: "pens", Title: "Pilot Custom 74"}}
}

func reasonOf(t *testing.T, err error) string {
	t.Helper()
	f, ok := fault.As(err)
	if !ok {
		t.Fatalf("expected a classified fault, got %v", err)
	}
	if f.Retryable {
		t.Fatalf("a rejected reply must not be retried: %+v", f)
	}
	return f.Reason
}

func TestCommentResolvesOfferedReferencesOnce(t *testing.T) {
	out, err := NewActionResponse(ResponseKindComment, "  [[c1]] 후보가 필기감과 내구성이 함께 높았어요. [[c2]]도 볼 만하고, [[c1]]의 실제 필기감은 확인하지 못했어요.\r\n", "ko-KR", "model", offeredReferences(), time.Unix(10, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if out.SchemaVersion != ThreadResponseSchema || out.Kind != ResponseKindComment || out.Locale != "ko-KR" || strings.ContainsAny(out.Body, "\r") || strings.HasPrefix(out.Body, " ") {
		t.Fatalf("unexpected response %+v", out)
	}
	if len(out.References) != 2 || out.References[0].CandidateID != "p-1" || out.References[1].CandidateID != "p-2" {
		t.Fatalf("references must be offered ones, each once, in order of use: %+v", out.References)
	}
}

func TestReplyRejectsWhatTheServerDidNotOffer(t *testing.T) {
	long := strings.Repeat("가", 601)
	cases := map[string]struct{ kind, body, reason string }{
		"unknown reference": {ResponseKindComment, "[[c9]]가 가장 좋아요.", "THREAD_RESPONSE_REFERENCE_UNKNOWN"},
		"broken token":      {ResponseKindComment, "[[c1] 가 가장 좋아요.", "THREAD_RESPONSE_REFERENCE_MALFORMED"},
		"link":              {ResponseKindComment, "[[c1]] 자세한 내용은 https://example.com 에서 보세요.", "THREAD_RESPONSE_LINK_FORBIDDEN"},
		"empty":             {ResponseKindComment, "   ", "THREAD_RESPONSE_LENGTH_INVALID"},
		"too long":          {ResponseKindComment, long, "THREAD_RESPONSE_LENGTH_INVALID"},
		"unknown kind":      {"SUMMARY", "[[c1]]", "THREAD_RESPONSE_KIND_INVALID"},
	}
	for name, tc := range cases {
		_, err := NewActionResponse(tc.kind, tc.body, "ko-KR", "model", offeredReferences(), time.Now())
		if err == nil || reasonOf(t, err) != tc.reason {
			t.Fatalf("%s: want %s, got %v", name, tc.reason, err)
		}
	}
}

// Shape validation deliberately makes no factual promise about prices or names.
func TestBothRepliesAllowPricesWithoutNotationDependentRejection(t *testing.T) {
	for _, kind := range []string{ResponseKindComment, ResponseKindAnswer} {
		for _, body := range []string{"[[c1]]는 259,000원이에요.", "[[c1]] is $131.00.", "[[c1]] costs 131 USD.", "[[c1]]는 131달러, 차이는 약 9만 원이에요.", "[[c1]] is USD 999."} {
			if _, err := NewActionResponse(kind, body, "ko-KR", "model", offeredReferences(), time.Now()); err != nil {
				t.Fatalf("%s %q must pass shape validation: %v", kind, body, err)
			}
		}
	}
}

func researchedThread(count int) CurationThread {
	return CurationThread{Status: "RUNNING", Actions: []CurationAction{
		{ID: "a1", Type: CurationActionAutoStart, Status: "SUCCEEDED"},
		{ID: "a2", Type: CurationActionTargetResearchAgain, Status: "SUCCEEDED", Jobs: []ActionJobResult{{JobID: "j1", Kind: "RESEARCH_ROUND", Status: "SUCCEEDED", Effects: []ActionEffect{{Kind: "CANDIDATES_ADDED", TargetID: "pens", Count: count}}}}},
	}}
}

func TestOnlyAResearchThatAddedCandidatesEarnsAComment(t *testing.T) {
	added := researchedThread(4)
	if !added.NeedsResponseComment() {
		t.Fatal("a research that added candidates needs a comment")
	}
	added.AppendResponseAction("a3")
	last := added.Actions[2]
	if last.Type != CurationActionResponse || last.Instruction != ResponseKindComment || last.GeneratedByActionID != "a2" || last.Status != "PENDING" || last.InputRevision != 1 {
		t.Fatalf("unexpected response action %+v", last)
	}
	if added.NeedsResponseComment() {
		t.Fatal("a Thread writes one reply only")
	}
	empty := researchedThread(0)
	if empty.NeedsResponseComment() {
		t.Fatal("an empty research keeps its fixed sentence")
	}
	settings := CurationThread{Status: "RUNNING", Actions: []CurationAction{{ID: "a1", Type: CurationActionBudgetChange, Status: "SUCCEEDED"}}}
	if settings.NeedsResponseComment() {
		t.Fatal("a settings-only Thread keeps its fixed sentence")
	}
	failed := researchedThread(4)
	failed.Actions[1].Status = "FAILED"
	if failed.NeedsResponseComment() {
		t.Fatal("a failed Thread never writes a comment")
	}
	closed := researchedThread(4)
	closed.Status = "SUCCEEDED"
	if closed.NeedsResponseComment() {
		t.Fatal("a closed Thread is never reopened for a comment")
	}
}

func TestALostReplyLeavesTheThreadSuccessful(t *testing.T) {
	thread := researchedThread(4)
	thread.AppendResponseAction("a3")
	action := &thread.Actions[2]
	action.Status = "RUNNING"
	action.SettleResponseJobs([]ActionJobResult{{JobID: "j2", Status: "RUNNING"}})
	if action.Status != "RUNNING" {
		t.Fatal("an open Job keeps the Action running")
	}
	action.SettleResponseJobs([]ActionJobResult{{JobID: "j2", Status: "FAILED", ReasonCode: "THREAD_RESPONSE_PRICE_FORBIDDEN"}})
	if action.Status != "SUCCEEDED" || action.ReasonCode != ReasonResponseUnavailable || action.Response != nil {
		t.Fatalf("a failed reply must settle as unavailable: %+v", action)
	}
	thread.RefreshStatus()
	if thread.Status != "SUCCEEDED" {
		t.Fatalf("the Thread outcome must not depend on the reply: %s", thread.Status)
	}
}

func TestOnlyALoneAnswerIsAValidPlannedResponse(t *testing.T) {
	answer := ActionPlan{Actions: []CurationAction{{Type: CurationActionResponse, Instruction: ResponseKindAnswer}}}
	if err := answer.ValidatePlan(); err != nil {
		t.Fatal(err)
	}
	comment := ActionPlan{Actions: []CurationAction{{Type: CurationActionResponse, Instruction: ResponseKindComment}}}
	if comment.ValidatePlan() == nil {
		t.Fatal("an interpreter cannot plan a comment; the Thread appends it itself")
	}
	mixed := ActionPlan{Actions: []CurationAction{{Type: CurationActionTargetResearchAgain, TargetID: "pens"}, {Type: CurationActionResponse, Instruction: ResponseKindAnswer}}}
	if mixed.ValidatePlan() == nil {
		t.Fatal("an answer runs alone")
	}
}
