package http

import (
	"context"
	"net/http"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type productReactionService interface {
	SaveProductReaction(context.Context, researchapp.SaveProductReactionInput) (researchdomain.ProductReaction, error)
}

func (h *LiveCatalogReviewHandlerV2) ProductReaction(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	service, ok := h.workspace.(productReactionService)
	if !ok {
		httpapi.WriteError(w, 503, "PRODUCT_REACTION_UNAVAILABLE", "Product reaction unavailable")
		return
	}
	var input struct {
		SchemaVersion   string                          `json:"schemaVersion"`
		ProductRef      researchdomain.SourceProductRef `json:"productRef"`
		Pinned          *bool                           `json:"pinned"`
		Sentiment       string                          `json:"sentiment"`
		ExpectedVersion *int64                          `json:"expectedVersion"`
	}
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	if input.SchemaVersion != "vitlane.product-reaction.v1" || input.Pinned == nil || input.ExpectedVersion == nil {
		httpapi.WriteError(w, 400, "PRODUCT_REACTION_INVALID", "Invalid product reaction")
		return
	}
	value, err := service.SaveProductReaction(r.Context(), researchapp.SaveProductReactionInput{UserID: user, CurationID: r.PathValue("curationId"), CandidateID: r.PathValue("candidateId"), ProductRef: input.ProductRef, Pinned: *input.Pinned, Sentiment: input.Sentiment, ExpectedVersion: *input.ExpectedVersion})
	if err != nil {
		httpapi.WriteFault(w, r, err, "Could not save product reaction")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, map[string]any{"schemaVersion": "vitlane.product-reaction.v1", "reaction": value})
}
