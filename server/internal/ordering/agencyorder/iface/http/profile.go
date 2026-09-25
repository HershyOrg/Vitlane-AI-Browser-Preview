package http

import (
	"net/http"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

func AgentProfileDocument() map[string]any {
	return map[string]any{
		"ucp": map[string]any{
			"version": "2026-04-08",
			"capabilities": map[string]any{
				"dev.ucp.shopping.catalog.search": []map[string]string{{"version": "2026-04-08"}},
				"dev.ucp.shopping.catalog.lookup": []map[string]string{{"version": "2026-04-08"}},
				"dev.shopify.catalog":             []map[string]string{{"version": "2026-04-08"}},
				"dev.shopify.catalog.global":      []map[string]string{{"version": "2026-04-08"}},
				"dev.ucp.shopping.cart":           []map[string]string{{"version": "2026-04-08"}},
				"dev.ucp.shopping.checkout":       []map[string]string{{"version": "2026-04-08"}},
				"dev.ucp.shopping.fulfillment":    []map[string]string{{"version": "2026-04-08"}},
			},
		},
	}
}

func AgentProfileHash() (string, error) {
	return shareddomain.CanonicalJSONHash(AgentProfileDocument())
}

func AgentProfile(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	httpapi.WriteJSON(w, http.StatusOK, AgentProfileDocument())
}
