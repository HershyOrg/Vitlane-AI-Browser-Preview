package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

type handlerRepository struct {
	record  curationapp.PlanRecord
	task    *curationdomain.PlanningTask
	runs    map[string]curationdomain.CurationRun
	actions map[string]curationdomain.CurationAction
}

func (r *handlerRepository) CreateIdempotent(
	_ context.Context,
	plan curationdomain.PlanSnapshot,
	targets []curationdomain.PlanTarget,
	task *curationdomain.PlanningTask,
	_ string,
	_ []byte,
) (curationapp.PlanRecord, bool, error) {
	curation, err := curationdomain.NewCuration(curationdomain.NewCurationInput{
		ID:             curationdomain.CurationID("curation-" + string(plan.ID)),
		ShoppingPlanID: plan.ID,
		UserID:         plan.UserID,
		Now:            plan.CreatedAt,
	})
	if err != nil {
		return curationapp.PlanRecord{}, false, err
	}
	for index := range targets {
		targets[index].CurationID = curation.ID
		targets[index].UserID = plan.UserID
	}
	r.record = curationapp.PlanRecord{
		Plan: plan, Curation: curation, Targets: targets,
	}
	r.task = task
	return r.record, false, nil
}

func (r *handlerRepository) Get(
	_ context.Context, userID, planID string, _ bool,
) (curationapp.PlanRecord, error) {
	if string(r.record.Plan.UserID) != userID || string(r.record.Plan.ID) != planID {
		return curationapp.PlanRecord{}, curationdomain.ErrPlanNotFound
	}
	return r.record, nil
}

func (r *handlerRepository) InsertTargets(
	_ context.Context,
	targets []curationdomain.PlanTarget,
) error {
	r.record.Targets = append(r.record.Targets, targets...)
	return nil
}

func (r *handlerRepository) GetCuration(
	_ context.Context,
	userID, curationID string,
	_ bool,
) (curationdomain.Curation, error) {
	if string(r.record.Curation.UserID) != userID ||
		string(r.record.Curation.ID) != curationID {
		return curationdomain.Curation{}, curationdomain.ErrCurationNotFound
	}
	return r.record.Curation, nil
}

func (r *handlerRepository) GetCurationAction(
	_ context.Context,
	userID, actionID string,
	_ bool,
) (curationdomain.CurationAction, error) {
	action, found := r.actions[actionID]
	if !found || string(action.ActorUserID) != userID {
		return curationdomain.CurationAction{}, curationapp.ErrCurationActionNotFound
	}
	return action, nil
}

func (r *handlerRepository) InsertCurationAction(
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

func (r *handlerRepository) ListCurationActions(
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

func (r *handlerRepository) GetPlanningTask(
	context.Context, string, string, bool,
) (curationdomain.PlanningTask, error) {
	if r.task == nil {
		return curationdomain.PlanningTask{}, curationdomain.ErrPlanningTaskNotFound
	}
	return *r.task, nil
}

func (r *handlerRepository) ListPlanningTasks(
	context.Context, string, string, int,
) ([]curationdomain.PlanningTask, error) {
	return []curationdomain.PlanningTask{}, nil
}

func (r *handlerRepository) UpdatePlanningTask(
	context.Context, curationdomain.PlanningTask,
) error {
	return nil
}

func (r *handlerRepository) FindProposal(
	context.Context, string, string, string,
) (curationdomain.PlanningProposal, bool, error) {
	return curationdomain.PlanningProposal{}, false, nil
}

func (r *handlerRepository) InsertProposal(
	context.Context, curationdomain.PlanningProposal,
) error {
	return nil
}

func (r *handlerRepository) CreateExpansion(
	_ context.Context,
	run curationdomain.CurationRun,
	task curationdomain.PlanningTask,
) (curationdomain.CurationRun, curationdomain.PlanningTask, bool, error) {
	if r.runs == nil {
		r.runs = map[string]curationdomain.CurationRun{}
	}
	r.runs[string(run.ID)] = run
	r.task = &task
	return run, task, false, nil
}

func (r *handlerRepository) GetCurationRun(
	_ context.Context, userID, planID, runID string, _ bool,
) (curationdomain.CurationRun, error) {
	run, ok := r.runs[runID]
	if !ok || string(run.UserID) != userID || string(run.PlanID) != planID {
		return curationdomain.CurationRun{}, curationdomain.ErrCurationRunNotFound
	}
	return run, nil
}

func (r *handlerRepository) GetCurationRunByTask(
	ctx context.Context, userID, planID, taskID string, forUpdate bool,
) (curationdomain.CurationRun, error) {
	for runID, run := range r.runs {
		if string(run.PlanningTaskID) == taskID {
			return r.GetCurationRun(ctx, userID, planID, runID, forUpdate)
		}
	}
	return curationdomain.CurationRun{}, curationdomain.ErrCurationRunNotFound
}

func (r *handlerRepository) FindOpenExpansion(
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

func (r *handlerRepository) GetExpansionByIdempotency(
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

func (r *handlerRepository) UpdateCurationRun(
	_ context.Context, run curationdomain.CurationRun,
) error {
	r.runs[string(run.ID)] = run
	return nil
}

type handlerSessions struct {
	session shoppingsessiondomain.ShoppingSession
}

func (s *handlerSessions) CreateReady(
	_ context.Context, input shoppingsessionapp.CreateReadyInput,
) (shoppingsessiondomain.ShoppingSession, error) {
	if s.session.ID != "" {
		return s.session, nil
	}
	target, _ := json.Marshal(input.TargetSnapshot)
	scope, _ := json.Marshal(input.ScopeSnapshot)
	s.session = shoppingsessiondomain.ShoppingSession{
		ID: "session-1", PlanTargetID: shoppingsessiondomain.PlanTargetID(input.TargetID),
		UserID: shoppingsessiondomain.UserID(input.UserID), TargetSnapshot: target,
		ResearchScopeSnapshot: scope, Status: shoppingsessiondomain.SessionStatusReady,
		Version: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	return s.session, nil
}

func (s *handlerSessions) Get(context.Context, string, string) (shoppingsessiondomain.ShoppingSession, error) {
	return s.session, nil
}

func (s *handlerSessions) FindByTarget(
	context.Context, string, string,
) (shoppingsessiondomain.ShoppingSession, error) {
	if s.session.ID == "" {
		return shoppingsessiondomain.ShoppingSession{}, shoppingsessiondomain.ErrSessionNotFound
	}
	return s.session, nil
}

type handlerTransactor struct{}

func (handlerTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type handlerJobCreator struct{}

func (handlerJobCreator) LockActionAdmission(context.Context, string) error {
	return nil
}

func (handlerJobCreator) CreatePlanningJob(
	context.Context,
	curationapp.CreatePlanningJobInput,
) (curationapp.IntelligenceJobRef, error) {
	return curationapp.IntelligenceJobRef{JobID: "job-1"}, nil
}

func (handlerJobCreator) PlanHasActiveWork(
	context.Context, string, string,
) (bool, error) {
	return false, nil
}

func (handlerJobCreator) ActiveActionCount(
	context.Context, string,
) (int, error) {
	return 0, nil
}

type handlerClock struct{}

func (handlerClock) Now() time.Time {
	return time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
}

type handlerIDs struct{ index int }

func (i *handlerIDs) NewID() string {
	i.index++
	return []string{"plan-1", "target-1", "task-1"}[i.index-1]
}

type handlerSequenceIDs struct {
	values []string
	index  int
}

func (i *handlerSequenceIDs) NewID() string {
	value := i.values[i.index]
	i.index++
	return value
}

func TestHTTPSingleCreateStartsInitialPlanningAtomically(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := curationapp.NewService(
		&handlerRepository{}, &handlerSessions{}, handlerTransactor{}, handlerClock{},
		&handlerIDs{}, logger,
	)
	handler := NewHandler(service, logger)
	mux := nethttp.NewServeMux()
	mux.HandleFunc("POST /api/v1/shopping-plans", handler.CreatePlan)

	created := performJSON(t, mux, "POST", "/api/v1/shopping-plans", `{
		"originalIntent":"가벼운 캠핑 의자",
		"planningMode":"SINGLE",
		"agentMode":"MANAGED",
		"modelKey":"gpt-5-nano",
		"totalBudget":{"amount":"100","currency":"USD"},
		"executionMode":"EXPERIMENT",
		"location":{"country":"KR","city":"서울"},
		"category":"camping-chair",
		"urlMode":"NONE"
	}`)
	if created.Code != nethttp.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}

	var body struct {
		Curation struct {
			Phase string `json:"phase"`
		} `json:"curation"`
		PlanningTask *curationdomain.PlanningTask `json:"planningTask"`
		Sessions     []struct {
			Status string `json:"status"`
		} `json:"sessions"`
		Targets []struct {
			TargetHash string `json:"targetHash"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if body.Curation.Phase != "PLANNING" ||
		body.PlanningTask == nil ||
		len(body.Sessions) != 0 ||
		len(body.Targets) != 0 {
		t.Fatalf("single create did not start Planning: %s", created.Body.String())
	}
}

func TestHTTPIntentNextStepStartsAgentWorkWithoutActivationMaterial(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repository := &handlerRepository{}
	service := curationapp.NewService(
		repository, &handlerSessions{}, handlerTransactor{}, handlerClock{},
		&handlerSequenceIDs{values: []string{
			"plan-1", "unused-target", "unused-create-task",
			"task-initial", "run-initial",
		}}, logger,
	)
	service.EnableIntelligenceWork(handlerJobCreator{})
	handler := NewHandler(service, logger)
	mux := nethttp.NewServeMux()
	mux.HandleFunc("POST /api/v1/shopping-plans", handler.CreatePlan)
	mux.HandleFunc("GET /api/v1/shopping-plans/{planId}", handler.GetPlan)

	created := performJSON(t, mux, "POST", "/api/v1/shopping-plans", `{
		"originalIntent":"업무용 책상과 조명",
		"agentMode":"MANAGED",
		"modelKey":"gpt-5-nano",
		"planningMode":"AUTO",
		"totalBudget":{"amount":"500","currency":"USD"},
		"executionMode":"EXPERIMENT",
		"location":{"country":"KR","city":"서울"},
		"urlMode":"NONE"
	}`)
	if created.Code != nethttp.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	assertNoAgentSecret(t, created.Body.String())
	var createdBody map[string]json.RawMessage
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatal(err)
	}
	if _, found := createdBody["intelligenceJob"]; !found {
		t.Fatalf("Intent NextStep must create an intelligence job: %s", created.Body.String())
	}
	if _, found := createdBody["planningTask"]; !found {
		t.Fatalf("Intent NextStep must create PlanningTask: %s", created.Body.String())
	}
	if repository.task == nil ||
		len(repository.runs) != 0 ||
		len(repository.actions) != 1 ||
		repository.actions["11111111-1111-4111-8111-111111111111"].Type !=
			curationdomain.CurationActionIntentNextStep {
		t.Fatalf(
			"AUTO create missed task/action: task=%#v runs=%#v actions=%#v",
			repository.task,
			repository.runs,
			repository.actions,
		)
	}

	got := performJSON(
		t, mux, "GET", "/api/v1/shopping-plans/plan-1", "",
	)
	if got.Code != nethttp.StatusOK {
		t.Fatalf("get status=%d body=%s", got.Code, got.Body.String())
	}
	assertNoAgentSecret(t, got.Body.String())
}

func TestUserIDHeaderCannotAuthenticate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := curationapp.NewService(
		&handlerRepository{}, &handlerSessions{}, handlerTransactor{}, handlerClock{},
		&handlerIDs{}, logger,
	)
	handler := NewHandler(service, logger)
	request := httptest.NewRequest("GET", "/api/v1/shopping-plans/plan-1", nil)
	request.SetPathValue("planId", "plan-1")
	request.Header.Set("X-Vitlane-User-Id", "user-1")
	response := httptest.NewRecorder()
	handler.GetPlan(response, request)
	if response.Code != nethttp.StatusUnauthorized {
		t.Fatalf("header-only request status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHTTPCreateExpansionRequiresCurationActionLineage(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	budget, _ := shareddomain.NewMoney("100", "USD")
	location, _ := shareddomain.NewLocationContext("KR", "서울")
	targetBudget, _ := shareddomain.NewMoney("60", "USD")
	repository := &handlerRepository{record: curationapp.PlanRecord{
		Plan: curationdomain.PlanSnapshot{
			ID: "plan-1", UserID: "user-1", OriginalIntent: "업무 환경 구성",
			PlanningMode:  curationdomain.PlanningModeAuto,
			ExecutionMode: curationdomain.ExecutionModeExperiment,
			TotalBudget:   budget, LocationContext: location,
		},
		Curation: curationdomain.Curation{
			ID: "curation-1", ShoppingPlanID: "plan-1", UserID: "user-1",
			Phase: curationdomain.CurationPhaseCurating, Version: 3,
			CreatedAt: time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC),
		},
		Targets: []curationdomain.PlanTarget{{
			ID: "target-1", PlanID: "plan-1", CurationID: "curation-1",
			UserID: "user-1", Title: "노트북",
			NormalizedIntent: "업무용 노트북", Category: "laptop",
			AllocatedBudget: targetBudget, Version: 2,
		}},
	}}
	service := curationapp.NewService(
		repository, &handlerSessions{}, handlerTransactor{}, handlerClock{},
		&handlerSequenceIDs{values: []string{"task-expansion", "run-expansion"}},
		logger,
	)
	service.EnableIntelligenceWork(handlerJobCreator{})
	handler := NewHandler(service, logger)
	mux := nethttp.NewServeMux()
	mux.HandleFunc(
		"POST /api/v1/shopping-plans/{planId}/expansions",
		handler.CreateExpansion,
	)
	mux.HandleFunc(
		"GET /api/v1/shopping-plans/{planId}/expansions/current",
		handler.GetCurrentExpansion,
	)
	response := performJSON(
		t, mux, "POST", "/api/v1/shopping-plans/plan-1/expansions",
		`{"curationId":"curation-1",`+
			`"curationActionId":"11111111-1111-4111-8111-111111111111",`+
			`"type":"CURATION_ADD_TARGETS",`+
			`"instruction":"휴대용 모니터도 추가해줘",`+
			`"expectedCurationVersion":3}`,
	)
	if response.Code != nethttp.StatusAccepted {
		t.Fatalf("expansion status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Run curationdomain.CurationRun `json:"run"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Run.Kind != curationdomain.CurationRunExpansion ||
		body.Run.Instruction != "휴대용 모니터도 추가해줘" {
		t.Fatalf("unexpected expansion response: %s", response.Body.String())
	}
	assertNoAgentSecret(t, response.Body.String())
	// ADR-0038: the response carries a job reference and nothing that could
	// authorize work on its own.
	var workBody struct {
		IntelligenceJob curationapp.IntelligenceJobRef `json:"intelligenceJob"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &workBody); err != nil {
		t.Fatal(err)
	}
	if workBody.IntelligenceJob.JobID != "job-1" {
		t.Fatalf("unexpected expansion job reference: %s", response.Body.String())
	}
	current := performJSON(
		t, mux, "GET",
		"/api/v1/shopping-plans/plan-1/expansions/current", "",
	)
	if current.Code != nethttp.StatusOK {
		t.Fatalf("current expansion status=%d body=%s", current.Code, current.Body.String())
	}
	var currentBody curationapp.ExpansionResult
	if err := json.Unmarshal(current.Body.Bytes(), &currentBody); err != nil {
		t.Fatal(err)
	}
	if currentBody.Run.ID != body.Run.ID ||
		currentBody.PlanningTask.ID != body.Run.PlanningTaskID {
		t.Fatalf("unexpected current expansion: %s", current.Body.String())
	}
	assertNoAgentSecret(t, current.Body.String())
}

func assertNoAgentSecret(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{
		"activation-secret-must-never-reach-browser",
		`"activationRef"`,
		`"claimHandle"`,
		`"claimSecret"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
}

func performJSON(
	t *testing.T,
	handler nethttp.Handler,
	method, path, body string,
) *httptest.ResponseRecorder {
	return performJSONWithIdempotency(
		t,
		handler,
		method,
		path,
		body,
		"11111111-1111-4111-8111-111111111111",
	)
}

func performJSONWithIdempotency(
	t *testing.T,
	handler nethttp.Handler,
	method, path, body, idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request = request.WithContext(sharedapp.WithWebPrincipal(
		request.Context(),
		sharedapp.WebPrincipal{
			UserID: "user-1", AuthSessionID: "auth-session-1",
		},
	))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

var _ sharedapp.Clock = handlerClock{}

func TestHTTPCreatePlanRejectsRetiredExternalAgent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := curationapp.NewService(
		&handlerRepository{}, &handlerSessions{}, handlerTransactor{}, handlerClock{},
		&handlerIDs{}, logger,
	)
	handler := NewHandler(service, logger)
	mux := nethttp.NewServeMux()
	mux.HandleFunc("POST /api/v1/shopping-plans", handler.CreatePlan)

	rejected := performJSON(t, mux, "POST", "/api/v1/shopping-plans", `{
		"originalIntent":"가벼운 캠핑 의자",
		"planningMode":"SINGLE",
		"agentMode":"EXTERNAL",
		"totalBudget":{"amount":"100","currency":"USD"},
		"executionMode":"EXPERIMENT",
		"location":{"country":"KR","city":"서울"},
		"category":"camping-chair",
		"urlMode":"NONE"
	}`)
	if rejected.Code != nethttp.StatusConflict {
		t.Fatalf("status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	// A machine-readable reason is the whole point: the browser has to tell
	// the retirement apart from a generic failure it should retry.
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rejected.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "EXTERNAL_AGENT_RETIRED" {
		t.Fatalf("reason code=%q body=%s", body.Error.Code, rejected.Body.String())
	}
}
