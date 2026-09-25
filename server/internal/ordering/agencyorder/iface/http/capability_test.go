package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	liveapp "github.com/vitlane/vitlane/server/internal/ordering/livecontrol/app"
)

type testLiveCapabilityReader struct{ snapshot liveapp.Snapshot }

func (r testLiveCapabilityReader) State(context.Context) (liveapp.Snapshot, error) {
	return r.snapshot, nil
}

func TestCapabilityDoesNotRouteUsersIntoDisabledOrderAPI(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		enabled     bool
		provider    string
		sandbox     bool
		live        bool
		liveReady   bool
		wantState   string
		wantReason  string
		wantSource  string
		wantSandbox string
		wantLive    string
	}{
		{name: "disabled", wantState: "UNAVAILABLE", wantReason: "AGENCY_ORDER_DISABLED", wantSource: "NONE", wantSandbox: "UNAVAILABLE", wantLive: "UNAVAILABLE"},
		{name: "ready without paypal", enabled: true, provider: "shopify", wantState: "READY", wantSource: "SHOPIFY", wantSandbox: "UNAVAILABLE", wantLive: "UNAVAILABLE"},
		{name: "sandbox and killed live", enabled: true, provider: "shopify", sandbox: true, live: true, wantState: "READY", wantSource: "SHOPIFY", wantSandbox: "READY", wantLive: "PAUSED"},
		{name: "all three ready", enabled: true, provider: "shopify", sandbox: true, live: true, liveReady: true, wantState: "READY", wantSource: "SHOPIFY", wantSandbox: "READY", wantLive: "READY"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			gate := liveapp.Gate{StaticAllowed: testCase.live, RuntimeKilled: !testCase.liveReady, Effective: testCase.liveReady}
			Capability(testCase.enabled, testCase.provider, testCase.sandbox, testCase.live,
				testLiveCapabilityReader{snapshot: liveapp.Snapshot{Version: 8, OrderIssue: gate, PayPalMoney: gate, MerchantEffect: gate}}).ServeHTTP(
				response, httptest.NewRequest(http.MethodGet, "/api/v1/agency-order-capability", nil),
			)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body struct {
				SchemaVersion string `json:"schemaVersion"`
				Capability    struct {
					State              string `json:"state"`
					ReasonCode         string `json:"reasonCode"`
					CheckoutProvider   string `json:"checkoutProvider"`
					CapabilityRevision int64  `json:"capabilityRevision"`
					PaymentRails       struct {
						PayPalSandbox struct {
							State string `json:"state"`
						} `json:"paypalSandbox"`
						PayPalLive struct {
							State string `json:"state"`
						} `json:"paypalLive"`
					} `json:"paymentRails"`
				} `json:"capability"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.SchemaVersion != "vitlane.agency-order-capability.v2" ||
				body.Capability.State != testCase.wantState ||
				body.Capability.ReasonCode != testCase.wantReason ||
				body.Capability.CheckoutProvider != testCase.wantSource ||
				body.Capability.PaymentRails.PayPalSandbox.State != testCase.wantSandbox ||
				body.Capability.PaymentRails.PayPalLive.State != testCase.wantLive {
				t.Fatalf("body=%+v", body)
			}
		})
	}
}
