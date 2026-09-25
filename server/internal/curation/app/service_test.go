package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type sequenceIDs struct {
	values []string
	index  int
}

func (g *sequenceIDs) NewID() string {
	value := g.values[g.index]
	g.index++
	return value
}

type passthroughTransactor struct{}

func (passthroughTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type memoryPlanningRepository struct {
	record     PlanRecord
	task       *curationdomain.PlanningTask
	proposals  map[string]curationdomain.PlanningProposal
	actions    map[string]curationdomain.CurationAction
	createKey  string
	createHash []byte
}

type memoryCurationRepository struct {
	*memoryPlanningRepository
	runs map[string]curationdomain.CurationRun
}

func (r *memoryCurationRepository) CreateExpansion(
	_ context.Context,
	run curationdomain.CurationRun,
	task curationdomain.PlanningTask,
) (curationdomain.CurationRun, curationdomain.PlanningTask, bool, error) {
	if r.runs == nil {
		r.runs = map[string]curationdomain.CurationRun{}
	}
	for _, existing := range r.runs {
		if existing.IdempotencyKey == run.IdempotencyKey {
			return existing, *r.task, true, nil
		}
	}
	r.runs[string(run.ID)] = run
	r.task = &task
	return run, task, false, nil
}

func (r *memoryCurationRepository) GetCurationRun(
	_ context.Context, userID, planID, runID string, _ bool,
) (curationdomain.CurationRun, error) {
	run, ok := r.runs[runID]
	if !ok || string(run.UserID) != userID || string(run.PlanID) != planID {
		return curationdomain.CurationRun{}, curationdomain.ErrCurationRunNotFound
	}
	return run, nil
}

func (r *memoryCurationRepository) GetCurationRunByTask(
	ctx context.Context, userID, planID, taskID string, forUpdate bool,
) (curationdomain.CurationRun, error) {
	for id, run := range r.runs {
		if string(run.PlanningTaskID) == taskID {
			return r.GetCurationRun(ctx, userID, planID, id, forUpdate)
		}
	}
	return curationdomain.CurationRun{}, curationdomain.ErrCurationRunNotFound
}

func (r *memoryCurationRepository) FindOpenExpansion(
	_ context.Context, userID, planID string,
) (curationdomain.CurationRun, bool, error) {
	for _, run := range r.runs {
		if string(run.UserID) != userID || string(run.PlanID) != planID ||
			run.Kind != curationdomain.CurationRunExpansion {
			continue
		}
		switch run.Status {
		case curationdomain.CurationRunRequested,
			curationdomain.CurationRunMaterializing:
			return run, true, nil
		}
	}
	return curationdomain.CurationRun{}, false, nil
}

func (r *memoryCurationRepository) GetExpansionByIdempotency(
	_ context.Context, userID, idempotencyKey string, _ bool,
) (curationdomain.CurationRun, curationdomain.PlanningTask, error) {
	for _, run := range r.runs {
		if string(run.UserID) == userID &&
			run.IdempotencyKey == idempotencyKey {
			if r.task == nil || r.task.ID != run.PlanningTaskID {
				return curationdomain.CurationRun{}, curationdomain.PlanningTask{},
					curationdomain.ErrPlanningTaskNotFound
			}
			return run, *r.task, nil
		}
	}
	return curationdomain.CurationRun{}, curationdomain.PlanningTask{},
		curationdomain.ErrCurationRunNotFound
}

func (r *memoryCurationRepository) UpdateCurationRun(
	_ context.Context, run curationdomain.CurationRun,
) error {
	if _, ok := r.runs[string(run.ID)]; !ok {
		return curationdomain.ErrCurationRunNotFound
	}
	r.runs[string(run.ID)] = run
	return nil
}

func (r *memoryPlanningRepository) CreateIdempotent(
	_ context.Context,
	plan curationdomain.PlanSnapshot,
	targets []curationdomain.PlanTarget,
	task *curationdomain.PlanningTask,
	idempotencyKey string,
	requestHash []byte,
) (PlanRecord, bool, error) {
	if r.createKey != "" {
		if r.createKey != idempotencyKey ||
			!bytes.Equal(r.createHash, requestHash) {
			return PlanRecord{}, false, curationdomain.ErrIdempotencyKeyReused
		}
		return r.record, true, nil
	}
	curation, err := curationdomain.NewCuration(curationdomain.NewCurationInput{
		ID:             curationdomain.CurationID("curation-" + string(plan.ID)),
		ShoppingPlanID: plan.ID, UserID: plan.UserID, Now: plan.CreatedAt,
	})
	if err != nil {
		return PlanRecord{}, false, err
	}
	for index := range targets {
		targets[index].CurationID = curation.ID
		targets[index].UserID = plan.UserID
	}
	r.record = PlanRecord{Plan: plan, Curation: curation, Targets: targets}
	r.task = task
	r.createKey = idempotencyKey
	r.createHash = append([]byte(nil), requestHash...)
	if r.proposals == nil {
		r.proposals = map[string]curationdomain.PlanningProposal{}
	}
	return r.record, false, nil
}

func (r *memoryPlanningRepository) Get(
	_ context.Context, userID, planID string, _ bool,
) (PlanRecord, error) {
	if string(r.record.Plan.UserID) != userID || string(r.record.Plan.ID) != planID {
		return PlanRecord{}, curationdomain.ErrPlanNotFound
	}
	return r.record, nil
}

func (r *memoryPlanningRepository) InsertTargets(
	_ context.Context,
	targets []curationdomain.PlanTarget,
) error {
	r.record.Targets = append(r.record.Targets, targets...)
	return nil
}

func (r *memoryPlanningRepository) GetCuration(
	_ context.Context, userID, curationID string, _ bool,
) (curationdomain.Curation, error) {
	if string(r.record.Curation.UserID) != userID ||
		string(r.record.Curation.ID) != curationID {
		return curationdomain.Curation{}, curationdomain.ErrCurationNotFound
	}
	return r.record.Curation, nil
}

func (r *memoryPlanningRepository) SaveCuration(
	_ context.Context,
	previousVersion int64,
	curation curationdomain.Curation,
) error {
	if r.record.Curation.Version != previousVersion {
		return curationdomain.ErrVersionConflict
	}
	r.record.Curation = curation
	return nil
}

func (r *memoryPlanningRepository) GetCurationAction(
	_ context.Context,
	userID, actionID string,
	_ bool,
) (curationdomain.CurationAction, error) {
	action, found := r.actions[actionID]
	if !found || string(action.ActorUserID) != userID {
		return curationdomain.CurationAction{}, ErrCurationActionNotFound
	}
	return action, nil
}

func (r *memoryPlanningRepository) InsertCurationAction(
	_ context.Context,
	action curationdomain.CurationAction,
) (bool, error) {
	if r.actions == nil {
		r.actions = map[string]curationdomain.CurationAction{}
	}
	if _, found := r.actions[string(action.ID)]; found {
		return false, nil
	}
	r.actions[string(action.ID)] = action
	return true, nil
}

func (r *memoryPlanningRepository) ListCurationActions(
	_ context.Context,
	userID, curationID string,
	_ int,
) ([]curationdomain.CurationAction, error) {
	result := make([]curationdomain.CurationAction, 0, len(r.actions))
	for _, action := range r.actions {
		if string(action.ActorUserID) == userID &&
			string(action.CurationID) == curationID {
			result = append(result, action)
		}
	}
	return result, nil
}

func (r *memoryPlanningRepository) GetPlanningTask(
	_ context.Context, userID, planID string, _ bool,
) (curationdomain.PlanningTask, error) {
	if r.task == nil || string(r.task.UserID) != userID || string(r.task.PlanID) != planID {
		return curationdomain.PlanningTask{}, curationdomain.ErrPlanningTaskNotFound
	}
	return *r.task, nil
}

func (r *memoryPlanningRepository) GetPlanningTaskByID(
	_ context.Context, userID, taskID string, _ bool,
) (curationdomain.PlanningTask, error) {
	if r.task == nil || string(r.task.UserID) != userID ||
		string(r.task.ID) != taskID {
		return curationdomain.PlanningTask{}, curationdomain.ErrPlanningTaskNotFound
	}
	return *r.task, nil
}

func (r *memoryPlanningRepository) ListPlanningTasks(
	_ context.Context, userID, planID string, _ int,
) ([]curationdomain.PlanningTask, error) {
	task, err := r.GetPlanningTask(context.Background(), userID, planID, false)
	if err != nil || task.Status != curationdomain.PlanningTaskStatusRequested {
		return []curationdomain.PlanningTask{}, err
	}
	return []curationdomain.PlanningTask{task}, nil
}

func (r *memoryPlanningRepository) UpdatePlanningTask(
	_ context.Context, task curationdomain.PlanningTask,
) error {
	r.task = &task
	return nil
}

func proposalKey(taskID, grantID, clientID string) string {
	return taskID + ":" + grantID + ":" + clientID
}

func (r *memoryPlanningRepository) FindIntelligenceProposal(
	_ context.Context, taskID, jobID, clientID string,
) (curationdomain.PlanningProposal, bool, error) {
	proposal, ok := r.proposals[proposalKey(taskID, jobID, clientID)]
	return proposal, ok, nil
}

func (r *memoryPlanningRepository) InsertProposal(
	_ context.Context, proposal curationdomain.PlanningProposal,
) error {
	r.proposals[proposalKey(
		string(proposal.TaskID), proposal.IntelligenceJobID,
		proposal.ClientProposalID,
	)] = proposal
	return nil
}

func (r *memoryPlanningRepository) GetPlanningProposalResult(
	_ context.Context, userID, taskID, proposalID string,
) (curationdomain.PlanningProposal, error) {
	for _, proposal := range r.proposals {
		if string(proposal.UserID) == userID &&
			string(proposal.TaskID) == taskID &&
			string(proposal.ID) == proposalID {
			return proposal, nil
		}
	}
	return curationdomain.PlanningProposal{}, curationdomain.ErrProposalInvalid
}

type memorySessions struct {
	sessions map[string]shoppingsessiondomain.ShoppingSession
}

func (s *memorySessions) CreateReady(
	_ context.Context, input shoppingsessionapp.CreateReadyInput,
) (shoppingsessiondomain.ShoppingSession, error) {
	if s.sessions == nil {
		s.sessions = map[string]shoppingsessiondomain.ShoppingSession{}
	}
	if session, ok := s.sessions[input.TargetID]; ok {
		return session, nil
	}
	session := shoppingsessiondomain.ShoppingSession{
		ID:           shoppingsessiondomain.ShoppingSessionID("session-" + input.TargetID),
		PlanTargetID: shoppingsessiondomain.PlanTargetID(input.TargetID),
		UserID:       shoppingsessiondomain.UserID(input.UserID),
		Status:       shoppingsessiondomain.SessionStatusReady,
	}
	s.sessions[input.TargetID] = session
	return session, nil
}

func (s *memorySessions) Get(
	_ context.Context, _ string, sessionID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	for _, session := range s.sessions {
		if string(session.ID) == sessionID {
			return session, nil
		}
	}
	return shoppingsessiondomain.ShoppingSession{}, shoppingsessiondomain.ErrSessionNotFound
}

func (s *memorySessions) FindByTarget(
	_ context.Context, _ string, targetID string,
) (shoppingsessiondomain.ShoppingSession, error) {
	session, ok := s.sessions[targetID]
	if !ok {
		return shoppingsessiondomain.ShoppingSession{}, shoppingsessiondomain.ErrSessionNotFound
	}
	return session, nil
}

func newTestService(repository Repository, sessions *memorySessions) *Service {
	ids := &sequenceIDs{values: []string{
		"plan-1", "target-single", "unused-create-task",
		"task-initial", "run-initial",
		"proposal-1", "target-chair", "target-lamp",
		"proposal-2", "target-extra",
	}}
	return NewService(
		repository, sessions, passthroughTransactor{},
		fixedClock{now: time.Date(2026, 7, 18, 1, 2, 3, 0, time.UTC)},
		ids, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func startInitialPlanning(
	t *testing.T,
	service *Service,
	created PlanResult,
	actionID string,
) ExpansionResult {
	t.Helper()
	if created.PlanningTask == nil {
		t.Fatal("Intent NextStep did not create the INITIAL PlanningTask")
	}
	run := curationdomain.CurationRun{
		ID:             curationdomain.CurationRunID(actionID),
		CurationID:     created.Curation.ID,
		PlanID:         created.Plan.ID,
		UserID:         created.Plan.UserID,
		Kind:           curationdomain.CurationRunInitial,
		Instruction:    created.Plan.OriginalIntent,
		PlanningTaskID: created.PlanningTask.ID,
		Status:         curationdomain.CurationRunRequested,
		IdempotencyKey: actionID,
		CreatedAt:      created.PlanningTask.CreatedAt,
		UpdatedAt:      created.PlanningTask.UpdatedAt,
	}
	repository, ok := service.repository.(*memoryCurationRepository)
	if !ok {
		t.Fatal("test repository does not support CurationRun")
	}
	repository.runs[string(run.ID)] = run
	return ExpansionResult{
		Run: run, PlanningTask: *created.PlanningTask,
	}
}

type planningTransactionKey struct{}

type markingTransactor struct{}

func (markingTransactor) WithinTransaction(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	return fn(context.WithValue(ctx, planningTransactionKey{}, true))
}

func TestSingleCreateStartsPlanningWithIntentSettings(t *testing.T) {
	repository := &memoryPlanningRepository{}
	sessions := &memorySessions{}
	service := newTestService(repository, sessions)

	created, err := service.CreatePlan(context.Background(), CreatePlanInput{
		UserID: "user-1", OriginalIntent: "가벼운 캠핑 의자", PlanningMode: "SINGLE",
		ExecutionMode: "EXPERIMENT",
		TotalBudget:   MoneyInput{Amount: "100", Currency: "USD"},
		Country:       "KR", City: "서울", Category: "camping-chair", URLMode: "NONE",
		AgentMode: "MANAGED", ModelKey: "gpt-5-nano",
		IdempotencyKey: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(created.Targets) != 0 ||
		created.Curation.Phase != curationdomain.CurationPhasePlanning ||
		len(created.Sessions) != 0 ||
		created.PlanningTask == nil ||
		created.Plan.ResearchScope.Category != "camping-chair" {
		t.Fatalf("unexpected single result: %#v", created)
	}

	if len(sessions.sessions) != 0 {
		t.Fatal("SINGLE must wait for the INITIAL Planning proposal")
	}
	initialContext, err := service.GetPlanningContext(
		context.Background(), "user-1", string(created.Plan.ID),
		string(created.PlanningTask.ID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if initialContext.OriginalIntent != created.Plan.OriginalIntent {
		t.Fatalf(
			"initial request = %q, want plan Intent %q",
			initialContext.OriginalIntent,
			created.Plan.OriginalIntent,
		)
	}
}

func TestSingleModeAllowsExplicitMultiTargetExpansion(t *testing.T) {
	base := &memoryPlanningRepository{}
	sessions := &memorySessions{}
	initialRepository := &memoryCurationRepository{
		memoryPlanningRepository: base,
		runs:                     map[string]curationdomain.CurationRun{},
	}
	initial := newTestService(initialRepository, sessions)
	created, err := initial.CreatePlan(context.Background(), CreatePlanInput{
		UserID: "user-1", OriginalIntent: "노이즈 캔슬링 헤드폰", PlanningMode: "SINGLE",
		ExecutionMode: "EXPERIMENT",
		TotalBudget:   MoneyInput{Amount: "100", Currency: "USD"},
		Country:       "KR", City: "서울", Category: "headphones", URLMode: "NONE",
		AgentMode: "MANAGED", ModelKey: "gpt-5-nano",
		IdempotencyKey: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	initialRun := startInitialPlanning(
		t, initial, created,
		"22222222-2222-4222-8222-222222222222",
	)
	if _, err := initial.SubmitPlanningProposalForIntelligence(
		context.Background(), "user-1", "job-1",
		SubmitPlanningProposalInput{
			UserID: "user-1", PlanID: string(created.Plan.ID),
			TaskID:           string(initialRun.PlanningTask.ID),
			ClientProposalID: "33333333-3333-4333-8333-333333333333",
			ContextVersion:   initialRun.PlanningTask.ContextVersion,
			ContextHash:      initialRun.PlanningTask.ContextHash,
			SchemaVersion:    curationdomain.PlanningProposalSchemaV1,
			Targets: []TargetInput{{
				Title: "헤드폰", NormalizedIntent: "노이즈 캔슬링 헤드폰",
				Category:        "headphones",
				AllocatedBudget: MoneyInput{Amount: "100", Currency: "USD"},
				URLMode:         "NONE",
			}},
		},
	); err != nil {
		t.Fatalf("materialize SINGLE initial target: %v", err)
	}
	repository := &memoryCurationRepository{
		memoryPlanningRepository: base,
		runs:                     map[string]curationdomain.CurationRun{},
	}
	service := NewService(
		repository,
		sessions,
		passthroughTransactor{},
		fixedClock{now: time.Date(2026, 7, 18, 2, 2, 3, 0, time.UTC)},
		&sequenceIDs{values: []string{
			"task-single-expansion",
			"run-single-expansion",
			"proposal-single-expansion",
			"target-keyboard",
			"target-mouse",
		}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	expansion, err := service.CreateExpansion(
		context.Background(),
		CreateExpansionInput{
			UserID: "user-1", PlanID: string(created.Plan.ID),
			CurationActionID: "33333333-3333-4333-8333-333333333333",
			Instruction:      "키보드와 마우스도 추가해줘",
			IdempotencyKey:   "22222222-2222-4222-8222-222222222222",
		},
	)
	if err != nil {
		t.Fatalf("explicit SINGLE expansion should be available: %v", err)
	}
	available, err := service.EnsurePlanningAvailable(
		context.Background(), "user-1", string(created.Plan.ID),
	)
	if err != nil || available.ID != expansion.PlanningTask.ID {
		t.Fatalf("SINGLE expansion task should be eligible for an exact Grant: task=%#v err=%v", available, err)
	}
	result, err := service.SubmitPlanningProposalForIntelligence(
		context.Background(), "user-1", "job-1",
		SubmitPlanningProposalInput{
			UserID: "user-1", PlanID: string(created.Plan.ID),
			TaskID:           string(expansion.PlanningTask.ID),
			ClientProposalID: "33333333-3333-4333-8333-333333333333",
			ContextVersion:   expansion.PlanningTask.ContextVersion,
			ContextHash:      expansion.PlanningTask.ContextHash,
			SchemaVersion:    curationdomain.PlanningProposalSchemaV1,
			Targets: []TargetInput{{
				Title: "키보드", NormalizedIntent: "휴대용 키보드",
				Category:        "keyboard",
				AllocatedBudget: MoneyInput{Amount: "40", Currency: "USD"},
				URLMode:         "NONE",
			}, {
				Title: "마우스", NormalizedIntent: "휴대용 마우스",
				Category:        "mouse",
				AllocatedBudget: MoneyInput{Amount: "30", Currency: "USD"},
				URLMode:         "NONE",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if base.record.Plan.PlanningMode != curationdomain.PlanningModeSingle ||
		len(base.record.Targets) != 3 ||
		len(result.CreatedTargetIDs) != 2 ||
		len(result.CreatedSessionIDs) != 2 {
		t.Fatalf("unexpected SINGLE expansion: result=%#v aggregate=%#v", result, base.record)
	}
}

func TestAutoProposalConfirmsTargetsAndCreatesSessions(t *testing.T) {
	base := &memoryPlanningRepository{}
	repository := &memoryCurationRepository{
		memoryPlanningRepository: base,
		runs:                     map[string]curationdomain.CurationRun{},
	}
	sessions := &memorySessions{}
	service := newTestService(repository, sessions)
	created, err := service.CreatePlan(context.Background(), CreatePlanInput{
		UserID: "user-1", OriginalIntent: "100달러 캠핑 장비", PlanningMode: "AUTO",
		ExecutionMode: "EXPERIMENT",
		TotalBudget:   MoneyInput{Amount: "100", Currency: "USD"},
		Country:       "KR", City: "서울", URLMode: "NONE",
		AgentMode: "MANAGED", ModelKey: "gpt-5-nano",
		IdempotencyKey: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Targets) != 0 || created.PlanningTask == nil {
		t.Fatalf("AUTO creation must start INITIAL Planning work: %#v", created)
	}
	initial := startInitialPlanning(
		t, service, created,
		"22222222-2222-4222-8222-222222222222",
	)

	proposalInput := SubmitPlanningProposalInput{
		UserID: "user-1", PlanID: "plan-1",
		TaskID:           string(initial.PlanningTask.ID),
		ClientProposalID: "33333333-3333-4333-8333-333333333333",
		ContextVersion:   initial.PlanningTask.ContextVersion,
		ContextHash:      initial.PlanningTask.ContextHash,
		SchemaVersion:    curationdomain.PlanningProposalSchemaV1,
		Targets: []TargetInput{
			{
				Title: "의자", NormalizedIntent: "가벼운 캠핑 의자", Category: "camping-chair",
				AllocatedBudget: MoneyInput{Amount: "60", Currency: "USD"}, URLMode: "NONE",
			},
			{
				Title: "랜턴", NormalizedIntent: "충전식 캠핑 랜턴", Category: "camping-lantern",
				AllocatedBudget: MoneyInput{Amount: "40", Currency: "USD"}, URLMode: "NONE",
			},
		},
	}
	submitted, err := service.SubmitPlanningProposalForIntelligence(context.Background(), "user-1", "job-1", proposalInput)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.ValidationStatus != curationdomain.ProposalValidationAccepted ||
		len(repository.record.Targets) != 2 ||
		repository.record.Curation.Version != initial.PlanningTask.ContextVersion+1 ||
		len(submitted.CreatedSessionIDs) != 2 ||
		submitted.NextAction != "START_CURATING" {
		t.Fatalf("proposal not materialized: %#v %#v", submitted, repository.record)
	}
	replayed, err := service.SubmitPlanningProposalForIntelligence(context.Background(), "user-1", "job-1", proposalInput)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if len(replayed.CreatedSessionIDs) != 2 || len(sessions.sessions) != 2 {
		t.Fatal("idempotent replay did not return the existing sessions")
	}

	proposalInput.ClientProposalID = "44444444-4444-4444-8444-444444444444"
	_, err = service.SubmitPlanningProposalForIntelligence(context.Background(), "user-1", "job-1", proposalInput)
	if !errors.Is(err, curationdomain.ErrPlanningTaskClosed) {
		t.Fatalf("expected closed task, got %v", err)
	}
}

func TestRejectedProposalIsRecorded(t *testing.T) {
	base := &memoryPlanningRepository{}
	repository := &memoryCurationRepository{
		memoryPlanningRepository: base,
		runs:                     map[string]curationdomain.CurationRun{},
	}
	service := newTestService(repository, &memorySessions{})
	created, err := service.CreatePlan(context.Background(), CreatePlanInput{
		UserID: "user-1", OriginalIntent: "캠핑 장비", PlanningMode: "AUTO",
		ExecutionMode: "EXPERIMENT",
		TotalBudget:   MoneyInput{Amount: "100", Currency: "USD"},
		Country:       "KR", URLMode: "NONE",
		AgentMode: "MANAGED", ModelKey: "gpt-5-nano",
		IdempotencyKey: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	initial := startInitialPlanning(
		t, service, created,
		"22222222-2222-4222-8222-222222222222",
	)
	result, err := service.SubmitPlanningProposalForIntelligence(context.Background(), "user-1", "job-1", SubmitPlanningProposalInput{
		UserID: "user-1", PlanID: "plan-1",
		TaskID:           string(initial.PlanningTask.ID),
		ClientProposalID: "33333333-3333-4333-8333-333333333333",
		ContextVersion:   initial.PlanningTask.ContextVersion,
		ContextHash:      initial.PlanningTask.ContextHash,
		SchemaVersion:    curationdomain.PlanningProposalSchemaV1,
		Targets: []TargetInput{{
			Title: "의자", NormalizedIntent: "의자", Category: "chair",
			AllocatedBudget: MoneyInput{Amount: "101", Currency: "USD"}, URLMode: "NONE",
		}},
	})
	if !errors.Is(err, curationdomain.ErrTargetBudgetInvalid) {
		t.Fatalf("expected budget rejection, got %v", err)
	}
	if result.ValidationStatus != curationdomain.ProposalValidationRejected ||
		len(repository.proposals) != 1 {
		t.Fatalf("rejection was not recorded: %#v", result)
	}
}

func TestExpansionCreatesOnlyNewTargetsAndContinuesExactSessions(t *testing.T) {
	base := &memoryPlanningRepository{}
	sessions := &memorySessions{}
	repository := &memoryCurationRepository{
		memoryPlanningRepository: base,
		runs:                     map[string]curationdomain.CurationRun{},
	}
	initialService := newTestService(repository, sessions)
	created, err := initialService.CreatePlan(context.Background(), CreatePlanInput{
		UserID: "user-1", OriginalIntent: "캠핑 준비", PlanningMode: "AUTO",
		ExecutionMode: "EXPERIMENT",
		TotalBudget:   MoneyInput{Amount: "100", Currency: "USD"},
		Country:       "KR", City: "서울", URLMode: "NONE",
		AgentMode: "MANAGED", ModelKey: "gpt-5-nano",
		IdempotencyKey: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	initial := startInitialPlanning(
		t, initialService, created,
		"22222222-2222-4222-8222-222222222222",
	)
	if _, err := initialService.SubmitPlanningProposalForIntelligence(
		context.Background(), "user-1", "job-1",
		SubmitPlanningProposalInput{
			UserID: "user-1", PlanID: "plan-1",
			TaskID:           string(initial.PlanningTask.ID),
			ClientProposalID: "33333333-3333-4333-8333-333333333333",
			ContextVersion:   initial.PlanningTask.ContextVersion,
			ContextHash:      initial.PlanningTask.ContextHash,
			SchemaVersion:    curationdomain.PlanningProposalSchemaV1,
			Targets: []TargetInput{{
				Title: "의자", NormalizedIntent: "가벼운 캠핑 의자",
				Category:        "camping-chair",
				AllocatedBudget: MoneyInput{Amount: "60", Currency: "USD"},
				URLMode:         "NONE",
			}, {
				Title: "랜턴", NormalizedIntent: "충전식 캠핑 랜턴",
				Category:        "camping-lantern",
				AllocatedBudget: MoneyInput{Amount: "40", Currency: "USD"},
				URLMode:         "NONE",
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	original := base.record.Targets[0]

	service := NewService(
		repository, sessions, passthroughTransactor{},
		fixedClock{now: time.Date(2026, 7, 18, 2, 2, 3, 0, time.UTC)},
		&sequenceIDs{values: []string{
			"task-expansion", "run-expansion", "proposal-expansion", "target-monitor",
			"target-webcam",
		}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	expansionInput := CreateExpansionInput{
		UserID: "user-1", PlanID: "plan-1",
		CurationActionID: "44444444-4444-4444-8444-444444444444",
		Instruction:      "휴대용 모니터도 추가해줘",
		IdempotencyKey:   "33333333-3333-4333-8333-333333333333",
	}
	expansion, err := service.CreateExpansion(
		context.Background(), expansionInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	replayedExpansion, err := service.CreateExpansion(
		context.Background(), expansionInput,
	)
	if err != nil || !replayedExpansion.Replay ||
		replayedExpansion.Run.ID != expansion.Run.ID ||
		replayedExpansion.PlanningTask.ID != expansion.PlanningTask.ID {
		t.Fatalf("same expansion request was not replayed: %#v err=%v", replayedExpansion, err)
	}
	if _, err := service.CreateExpansion(
		context.Background(),
		CreateExpansionInput{
			UserID: "user-1", PlanID: "plan-1",
			CurationActionID: "66666666-6666-4666-8666-666666666666",
			Instruction:      "웹캠도 추가해줘",
			IdempotencyKey:   "55555555-5555-4555-8555-555555555555",
		},
	); !errors.Is(err, curationdomain.ErrExpansionInProgress) {
		t.Fatalf("parallel expansion should conflict: %v", err)
	}
	current, err := service.GetCurrentExpansion(
		context.Background(), "user-1", "plan-1",
	)
	if err != nil || current.Run.ID != expansion.Run.ID ||
		current.PlanningTask.ID != expansion.PlanningTask.ID {
		t.Fatalf("current expansion recovery failed: %#v err=%v", current, err)
	}
	contextResult, err := service.GetPlanningContext(
		context.Background(), "user-1", "plan-1",
		string(expansion.PlanningTask.ID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if contextResult.RunKind != curationdomain.CurationRunExpansion ||
		contextResult.Instruction != "휴대용 모니터도 추가해줘" ||
		contextResult.OriginalIntent != "휴대용 모니터도 추가해줘" ||
		len(contextResult.ExistingTargets) != 2 {
		t.Fatalf("unexpected expansion context: %#v", contextResult)
	}
	result, err := service.SubmitPlanningProposalForIntelligence(
		context.Background(), "user-1", "job-1",
		SubmitPlanningProposalInput{
			UserID: "user-1", PlanID: "plan-1", TaskID: string(expansion.PlanningTask.ID),
			ClientProposalID: "44444444-4444-4444-8444-444444444444",
			ContextVersion:   expansion.PlanningTask.ContextVersion,
			ContextHash:      expansion.PlanningTask.ContextHash,
			SchemaVersion:    curationdomain.PlanningProposalSchemaV1,
			Targets: []TargetInput{{
				Title: "휴대용 모니터", NormalizedIntent: "휴대용 모니터",
				Category:        "portable-monitor",
				AllocatedBudget: MoneyInput{Amount: "40", Currency: "USD"},
				URLMode:         "NONE",
			}, {
				Title: "웹캠", NormalizedIntent: "화상 회의용 웹캠",
				Category:        "webcam",
				AllocatedBudget: MoneyInput{Amount: "30", Currency: "USD"},
				URLMode:         "NONE",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.record.Targets) != 4 ||
		base.record.Targets[0].ID != original.ID ||
		base.record.Targets[0].Version != original.Version {
		t.Fatalf("expansion mutated existing targets: %#v", base.record.Targets)
	}
	if result.NextAction != "START_CURATING" ||
		len(result.CreatedTargetIDs) != 2 ||
		len(result.CreatedSessionIDs) != 2 {
		t.Fatalf("planning did not stop at READY sessions: %#v", result)
	}
	run := repository.runs["run-expansion"]
	if run.Status != curationdomain.CurationRunCompleted ||
		base.record.Targets[2].CreatedByCurationRunID == nil ||
		*base.record.Targets[2].CreatedByCurationRunID != run.ID ||
		base.record.Targets[3].CreatedByCurationRunID == nil ||
		*base.record.Targets[3].CreatedByCurationRunID != run.ID {
		t.Fatalf("expansion provenance/status missing: run=%#v targets=%#v", run, base.record.Targets)
	}
}

func TestCreatePlanRejectsRetiredExternalAgent(t *testing.T) {
	repository := &memoryPlanningRepository{}
	service := newTestService(repository, &memorySessions{})
	_, err := service.CreatePlan(context.Background(), CreatePlanInput{
		UserID: "user-1", OriginalIntent: "가벼운 캠핑 의자", PlanningMode: "SINGLE",
		ExecutionMode: "EXPERIMENT",
		TotalBudget:   MoneyInput{Amount: "100", Currency: "USD"},
		Country:       "KR", City: "서울", Category: "camping-chair", URLMode: "NONE",
		AgentMode:      "EXTERNAL",
		IdempotencyKey: "31111111-1111-4111-8111-111111111111",
	})
	if !errors.Is(err, curationdomain.ErrExternalAgentRetired) {
		t.Fatalf("expected ErrExternalAgentRetired, got %v", err)
	}
}

func TestCreatePlanReplaysExternalPlanStoredBeforeCutover(t *testing.T) {
	repository := &memoryPlanningRepository{}
	service := newTestService(repository, &memorySessions{})
	input := CreatePlanInput{
		UserID: "user-1", OriginalIntent: "가벼운 캠핑 의자", PlanningMode: "SINGLE",
		ExecutionMode: "EXPERIMENT",
		TotalBudget:   MoneyInput{Amount: "100", Currency: "USD"},
		Country:       "KR", City: "서울", Category: "camping-chair", URLMode: "NONE",
		AgentMode:      "EXTERNAL",
		IdempotencyKey: "32222222-2222-4222-8222-222222222222",
	}
	// The in-memory repository keeps the row the rejected call wrote, which
	// stands in for an EXTERNAL plan stored before the cutover deploy.
	if _, err := service.CreatePlan(context.Background(), input); !errors.Is(
		err, curationdomain.ErrExternalAgentRetired,
	) {
		t.Fatalf("expected rejection, got %v", err)
	}
	replayed, err := service.CreatePlan(context.Background(), input)
	if err != nil {
		t.Fatalf("replay of stored EXTERNAL plan must succeed: %v", err)
	}
	if replayed.Plan.AgentMode != curationdomain.AgentModeExternal {
		t.Fatalf("replay changed agent mode: %#v", replayed.Plan)
	}
}

func TestCreateExpansionRejectsRetiredExternalAgent(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	base := &memoryPlanningRepository{record: PlanRecord{
		Plan: curationdomain.PlanSnapshot{
			ID: "plan-1", UserID: "user-1",
			AgentMode: curationdomain.AgentModeExternal,
		},
		Curation: curationdomain.Curation{
			ID: "curation-1", ShoppingPlanID: "plan-1", UserID: "user-1",
			Phase: curationdomain.CurationPhasePlanning, Version: 2,
			CreatedAt: now, UpdatedAt: now,
		},
		Targets: []curationdomain.PlanTarget{{
			ID: "target-1", PlanID: "plan-1", Version: 1,
		}},
	}}
	repository := &memoryCurationRepository{
		memoryPlanningRepository: base,
		runs:                     map[string]curationdomain.CurationRun{},
	}
	service := NewService(
		repository, &memorySessions{}, markingTransactor{},
		fixedClock{now: now},
		&sequenceIDs{values: []string{"task-expansion-1", "run-expansion-1"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	_, err := service.CreateExpansion(context.Background(), CreateExpansionInput{
		UserID: "user-1", AuthSessionID: "auth-session-1",
		PlanID: "plan-1", Instruction: "스탠드 조명도 추가해줘",
		CurationActionID: "34444444-4444-4444-8444-444444444444",
		IdempotencyKey:   "35555555-5555-4555-8555-555555555555",
	})
	if !errors.Is(err, curationdomain.ErrExternalAgentRetired) {
		t.Fatalf("expected expansion rejection, got %v", err)
	}
}
