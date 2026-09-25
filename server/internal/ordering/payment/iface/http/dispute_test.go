package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

func TestPayPalDisputeOperatorEndpointsRejectMissingEnvironment(t *testing.T) {
	handler := &Handler{}
	for _, test := range []struct {
		name   string
		method string
		call   http.HandlerFunc
	}{
		{name: "queue", method: http.MethodGet, call: handler.ListPayPalDisputes},
		{name: "view", method: http.MethodGet, call: handler.GetPayPalDispute},
		{name: "action", method: http.MethodPost, call: handler.RecordPayPalDisputeAction},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/disputes", nil)
			request = request.WithContext(sharedapp.WithAuthenticatedUserID(
				request.Context(), "operator-1",
			))
			response := httptest.NewRecorder()
			test.call(response, request)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d want=%d", response.Code, http.StatusUnprocessableEntity)
			}
		})
	}
}

func TestRequirePayPalDisputeEnvironmentFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		query  string
		want   string
		wantOK bool
		status int
	}{
		{name: "sandbox", query: "?environment=SANDBOX", want: "SANDBOX", wantOK: true},
		{name: "normalized live", query: "?environment=%20live%20", want: "LIVE", wantOK: true},
		{name: "missing", wantOK: false, status: http.StatusUnprocessableEntity},
		{name: "unknown", query: "?environment=PRODUCTION", wantOK: false, status: http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/disputes"+test.query, nil)
			response := httptest.NewRecorder()
			got, ok := requirePayPalDisputeEnvironment(response, request)
			if ok != test.wantOK || got != test.want {
				t.Fatalf("environment=%q ok=%v", got, ok)
			}
			if !ok && response.Code != test.status {
				t.Fatalf("status=%d want=%d", response.Code, test.status)
			}
		})
	}
}

func TestWebhookEnvelopeExtractsOnlySafeDisputeBinding(t *testing.T) {
	var envelope webhookEnvelope
	if err := json.Unmarshal([]byte(`{
		"id":"WH-1","event_type":"CUSTOMER.DISPUTE.CREATED",
		"create_time":"2026-08-27T03:00:00Z","resource":{
			"dispute_id":"PP-D-1","status":"WAITING_FOR_SELLER_RESPONSE",
			"disputed_transactions":[{"seller_transaction_id":"CAPTURE-1"}],
			"seller_response_due_date":"2026-08-29T03:00:00Z"
		}
	}`), &envelope); err != nil {
		t.Fatal(err)
	}
	captureID, err := envelope.disputedCaptureID()
	if err != nil || captureID != "CAPTURE-1" ||
		firstValue(envelope.Resource.DisputeID, envelope.Resource.ID) != "PP-D-1" ||
		parsePayPalTime(envelope.Resource.SellerResponseDueDate) == nil {
		t.Fatalf("capture=%q envelope=%+v err=%v", captureID, envelope, err)
	}
}

func TestWebhookEnvelopeRejectsAmbiguousDisputeTransactions(t *testing.T) {
	var envelope webhookEnvelope
	if err := json.Unmarshal([]byte(`{
		"resource":{"disputed_transactions":[
			{"seller_transaction_id":"CAPTURE-1"},
			{"seller_transaction_id":"CAPTURE-2"}
		]}
	}`), &envelope); err != nil {
		t.Fatal(err)
	}
	if _, err := envelope.disputedCaptureID(); !errors.Is(err, domain.ErrDisputeInvalid) {
		t.Fatalf("ambiguous capture error=%v", err)
	}
}
