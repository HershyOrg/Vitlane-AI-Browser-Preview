package http

import (
	"net/http"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type executeTargetRemoveActionRequest struct {
	TargetID                string `json:"targetId"`
	ExpectedCurationVersion int64  `json:"expectedCurationVersion"`
}

// ExecuteTargetRemoveAction is the typed Planning owner route. The generic
// immutable record primitive must not dispatch this PATCH_ONLY mutation.
func (h *Handler) ExecuteTargetRemoveAction(
	w http.ResponseWriter,
	r *http.Request,
) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request executeTargetRemoveActionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	curationID := curationActionPathValue(r, "curationId")
	actionID := curationActionPathValue(r, "actionId")
	result, err := h.service.ExecuteTargetRemoveAction(
		r.Context(),
		curationapp.ExecuteTargetRemoveActionInput{
			ActionID:                actionID,
			UserID:                  userID,
			CurationID:              curationID,
			TargetID:                request.TargetID,
			ExpectedCurationVersion: request.ExpectedCurationVersion,
		},
	)
	if err != nil {
		h.writeCurationActionError(
			w,
			r,
			userID,
			curationID,
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
