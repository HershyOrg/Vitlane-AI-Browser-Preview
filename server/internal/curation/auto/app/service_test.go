package app

import (
	"context"
	"testing"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	autodomain "github.com/vitlane/vitlane/server/internal/curation/auto/domain"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

const testRequestID = "11111111-1111-4111-8111-111111111111"

type planReaderStub struct{ result curationapp.PlanResult }

func (s *planReaderStub) GetByCuration(context.Context, string, string) (curationapp.PlanResult, error) {
	return s.result, nil
}

type addExecutorStub struct{ calls int }

func (s *addExecutorStub) ExecuteExpansionAction(
	context.Context,
	curationapp.ExecuteExpansionActionInput,
) (curationapp.ExpansionResult, error) {
	s.calls++
	return curationapp.ExpansionResult{}, nil
}

type researchExecutorStub struct {
	input researchapp.ResearchAgainInput
	calls int
}

func (s *researchExecutorStub) ResearchAgain(
	_ context.Context,
	input researchapp.ResearchAgainInput,
) (researchapp.ResearchAgainResult, error) {
	s.input = input
	s.calls++
	return researchapp.ResearchAgainResult{}, nil
}

type repositoryStub struct{ record *Record }

func (s *repositoryStub) Get(_ context.Context, userID, id string) (Record, bool, error) {
	if s.record == nil || s.record.UserID != userID || s.record.ID != id {
		return Record{}, false, nil
	}
	return *s.record, true, nil
}

func (s *repositoryStub) Begin(_ context.Context, record Record) (Record, bool, error) {
	if s.record != nil {
		return *s.record, false, nil
	}
	s.record = &record
	return record, true, nil
}

func (s *repositoryStub) Resolve(_ context.Context, _, _ string, resolution ResolutionRecord) error {
	s.record.Status = "RESOLVED"
	s.record.Decision = resolution.Decision
	s.record.Source = resolution.Source
	s.record.TargetID = resolution.TargetID
	s.record.SessionID = resolution.SessionID
	s.record.SessionVersion = resolution.SessionVersion
	s.record.ReasonCode = resolution.ReasonCode
	return nil
}

func (s *repositoryStub) MarkNeedsSelection(_ context.Context, _, _, reason string, source autodomain.Source) error {
	s.record.Status = "NEEDS_SELECTION"
	s.record.Decision = autodomain.DecisionNeedsSelection
	s.record.Source = source
	s.record.ReasonCode = reason
	return nil
}

func (s *repositoryStub) MarkExecuted(context.Context, string, string) error {
	s.record.Status = "EXECUTED"
	return nil
}

type providerStub struct{ calls int }

func (*providerStub) Kind() intelligencedomain.ProviderKind {
	return intelligencedomain.ProviderManaged
}
func (*providerStub) Available(context.Context, string) error { return nil }
func (s *providerStub) Complete(
	_ context.Context,
	request intelligenceapp.CompletionRequest,
) (intelligenceapp.CompletionResult, error) {
	s.calls++
	if request.SchemaName != autoResolutionSchemaName {
		return intelligenceapp.CompletionResult{}, autodomain.ErrInvalid
	}
	return intelligenceapp.CompletionResult{Content: `{
		"operation":"REFINE",
		"productReferenceEnglish":"travel adapter",
		"modifierTermsEnglish":[],
		"referencedOrdinal":null,
		"decision":"RESEARCH_AGAIN",
		"targetId":"22222222-2222-4222-8222-222222222222",
		"reasonCode":"MODEL_TARGET_MATCH"
	}`}, nil
}

func TestKoreanRequestIsNormalizedAndDispatchedAsExistingTargetResearch(t *testing.T) {
	targetID := "22222222-2222-4222-8222-222222222222"
	sessionID := "33333333-3333-4333-8333-333333333333"
	plans := &planReaderStub{result: curationapp.PlanResult{
		Plan: curationdomain.PlanSnapshot{
			ID:       curationdomain.ShoppingPlanID("44444444-4444-4444-8444-444444444444"),
			ModelKey: "gpt-5.6-luna",
		},
		Curation: curationdomain.Curation{
			ID:    curationdomain.CurationID("55555555-5555-4555-8555-555555555555"),
			Phase: curationdomain.CurationPhaseCurating, Version: 4,
		},
		Targets: []curationdomain.PlanTarget{{
			ID: curationdomain.PlanTargetID(targetID), Title: "여행용 멀티 어댑터",
			NormalizedIntent: "compact universal travel adapter", OrderIndex: 0,
		}},
		Sessions: []shoppingsessiondomain.ShoppingSession{{
			ID:           shoppingsessiondomain.ShoppingSessionID(sessionID),
			PlanTargetID: shoppingsessiondomain.PlanTargetID(targetID),
			Status:       shoppingsessiondomain.SessionStatusReviewing, Version: 7,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}},
	}}
	provider := &providerStub{}
	research := &researchExecutorStub{}
	repository := &repositoryStub{}
	service := NewService(plans, &addExecutorStub{}, research, repository, provider, "gpt-5.6-luna")

	result, err := service.Execute(context.Background(), Input{
		UserID: "user-1", AuthSessionID: "auth-1",
		CurationID:              "55555555-5555-4555-8555-555555555555",
		Request:                 "여행용 어뎁터 좀더 조사해봐",
		ExpectedCurationVersion: 4, ClientRequestID: testRequestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "EXECUTED" || result.Decision != autodomain.DecisionResearchAgain ||
		result.TargetID != targetID || result.Source != autodomain.SourceManaged {
		t.Fatalf("unexpected result: %#v", result)
	}
	if provider.calls != 1 || research.calls != 1 || research.input.TargetID != targetID ||
		research.input.SessionID != sessionID || research.input.Feedback != "여행용 어뎁터 좀더 조사해봐" {
		t.Fatalf("provider=%d research=%d input=%#v", provider.calls, research.calls, research.input)
	}
}

func TestEnglishHighConfidenceRequestSkipsManagedClassifier(t *testing.T) {
	targetID := "22222222-2222-4222-8222-222222222222"
	plans := testSingleTargetPlan(targetID)
	provider := &providerStub{}
	research := &researchExecutorStub{}
	service := NewService(plans, &addExecutorStub{}, research, &repositoryStub{}, provider, "gpt-5.6-luna")

	result, err := service.Execute(context.Background(), Input{
		UserID: "user-1", AuthSessionID: "auth-1",
		CurationID:              "55555555-5555-4555-8555-555555555555",
		Request:                 "Research the travel adaptor more",
		ExpectedCurationVersion: 4, ClientRequestID: testRequestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source != autodomain.SourceDeterministic || provider.calls != 0 || research.calls != 1 {
		t.Fatalf("unexpected result=%#v provider=%d research=%d", result, provider.calls, research.calls)
	}
}

func testSingleTargetPlan(targetID string) *planReaderStub {
	sessionID := "33333333-3333-4333-8333-333333333333"
	return &planReaderStub{result: curationapp.PlanResult{
		Plan: curationdomain.PlanSnapshot{
			ID:       curationdomain.ShoppingPlanID("44444444-4444-4444-8444-444444444444"),
			ModelKey: "gpt-5.6-luna",
		},
		Curation: curationdomain.Curation{
			ID:    curationdomain.CurationID("55555555-5555-4555-8555-555555555555"),
			Phase: curationdomain.CurationPhaseCurating, Version: 4,
		},
		Targets: []curationdomain.PlanTarget{{
			ID:               curationdomain.PlanTargetID(targetID),
			NormalizedIntent: "compact universal travel adapter", OrderIndex: 0,
		}},
		Sessions: []shoppingsessiondomain.ShoppingSession{{
			ID:           shoppingsessiondomain.ShoppingSessionID(sessionID),
			PlanTargetID: shoppingsessiondomain.PlanTargetID(targetID),
			Status:       shoppingsessiondomain.SessionStatusReviewing, Version: 7,
		}},
	}}
}

type budgetOnlyProvider struct{ providerStub }

func (p *budgetOnlyProvider) Complete(_ context.Context, r intelligenceapp.CompletionRequest) (intelligenceapp.CompletionResult, error) {
	p.calls++
	return intelligenceapp.CompletionResult{Content: `{"operation":"UNKNOWN","productReferenceEnglish":"","modifierTermsEnglish":[],"referencedOrdinal":null,"decision":"NEEDS_SELECTION","targetId":null,"reasonCode":"BUDGET_SETTINGS_ONLY"}`}, nil
}
func TestBudgetOnlyRequestCannotAddOrResearchTargets(t *testing.T) {
	for _, empty := range []bool{false, true} {
		for _, request := range []string{"increase travel adapter budget to $100", "여행용 어댑터 예산을 10만원으로 올려줘"} {
			plan := testSingleTargetPlan("22222222-2222-4222-8222-222222222222")
			if empty {
				plan.result.Targets = nil
				plan.result.Sessions = nil
			}
			add, research, provider := &addExecutorStub{}, &researchExecutorStub{}, &budgetOnlyProvider{}
			service := NewService(plan, add, research, &repositoryStub{}, provider, "model")
			input := Input{UserID: "user-1", AuthSessionID: "auth-1", CurationID: string(plan.result.Curation.ID), Request: request, ExpectedCurationVersion: plan.result.Curation.Version, ClientRequestID: testRequestID}
			result, err := service.Execute(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if result.ReasonCode != "BUDGET_SETTINGS_ONLY" || add.calls != 0 || research.calls != 0 || provider.calls != 1 {
				t.Fatalf("empty=%v result=%+v calls=%d/%d/%d", empty, result, add.calls, research.calls, provider.calls)
			}
			replay, err := service.Execute(context.Background(), input)
			if err != nil || !replay.Replay || provider.calls != 1 {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
		}
	}
}

func TestSocialReplyDoesNotAcceptProposalOrCreateWork(t *testing.T) {
	for _, text := range []string{"네", "해줘", "고마워", "thanks", "yes"} {
		plans := &planReaderStub{result: curationapp.PlanResult{Curation: curationdomain.Curation{ID: "curation", Version: 1}}}
		add := &addExecutorStub{}
		research := &researchExecutorStub{}
		provider := &providerStub{}
		s := NewService(plans, add, research, &repositoryStub{}, provider, "model")
		in := Input{UserID: "user", AuthSessionID: "auth", CurationID: "curation", Request: text, ExpectedCurationVersion: 1, ClientRequestID: testRequestID}
		result, err := s.Execute(context.Background(), in)
		if err != nil || result.Status != "NO_ACTION" || result.Decision != autodomain.DecisionNoAction || add.calls != 0 || research.calls != 0 || provider.calls != 0 {
			t.Fatalf("%s: %+v %v calls %d %d %d", text, result, err, add.calls, research.calls, provider.calls)
		}
		replay, err := s.Execute(context.Background(), in)
		if err != nil || replay.Status != "NO_ACTION" || !replay.Replay {
			t.Fatalf("replay: %+v %v", replay, err)
		}
	}
}
