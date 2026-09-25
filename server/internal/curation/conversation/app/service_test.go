package app

import (
	"context"
	"errors"
	a "github.com/vitlane/vitlane/server/internal/curation/auto/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	i "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	id "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	r "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"testing"
)

type txStub struct{}

func (txStub) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type repoStub struct {
	message  d.FollowUp
	state    string
	intake   int
	finished int
	chosen   *d.FollowUp
}

func (s *repoStub) Read(context.Context, string, string) (Conversation, error) {
	return Conversation{}, nil
}
func (s *repoStub) LockMessage(context.Context, ResponseInput) (d.FollowUp, int64, error) {
	return s.message, 7, nil
}
func (s *repoStub) SaveResponse(_ context.Context, in ResponseInput, state string) error {
	s.state = state
	s.message.Status = state
	s.message.Version++
	s.message.ResponseRequestID = in.ClientRequestID
	return nil
}
func (s *repoStub) AdmitManual(context.Context, RequestInput) (bool, error) {
	s.intake++
	return false, nil
}
func (s *repoStub) DispatchContext(context.Context, string) error { return nil }
func (s *repoStub) ClaimResponse(context.Context) (*Generation, error) {
	return &Generation{ResponseID: "response", Choices: []d.FollowUp{s.message}}, nil
}
func (s *repoStub) FinishGeneration(_ context.Context, _ *Generation, m *d.FollowUp) error {
	s.finished++
	s.chosen = m
	return nil
}

type researchStub struct {
	calls int
	input r.ResearchAgainInput
}

func (s *researchStub) ResearchAgain(_ context.Context, in r.ResearchAgainInput) (r.ResearchAgainResult, error) {
	s.calls++
	s.input = in
	return r.ResearchAgainResult{}, nil
}

type autoStub struct{ calls int }

func (s *autoStub) Execute(context.Context, a.Input) (a.Result, error) {
	s.calls++
	return a.Result{}, errors.New("must not classify acceptance")
}
func TestProposalAcceptanceDispatchesStoredPayloadWithoutAutoOrTargetSelection(t *testing.T) {
	repo := &repoStub{message: d.FollowUp{ID: "11111111-1111-4111-8111-111111111111", Kind: "PROPOSAL", Status: "PENDING", Version: 1, Payload: &d.FollowUpAction{Kind: "RESEARCH_AGAIN", TargetID: "saved-target", SessionID: "saved-session", SessionVersion: 9, CriteriaVersion: 3, Feedback: "saved exact feedback"}}}
	research := &researchStub{}
	auto := &autoStub{}
	s := &Service{Repository: repo, Tx: txStub{}, Research: research, Auto: auto}
	in := ResponseInput{MessageID: repo.message.ID, UserID: "user", AuthSessionID: "auth", CurationID: "curation", Response: "ACCEPT", ExpectedVersion: 1, ClientRequestID: "22222222-2222-4222-8222-222222222222"}
	if err := s.Respond(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if research.calls != 1 || auto.calls != 0 || repo.intake != 1 || repo.state != "ACCEPTED" {
		t.Fatalf("calls %d/%d intake %d state %s", research.calls, auto.calls, repo.intake, repo.state)
	}
	if research.input.TargetID != "saved-target" || research.input.SessionID != "saved-session" || research.input.Feedback != "saved exact feedback" || research.input.ExpectedCurationVersion != 7 || *research.input.ExpectedCriteriaVersion != 3 {
		t.Fatalf("changed action %+v", research.input)
	}
	if err := s.Respond(context.Background(), in); err != nil || research.calls != 1 {
		t.Fatal("duplicate execution", err)
	}
	in.Response = "DISMISS"
	if err := s.Respond(context.Background(), in); err == nil {
		t.Fatal("accepted proposal changed to dismissed")
	}
}
func TestDismissDoesNotAdmitResearchAndOptionalGenerationUnavailableIsEmpty(t *testing.T) {
	repo := &repoStub{message: d.FollowUp{ID: "11111111-1111-4111-8111-111111111111", Kind: "PROPOSAL", Status: "PENDING", Version: 1, Payload: &d.FollowUpAction{}}}
	s := &Service{Repository: repo, Tx: txStub{}}
	if err := s.Respond(context.Background(), ResponseInput{MessageID: repo.message.ID, Response: "DISMISS", ExpectedVersion: 1, ClientRequestID: "22222222-2222-4222-8222-222222222222"}); err != nil {
		t.Fatal(err)
	}
	if repo.intake != 0 || repo.state != "DISMISSED" {
		t.Fatal("dismiss admitted an action")
	}
	if err := s.Tick(context.Background()); err != nil || repo.finished != 1 || repo.chosen != nil {
		t.Fatal("optional generation did not fail closed", err)
	}
}

type providerStub struct {
	content string
	err     error
	calls   int
}

func (*providerStub) Kind() id.ProviderKind                   { return id.ProviderKind("MANAGED") }
func (*providerStub) Available(context.Context, string) error { return nil }
func (p *providerStub) Complete(_ context.Context, in i.CompletionRequest) (i.CompletionResult, error) {
	p.calls++
	return i.CompletionResult{Content: p.content}, p.err
}
func TestGenerationOnlyAcceptsOneValidChoice(t *testing.T) {
	for _, tc := range []struct {
		content string
		err     error
		want    bool
	}{{`{"choice":0}`, nil, true}, {`{"choice":-1}`, nil, false}, {`{"choice":1}`, nil, false}, {`{"choice":[0,0]}`, nil, false}, {`{}`, nil, false}, {`{"choice":0}`, errors.New("provider unavailable"), false}} {
		repo := &repoStub{message: d.FollowUp{Kind: "PROPOSAL"}}
		provider := &providerStub{content: tc.content, err: tc.err}
		s := &Service{Repository: repo, Provider: provider}
		if err := s.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		if provider.calls != 1 || repo.finished != 1 || (repo.chosen != nil) != tc.want {
			t.Fatalf("%+v provider=%d chosen=%+v", tc, provider.calls, repo.chosen)
		}
	}
}

type backgroundStub struct {
	calls  int
	action d.FollowUpAction
}

func (s *backgroundStub) AcceptSubscription(_ context.Context, _, _, _ string, a d.FollowUpAction) error {
	s.calls++
	s.action = a
	return nil
}
func (*backgroundStub) SubscriptionChoices(context.Context, string, string, string) ([]d.FollowUp, error) {
	return nil, nil
}
func TestBackgroundProposalAcceptanceDoesNotStartForegroundAndReplays(t *testing.T) {
	repo := &repoStub{message: d.FollowUp{ID: "11111111-1111-4111-8111-111111111111", Kind: "PROPOSAL", Status: "PENDING", Version: 1, Payload: &d.FollowUpAction{Kind: "SUBSCRIBE_DEALS", TargetID: "saved-target", Subscription: &d.SubscriptionTerms{Country: "KR"}}}}
	bg := &backgroundStub{}
	research := &researchStub{}
	s := &Service{Repository: repo, Tx: txStub{}, Background: bg, Research: research}
	in := ResponseInput{MessageID: repo.message.ID, UserID: "user", CurationID: "curation", Response: "ACCEPT", ExpectedVersion: 1, ClientRequestID: "22222222-2222-4222-8222-222222222222"}
	if e := s.Respond(context.Background(), in); e != nil {
		t.Fatal(e)
	}
	if e := s.Respond(context.Background(), in); e != nil {
		t.Fatal(e)
	}
	if bg.calls != 1 || research.calls != 0 || repo.intake != 0 || bg.action.TargetID != "saved-target" {
		t.Fatalf("bg=%d fg=%d intake=%d action=%+v", bg.calls, research.calls, repo.intake, bg.action)
	}
}
