package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

const (
	actionTestCurationID = "11111111-1111-4111-8111-111111111111"
	actionTestActionID   = "22222222-2222-4222-8222-222222222222"
)

type actionTestRepository struct {
	*memoryPlanningRepository
	curation      curationdomain.Curation
	actions       map[string]curationdomain.CurationAction
	order         []string
	insertCalls   int
	getForUpdate  int
	listLimit     int
	insertRace    bool
	raceDifferent bool
	activeWork    bool
}

func (r *actionTestRepository) HasActiveCurationWork(
	_ context.Context,
	userID, curationID string,
) (bool, error) {
	if string(r.curation.UserID) != userID ||
		string(r.curation.ID) != curationID {
		return false, curationdomain.ErrCurationNotFound
	}
	return r.activeWork, nil
}

func (r *actionTestRepository) GetCuration(
	_ context.Context,
	userID, curationID string,
	forUpdate bool,
) (curationdomain.Curation, error) {
	if forUpdate {
		r.getForUpdate++
	}
	if string(r.curation.UserID) != userID ||
		string(r.curation.ID) != curationID {
		return curationdomain.Curation{}, curationdomain.ErrCurationNotFound
	}
	return r.curation, nil
}

func (r *actionTestRepository) GetCurationAction(
	_ context.Context,
	userID, actionID string,
	_ bool,
) (curationdomain.CurationAction, error) {
	action, ok := r.actions[actionID]
	if !ok || string(action.ActorUserID) != userID {
		return curationdomain.CurationAction{}, ErrCurationActionNotFound
	}
	return action, nil
}

func (r *actionTestRepository) InsertCurationAction(
	_ context.Context,
	action curationdomain.CurationAction,
) (bool, error) {
	r.insertCalls++
	if r.actions == nil {
		r.actions = map[string]curationdomain.CurationAction{}
	}
	actionID := string(action.ID)
	if r.insertRace {
		r.insertRace = false
		if r.raceDifferent {
			action.RequestHash[0] ^= 0xff
		}
		r.actions[actionID] = action
		r.order = append(r.order, actionID)
		return false, nil
	}
	if _, exists := r.actions[actionID]; exists {
		return false, nil
	}
	r.actions[actionID] = action
	r.order = append(r.order, actionID)
	return true, nil
}

func (r *actionTestRepository) ListCurationActions(
	_ context.Context,
	userID, curationID string,
	limit int,
) ([]curationdomain.CurationAction, error) {
	r.listLimit = limit
	result := make([]curationdomain.CurationAction, 0, limit)
	for _, actionID := range r.order {
		action := r.actions[actionID]
		if string(action.ActorUserID) != userID ||
			string(action.CurationID) != curationID {
			continue
		}
		result = append(result, action)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func TestGetAvailableCurationActionsUsesPersistedPhaseAndVersion(t *testing.T) {
	t.Parallel()
	repository := newActionTestRepository(
		curationdomain.CurationPhasePlanning,
		3,
	)
	service := newActionTestService(repository)

	result, err := service.GetAvailableCurationActions(
		context.Background(),
		"user-1",
		actionTestCurationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.CurationID != actionTestCurationID ||
		result.Phase != curationdomain.CurationPhasePlanning ||
		result.Version != 3 ||
		len(result.Actions) != 3 {
		t.Fatalf("unexpected available actions: %#v", result)
	}
	for _, action := range result.Actions {
		if action.ExpectedResourceVersion != 3 {
			t.Fatalf("catalog did not bind current version: %#v", action)
		}
		if action.ID == curationdomain.CurationActionPlanningStartCurating &&
			(action.Enabled ||
				action.UnavailableReason != "CURATION_TARGETS_REQUIRED") {
			t.Fatalf("target-less start action must be disabled: %#v", action)
		}
	}

	repository.record.Targets = []curationdomain.PlanTarget{{
		ID: "target-1",
	}}
	result, err = service.GetAvailableCurationActions(
		context.Background(),
		"user-1",
		actionTestCurationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Actions {
		if action.ID == curationdomain.CurationActionPlanningStartCurating &&
			(!action.Enabled || action.UnavailableReason != "") {
			t.Fatalf("start action with a target must be enabled: %#v", action)
		}
	}

	archivedAt := time.Now().UTC()
	repository.curation.ArchivedAt = &archivedAt
	repository.record.Curation.ArchivedAt = &archivedAt
	_, err = service.GetAvailableCurationActions(
		context.Background(),
		"user-1",
		actionTestCurationID,
	)
	if !errors.Is(err, curationdomain.ErrCurationArchived) {
		t.Fatalf("error=%v want=%v", err, curationdomain.ErrCurationArchived)
	}
}

func TestRecordCurationActionCreatesOnceAndReplaysByIDAndHash(t *testing.T) {
	t.Parallel()
	repository := newActionTestRepository(
		curationdomain.CurationPhasePlanning,
		3,
	)
	service := newActionTestService(repository)
	input := planningAddTargetsActionInput(actionTestActionID, 3)

	first, err := service.RecordCurationAction(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replay ||
		first.Action.Type != curationdomain.CurationActionPlanningAddTargets ||
		first.Action.EffectKind != curationdomain.CurationActionEffectIntelligence ||
		first.Action.PhaseAtRequest != curationdomain.CurationActionPhasePlanning ||
		first.Action.RequestHash == (curationdomain.CurationActionRequestHash{}) ||
		repository.insertCalls != 1 ||
		repository.getForUpdate != 1 {
		t.Fatalf("unexpected first record: result=%#v repo=%#v", first, repository)
	}

	// Replay is keyed by the immutable ID+hash, not by whatever phase/version
	// the Curation reached after the original transaction committed.
	repository.curation.Phase = curationdomain.CurationPhaseCurating
	repository.curation.Version = 4
	second, err := service.RecordCurationAction(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replay ||
		second.Action.ID != first.Action.ID ||
		second.Action.RequestHash != first.Action.RequestHash ||
		repository.insertCalls != 1 {
		t.Fatalf("unexpected replay: first=%#v second=%#v", first, second)
	}

	different := input
	different.Body = "다른 요청"
	if _, err := service.RecordCurationAction(
		context.Background(),
		different,
	); !errors.Is(err, curationdomain.ErrIdempotencyKeyReused) {
		t.Fatalf("error=%v want=%v", err, curationdomain.ErrIdempotencyKeyReused)
	}
}

func TestRecordCurationActionRejectsDistinctActionWhileWorkIsActive(
	t *testing.T,
) {
	t.Parallel()
	repository := newActionTestRepository(
		curationdomain.CurationPhasePlanning,
		3,
	)
	repository.activeWork = true
	service := newActionTestService(repository)

	_, err := service.RecordCurationAction(
		context.Background(),
		planningAddTargetsActionInput(actionTestActionID, 3),
	)
	if !errors.Is(err, curationdomain.ErrCurationActionInProgress) {
		t.Fatalf(
			"error=%v want=%v",
			err,
			curationdomain.ErrCurationActionInProgress,
		)
	}
	if repository.insertCalls != 0 || len(repository.actions) != 0 {
		t.Fatalf("active-work rejection wrote an action: %#v", repository)
	}
}

func TestRecordInitialIntentActionAllowsItsOwnPlanningWork(t *testing.T) {
	t.Parallel()
	repository := newActionTestRepository(
		curationdomain.CurationPhasePlanning,
		1,
	)
	repository.activeWork = true
	service := newActionTestService(repository)

	result, err := service.recordInitialIntentCurationAction(
		context.Background(),
		RecordCurationActionInput{
			ActionID: actionTestActionID, UserID: "user-1",
			CurationID:              actionTestCurationID,
			Type:                    curationdomain.CurationActionIntentNextStep,
			SubjectType:             curationdomain.CurationActionSubjectIntent,
			Body:                    "캠핑 의자와 충전식 랜턴을 찾아줘",
			ExpectedCurationVersion: 1,
			SourceRefType: curationdomain.
				CurationActionSourceShoppingPlan,
			SourceRefID: "plan-1",
		},
	)
	if err != nil {
		t.Fatalf("record initial Intent action: %v", err)
	}
	if result.Replay || result.Action.EffectKind !=
		curationdomain.CurationActionEffectIntelligence {
		t.Fatalf("unexpected initial Intent action: %#v", result)
	}
	if repository.insertCalls != 1 || len(repository.actions) != 1 {
		t.Fatalf("initial Intent action was not stored: %#v", repository)
	}
}

func TestRecordManagedContinuationAllowsItsAuthorizingPlanningWork(
	t *testing.T,
) {
	t.Parallel()
	repository := newActionTestRepository(
		curationdomain.CurationPhasePlanning,
		3,
	)
	repository.activeWork = true
	service := newActionTestService(repository)
	curationID := actionTestCurationID

	result, err := service.RecordManagedContinuationCurationAction(
		context.Background(),
		RecordCurationActionInput{
			ActionID: actionTestActionID, UserID: "user-1",
			CurationID: actionTestCurationID,
			Type: curationdomain.
				CurationActionPlanningStartCurating,
			SubjectType:             curationdomain.CurationActionSubjectCuration,
			SubjectID:               &curationID,
			ExpectedCurationVersion: 3,
			SourceRefType: curationdomain.
				CurationActionSourceResearchStartRequest,
			SourceRefID: actionTestActionID,
		},
	)
	if err != nil {
		t.Fatalf("record managed continuation: %v", err)
	}
	if result.Replay || repository.insertCalls != 1 {
		t.Fatalf("unexpected continuation result: %#v", result)
	}

	_, err = service.RecordManagedContinuationCurationAction(
		context.Background(),
		planningAddTargetsActionInput(
			"33333333-3333-4333-8333-333333333333",
			3,
		),
	)
	if !errors.Is(err, curationdomain.ErrCurationActionTypeInvalid) {
		t.Fatalf(
			"error=%v want=%v",
			err,
			curationdomain.ErrCurationActionTypeInvalid,
		)
	}
}

func TestRecordCurationActionRejectsPatchStaleAndWrongPhase(t *testing.T) {
	t.Parallel()
	targetID := "target-1"
	tests := []struct {
		name    string
		input   RecordCurationActionInput
		version int64
		want    error
	}{
		{
			name: "patch only",
			input: RecordCurationActionInput{
				ActionID: actionTestActionID, UserID: "user-1",
				CurationID:  actionTestCurationID,
				Type:        curationdomain.CurationActionTargetRemove,
				SubjectType: curationdomain.CurationActionSubjectTarget,
				SubjectID:   &targetID, ExpectedCurationVersion: 3,
				SourceRefType: "CURATION_RUN", SourceRefID: "run-1",
			},
			version: 3,
			want:    curationdomain.ErrCurationActionNotAppendable,
		},
		{
			name:    "stale version",
			input:   planningAddTargetsActionInput(actionTestActionID, 2),
			version: 3,
			want:    curationdomain.ErrVersionConflict,
		},
		{
			name: "curating action in planning",
			input: RecordCurationActionInput{
				ActionID: actionTestActionID, UserID: "user-1",
				CurationID:  actionTestCurationID,
				Type:        curationdomain.CurationActionTargetResearchAgain,
				SubjectType: curationdomain.CurationActionSubjectTarget,
				SubjectID:   &targetID, Body: "다시 조사해줘",
				ExpectedCurationVersion: 3,
				SourceRefType:           "RESEARCH_FEEDBACK", SourceRefID: "feedback-1",
			},
			version: 3,
			want:    curationdomain.ErrCurationActionUnavailable,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := newActionTestRepository(
				curationdomain.CurationPhasePlanning,
				test.version,
			)
			service := newActionTestService(repository)
			_, err := service.RecordCurationAction(
				context.Background(),
				test.input,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			if repository.insertCalls != 0 {
				t.Fatalf("invalid action was inserted: %#v", repository.actions)
			}
		})
	}
}

func TestRecordIntentNextStepUsesHavingIntentAgainstCreatedCurationV1(
	t *testing.T,
) {
	t.Parallel()
	repository := newActionTestRepository(
		curationdomain.CurationPhasePlanning,
		1,
	)
	service := newActionTestService(repository)
	result, err := service.RecordCurationAction(
		context.Background(),
		RecordCurationActionInput{
			ActionID: actionTestActionID, UserID: "user-1",
			CurationID:  actionTestCurationID,
			Type:        curationdomain.CurationActionIntentNextStep,
			SubjectType: curationdomain.CurationActionSubjectIntent,
			Body:        "업무용 의자를 찾아줘", ExpectedCurationVersion: 1,
			SourceRefType: "SHOPPING_PLAN", SourceRefID: "plan-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action.PhaseAtRequest !=
		curationdomain.CurationActionPhaseHavingIntent ||
		result.Action.RequestedTransitionTo == nil ||
		*result.Action.RequestedTransitionTo != curationdomain.CurationPhasePlanning {
		t.Fatalf("unexpected initial action: %#v", result.Action)
	}

	repository = newActionTestRepository(
		curationdomain.CurationPhasePlanning,
		2,
	)
	service = newActionTestService(repository)
	_, err = service.RecordCurationAction(
		context.Background(),
		RecordCurationActionInput{
			ActionID: actionTestActionID, UserID: "user-1",
			CurationID:  actionTestCurationID,
			Type:        curationdomain.CurationActionIntentNextStep,
			SubjectType: curationdomain.CurationActionSubjectIntent,
			Body:        "업무용 의자를 찾아줘", ExpectedCurationVersion: 2,
			SourceRefType: "SHOPPING_PLAN", SourceRefID: "plan-1",
		},
	)
	if !errors.Is(err, curationdomain.ErrCurationActionUnavailable) {
		t.Fatalf("error=%v want=%v", err, curationdomain.ErrCurationActionUnavailable)
	}
}

func TestRecordOwnedPatchCurationActionPersistsOwnerLineage(t *testing.T) {
	t.Parallel()
	repository := newActionTestRepository(
		curationdomain.CurationPhaseCurating,
		3,
	)
	service := newActionTestService(repository)
	selectionID := "selection-1"
	result, err := service.RecordOwnedPatchCurationAction(
		context.Background(),
		RecordCurationActionInput{
			ActionID: actionTestActionID, UserID: "user-1",
			CurationID:              actionTestCurationID,
			Type:                    curationdomain.CurationActionSelectionMutation,
			SubjectType:             curationdomain.CurationActionSubjectSelection,
			SubjectID:               &selectionID,
			Body:                    `{"action":"UPDATE_QUANTITY","selectionId":"selection-1"}`,
			ExpectedCurationVersion: 3,
			SourceRefType:           curationdomain.CurationActionSourceSelectionCommand,
			SourceRefID:             actionTestActionID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Replay || repository.insertCalls != 1 ||
		result.Action.ID != curationdomain.CurationActionID(actionTestActionID) ||
		result.Action.SourceRefID != actionTestActionID ||
		curationdomain.CurationActionAppendsTranscript(result.Action.Type) {
		t.Fatalf("result=%#v actions=%#v", result, repository.actions)
	}
}

func TestRecordCurationActionResolvesConcurrentInsertByHash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		raceDifferent bool
		wantReplay    bool
		want          error
	}{
		{name: "same hash", wantReplay: true},
		{
			name: "different hash", raceDifferent: true,
			want: curationdomain.ErrIdempotencyKeyReused,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := newActionTestRepository(
				curationdomain.CurationPhasePlanning,
				3,
			)
			repository.insertRace = true
			repository.raceDifferent = test.raceDifferent
			service := newActionTestService(repository)
			result, err := service.RecordCurationAction(
				context.Background(),
				planningAddTargetsActionInput(actionTestActionID, 3),
			)
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("error=%v want=%v", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Replay != test.wantReplay || repository.insertCalls != 1 {
				t.Fatalf("unexpected raced result: %#v", result)
			}
		})
	}
}

func TestListCurationActionsCapsRepositoryLimit(t *testing.T) {
	t.Parallel()
	repository := newActionTestRepository(
		curationdomain.CurationPhasePlanning,
		3,
	)
	service := newActionTestService(repository)
	for index, actionID := range []string{
		"22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333",
	} {
		input := planningAddTargetsActionInput(actionID, 3)
		input.Body = []string{"첫 요청", "두 번째 요청"}[index]
		if _, err := service.RecordCurationAction(
			context.Background(),
			input,
		); err != nil {
			t.Fatal(err)
		}
	}

	actions, err := service.ListCurationActions(
		context.Background(),
		"user-1",
		actionTestCurationID,
		999,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 ||
		actions[0].ID != "22222222-2222-4222-8222-222222222222" ||
		actions[1].ID != "33333333-3333-4333-8333-333333333333" ||
		repository.listLimit != maxCurationActionListLimit {
		t.Fatalf(
			"unexpected list actions=%#v limit=%d",
			actions,
			repository.listLimit,
		)
	}
}

func newActionTestRepository(
	phase curationdomain.CurationPhase,
	version int64,
) *actionTestRepository {
	now := time.Date(2026, 7, 31, 4, 5, 6, 0, time.UTC)
	curation := curationdomain.Curation{
		ID: actionTestCurationID, ShoppingPlanID: "plan-1",
		UserID: "user-1", Phase: phase, Version: version,
		CreatedAt: now, UpdatedAt: now,
	}
	return &actionTestRepository{
		memoryPlanningRepository: &memoryPlanningRepository{
			record: PlanRecord{
				Plan: curationdomain.PlanSnapshot{
					ID: "plan-1", UserID: "user-1",
				},
				Curation: curation,
				Targets:  []curationdomain.PlanTarget{},
			},
		},
		curation: curation,
		actions:  map[string]curationdomain.CurationAction{},
	}
}

func newActionTestService(repository *actionTestRepository) *Service {
	service := NewService(
		repository,
		&memorySessions{},
		passthroughTransactor{},
		fixedClock{now: time.Date(2026, 7, 31, 7, 8, 9, 0, time.UTC)},
		&sequenceIDs{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	service.EnableCurationForegroundWork(repository)
	return service
}

func planningAddTargetsActionInput(
	actionID string,
	expectedVersion int64,
) RecordCurationActionInput {
	subjectID := actionTestCurationID
	return RecordCurationActionInput{
		ActionID: actionID, UserID: "user-1",
		CurationID:  actionTestCurationID,
		Type:        curationdomain.CurationActionPlanningAddTargets,
		SubjectType: curationdomain.CurationActionSubjectTargetList,
		SubjectID:   &subjectID,
		Body:        "조명을 추가해줘", ExpectedCurationVersion: expectedVersion,
		SourceRefType: "CURATION_RUN", SourceRefID: "run-1",
	}
}
