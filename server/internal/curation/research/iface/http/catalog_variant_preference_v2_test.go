package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

func TestSaveWorkspaceInteractionMapsAtomicLikedSnapshotCommand(t *testing.T) {
	service := &catalogPreferenceHTTPServiceV2{}
	handler := &LiveCatalogReviewHandlerV2{workspace: service}
	request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{
		"candidateId":"candidate-1",
		"variantId":"gid://shopify/ProductVariant/1",
		"pinned":true,
		"sentiment":"like",
		"likedSnapshot":{
			"productTitle":"Commuter Pack",
			"variantTitle":"Black / Small",
			"productUrl":"https://shop.example/products/commuter-pack?variant=1",
			"merchant":"Shop Example",
			"priceMinor":8200,
			"currency":"USD",
			"targetTitle":"Commuter backpack"
		}
	}`))
	request.SetPathValue("curationId", "00000000-0000-4000-8000-000000000002")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(
		request.Context(), "00000000-0000-4000-8000-000000000001",
	))
	response := httptest.NewRecorder()

	handler.SaveWorkspaceInteraction(response, request)

	if response.Code != http.StatusNoContent || service.calls != 1 ||
		service.input.UserID != "00000000-0000-4000-8000-000000000001" ||
		service.input.CurationID != "00000000-0000-4000-8000-000000000002" ||
		service.input.Sentiment != "LIKE" || service.input.LikedSnapshot == nil ||
		service.input.LikedSnapshot.VariantTitle != "Black / Small" ||
		service.input.LikedSnapshot.PriceMinor != 8200 {
		t.Fatalf("status=%d calls=%d input=%+v", response.Code, service.calls, service.input)
	}
}

type catalogPreferenceHTTPServiceV2 struct {
	catalogWorkspaceServiceV2
	calls int
	input researchapp.CatalogSaveVariantInteractionInputV2
}

func (service *catalogPreferenceHTTPServiceV2) SaveWorkspaceInteractionV2(
	_ context.Context,
	input researchapp.CatalogSaveVariantInteractionInputV2,
) error {
	service.calls++
	service.input = input
	return nil
}
