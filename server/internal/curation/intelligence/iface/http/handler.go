package http

import (
	"context"
	"errors"
	nethttp "net/http"

	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type Handler struct {
	service *intelligenceapp.Service
	threads ThreadActionCanceller
}

func NewHandler(service *intelligenceapp.Service) *Handler {
	return &Handler{service: service}
}

// ThreadActionCanceller settles the Thread that owns an Action. Without it the
// legacy cancel route only cancelled the Jobs and left the Thread active, so
// the workspace kept its composer disabled until a worker noticed.
type ThreadActionCanceller interface {
	CancelActionByID(ctx context.Context, userID, actionID string) (cancelledJobs int, handled bool, err error)
}

func (h *Handler) SetThreadCanceller(threads ThreadActionCanceller) { h.threads = threads }

// Retry reopens a failed job the user asked to run again. Only a failure the
// Server classified as retryable is reopened, and the attempt ceiling still
// applies, so this cannot become an open-ended spend of the user's allowance.
func (h *Handler) Retry(w nethttp.ResponseWriter, r *nethttp.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	ref, err := h.service.RetryJob(r.Context(), userID, r.PathValue("jobId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, nethttp.StatusAccepted, ref)
}

// CancelAction stops the work one user action started. It is the user's way
// out of a plan that is mid-action: nothing already produced is undone, but
// the rest is not attempted and the plan admits its next action.
func (h *Handler) CancelAction(w nethttp.ResponseWriter, r *nethttp.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	if h.threads != nil {
		cancelled, handled, err := h.threads.CancelActionByID(
			r.Context(), userID, r.PathValue("actionId"),
		)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if handled {
			httpapi.WriteJSON(w, nethttp.StatusOK, cancelActionResponse{
				CancelledJobs: cancelled,
			})
			return
		}
	}
	cancelled, err := h.service.CancelAction(
		r.Context(), userID, r.PathValue("actionId"),
	)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, nethttp.StatusOK, cancelActionResponse{
		CancelledJobs: cancelled,
	})
}

// CancelledJobs is zero when the action already finished. That is a success:
// the user asked for it to stop and it is stopped.
type cancelActionResponse struct {
	CancelledJobs int `json:"cancelledJobs"`
}

func writeError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "실행 요청을 처리하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, intelligencedomain.ErrJobNotFound):
		httpapi.WriteError(
			w, nethttp.StatusNotFound,
			intelligencedomain.ErrJobNotFound.Error(),
			"실행 기록을 찾을 수 없습니다.",
		)
	case errors.Is(err, intelligencedomain.ErrJobClosed):
		httpapi.WriteError(
			w, nethttp.StatusConflict,
			intelligencedomain.ErrJobClosed.Error(),
			"다시 시도할 수 있는 상태가 아닙니다. 최신 상태를 확인해 주세요.",
		)
	case errors.Is(err, intelligencedomain.ErrRetryExhausted):
		httpapi.WriteError(
			w, nethttp.StatusConflict,
			intelligencedomain.ErrRetryExhausted.Error(),
			"재시도 횟수를 모두 사용했습니다. 새 요청으로 다시 시작해 주세요.",
		)
	default:
		httpapi.WriteError(
			w, nethttp.StatusInternalServerError, "INTERNAL_ERROR",
			"요청을 처리하지 못했습니다.",
		)
	}
}
