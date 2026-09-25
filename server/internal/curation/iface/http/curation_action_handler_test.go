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

const (
	handlerActionCurationID = "11111111-1111-4111-8111-111111111111"
	handlerActionID         = "22222222-2222-4222-8222-222222222222"
)

type curationActionHTTPRepository struct {
	*handlerRepository
	curation curationdomain.Curation
	actions  map[string]curationdomain.CurationAction
	order    []string
}

func (r *curationActionHTTPRepository) GetCuration(
	_ context.Context,
	userID, curationID string,
	_ bool,
) (curationdomain.Curation, error) {
	if string(r.curation.UserID) != userID ||
		string(r.curation.ID) != curationID {
		return curationdomain.Curation{}, curationdomain.ErrCurationNotFound
	}
	return r.curation, nil
}

func (r *curationActionHTTPRepository) GetCurationAction(
	_ context.Context,
	userID, actionID string,
	_ bool,
) (curationdomain.CurationAction, error) {
	action, ok := r.actions[actionID]
	if !ok || string(action.ActorUserID) != userID {
		return curationdomain.CurationAction{},
			curationapp.ErrCurationActionNotFound
	}
	return action, nil
}

func (r *curationActionHTTPRepository) InsertCurationAction(
	_ context.Context,
	action curationdomain.CurationAction,
) (bool, error) {
	actionID := string(action.ID)
	if _, exists := r.actions[actionID]; exists {
		return false, nil
	}
	r.actions[actionID] = action
	r.order = append(r.order, actionID)
	return true, nil
}

func (r *curationActionHTTPRepository) ListCurationActions(
	_ context.Context,
	userID, curationID string,
	limit int,
) ([]curationdomain.CurationAction, error) {
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

func TestCurationActionHTTPAvailableAndImmutablePut(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 9, 10, 11, 0, time.UTC)
	curation := curationdomain.Curation{
		ID: handlerActionCurationID, ShoppingPlanID: "plan-1",
		UserID: "user-1", Phase: curationdomain.CurationPhasePlanning,
		Version: 3, CreatedAt: now, UpdatedAt: now,
	}
	repository := &curationActionHTTPRepository{
		handlerRepository: &handlerRepository{
			record: curationapp.PlanRecord{
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := curationapp.NewService(
		repository,
		&handlerSessions{},
		handlerTransactor{},
		handlerClock{},
		&handlerIDs{},
		logger,
	)
	handler := NewHandler(service, logger)
	mux := nethttp.NewServeMux()
	mux.HandleFunc(
		"GET /api/v1/curations/{curationId}/available-actions",
		handler.GetAvailableCurationActions,
	)
	mux.HandleFunc(
		"PUT /api/v1/curation-actions/{actionId}",
		handler.PutCurationAction,
	)

	available := performJSON(
		t,
		mux,
		"GET",
		"/api/v1/curations/"+handlerActionCurationID+"/available-actions",
		"",
	)
	if available.Code != nethttp.StatusOK ||
		!strings.Contains(available.Body.String(), `"phase":"PLANNING"`) ||
		!strings.Contains(
			available.Body.String(),
			`"alias":"@TargetList-AddTarget"`,
		) ||
		!strings.Contains(
			available.Body.String(),
			`"expectedResourceVersion":3`,
		) {
		t.Fatalf(
			"available status=%d body=%s",
			available.Code,
			available.Body.String(),
		)
	}

	body := `{
		"curationId":"` + handlerActionCurationID + `",
		"type":"PLANNING_ADD_TARGETS",
		"subjectType":"TARGET_LIST",
		"subjectId":"` + handlerActionCurationID + `",
		"body":"조명을 추가해줘",
		"expectedCurationVersion":3,
		"sourceRefType":"CURATION_RUN",
		"sourceRefId":"run-1"
	}`
	first := performJSON(
		t,
		mux,
		"PUT",
		"/api/v1/curation-actions/"+handlerActionID,
		body,
	)
	if first.Code != nethttp.StatusCreated ||
		!strings.Contains(first.Body.String(), `"replay":false`) ||
		!strings.Contains(
			first.Body.String(),
			`"effectKind":"INTELLIGENCE"`,
		) ||
		strings.Contains(first.Body.String(), "requestHash") {
		t.Fatalf(
			"first status=%d body=%s",
			first.Code,
			first.Body.String(),
		)
	}

	replay := performJSON(
		t,
		mux,
		"PUT",
		"/api/v1/curation-actions/"+handlerActionID,
		body,
	)
	if replay.Code != nethttp.StatusOK ||
		!strings.Contains(replay.Body.String(), `"replay":true`) ||
		len(repository.actions) != 1 {
		t.Fatalf(
			"replay status=%d body=%s actions=%d",
			replay.Code,
			replay.Body.String(),
			len(repository.actions),
		)
	}

	different := strings.Replace(
		body,
		"조명을 추가해줘",
		"다른 요청",
		1,
	)
	conflict := performJSON(
		t,
		mux,
		"PUT",
		"/api/v1/curation-actions/"+handlerActionID,
		different,
	)
	if conflict.Code != nethttp.StatusConflict ||
		!strings.Contains(
			conflict.Body.String(),
			`"code":"IDEMPOTENCY_KEY_REUSED"`,
		) {
		t.Fatalf(
			"conflict status=%d body=%s",
			conflict.Code,
			conflict.Body.String(),
		)
	}
}

func TestCurationActionHTTPRejectsPatchOnlyRecord(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 9, 10, 11, 0, time.UTC)
	repository := &curationActionHTTPRepository{
		handlerRepository: &handlerRepository{},
		curation: curationdomain.Curation{
			ID: handlerActionCurationID, ShoppingPlanID: "plan-1",
			UserID: "user-1", Phase: curationdomain.CurationPhasePlanning,
			Version: 3, CreatedAt: now, UpdatedAt: now,
		},
		actions: map[string]curationdomain.CurationAction{},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := curationapp.NewService(
		repository,
		&handlerSessions{},
		handlerTransactor{},
		handlerClock{},
		&handlerIDs{},
		logger,
	)
	handler := NewHandler(service, logger)
	mux := nethttp.NewServeMux()
	mux.HandleFunc(
		"PUT /api/v1/curation-actions/{actionId}",
		handler.PutCurationAction,
	)
	response := performJSON(
		t,
		mux,
		"PUT",
		"/api/v1/curation-actions/33333333-3333-4333-8333-333333333333",
		`{
			"curationId":"`+handlerActionCurationID+`",
			"type":"TARGET_REMOVE",
			"subjectType":"TARGET",
			"subjectId":"target-1",
			"body":"",
			"expectedCurationVersion":3,
			"sourceRefType":"CURATION_RUN",
			"sourceRefId":"run-1"
		}`,
	)
	if response.Code != nethttp.StatusUnprocessableEntity ||
		!strings.Contains(
			response.Body.String(),
			`"code":"CURATION_ACTION_NOT_APPENDABLE"`,
		) ||
		len(repository.actions) != 0 {
		t.Fatalf(
			"patch status=%d body=%s actions=%d",
			response.Code,
			response.Body.String(),
			len(repository.actions),
		)
	}
}
