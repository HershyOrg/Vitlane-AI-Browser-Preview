package http

import (
	"net/http"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

type CatalogCartHandlerV2 struct {
	service *curationapp.CatalogCartServiceV2
}

func NewCatalogCartHandlerV2(service *curationapp.CatalogCartServiceV2) *CatalogCartHandlerV2 {
	return &CatalogCartHandlerV2{service: service}
}

type catalogCartItemRequestV2 struct {
	CartItemID        string   `json:"cartItemId"`
	TargetID          string   `json:"targetId"`
	CandidateID       string   `json:"candidateId"`
	ProductTitle      string   `json:"productTitle"`
	ProductURL        string   `json:"productUrl,omitempty"`
	MerchantName      string   `json:"merchantName,omitempty"`
	SellerDomain      string   `json:"sellerDomain,omitempty"`
	IntentPoint       string   `json:"intentPoint,omitempty"`
	VariantID         string   `json:"variantId"`
	VariantTitle      string   `json:"variantTitle"`
	SelectedOptions   []string `json:"selectedOptions"`
	PreviewPriceMinor int64    `json:"previewPriceMinor"`
	PreviewCurrency   string   `json:"previewCurrency"`
	Quantity          int      `json:"quantity"`
	ObservedAt        string   `json:"observedAt"`
}

type replaceCatalogCartRequestV2 struct {
	ExpectedVersion int64                      `json:"expectedVersion"`
	Items           []catalogCartItemRequestV2 `json:"items"`
}

type catalogCartResponseV2 struct {
	SchemaVersion string                      `json:"schemaVersion"`
	CurationID    string                      `json:"curationId"`
	Version       int64                       `json:"version"`
	Country       string                      `json:"country"`
	Currency      string                      `json:"currency"`
	Items         []catalogCartItemResponseV2 `json:"items"`
	UpdatedAt     string                      `json:"updatedAt,omitempty"`
}

type catalogCartItemResponseV2 struct {
	catalogCartItemRequestV2
	AddedAt string `json:"addedAt"`
}

func (handler *CatalogCartHandlerV2) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	state, err := handler.service.Get(r.Context(), userID, r.PathValue("curationId"))
	if err != nil {
		httpapi.WriteFault(w, r, err, "CartView를 불러오지 못했습니다.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, mapCatalogCartResponseV2(state))
}

func (handler *CatalogCartHandlerV2) Replace(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	var request replaceCatalogCartRequestV2
	if !httpapi.DecodeJSON(w, r, &request) {
		return
	}
	items := make([]curationapp.CatalogCartItemV2, 0, len(request.Items))
	for _, item := range request.Items {
		observedAt, err := time.Parse(time.RFC3339, item.ObservedAt)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "PHASE8_CART_ITEM_INVALID", "Cart item을 확인해 주세요.")
			return
		}
		items = append(items, curationapp.CatalogCartItemV2{
			TargetID: item.TargetID, Merchant: item.MerchantName,
			SellerDomain: item.SellerDomain, IntentPoint: item.IntentPoint,
			Item: curationdomain.CartItemV2{
				ID: item.CartItemID, CandidateID: item.CandidateID,
				ProductTitleSnapshot: item.ProductTitle, ProductURL: item.ProductURL,
				VariantID: item.VariantID, VariantTitleSnapshot: item.VariantTitle,
				SelectedOptions:   append([]string(nil), item.SelectedOptions...),
				PreviewPriceMinor: item.PreviewPriceMinor,
				PreviewCurrency:   item.PreviewCurrency, Quantity: item.Quantity,
				ObservedAt: observedAt,
			},
		})
	}
	state, err := handler.service.Replace(
		r.Context(), userID, r.PathValue("curationId"),
		request.ExpectedVersion, items,
	)
	if err != nil {
		httpapi.WriteFault(w, r, err, "CartView를 저장하지 못했습니다.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, mapCatalogCartResponseV2(state))
}

func mapCatalogCartResponseV2(state curationapp.CatalogCartStateV2) catalogCartResponseV2 {
	response := catalogCartResponseV2{
		SchemaVersion: "vitlane.cart-view.v2",
		CurationID:    state.CurationID, Version: state.Version,
		Country: state.Country, Currency: state.Currency,
		Items: []catalogCartItemResponseV2{},
	}
	if !state.UpdatedAt.IsZero() {
		response.UpdatedAt = state.UpdatedAt.Format(time.RFC3339Nano)
	}
	for _, value := range state.Items {
		response.Items = append(response.Items, catalogCartItemResponseV2{
			catalogCartItemRequestV2: catalogCartItemRequestV2{
				CartItemID: value.Item.ID, TargetID: value.TargetID,
				CandidateID:  value.Item.CandidateID,
				ProductTitle: value.Item.ProductTitleSnapshot,
				ProductURL:   value.Item.ProductURL, MerchantName: value.Merchant,
				SellerDomain: value.SellerDomain, IntentPoint: value.IntentPoint,
				VariantID:         value.Item.VariantID,
				VariantTitle:      value.Item.VariantTitleSnapshot,
				SelectedOptions:   append([]string(nil), value.Item.SelectedOptions...),
				PreviewPriceMinor: value.Item.PreviewPriceMinor,
				PreviewCurrency:   value.Item.PreviewCurrency,
				Quantity:          value.Item.Quantity,
				ObservedAt:        value.Item.ObservedAt.Format(time.RFC3339Nano),
			},
			AddedAt: value.Item.AddedAt.Format(time.RFC3339Nano),
		})
	}
	return response
}
