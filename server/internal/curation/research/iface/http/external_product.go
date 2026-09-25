package http

import (
	"context"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"net/http"
)

type externalProductService interface {
	ExternalProductState(context.Context, string, string, string) (map[string]any, error)
	MarkExternalPurchase(context.Context, researchapp.MarkExternalPurchaseInput) (researchapp.PurchaseFeedback, error)
}

func (h *LiveCatalogReviewHandlerV2) ExternalProduct(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	service, ok := h.workspace.(externalProductService)
	if !ok {
		httpapi.WriteError(w, 503, "EXTERNAL_PRODUCT_UNAVAILABLE", "External product unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	curation, candidate := r.PathValue("curationId"), r.PathValue("candidateId")
	if r.Method == http.MethodGet {
		result, err := service.ExternalProductState(r.Context(), user, curation, candidate)
		if err != nil {
			httpapi.WriteFault(w, r, err, "Product state unavailable")
			return
		}
		httpapi.WriteJSON(w, 200, result)
		return
	}
	var input struct {
		SchemaVersion   string                           `json:"schemaVersion"`
		ProductRef      *researchdomain.SourceProductRef `json:"productRef"`
		Checked         *bool                            `json:"checked"`
		ExpectedVersion *int64                           `json:"expectedVersion"`
	}
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	if input.SchemaVersion != "vitlane.external-product-purchase.v1" || input.ProductRef == nil || input.Checked == nil || input.ExpectedVersion == nil {
		httpapi.WriteError(w, 400, "PURCHASE_RECORD_INVALID", "Invalid product purchase check")
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := service.MarkExternalPurchase(r.Context(), researchapp.MarkExternalPurchaseInput{UserID: user, CurationID: curation, CandidateID: candidate, ProductRef: input.ProductRef, Checked: *input.Checked, ExpectedVersion: *input.ExpectedVersion, IdempotencyKey: key})
	if err != nil {
		httpapi.WriteFault(w, r, err, "Purchase check unavailable")
		return
	}
	httpapi.WriteJSON(w, 200, result)
}
