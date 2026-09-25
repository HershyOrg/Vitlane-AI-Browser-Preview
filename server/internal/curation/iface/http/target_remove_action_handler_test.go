package http

import (
	"context"
	"io"
	"log/slog"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

const handlerTargetRemoveID = "33333333-3333-4333-8333-333333333333"

type targetRemoveHTTPRepository struct {
	*curationActionHTTPRepository
	target curationdomain.PlanTarget
}

func (r *targetRemoveHTTPRepository) GetCurationTarget(
	_ context.Context,
	userID, curationID, targetID string,
	_ bool,
) (curationdomain.PlanTarget, error) {
	if string(r.target.UserID) != userID ||
		string(r.target.CurationID) != curationID ||
		string(r.target.ID) != targetID ||
		r.target.RemovedAt != nil {
		return curationdomain.PlanTarget{},
			curationdomain.ErrTargetNotFound
	}
	return r.target, nil
}

func (r *targetRemoveHTTPRepository) SaveCurationTargetRemoval(
	_ context.Context,
	previousVersion int64,
	target curationdomain.PlanTarget,
) error {
	if r.target.Version != previousVersion {
		return curationdomain.ErrVersionConflict
	}
	r.target = target
	return nil
}

func (r *targetRemoveHTTPRepository) SaveCuration(
	_ context.Context,
	previousVersion int64,
	curation curationdomain.Curation,
) error {
	if r.curation.Version != previousVersion {
		return curationdomain.ErrVersionConflict
	}
	r.curation = curation
	return nil
}

type targetRemoveHTTPClock struct{}

func (targetRemoveHTTPClock) Now() time.Time {
	return time.Date(2026, 7, 31, 12, 13, 14, 0, time.UTC)
}

type targetRemoveHTTPSelectionRemover struct{}

func (targetRemoveHTTPSelectionRemover) RemoveActiveSelectionsForTarget(
	context.Context,
	string,
	string,
	string,
	time.Time,
) error {
	return nil
}

func TestTargetRemoveActionHTTPCommitsAndReplaysTypedOwnerCommand(
	t *testing.T,
) {
	t.Parallel()
	repository := newTargetRemoveHTTPRepository()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := curationapp.NewService(
		repository,
		&handlerSessions{},
		handlerTransactor{},
		targetRemoveHTTPClock{},
		&handlerIDs{},
		logger,
	)
	service.EnableTargetSelectionRemover(targetRemoveHTTPSelectionRemover{})
	handler := NewHandler(service, logger)
	mux := nethttp.NewServeMux()
	mux.HandleFunc(
		"PUT /api/v1/curations/{curationId}/actions/{actionId}/target-remove",
		handler.ExecuteTargetRemoveAction,
	)
	path := "/api/v1/curations/" + handlerActionCurationID +
		"/actions/" + handlerActionID + "/target-remove"
	body := `{
		"targetId":"` + handlerTargetRemoveID + `",
		"expectedCurationVersion":3
	}`

	first := performJSON(t, mux, "PUT", path, body)
	if first.Code != nethttp.StatusCreated ||
		!strings.Contains(first.Body.String(), `"replay":false`) ||
		!strings.Contains(first.Body.String(), `"type":"TARGET_REMOVE"`) ||
		!strings.Contains(
			first.Body.String(),
			`"sourceRefType":"PLAN_TARGET"`,
		) ||
		!strings.Contains(first.Body.String(), `"curationVersion":4`) ||
		repository.curation.Version != 4 ||
		repository.target.Version != 2 ||
		repository.target.RemovedAt == nil ||
		len(repository.actions) != 1 {
		t.Fatalf(
			"first status=%d body=%s repository=%#v",
			first.Code,
			first.Body.String(),
			repository,
		)
	}

	replay := performJSON(t, mux, "PUT", path, body)
	if replay.Code != nethttp.StatusOK ||
		!strings.Contains(replay.Body.String(), `"replay":true`) ||
		!strings.Contains(replay.Body.String(), `"curationVersion":4`) ||
		repository.curation.Version != 4 ||
		repository.target.Version != 2 ||
		len(repository.actions) != 1 {
		t.Fatalf(
			"replay status=%d body=%s repository=%#v",
			replay.Code,
			replay.Body.String(),
			repository,
		)
	}
}

func TestTargetRemoveActionHTTPHidesForeignTarget(t *testing.T) {
	t.Parallel()
	repository := newTargetRemoveHTTPRepository()
	repository.target.UserID = "user-2"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := curationapp.NewService(
		repository,
		&handlerSessions{},
		handlerTransactor{},
		targetRemoveHTTPClock{},
		&handlerIDs{},
		logger,
	)
	service.EnableTargetSelectionRemover(targetRemoveHTTPSelectionRemover{})
	handler := NewHandler(service, logger)
	mux := nethttp.NewServeMux()
	mux.HandleFunc(
		"PUT /api/v1/curations/{curationId}/actions/{actionId}/target-remove",
		handler.ExecuteTargetRemoveAction,
	)
	response := performJSON(
		t,
		mux,
		"PUT",
		"/api/v1/curations/"+handlerActionCurationID+
			"/actions/"+handlerActionID+"/target-remove",
		`{
			"targetId":"`+handlerTargetRemoveID+`",
			"expectedCurationVersion":3
		}`,
	)
	if response.Code != nethttp.StatusNotFound ||
		!strings.Contains(
			response.Body.String(),
			`"code":"TARGET_NOT_FOUND"`,
		) ||
		len(repository.actions) != 0 ||
		repository.curation.Version != 3 {
		t.Fatalf(
			"status=%d body=%s repository=%#v",
			response.Code,
			response.Body.String(),
			repository,
		)
	}
}

func newTargetRemoveHTTPRepository() *targetRemoveHTTPRepository {
	now := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	actionRepository := &curationActionHTTPRepository{
		handlerRepository: &handlerRepository{},
		curation: curationdomain.Curation{
			ID:             handlerActionCurationID,
			ShoppingPlanID: "plan-1",
			UserID:         "user-1",
			Phase:          curationdomain.CurationPhasePlanning,
			Version:        3,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		actions: map[string]curationdomain.CurationAction{},
	}
	return &targetRemoveHTTPRepository{
		curationActionHTTPRepository: actionRepository,
		target: curationdomain.PlanTarget{
			ID:         handlerTargetRemoveID,
			CurationID: actionRepository.curation.ID,
			UserID:     actionRepository.curation.UserID,
			PlanID:     actionRepository.curation.ShoppingPlanID,
			Version:    1,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}
}
