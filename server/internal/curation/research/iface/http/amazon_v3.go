package http

import (
	"context"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	"net/http"
)

type amazonWorkspaceService interface {
	ResolveAmazonVariant(context.Context, string, string, string, string, researchdomain.SourceVariantRef) (researchapp.CatalogProductObservation, error)
	ResolveExternalLink(context.Context, string, string, string) (string, error)
	AmazonState(context.Context, string, string, string) (map[string]any, error)
	MarkExternalPurchase(context.Context, researchapp.MarkExternalPurchaseInput) (researchapp.PurchaseFeedback, error)
}

func (h *LiveCatalogReviewHandlerV2) AmazonCandidate(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	service, ok := h.workspace.(amazonWorkspaceService)
	if !ok {
		httpapi.WriteError(w, 503, "AMAZON_SOURCE_DISABLED", "Amazon 기능을 사용할 수 없습니다.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	curation, candidate := r.PathValue("curationId"), r.PathValue("candidateId")
	switch r.PathValue("amazonAction") {
	case "state":
		result, err := service.AmazonState(r.Context(), user, curation, candidate)
		if err != nil {
			httpapi.WriteFault(w, r, err, "상품 상태를 불러오지 못했습니다.")
			return
		}
		httpapi.WriteJSON(w, 200, result)
	case "external-link":
		link, err := service.ResolveExternalLink(r.Context(), user, curation, candidate)
		if err != nil {
			httpapi.WriteFault(w, r, err, "구매 링크를 확인하지 못했습니다.")
			return
		}
		httpapi.WriteJSON(w, 200, map[string]string{"schemaVersion": "vitlane.external-link.v3", "source": "AMAZON", "url": link})
	case "resolve":
		var in struct {
			RelationToken string                          `json:"relationToken"`
			VariantRef    researchdomain.SourceVariantRef `json:"variantRef"`
		}
		if !httpapi.DecodeJSON(w, r, &in) {
			return
		}
		p, err := service.ResolveAmazonVariant(r.Context(), user, curation, candidate, in.RelationToken, in.VariantRef)
		if err != nil {
			httpapi.WriteFault(w, r, err, "선택한 옵션을 확인하지 못했습니다.")
			return
		}
		result := mapLiveCatalogReviewResultV2(researchapp.LiveCatalogReviewResultV2{Search: researchapp.CatalogProductSearchResult{Products: []researchapp.CatalogProductObservation{p}}})
		httpapi.WriteJSON(w, 200, map[string]any{"schemaVersion": "vitlane.amazon-variant.v3", "product": result.Products[0]})
	case "purchase-check":
		var body struct {
			VariantRef      researchdomain.SourceVariantRef       `json:"variantRef"`
			Checked         *bool                                 `json:"checked"`
			ExpectedVersion *int64                                `json:"expectedVersion"`
			Snapshot        *researchdomain.PurchaseCheckSnapshot `json:"snapshot"`
		}
		if !httpapi.DecodeJSON(w, r, &body) {
			return
		}
		if body.Checked == nil || body.ExpectedVersion == nil {
			httpapi.WriteError(w, 400, "PURCHASE_RECORD_INVALID", "구매 체크 상태와 버전이 필요합니다.")
			return
		}
		in := researchapp.MarkExternalPurchaseInput{VariantRef: body.VariantRef, Checked: *body.Checked, ExpectedVersion: *body.ExpectedVersion, Snapshot: body.Snapshot}
		key, ok := requireIdempotencyKey(w, r)
		if !ok {
			return
		}
		in.UserID = user
		in.CurationID = curation
		in.CandidateID = candidate
		in.IdempotencyKey = key
		result, err := service.MarkExternalPurchase(r.Context(), in)
		if err != nil {
			httpapi.WriteFault(w, r, err, "구매 체크를 저장하지 못했습니다.")
			return
		}
		httpapi.WriteJSON(w, 200, result)
	default:
		http.NotFound(w, r)
	}
}

type AmazonUsageHandler struct {
	Control researchapp.AmazonControlRepository
	Mode    string
	Repo    researchapp.CatalogAPIUsageRepository
	Gateway researchapp.AmazonCatalogGateway
}

func (h *AmazonUsageHandler) Usage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	control, err := h.Control.ReadAmazonControl(r.Context())
	if err != nil {
		httpapi.WriteFault(w, r, err, "Amazon 운영 설정을 불러오지 못했습니다.")
		return
	}
	if h.Gateway != nil && control.Enabled {
		usage, err := h.Repo.ReadAmazonUsage(r.Context())
		if err == nil && (r.URL.Query().Get("refresh") == "true" || usage.Quota == nil) {
			_, _ = h.Gateway.RefreshAmazonUsage(r.Context())
		}
	}
	usage, err := h.Repo.ReadAmazonUsage(r.Context())
	if err != nil {
		httpapi.WriteFault(w, r, err, "Amazon API 사용량을 불러오지 못했습니다.")
		return
	}
	usage.Control = control
	usage.Configured = h.Gateway != nil
	usage.Enabled = usage.Configured && control.Enabled
	usage.Mode = h.Mode
	if !usage.Enabled {
		usage.Mode = "DISABLED"
	} else if usage.Mode == "" {
		usage.Mode = "LIVE"
	}
	httpapi.WriteJSON(w, 200, usage)
}

func (h *AmazonUsageHandler) SetControl(w http.ResponseWriter, r *http.Request) {
	user, ok := httpapi.RequireAuthenticatedUserID(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		Enabled         *bool  `json:"enabled"`
		ExpectedVersion *int64 `json:"expectedVersion"`
	}
	if !httpapi.DecodeJSON(w, r, &body) {
		return
	}
	if body.Enabled == nil || body.ExpectedVersion == nil {
		httpapi.WriteError(w, 400, "AMAZON_CONTROL_INVALID", "상태와 버전이 필요합니다.")
		return
	}
	control, err := researchapp.SetAmazonControl(r.Context(), h.Control, h.Gateway != nil, user, *body.Enabled, *body.ExpectedVersion)
	if err != nil {
		httpapi.WriteFault(w, r, err, "Amazon 운영 설정을 저장하지 못했습니다.")
		return
	}
	mode := h.Mode
	if mode == "" {
		mode = "LIVE"
	}
	if h.Gateway == nil || !control.Enabled {
		mode = "DISABLED"
	}
	httpapi.WriteJSON(w, 200, map[string]any{"schemaVersion": "vitlane.amazon-source-control.v1", "control": control, "configured": h.Gateway != nil, "enabled": h.Gateway != nil && control.Enabled, "mode": mode})
}
