// 회수 기입 창구(운영정합 5차 PR-D) — 간이 회수 원장의 목록·수동 생성·수취
// 기입·포기·수동 행 삭제. 금액 변경은 fresh 운영자 인증 route가 감싼다.
package http

import (
	"net/http"

	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	httpapi "github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

func (h *Handler) ListRecovery(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpapi.RequireAuthenticatedUserID(w, r); !ok {
		return
	}
	surface, err := h.service.ListRecovery(r.Context(), r.URL.Query().Get("referenceId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.procurement-recovery.v1",
		"surface":       surface,
	})
}

type recoveryCreateRequest struct {
	MerchantOrderID     string `json:"merchantOrderId"`
	Cause               string `json:"cause"`
	ExpectedAmountMinor int64  `json:"expectedAmountMinor"`
	ReceivedAmountMinor int64  `json:"receivedAmountMinor"`
	Note                string `json:"note"`
}

func (h *Handler) CreateRecovery(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request recoveryCreateRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	entry, replayed, err := h.service.CreateRecovery(
		r.Context(), operatorID, r.Header.Get("Idempotency-Key"),
		procurementapp.RecoveryCreateInput{
			MerchantOrderID:     request.MerchantOrderID,
			Cause:               request.Cause,
			ExpectedAmountMinor: request.ExpectedAmountMinor,
			ReceivedAmountMinor: request.ReceivedAmountMinor,
			Note:                request.Note,
		},
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{"entry": entry, "replay": replayed})
}

type recoveryRecordRequest struct {
	ReceivedAmountMinor int64  `json:"receivedAmountMinor"`
	Note                string `json:"note"`
	ExpectedVersion     int64  `json:"expectedVersion"`
}

func (h *Handler) RecordRecovery(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request recoveryRecordRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	entry, replayed, err := h.service.RecordRecovery(
		r.Context(), r.PathValue("entryId"), operatorID,
		r.Header.Get("Idempotency-Key"),
		request.ReceivedAmountMinor, request.Note, request.ExpectedVersion,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"entry": entry, "replay": replayed})
}

type recoveryWaiveRequest struct {
	Note            string `json:"note"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

func (h *Handler) WaiveRecovery(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request recoveryWaiveRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	entry, replayed, err := h.service.WaiveRecovery(
		r.Context(), r.PathValue("entryId"), operatorID,
		r.Header.Get("Idempotency-Key"), request.Note, request.ExpectedVersion,
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"entry": entry, "replay": replayed})
}

type recoveryDeleteRequest struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}

func (h *Handler) DeleteRecovery(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request recoveryDeleteRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	if err := h.service.DeleteRecovery(
		r.Context(), r.PathValue("entryId"), operatorID, request.ExpectedVersion,
	); err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
