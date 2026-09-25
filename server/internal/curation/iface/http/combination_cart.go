package http

import (
	a "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"net/http"
	"time"
)

func (h *ThreadHandler) CombinationStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	out, err := h.service.CombinationStatus(r.Context(), user, r.PathValue("curationId"), r.PathValue("threadId"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, 200, out)
}
func (h *ThreadHandler) ApplyCombinationCart(w http.ResponseWriter, r *http.Request) {
	h.applyCart(w, r, false)
}
func (h *ThreadHandler) ApplyRepresentativeCart(w http.ResponseWriter, r *http.Request) {
	h.applyCart(w, r, true)
}
func (h *ThreadHandler) applyCart(w http.ResponseWriter, r *http.Request, representatives bool) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var body struct {
		SchemaVersion   string                     `json:"schemaVersion"`
		CommandID       string                     `json:"commandId"`
		ExpectedVersion int64                      `json:"expectedVersion"`
		Mode            string                     `json:"mode"`
		Items           []catalogCartItemRequestV2 `json:"items"`
	}
	if !httpapi.DecodeJSON(w, r, &body) {
		return
	}
	if representatives {
		if body.SchemaVersion != "vitlane.representative-cart-command.v1" {
			httpapi.WriteError(w, 400, "COMBINATION_CART_INVALID", "요청 형식을 확인해 주세요.")
			return
		}
		body.Mode = "ADD"
	}
	if body.CommandID == "" || r.Header.Get("Idempotency-Key") != body.CommandID {
		httpapi.WriteError(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "요청 식별자를 확인해 주세요.")
		return
	}
	in := a.CombinationCartCommand{CommandID: body.CommandID, ExpectedVersion: body.ExpectedVersion, Mode: body.Mode, Items: []a.CatalogCartItemV2{}}
	for _, row := range body.Items {
		observed, err := time.Parse(time.RFC3339Nano, row.ObservedAt)
		if err != nil {
			httpapi.WriteError(w, 400, "PHASE8_CART_ITEM_INVALID", "상품 옵션을 확인해 주세요.")
			return
		}
		in.Items = append(in.Items, a.CatalogCartItemV2{TargetID: row.TargetID, Merchant: row.MerchantName, SellerDomain: row.SellerDomain, IntentPoint: row.IntentPoint, Item: d.CartItemV2{ID: row.CartItemID, CandidateID: row.CandidateID, ProductTitleSnapshot: row.ProductTitle, ProductURL: row.ProductURL, VariantID: row.VariantID, VariantTitleSnapshot: row.VariantTitle, SelectedOptions: row.SelectedOptions, PreviewPriceMinor: row.PreviewPriceMinor, PreviewCurrency: row.PreviewCurrency, Quantity: row.Quantity, ObservedAt: observed}})
	}
	var out a.CatalogCartStateV2
	var err error
	if representatives {
		out, err = h.service.ApplyRepresentativeCart(r.Context(), user, r.PathValue("curationId"), in)
	} else {
		out, err = h.service.ApplyCombinationCart(r.Context(), user, r.PathValue("curationId"), r.PathValue("threadId"), in)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.WriteJSON(w, 200, mapCatalogCartResponseV2(out))
}
