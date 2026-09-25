package http

import (
	"context"
	"errors"
	"net/http"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type payPalResourceAdoptionService interface {
	AdoptMOReauthorization(
		context.Context, string, string, string,
		paymentapp.AdoptMOReauthorizationInput,
	) (paymentapp.AdoptMOReauthorizationResult, error)
	AdoptPayPalMORefund(
		context.Context, paymentapp.AdoptPayPalMORefundInput,
	) (paymentapp.AdoptPayPalMORefundResult, error)
}

type adoptPayPalMORefundRequest struct {
	ProviderRefundID string                                    `json:"providerRefundId"`
	PublicRationale  string                                    `json:"publicRationale"`
	EvidenceSource   domain.PayPalRefundAdoptionEvidenceSource `json:"evidenceSource"`
	EvidenceHash     string                                    `json:"evidenceHash"`
	ObservedAt       time.Time                                 `json:"observedAt"`
}

// AdoptMOReauthorization verifies and adopts an existing PayPal
// authorization with a fresh GET. It never sends a provider create/authorize
// request and never allocates a new provider request key.
func (h *Handler) AdoptMOReauthorization(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var input paymentapp.AdoptMOReauthorizationInput
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	result, err := h.adoption.AdoptMOReauthorization(
		r.Context(), r.PathValue("merchantOrderId"), actorUserID,
		r.Header.Get("Idempotency-Key"), input,
	)
	if err != nil && result.Adoption.ID == "" {
		writePayPalResourceAdoptionError(w, r, err)
		return
	}
	status := http.StatusOK
	if errors.Is(err, domain.ErrFundingOutcomeUnknown) {
		status = http.StatusAccepted
	} else if err != nil && !errors.Is(err, domain.ErrFundingNotAvailable) {
		writePayPalResourceAdoptionError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, status, map[string]any{
		"schemaVersion": "vitlane.paypal-reauthorization-adoption.v1",
		"result":        result,
	})
}

// AdoptPayPalMORefund verifies and adopts an existing PayPal refund with a
// fresh GET. PENDING/FAILED provider outcomes are still truthful successful
// evidence writes, so the handler returns their persisted result as 202/200.
func (h *Handler) AdoptPayPalMORefund(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request adoptPayPalMORefundRequest
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	result, err := h.adoption.AdoptPayPalMORefund(
		r.Context(), paymentapp.AdoptPayPalMORefundInput{
			CompensationID:   r.PathValue("compensationId"),
			ProviderRefundID: request.ProviderRefundID,
			OperatorUserID:   actorUserID,
			PublicRationale:  request.PublicRationale,
			EvidenceSource:   request.EvidenceSource,
			EvidenceHash:     request.EvidenceHash,
			ObservedAt:       request.ObservedAt,
		},
	)
	if err != nil && result.Adoption.ID == "" {
		writePayPalResourceAdoptionError(w, r, err)
		return
	}
	status := http.StatusOK
	if errors.Is(err, domain.ErrCompensationOutcomeUnknown) {
		status = http.StatusAccepted
	} else if err != nil && !errors.Is(err, domain.ErrCompensationAttemptFailed) {
		writePayPalResourceAdoptionError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, status, map[string]any{
		"schemaVersion": "vitlane.paypal-mo-refund-adoption.v1",
		"result":        result,
	})
}

func writePayPalResourceAdoptionError(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := fault.As(err); ok {
		httpapi.WriteFault(w, r, err, "PayPal resource를 검증하지 못했습니다.")
		return
	}
	switch {
	case errors.Is(err, domain.ErrInvalid),
		errors.Is(err, domain.ErrPayPalRefundAdoptionInvalid):
		httpapi.WriteError(w, http.StatusBadRequest,
			"PAYPAL_RESOURCE_ADOPTION_INVALID",
			"PayPal resource adoption input is invalid.")
	case errors.Is(err, domain.ErrPayPalRefundAdoptionMissing),
		errors.Is(err, domain.ErrFundingOutcomeUnknown),
		errors.Is(err, domain.ErrFundingNotAvailable):
		httpapi.WriteError(w, http.StatusConflict,
			"PAYPAL_RESOURCE_ADOPTION_NOT_AVAILABLE",
			"This operation is no longer eligible for manual PayPal resource adoption.")
	case errors.Is(err, domain.ErrPayPalRefundAdoptionMismatch),
		errors.Is(err, domain.ErrInstructionMismatch):
		httpapi.WriteError(w, http.StatusUnprocessableEntity,
			"PAYPAL_RESOURCE_ADOPTION_MISMATCH",
			"The PayPal resource does not exactly match the stored operation.")
	case errors.Is(err, domain.ErrConflict):
		httpapi.WriteError(w, http.StatusConflict,
			"PAYPAL_RESOURCE_ADOPTION_CONFLICT",
			"Payment state changed. Refresh the reconciliation queue.")
	default:
		httpapi.WriteError(w, http.StatusServiceUnavailable,
			"PAYPAL_RESOURCE_ADOPTION_UNAVAILABLE",
			"PayPal could not be queried for exact resource verification.")
	}
}
