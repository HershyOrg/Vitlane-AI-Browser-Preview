package http

import (
	"errors"
	"net/http"
	"strings"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

// OperatorSessionsHandler backs the ops screen's session list and the
// audited force-revoke command (ADR-0040 §10).
type OperatorSessionsHandler struct {
	service                  *accountapp.OperatorSessionService
	marketingAdminEmails     EmailAllowlist
	settlementOperatorEmails EmailAllowlist
}

func NewOperatorSessionsHandler(
	service *accountapp.OperatorSessionService,
	marketingAdminEmails EmailAllowlist,
	settlementOperatorEmails EmailAllowlist,
) *OperatorSessionsHandler {
	return &OperatorSessionsHandler{
		service:                  service,
		marketingAdminEmails:     marketingAdminEmails,
		settlementOperatorEmails: settlementOperatorEmails,
	}
}

type activeSessionResponse struct {
	SessionID string `json:"sessionId"`
	UserID    string `json:"userId"`
	Email     string `json:"email"`
	Operator  bool   `json:"operator"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
}

type activeSessionsResponse struct {
	Sessions []activeSessionResponse `json:"sessions"`
	Count    int                     `json:"count"`
}

func (h *OperatorSessionsHandler) List(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	sessions, err := h.service.ListActiveSessions(r.Context())
	if err != nil {
		if _, ok := fault.As(err); ok {
			httpapi.WriteFault(w, r, err, "활성 세션을 불러오지 못했습니다.")
			return
		}
		httpapi.WriteError(
			w, http.StatusInternalServerError, "SESSION_LIST_FAILED",
			"활성 세션을 불러오지 못했습니다.",
		)
		return
	}
	response := activeSessionsResponse{
		Sessions: make([]activeSessionResponse, 0, len(sessions)),
		Count:    len(sessions),
	}
	for _, session := range sessions {
		response.Sessions = append(response.Sessions, activeSessionResponse{
			SessionID: session.SessionID,
			UserID:    session.UserID,
			Email:     session.Email,
			Operator: h.marketingAdminEmails.Allows(session.Email) ||
				h.settlementOperatorEmails.Allows(session.Email),
			CreatedAt: session.CreatedAt.UTC().Format(time.RFC3339),
			ExpiresAt: session.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}

type sessionRevocationRequest struct {
	ReasonDetail string `json:"reasonDetail"`
}

type sessionRevocationResponse struct {
	UserID          string `json:"userId"`
	RevokedSessions int64  `json:"revokedSessions"`
}

// RevokeUser revokes every active session of one user. The subject loses all
// browser access immediately; the decision and its reason are appended to
// the operator action audit in the same transaction.
func (h *OperatorSessionsHandler) RevokeUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := sharedapp.WebPrincipalFrom(r.Context())
	if !ok {
		httpapi.WriteError(
			w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.",
		)
		return
	}
	subjectUserID := strings.TrimSpace(r.PathValue("userId"))
	if subjectUserID == "" {
		httpapi.WriteError(
			w, http.StatusUnprocessableEntity, "USER_ID_REQUIRED",
			"user ID가 필요합니다.",
		)
		return
	}
	var request sessionRevocationRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	revoked, err := h.service.RevokeUserSessions(
		r.Context(), principal.UserID, subjectUserID, request.ReasonDetail,
	)
	if err != nil {
		if errors.Is(err, accountapp.ErrOperatorActionReasonInvalid) {
			httpapi.WriteError(
				w, http.StatusUnprocessableEntity, "OPERATOR_REASON_INVALID",
				"조치 사유는 8자 이상 500자 이하여야 합니다.",
			)
			return
		}
		if _, ok := fault.As(err); ok {
			httpapi.WriteFault(w, r, err, "세션 revoke에 실패했습니다.")
			return
		}
		httpapi.WriteError(
			w, http.StatusInternalServerError, "SESSION_REVOKE_FAILED",
			"세션 revoke에 실패했습니다.",
		)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, sessionRevocationResponse{
		UserID: subjectUserID, RevokedSessions: revoked,
	})
}
