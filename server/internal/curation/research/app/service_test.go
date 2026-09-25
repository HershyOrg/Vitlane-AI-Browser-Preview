package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

const testRoundID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

type testIDs struct{ index int }

func (g *testIDs) NewID() string {
	g.index++
	return "generated-" + string(rune('0'+g.index))
}

type testTransactor struct{}

func (testTransactor) WithinTransaction(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	return fn(ctx)
}

type testPlanningService struct {
	result          curationapp.PlanResult
	recordedPatches *[]curationapp.RecordCurationActionInput
	replay          bool
}

func planResultForSession(
	session shoppingsessiondomain.ShoppingSession,
	phase curationdomain.CurationPhase,
) curationapp.PlanResult {
	return curationapp.PlanResult{
		Plan: curationdomain.PlanSnapshot{
			ID: "plan-1", UserID: "user-1",
			ExecutionMode: curationdomain.ExecutionModeExperiment,
		},
		Curation: curationdomain.Curation{
			ID: "curation-1", ShoppingPlanID: "plan-1", UserID: "user-1",
			Phase: phase, Version: 1,
			CreatedAt: time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC),
		},
		Targets: []curationdomain.PlanTarget{{
			ID: curationdomain.PlanTargetID(session.PlanTargetID), PlanID: "plan-1",
		}},
		Sessions: []shoppingsessiondomain.ShoppingSession{session},
	}
}

func planResultWithRemovedTarget(
	session shoppingsessiondomain.ShoppingSession,
) curationapp.PlanResult {
	result := planResultForSession(session, curationdomain.CurationPhaseCurating)
	result.Targets = []curationdomain.PlanTarget{}
	result.Sessions = []shoppingsessiondomain.ShoppingSession{}
	return result
}

func (s testPlanningService) Get(
	context.Context, string, string,
) (curationapp.PlanResult, error) {
	return s.result, nil
}

func (s testPlanningService) RecordCurationAction(
	_ context.Context,
	input curationapp.RecordCurationActionInput,
) (curationapp.RecordCurationActionResult, error) {
	return curationapp.RecordCurationActionResult{
		Action: curationdomain.CurationAction{
			ID:         curationdomain.CurationActionID(input.ActionID),
			CurationID: curationdomain.CurationID(input.CurationID), Type: input.Type,
		},
		Replay: s.replay,
	}, nil
}

func (s testPlanningService) RecordManagedContinuationCurationAction(
	ctx context.Context,
	input curationapp.RecordCurationActionInput,
) (curationapp.RecordCurationActionResult, error) {
	return s.RecordCurationAction(ctx, input)
}

func (s testPlanningService) RecordOwnedPatchCurationAction(
	ctx context.Context,
	input curationapp.RecordCurationActionInput,
) (curationapp.RecordCurationActionResult, error) {
	if s.recordedPatches != nil {
		*s.recordedPatches = append(*s.recordedPatches, input)
	}
	return s.RecordCurationAction(ctx, input)
}

func (s testPlanningService) StartCurating(
	context.Context, string, string, int64,
) (curationapp.StartCuratingResult, error) {
	return curationapp.StartCuratingResult{
		Plan: s.result.Plan, Curation: s.result.Curation,
	}, nil
}

type testJobCreator struct {
	repository  *memoryResearchRepository
	calls       []CreateResearchJobInput
	planActive  bool
	activeCount int
}

func (c *testJobCreator) LockActionAdmission(context.Context, string) error { return nil }
func (c *testJobCreator) PlanHasActiveWork(
	context.Context, string, string,
) (bool, error) {
	return c.planActive, nil
}
func (c *testJobCreator) ActiveActionCount(context.Context, string) (int, error) {
	return c.activeCount, nil
}
func (c *testJobCreator) CreateResearchJob(
	_ context.Context,
	input CreateResearchJobInput,
) error {
	if c.repository.round.ID == "" {
		return errors.New("job created outside the product transaction")
	}
	c.calls = append(c.calls, input)
	return nil
}

type memoryResearchRepository struct {
	round          researchdomain.ResearchRound
	feedback       *researchdomain.ResearchFeedback
	candidates     map[string]researchdomain.Candidate
	configurations []researchdomain.CandidateConfiguration
}

func (r *memoryResearchRepository) NextRoundNumber(context.Context, string) (int, error) {
	if r.round.RoundNumber > 0 {
		return r.round.RoundNumber + 1, nil
	}
	return 1, nil
}
func (r *memoryResearchRepository) CreateRound(
	_ context.Context,
	round researchdomain.ResearchRound,
) error {
	r.round = round
	return nil
}
func (r *memoryResearchRepository) GetRound(
	_ context.Context, userID, roundID string, _ bool,
) (researchdomain.ResearchRound, error) {
	if r.round.ID != roundID || r.round.UserID != userID {
		return researchdomain.ResearchRound{}, researchdomain.ErrRoundNotFound
	}
	return r.round, nil
}
func (r *memoryResearchRepository) GetCurrentRound(
	context.Context, string, string,
) (researchdomain.ResearchRound, error) {
	if r.round.ID == "" {
		return researchdomain.ResearchRound{}, researchdomain.ErrRoundNotFound
	}
	return r.round, nil
}
func (r *memoryResearchRepository) UpdateRound(
	_ context.Context,
	round researchdomain.ResearchRound,
) error {
	r.round = round
	return nil
}
func (r *memoryResearchRepository) GetCandidate(
	_ context.Context, _ string, sessionID, candidateID string,
) (researchdomain.Candidate, error) {
	value, ok := r.candidates[candidateID]
	if !ok || value.ShoppingSessionID != sessionID {
		return researchdomain.Candidate{}, researchdomain.ErrCandidateInvalid
	}
	return value, nil
}
func (r *memoryResearchRepository) InsertConfiguration(
	_ context.Context,
	value researchdomain.CandidateConfiguration,
) (researchdomain.CandidateConfiguration, error) {
	value.ConfigurationSequence = int64(len(r.configurations) + 1)
	r.configurations = append(r.configurations, value)
	return value, nil
}
func (r *memoryResearchRepository) GetLatestConfiguration(
	_ context.Context, userID, sessionID, candidateID string,
) (researchdomain.CandidateConfiguration, error) {
	for index := len(r.configurations) - 1; index >= 0; index-- {
		value := r.configurations[index]
		if value.UserID == userID && value.ShoppingSessionID == sessionID &&
			value.CandidateID == candidateID {
			return value, nil
		}
	}
	return researchdomain.CandidateConfiguration{}, researchdomain.ErrConfigurationNotFound
}
func (r *memoryResearchRepository) GetConfiguration(
	_ context.Context, userID, sessionID, candidateID, configurationID string,
) (researchdomain.CandidateConfiguration, error) {
	for _, value := range r.configurations {
		if value.ID == configurationID && value.UserID == userID &&
			value.ShoppingSessionID == sessionID && value.CandidateID == candidateID {
			return value, nil
		}
	}
	return researchdomain.CandidateConfiguration{}, researchdomain.ErrConfigurationNotFound
}
func (r *memoryResearchRepository) ListRounds(
	context.Context, string, string,
) ([]researchdomain.ResearchRound, error) {
	if r.round.ID == "" {
		return []researchdomain.ResearchRound{}, nil
	}
	return []researchdomain.ResearchRound{r.round}, nil
}
func (r *memoryResearchRepository) CreateFeedback(
	_ context.Context,
	value researchdomain.ResearchFeedback,
) error {
	r.feedback = &value
	return nil
}
func (r *memoryResearchRepository) GetFeedbackForRound(
	_ context.Context, _ string, roundID string, _ bool,
) (researchdomain.ResearchFeedback, error) {
	if r.feedback != nil && r.feedback.NextRoundID == roundID {
		return *r.feedback, nil
	}
	return researchdomain.ResearchFeedback{}, researchdomain.ErrFeedbackNotFound
}
func (r *memoryResearchRepository) FindFeedbackByRequest(
	_ context.Context, _ string, requestID string,
) (researchdomain.ResearchFeedback, bool, error) {
	if r.feedback != nil && r.feedback.ClientRequestID == requestID {
		return *r.feedback, true, nil
	}
	return researchdomain.ResearchFeedback{}, false, nil
}
func (r *memoryResearchRepository) UpdateFeedback(
	_ context.Context,
	value researchdomain.ResearchFeedback,
) error {
	r.feedback = &value
	return nil
}

type memoryShoppingRepository struct {
	session shoppingsessiondomain.ShoppingSession
}

func (r *memoryShoppingRepository) Create(
	context.Context,
	shoppingsessiondomain.ShoppingSession,
) error {
	return nil
}
func (r *memoryShoppingRepository) FindByTarget(
	context.Context, string, string,
) (shoppingsessiondomain.ShoppingSession, error) {
	return r.session, nil
}
func (r *memoryShoppingRepository) Get(
	_ context.Context, userID, sessionID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	if string(r.session.UserID) != userID || string(r.session.ID) != sessionID {
		return shoppingsessiondomain.ShoppingSession{}, shoppingsessiondomain.ErrSessionNotFound
	}
	return r.session, nil
}
func (r *memoryShoppingRepository) GetForUpdate(
	ctx context.Context, userID, sessionID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	return r.Get(ctx, userID, sessionID)
}
func (r *memoryShoppingRepository) Save(
	_ context.Context,
	previousVersion int64,
	session shoppingsessiondomain.ShoppingSession,
) error {
	if r.session.Version != previousVersion {
		return shoppingsessiondomain.ErrSessionNotFound
	}
	r.session = session
	return nil
}

func newSubmitFixture(t *testing.T) (
	*Service,
	*memoryResearchRepository,
	*memoryShoppingRepository,
	ContextResult,
	time.Time,
) {
	t.Helper()
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	target := shoppingsessionapp.TargetSnapshot{
		ID: "target-1", PlanID: "plan-1", CurationID: "curation-1",
		Title: "캠핑 의자", NormalizedIntent: "가벼운 캠핑 의자",
		Category: "camping-chair", AllocatedBudget: mustMoney(t, "100", "USD"),
		TargetHash: "target-hash", TargetHashSchema: "vitlane.plan-target.v1",
	}
	scope := shoppingsessionapp.ResearchScopeSnapshot{
		Category: "camping-chair", Country: "KR", URLMode: "NONE",
	}
	contextSnapshot, err := json.Marshal(ResearchContext{
		RoundID: testRoundID, SessionID: "session-1", PlanID: "plan-1",
		RoundNumber: 1, Target: target, ResearchScope: scope,
		CandidateMinimum: 1, CandidateMaximum: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	round, err := researchdomain.NewRound(
		testRoundID, "session-1", "user-1", 1, contextSnapshot, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	roundID := round.ID
	targetSnapshot, _ := json.Marshal(target)
	scopeSnapshot, _ := json.Marshal(scope)
	shoppingRepository := &memoryShoppingRepository{session: shoppingsessiondomain.ShoppingSession{
		ID: "session-1", PlanTargetID: "target-1", UserID: "user-1",
		TargetSnapshot: targetSnapshot, ResearchScopeSnapshot: scopeSnapshot,
		Status:                 shoppingsessiondomain.SessionStatusReviewing,
		CurrentResearchRoundID: &roundID, Version: 3,
	}}
	researchRepository := &memoryResearchRepository{
		round: round, candidates: map[string]researchdomain.Candidate{},
	}
	clock := testClock{now: now}
	service := NewService(
		researchRepository,
		testPlanningService{result: planResultForSession(
			shoppingRepository.session, curationdomain.CurationPhaseCurating,
		)},
		shoppingsessionapp.NewService(shoppingRepository, clock, &testIDs{}),
		testTransactor{}, clock, &testIDs{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	return service, researchRepository, shoppingRepository, ContextResult{
		ContextSchema: round.ContextSchema, ContextVersion: round.ContextVersion,
		ContextHash: round.ContextHash,
	}, now
}

func seedHistoricalCandidate(
	t *testing.T,
	repository *memoryResearchRepository,
	now time.Time,
) researchdomain.Candidate {
	t.Helper()
	price := mustMoney(t, "49.90", "USD")
	value := researchdomain.Candidate{
		ID: "historical-candidate-1", ResearchSubmissionID: "historical-submission-1",
		ShoppingSessionID: "session-1",
		ProductURL:        "https://shop.example.com/products/chair",
		MerchantDomain:    "shop.example.com", Category: "camping-chair",
		Name: "Light Chair", Price: price,
		VariantDiscovery: researchdomain.VariantDiscovery{
			SchemaVersion:       researchdomain.VariantDiscoverySchemaV1,
			Status:              researchdomain.VariantDiscoveryUnknown,
			Fields:              []researchdomain.VariantField{},
			ProviderVariantRefs: []researchdomain.ProviderVariantRef{},
			ObservedAt:          now,
			Evidence: researchdomain.VariantDiscoveryEvidence{
				Summary:    "historical purchase compatibility snapshot",
				SourceURLs: []string{"https://shop.example.com/products/chair"},
			},
		},
		CandidateHashSchema: researchdomain.CandidateHashSchemaV3,
		CandidateHash:       "historical-candidate-hash", CreatedAt: now,
	}
	repository.candidates[value.ID] = value
	return value
}

func TestStartResearchCreatesOneRoundAndJobAndReplays(t *testing.T) {
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	target := shoppingsessionapp.TargetSnapshot{
		ID: "target-1", PlanID: "plan-1", CurationID: "curation-1",
		Title: "캠핑 의자", NormalizedIntent: "가벼운 캠핑 의자",
		Category: "camping-chair", AllocatedBudget: mustMoney(t, "100", "USD"),
		TargetHash: "target-hash", TargetHashSchema: "vitlane.plan-target.v1",
	}
	targetSnapshot, _ := json.Marshal(target)
	scopeSnapshot, _ := json.Marshal(shoppingsessionapp.ResearchScopeSnapshot{
		Category: "camping-chair", Country: "KR", URLMode: "NONE",
	})
	shoppingRepository := &memoryShoppingRepository{session: shoppingsessiondomain.ShoppingSession{
		ID: "session-1", PlanTargetID: "target-1", UserID: "user-1",
		TargetSnapshot: targetSnapshot, ResearchScopeSnapshot: scopeSnapshot,
		Status: shoppingsessiondomain.SessionStatusReady, Version: 1,
	}}
	researchRepository := &memoryResearchRepository{candidates: map[string]researchdomain.Candidate{}}
	ids := &testIDs{}
	service := NewService(
		researchRepository,
		testPlanningService{result: planResultForSession(
			shoppingRepository.session, curationdomain.CurationPhasePlanning,
		)},
		shoppingsessionapp.NewService(shoppingRepository, testClock{now: now}, ids),
		testTransactor{}, testClock{now: now}, ids,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	jobs := &testJobCreator{repository: researchRepository}
	service.EnableIntelligenceWork(jobs)
	input := StartResearchInput{
		UserID: "user-1", AuthSessionID: "auth-session-1",
		PlanID: "plan-1", CurationID: "curation-1",
		CurationActionID:        "11111111-1111-4111-8111-111111111111",
		ExpectedCurationVersion: 1, SessionIDs: []string{"session-1"},
		IdempotencyKey: "11111111-1111-4111-8111-111111111111",
	}
	created, err := service.StartResearch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Tasks) != 1 || len(jobs.calls) != 1 ||
		jobs.calls[0].ResearchRoundID != created.Tasks[0].RoundID ||
		jobs.calls[0].CurationActionID != input.CurationActionID {
		t.Fatalf("round/job lineage=%#v %#v", created, jobs.calls)
	}

	completedAt := now.Add(time.Minute)
	if err := researchRepository.round.CompleteFromCandidatePool(true, completedAt); err != nil {
		t.Fatal(err)
	}
	if err := shoppingRepository.session.CompleteResearch(
		researchRepository.round.ID, completedAt,
	); err != nil {
		t.Fatal(err)
	}
	replayedPlan := planResultForSession(
		shoppingRepository.session, curationdomain.CurationPhaseCurating,
	)
	replayedPlan.Curation.Version = 2
	service.plans = testPlanningService{result: replayedPlan, replay: true}
	replayed, err := service.StartResearch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Tasks[0].RoundID != created.Tasks[0].RoundID ||
		replayed.Tasks[0].Status != researchdomain.RoundStatusResultsReady ||
		len(jobs.calls) != 2 {
		t.Fatalf("replay=%#v jobs=%#v", replayed, jobs.calls)
	}
}

func TestTerminalResearchFailureReleasesInitialSession(t *testing.T) {
	service, repository, shopping, _, _ := newSubmitFixture(t)
	shopping.session.Status = shoppingsessiondomain.SessionStatusResearching

	if err := service.FailResearchRound(
		context.Background(), "user-1", repository.round.ID,
		"CANDIDATE_RANKING_EMPTY", false,
	); err != nil {
		t.Fatal(err)
	}
	if repository.round.Status != researchdomain.RoundStatusFailed ||
		repository.round.FailureReasonCode != "CANDIDATE_RANKING_EMPTY" ||
		shopping.session.Status != shoppingsessiondomain.SessionStatusReady ||
		shopping.session.CurrentResearchRoundID == nil ||
		*shopping.session.CurrentResearchRoundID != repository.round.ID {
		t.Fatalf("round=%#v session=%#v", repository.round, shopping.session)
	}
	if err := service.FailResearchRound(
		context.Background(), "user-1", repository.round.ID,
		"CANDIDATE_RANKING_EMPTY", false,
	); err != nil {
		t.Fatalf("terminal replay must be idempotent: %v", err)
	}
}

func TestIntelligenceContextIncludesExactFeedbackForReresearch(t *testing.T) {
	service, repository, _, _, _ := newSubmitFixture(t)
	repository.feedback = &researchdomain.ResearchFeedback{
		ID: "feedback-1", UserID: "user-1",
		PreviousRoundID: "previous-round", NextRoundID: repository.round.ID,
		Feedback:        "더 가볍고 등받이가 높은 후보를 찾아 주세요.",
		FeedbackVersion: 4, FeedbackHash: "feedback-hash",
		Status: researchdomain.FeedbackStatusActive,
	}
	result, err := service.GetContextForIntelligence(
		context.Background(), "user-1",
		"11111111-1111-4111-8111-111111111111", repository.round.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FeedbackRequired || result.FeedbackVersion != 4 ||
		result.FeedbackHash != "feedback-hash" ||
		result.FeedbackSummary != "더 가볍고 등받이가 높은 후보를 찾아 주세요." {
		t.Fatalf("feedback context=%#v", result)
	}
}

func TestGetPlanResearchReturnsLifecycleWithoutLegacyCandidateData(t *testing.T) {
	service, repository, _, _, now := newSubmitFixture(t)
	seedHistoricalCandidate(t, repository, now)
	result, err := service.GetPlanResearch(context.Background(), "user-1", "plan-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Groups) != 1 || result.Groups[0].Round == nil ||
		len(result.Groups[0].Rounds) != 1 {
		t.Fatalf("lifecycle projection=%#v", result)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if stringsContainAny(string(payload), "candidates", "submission") {
		t.Fatalf("legacy Candidate data leaked into lifecycle projection: %s", payload)
	}
}

func stringsContainAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func mustMoney(t *testing.T, amount, currency string) shareddomain.Money {
	t.Helper()
	money, err := shareddomain.NewMoney(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return money
}

var _ sharedapp.Transactor = testTransactor{}

func TestLateFailureOnTerminalRoundIsAbsorbedWithoutError(t *testing.T) {
	// 이미 다른 사유로 종결된 Round에 늦은 실패가 도착해도 오류를 돌리지
	// 않아야 한다. 오류가 되면 같은 트랜잭션의 Job 종결까지 롤백되어 실행
	// 불가능한 Job이 재시도만 반복하는 좀비가 된다(2026-08-16 production).
	service, repository, shopping, _, _ := newSubmitFixture(t)
	shopping.session.Status = shoppingsessiondomain.SessionStatusResearching
	if err := service.FailResearchRound(
		context.Background(), "user-1", repository.round.ID,
		"CANDIDATE_RANKING_EMPTY", false,
	); err != nil {
		t.Fatal(err)
	}
	if err := service.FailResearchRound(
		context.Background(), "user-1", repository.round.ID,
		"RESEARCH_ROUND_CLOSED", true,
	); err != nil {
		t.Fatalf("late mismatched failure must be absorbed: %v", err)
	}
	if repository.round.FailureReasonCode != "CANDIDATE_RANKING_EMPTY" {
		t.Fatalf("terminal fact was overwritten: %#v", repository.round)
	}
}

func TestCancelResearchRoundOnTerminalRoundSucceeds(t *testing.T) {
	// 취소의 목적은 일이 더 실행되지 않는 것이다. Round가 이미 terminal이면
	// 목적이 달성돼 있으므로 취소는 성공해야 하며, 오류가 되면 함께 취소
	// 중인 Job 트랜잭션까지 실패한다(2026-08-16 production 좀비 취소 불가).
	service, repository, shopping, _, _ := newSubmitFixture(t)
	shopping.session.Status = shoppingsessiondomain.SessionStatusResearching
	if err := service.FailResearchRound(
		context.Background(), "user-1", repository.round.ID,
		"CANDIDATE_RANKING_EMPTY", false,
	); err != nil {
		t.Fatal(err)
	}
	if err := service.CancelResearchRound(
		context.Background(), "user-1", repository.round.ID,
	); err != nil {
		t.Fatalf("cancel of a terminal round must succeed: %v", err)
	}
}

func TestAttachResearchWorkSkipsTerminalRound(t *testing.T) {
	// replay가 종결된 Round를 돌려준 경우 새 Job을 붙이면 실행 즉시
	// RESEARCH_ROUND_CLOSED로 거부되는 좀비가 된다. terminal Round에는 job을
	// 만들지 않는다.
	service, repository, _, _, now := newSubmitFixture(t)
	jobs := &testJobCreator{repository: repository}
	service.EnableIntelligenceWork(jobs)
	round := repository.round
	round.Status = researchdomain.RoundStatusFailed
	result := ResearchAgainResult{Round: round}
	if err := service.attachResearchWork(
		context.Background(),
		ResearchAgainInput{UserID: "user-1"},
		&result,
		true,
	); err != nil {
		t.Fatal(err)
	}
	if len(jobs.calls) != 0 {
		t.Fatalf("job attached to a terminal round: %#v", jobs.calls)
	}
	_ = now
}

func TestGetContextForIntelligenceClassifiesClosedRoundTerminal(t *testing.T) {
	// 종결 round 읽기가 unclassified 오류로 남으면 워커가
	// INTERNAL_FAILURE(retryable)로 오분류해 시도만 반복하고, 사용자에게는
	// 원인 불명 반복 실패로 보인다. 재시도로 달라지지 않는 사실 충돌임을
	// fault로 못박는다.
	service, repository, shopping, _, _ := newSubmitFixture(t)
	shopping.session.Status = shoppingsessiondomain.SessionStatusResearching
	if err := service.FailResearchRound(
		context.Background(), "user-1", repository.round.ID,
		"CANDIDATE_RANKING_EMPTY", false,
	); err != nil {
		t.Fatal(err)
	}
	_, err := service.GetContextForIntelligence(
		context.Background(), "user-1",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", repository.round.ID,
	)
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.Conflict ||
		failure.Reason != "RESEARCH_ROUND_CLOSED" || failure.Retryable {
		t.Fatalf("closed round read was not a terminal conflict fault: %v", err)
	}
	if !errors.Is(err, researchdomain.ErrRoundClosed) {
		t.Fatalf("fault lost the ErrRoundClosed identity: %v", err)
	}
}

func TestResearchCapacityIncludesHiddenBeforeAnyModelWork(t *testing.T) {
	service, repository, _, _, _ := newSubmitFixture(t)
	var ctx ResearchContext
	if err := json.Unmarshal(repository.round.ContextSnapshot, &ctx); err != nil {
		t.Fatal(err)
	}
	state := CatalogWorkspaceStoredStateV2{}
	for i := 0; i < 50; i++ {
		state.Candidates = append(state.Candidates, CatalogCandidateReferenceV2{PlanTargetID: ctx.Target.ID, Visible: i < 30})
	}
	service.liveCatalog = &LiveCatalogReviewServiceV2{workspace: &liveReviewWorkspaceRepositoryV2{state: state}}
	_, err := service.GetContextForIntelligence(context.Background(), "user-1", "11111111-1111-4111-8111-111111111111", repository.round.ID)
	f, ok := fault.As(err)
	if !ok || f.Reason != "RESEARCH_CANDIDATE_CAPACITY_REACHED" {
		t.Fatalf("capacity should stop context before model: %v", err)
	}
}
