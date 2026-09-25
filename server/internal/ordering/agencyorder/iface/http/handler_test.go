package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
)

func TestWriteErrorExposesMissingShippingRecovery(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/curations/curation-1/order-sheets", nil)
	writeError(response, request, agencydomain.ErrShippingRequired)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "SHIPPING_PROFILE_REQUIRED" || body.Error.Message == "" {
		t.Fatalf("body=%+v", body)
	}
}

func TestWriteErrorExposesClosedPayPalLiveIssueGate(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/order-sheets/sheet-1/issue", nil)
	writeError(response, request, agencyapp.ErrLiveIssueDisabled)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "PAYPAL_RAIL_UNAVAILABLE" {
		t.Fatalf("body=%+v", body)
	}
}

func TestRefundHTTPBodiesAreWholeMerchantOrderOnly(t *testing.T) {
	approved := true
	requestPayload, err := json.Marshal(refundRequestBody{
		MerchantOrderID: "mo-1", ReasonCode: "ITEM_DAMAGED_DEFECTIVE",
		PublicRationale: "The item arrived damaged.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(requestPayload) != `{"merchantOrderId":"mo-1","reasonCode":"ITEM_DAMAGED_DEFECTIVE","publicRationale":"The item arrived damaged."}` {
		t.Fatalf("request contract=%s", requestPayload)
	}
	decisionPayload, err := json.Marshal(refundDecisionBody{
		Approve: &approved, PublicRationale: "Evidence confirms damage.", InternalNote: "OPS-17",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(decisionPayload) != `{"approve":true,"publicRationale":"Evidence confirms damage.","internalNote":"OPS-17"}` {
		t.Fatalf("decision contract=%s", decisionPayload)
	}
}

func TestWriteErrorExposesConsumedOrderRecovery(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/order-sheets/sheet-1/preflight", nil)
	writeError(response, request, &agencydomain.OrderSheetAlreadyConsumedError{AgencyOrderID: "order-1"})
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code          string `json:"code"`
			AgencyOrderID string `json:"agencyOrderId"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "ORDER_SHEET_ALREADY_CONSUMED" || body.Error.AgencyOrderID != "order-1" {
		t.Fatalf("body=%+v", body)
	}
}

func TestWriteErrorExposesBlockedCartItemWithoutProviderPayload(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/curations/curation-1/order-sheets", nil)
	writeError(response, request, &agencydomain.OrderPreparationLineError{
		ReasonCode: "VARIANT_UNAVAILABLE",
		ItemTitle:  "Classic Rim Dinnerware Set",
		Retryable:  false,
	})
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code       string `json:"code"`
			ReasonCode string `json:"reasonCode"`
			ItemTitle  string `json:"itemTitle"`
			Retryable  bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "CONFLICT" || body.Error.ReasonCode != "VARIANT_UNAVAILABLE" ||
		body.Error.ItemTitle != "Classic Rim Dinnerware Set" || body.Error.Retryable {
		t.Fatalf("body=%+v", body)
	}
}
