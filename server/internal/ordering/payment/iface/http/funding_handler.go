package http

import (
	"errors"
	"net/http"
	"strings"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type AccountingHandler struct {
	service *paymentapp.AccountingService
}

func NewAccountingHandler(service *paymentapp.AccountingService) *AccountingHandler {
	return &AccountingHandler{service: service}
}

func (h *AccountingHandler) GetAccounting(w http.ResponseWriter, r *http.Request) {
	environment := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("environment")))
	// Environment is deliberately required. An implicit test default would
	// let an operator believe a filter was applied when the caller actually
	// omitted it, and would mix PayPal Sandbox/Live with GIWA Testnet facts.
	summary, err := h.service.GetAccounting(r.Context(), environment)
	if err != nil {
		writeFundingError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": "vitlane.order-accounting.v2",
		"summary":       summary,
	})
}

func writeFundingError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "주문 자금 정보를 처리하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "ORDER_ACCOUNTING_NOT_FOUND",
			"주문 회계 기록을 찾지 못했습니다.")
	case errors.Is(err, domain.ErrInvalid):
		httpapi.WriteError(w, http.StatusBadRequest, "ORDER_ACCOUNTING_INVALID",
			"주문 회계 요청이 올바르지 않습니다.")
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "ORDER_ACCOUNTING_INTERNAL",
			"주문 회계 정보를 처리하지 못했습니다.")
	}
}
