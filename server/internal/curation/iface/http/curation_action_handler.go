package http

import (
	"errors"
	"net/http"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

func (h *Handler) GetAvailableCurationActions(
	w http.ResponseWriter,
	r *http.Request,
) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	curationID := curationActionPathValue(r, "curationId")
	result, err := h.service.GetAvailableCurationActions(
		r.Context(),
		userID,
		curationID,
	)
	if err != nil {
		h.writeCurationActionError(
			w,
			r,
			userID,
			curationID,
			"",
			err,
		)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) ListCurationActions(
	w http.ResponseWriter,
	r *http.Request,
) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	curationID := curationActionPathValue(r, "curationId")
	actions, err := h.service.ListCurationActions(
		r.Context(),
		userID,
		curationID,
		100,
	)
	if err != nil {
		h.writeCurationActionError(
			w,
			r,
			userID,
			curationID,
			"",
			err,
		)
		return
	}
	httpapi.WriteJSON(
		w,
		http.StatusOK,
		map[string]any{"actions": actions},
	)
}

type putCurationActionRequest struct {
	CurationID              string                                     `json:"curationId"`
	Type                    curationdomain.CurationActionType          `json:"type"`
	SubjectType             curationdomain.CurationActionSubjectType   `json:"subjectType"`
	SubjectID               *string                                    `json:"subjectId"`
	Body                    string                                     `json:"body"`
	ExpectedCurationVersion int64                                      `json:"expectedCurationVersion"`
	SourceRefType           curationdomain.CurationActionSourceRefType `json:"sourceRefType"`
	SourceRefID             string                                     `json:"sourceRefId"`
}

// PutCurationAction exposes only the immutable record primitive. It is
// intentionally not route-registered yet: the owning product command must
// validate and create SourceRef before this endpoint can become a public
// dispatch path.
func (h *Handler) PutCurationAction(
	w http.ResponseWriter,
	r *http.Request,
) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request putCurationActionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	actionID := curationActionPathValue(r, "actionId")
	result, err := h.service.RecordCurationAction(
		r.Context(),
		curationapp.RecordCurationActionInput{
			ActionID: actionID, UserID: userID,
			CurationID: request.CurationID, Type: request.Type,
			SubjectType: request.SubjectType, SubjectID: request.SubjectID,
			Body:                    request.Body,
			ExpectedCurationVersion: request.ExpectedCurationVersion,
			SourceRefType:           request.SourceRefType,
			SourceRefID:             request.SourceRefID,
		},
	)
	if err != nil {
		h.writeCurationActionError(
			w,
			r,
			userID,
			request.CurationID,
			actionID,
			err,
		)
		return
	}
	status := http.StatusCreated
	if result.Replay {
		status = http.StatusOK
	}
	httpapi.WriteJSON(w, status, result)
}

func (h *Handler) writeCurationActionError(
	w http.ResponseWriter,
	r *http.Request,
	userID, curationID, actionID string,
	err error,
) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "큐레이션 행동을 처리하지 못했습니다.")
		return
	}
	status := http.StatusInternalServerError
	code := "INTERNAL_ERROR"
	message := "요청을 처리하지 못했습니다."
	switch {
	case errors.Is(err, curationdomain.ErrCurationNotFound),
		errors.Is(err, curationdomain.ErrTargetNotFound),
		errors.Is(err, curationapp.ErrCurationActionNotFound):
		status = http.StatusNotFound
		code = curationActionReasonCode(err)
		message = "큐레이션, 대상 또는 행동 기록을 찾을 수 없습니다."
	case errors.Is(err, curationdomain.ErrVersionConflict),
		errors.Is(err, curationdomain.ErrIdempotencyKeyReused),
		errors.Is(err, curationdomain.ErrCurationActionUnavailable),
		errors.Is(err, curationdomain.ErrCurationActionInProgress),
		errors.Is(err, curationdomain.ErrCurationArchived):
		status = http.StatusConflict
		code = curationActionReasonCode(err)
		message = "큐레이션 상태가 변경되었거나 같은 행동 ID가 이미 사용되었습니다."
	case errors.Is(err, curationdomain.ErrCurationActionInvalid),
		errors.Is(err, curationdomain.ErrCurationActionTypeInvalid),
		errors.Is(err, curationdomain.ErrCurationActionCommandInvalid),
		errors.Is(err, curationdomain.ErrCurationActionAliasUnknown),
		errors.Is(err, curationdomain.ErrCurationActionBodyInvalid),
		errors.Is(err, curationdomain.ErrCurationActionSubjectInvalid),
		errors.Is(err, curationdomain.ErrCurationActionVersionInvalid),
		errors.Is(err, curationdomain.ErrCurationActionNotAppendable):
		status = http.StatusUnprocessableEntity
		code = curationActionReasonCode(err)
		message = "행동의 타입, 대상, 본문 또는 version을 확인해 주세요."
	}
	if h.logger != nil {
		h.logger.WarnContext(
			r.Context(),
			"curation action rejected",
			"event", "curation_action.rejected",
			"result", "rejected",
			"reason_code", code,
			"request_id", sharedapp.RequestID(r.Context()),
			"user_id", userID,
			"curation_id", curationID,
			"action_id", actionID,
		)
	}
	httpapi.WriteError(w, status, code, message)
}

func curationActionReasonCode(err error) string {
	codes := []error{
		curationdomain.ErrCurationNotFound,
		curationdomain.ErrTargetNotFound,
		curationapp.ErrCurationActionNotFound,
		curationdomain.ErrVersionConflict,
		curationdomain.ErrIdempotencyKeyReused,
		curationdomain.ErrCurationActionUnavailable,
		curationdomain.ErrCurationActionInProgress,
		curationdomain.ErrCurationArchived,
		curationdomain.ErrCurationActionInvalid,
		curationdomain.ErrCurationActionTypeInvalid,
		curationdomain.ErrCurationActionCommandInvalid,
		curationdomain.ErrCurationActionAliasUnknown,
		curationdomain.ErrCurationActionBodyInvalid,
		curationdomain.ErrCurationActionSubjectInvalid,
		curationdomain.ErrCurationActionVersionInvalid,
		curationdomain.ErrCurationActionNotAppendable,
	}
	for _, code := range codes {
		if errors.Is(err, code) {
			return code.Error()
		}
	}
	return "INTERNAL_ERROR"
}

func curationActionPathValue(r *http.Request, name string) string {
	if value := r.PathValue(name); value != "" {
		return value
	}
	return r.PathValue("id")
}
