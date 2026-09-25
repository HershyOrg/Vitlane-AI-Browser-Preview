// Package http exposes what the Web needs to decide whether MANAGED is
// offerable and what today's spend looks like.
//
// The Web never sees provider model IDs or prices — only registry keys and
// labels — so swapping a provider stays a server-side config change.
package http

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"

	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
)

type Handler struct {
	budget   *runnerapp.BudgetService
	registry runnerdomain.Registry
	enabled  bool
}

func NewHandler(
	budget *runnerapp.BudgetService,
	registry runnerdomain.Registry,
	enabled bool,
) *Handler {
	return &Handler{budget: budget, registry: registry, enabled: enabled}
}

type modelResponse struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type capabilityResponse struct {
	Enabled         bool            `json:"enabled"`
	Models          []modelResponse `json:"models"`
	DefaultModelKey string          `json:"defaultModelKey"`
	ServerExhausted bool            `json:"serverExhausted"`
}

// Capability tells the Web whether to offer MANAGED at all. A disabled runner
// or an exhausted server both mean the toggle must not be selectable, because
// submitting would create a plan that can never run.
func (h *Handler) Capability(w http.ResponseWriter, r *http.Request) {
	response := capabilityResponse{Models: []modelResponse{}}
	if !h.enabled {
		httpapi.WriteJSON(w, http.StatusOK, response)
		return
	}
	models := h.registry.Models()
	response.Models = make([]modelResponse, 0, len(models))
	for _, model := range models {
		response.Models = append(response.Models, modelResponse{
			Key: model.Key, Label: model.Label,
		})
	}
	response.Enabled = true
	response.DefaultModelKey = h.registry.DefaultKey()
	if usage, err := h.budget.Usage(r.Context(), ""); err == nil {
		response.ServerExhausted = usage.ServerExhausted
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}

type usageResponse struct {
	Enabled           bool   `json:"enabled"`
	UsageDate         string `json:"usageDate"`
	UserSpentMicros   int64  `json:"userSpentMicros"`
	UserLimitMicros   int64  `json:"userLimitMicros"`
	UserExhausted     bool   `json:"userExhausted"`
	ServerExhausted   bool   `json:"serverExhausted"`
	ServerLimitMicros int64  `json:"serverLimitMicros"`
}

// Usage powers the account panel and the pre-submit gate. The server's own
// spend is reported as a boolean rather than an amount: a user needs to know
// the service is out of budget, not what Vitlane spends.
func (h *Handler) Usage(w http.ResponseWriter, r *http.Request) {
	if !h.enabled {
		httpapi.WriteJSON(w, http.StatusOK, usageResponse{})
		return
	}
	principal, ok := sharedapp.WebPrincipalFrom(r.Context())
	if !ok {
		httpapi.WriteError(
			w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.",
		)
		return
	}
	usage, err := h.budget.Usage(r.Context(), principal.UserID)
	if err != nil {
		if _, ok := fault.As(err); ok {
			httpapi.WriteFault(w, r, err, "사용량을 불러오지 못했습니다.")
			return
		}
		httpapi.WriteError(
			w, http.StatusInternalServerError, "MANAGED_RUNNER_USAGE_FAILED",
			"사용량을 불러오지 못했습니다.",
		)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, usageResponse{
		Enabled:         true,
		UsageDate:       usage.UsageDate.Format("2006-01-02"),
		UserSpentMicros: usage.UserSpentMicros,
		UserLimitMicros: usage.UserLimitMicros,
		UserExhausted:   usage.UserExhausted,
		ServerExhausted: usage.ServerExhausted,
		// The hard cap is published so the UI can explain that the ceiling is
		// the service's, not the user's.
		ServerLimitMicros: usage.ServerLimitMicros,
	})
}

type adminUsageSummaryResponse struct {
	InputTokens    int64   `json:"inputTokens"`
	OutputTokens   int64   `json:"outputTokens"`
	TotalTokens    int64   `json:"totalTokens"`
	RequestCount   int64   `json:"requestCount"`
	ReservedMicros int64   `json:"reservedMicros"`
	SettledMicros  int64   `json:"settledMicros"`
	SpentMicros    int64   `json:"spentMicros"`
	UsagePercent   float64 `json:"usagePercent"`
}

type adminUsageDayResponse struct {
	UsageDate string `json:"usageDate"`
	adminUsageSummaryResponse
}

type adminUsageResponse struct {
	Enabled              bool                      `json:"enabled"`
	Timezone             string                    `json:"timezone"`
	From                 string                    `json:"from"`
	Through              string                    `json:"through"`
	DailyLimitMicros     int64                     `json:"dailyLimitMicros"`
	AdmissionLimitMicros int64                     `json:"admissionLimitMicros"`
	Totals               adminUsageSummaryResponse `json:"totals"`
	Days                 []adminUsageDayResponse   `json:"days"`
}

// AdminUsage exposes the SERVER scope only. It never returns per-user usage,
// prompts, model responses or provider credentials.
func (h *Handler) AdminUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	days, ok := historyDays(r.URL.Query().Get("days"))
	if !ok {
		httpapi.WriteError(
			w, http.StatusUnprocessableEntity, "USAGE_RANGE_INVALID",
			"조회 기간은 7일, 30일 또는 90일이어야 합니다.",
		)
		return
	}
	series, err := h.budget.ServerUsageHistory(r.Context(), days)
	if err != nil {
		if _, ok := fault.As(err); ok {
			httpapi.WriteFault(w, r, err, "서버 API 사용량을 불러오지 못했습니다.")
			return
		}
		httpapi.WriteError(
			w, http.StatusInternalServerError, "MANAGED_RUNNER_USAGE_FAILED",
			"서버 API 사용량을 불러오지 못했습니다.",
		)
		return
	}
	response := adminUsageResponse{
		Enabled:              h.enabled,
		Timezone:             "UTC",
		From:                 series.From.Format("2006-01-02"),
		Through:              series.Through.Format("2006-01-02"),
		DailyLimitMicros:     series.DailyLimitMicros,
		AdmissionLimitMicros: series.AdmissionLimitMicros,
		Totals: usageSummary(
			series.Totals,
			series.DailyLimitMicros*int64(len(series.Days)),
		),
		Days: make([]adminUsageDayResponse, 0, len(series.Days)),
	}
	for _, day := range series.Days {
		response.Days = append(response.Days, adminUsageDayResponse{
			UsageDate: day.UsageDate.Format("2006-01-02"),
			adminUsageSummaryResponse: usageSummary(
				day.Counters, series.DailyLimitMicros,
			),
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}

type reservationResolutionRequest struct {
	Outcome             string `json:"outcome"`
	ReasonDetail        string `json:"reasonDetail"`
	EvidenceReference   string `json:"evidenceReference"`
	SettledAmountMicros *int64 `json:"settledAmountMicros"`
}

type reservationResolutionResponse struct {
	ReservationID       string `json:"reservationId"`
	Status              string `json:"status"`
	AmountMicros        int64  `json:"amountMicros"`
	SettledAmountMicros *int64 `json:"settledAmountMicros,omitempty"`
	CompletedAt         string `json:"completedAt"`
}

// ResolveUnknownReservation closes one UNKNOWN cost reservation with an
// audited operator decision (GAP-024). It works even when the runner is
// disabled so legacy UNKNOWN rows stay reconcilable, and it never triggers a
// provider call.
func (h *Handler) ResolveUnknownReservation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := sharedapp.WebPrincipalFrom(r.Context())
	if !ok {
		httpapi.WriteError(
			w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.",
		)
		return
	}
	reservationID := strings.TrimSpace(r.PathValue("reservationId"))
	if reservationID == "" {
		httpapi.WriteError(
			w, http.StatusUnprocessableEntity, "RESERVATION_ID_REQUIRED",
			"reservation ID가 필요합니다.",
		)
		return
	}
	var request reservationResolutionRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	outcome := runnerdomain.ReservationStatus(strings.TrimSpace(request.Outcome))
	if outcome != runnerdomain.ReservationSettled &&
		outcome != runnerdomain.ReservationReleased {
		httpapi.WriteError(
			w, http.StatusUnprocessableEntity, "RESERVATION_OUTCOME_INVALID",
			"판정은 SETTLED 또는 RELEASED여야 합니다.",
		)
		return
	}
	reason := strings.TrimSpace(request.ReasonDetail)
	if len([]rune(reason)) < 8 || len([]rune(reason)) > 500 {
		httpapi.WriteError(
			w, http.StatusUnprocessableEntity, "RESERVATION_REASON_INVALID",
			"판정 사유는 8자 이상 500자 이하여야 합니다.",
		)
		return
	}
	evidence := strings.TrimSpace(request.EvidenceReference)
	if evidence == "" {
		httpapi.WriteError(
			w, http.StatusUnprocessableEntity, "RESERVATION_EVIDENCE_REQUIRED",
			"provider 증거 참조가 필요합니다.",
		)
		return
	}
	if request.SettledAmountMicros != nil {
		if *request.SettledAmountMicros < 0 {
			httpapi.WriteError(
				w, http.StatusUnprocessableEntity, "RESERVATION_AMOUNT_INVALID",
				"settled 금액은 0 이상이어야 합니다.",
			)
			return
		}
		if outcome == runnerdomain.ReservationReleased {
			httpapi.WriteError(
				w, http.StatusUnprocessableEntity, "RESERVATION_AMOUNT_INVALID",
				"RELEASED 판정에는 settled 금액을 넣지 않습니다.",
			)
			return
		}
	}
	resolved, err := h.budget.ResolveUnknown(r.Context(), runnerapp.UnknownResolutionInput{
		ReservationID:       reservationID,
		OperatorUserID:      principal.UserID,
		Outcome:             outcome,
		ReasonDetail:        reason,
		EvidenceReference:   evidence,
		SettledAmountMicros: request.SettledAmountMicros,
	})
	if err != nil {
		switch {
		case errors.Is(err, runnerdomain.ErrReservationNotFound):
			httpapi.WriteError(
				w, http.StatusNotFound, "RESERVATION_NOT_FOUND",
				"reservation을 찾을 수 없습니다.",
			)
		case errors.Is(err, runnerdomain.ErrReservationNotUnknown):
			httpapi.WriteError(
				w, http.StatusConflict, "RESERVATION_NOT_UNKNOWN",
				"UNKNOWN 상태의 reservation만 판정할 수 있습니다.",
			)
		default:
			if _, ok := fault.As(err); ok {
				httpapi.WriteFault(w, r, err, "reservation 판정에 실패했습니다.")
				return
			}
			httpapi.WriteError(
				w, http.StatusInternalServerError, "RESERVATION_RESOLUTION_FAILED",
				"reservation 판정에 실패했습니다.",
			)
		}
		return
	}
	response := reservationResolutionResponse{
		ReservationID: resolved.ID,
		Status:        string(resolved.Status),
		AmountMicros:  resolved.AmountMicros,
	}
	if outcome == runnerdomain.ReservationSettled {
		settled := resolved.AmountMicros
		if request.SettledAmountMicros != nil {
			settled = *request.SettledAmountMicros
		}
		response.SettledAmountMicros = &settled
	}
	if resolved.CompletedAt != nil {
		response.CompletedAt = resolved.CompletedAt.UTC().Format(time.RFC3339)
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}

func historyDays(value string) (int, bool) {
	if strings.TrimSpace(value) == "" {
		return 30, true
	}
	days, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	switch days {
	case 7, 30, 90:
		return days, true
	default:
		return 0, false
	}
}

func usageSummary(
	counters runnerdomain.UsageCounters,
	limitMicros int64,
) adminUsageSummaryResponse {
	spent := counters.CommittedMicros()
	percent := 0.0
	if limitMicros > 0 {
		percent = math.Round(
			(float64(spent)*100/float64(limitMicros))*100,
		) / 100
	}
	return adminUsageSummaryResponse{
		InputTokens:    counters.InputTokens,
		OutputTokens:   counters.OutputTokens,
		TotalTokens:    counters.TotalTokens(),
		RequestCount:   counters.RequestCount,
		ReservedMicros: counters.ReservedMicros,
		SettledMicros:  counters.SettledMicros,
		SpentMicros:    spent,
		UsagePercent:   percent,
	}
}
