package paypal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestMinorToValue(t *testing.T) {
	cases := map[int64]string{
		0:     "0.00",
		5:     "0.05",
		50:    "0.50",
		100:   "1.00",
		10600: "106.00",
		10601: "106.01",
	}
	for minor, expected := range cases {
		if got := MinorToValue(minor); got != expected {
			t.Fatalf("MinorToValue(%d) = %q, want %q", minor, got, expected)
		}
	}
}

func TestOrderResponseReadsActualSellerReceivableBreakdown(t *testing.T) {
	var response orderResponse
	if err := json.Unmarshal([]byte(`{
		"id":"ORDER-1","status":"COMPLETED","purchase_units":[{
			"amount":{"currency_code":"USD","value":"105.50"},
			"payments":{"captures":[{
				"id":"CAP-1","status":"COMPLETED","create_time":"2026-08-22T00:00:00Z",
				"amount":{"currency_code":"USD","value":"105.50"},
				"seller_receivable_breakdown":{
					"gross_amount":{"currency_code":"USD","value":"105.50"},
					"paypal_fee":{"currency_code":"USD","value":"4.08"},
					"net_amount":{"currency_code":"USD","value":"101.42"}
				}
			}]}
		}]}`), &response); err != nil {
		t.Fatal(err)
	}
	order, err := response.toOrder()
	if err != nil {
		t.Fatal(err)
	}
	if !order.CaptureEconomicsReconciled || order.ProcessorFeeMinor != 408 ||
		order.NetReceivableMinor != 10142 {
		t.Fatalf("unexpected capture economics: %+v", order)
	}
}

func TestOrderResponseLeavesInvalidBreakdownUnreconciled(t *testing.T) {
	var response orderResponse
	if err := json.Unmarshal([]byte(`{
		"id":"ORDER-1","status":"COMPLETED","purchase_units":[{
			"amount":{"currency_code":"USD","value":"105.50"},
			"payments":{"captures":[{
				"id":"CAP-1","status":"COMPLETED",
				"amount":{"currency_code":"USD","value":"105.50"},
				"seller_receivable_breakdown":{
					"gross_amount":{"currency_code":"USD","value":"105.50"},
					"paypal_fee":{"currency_code":"USD","value":"4.08"},
					"net_amount":{"currency_code":"USD","value":"101.41"}
				}
			}]}
		}]}`), &response); err != nil {
		t.Fatal(err)
	}
	order, err := response.toOrder()
	if err != nil {
		t.Fatal(err)
	}
	if order.CaptureEconomicsReconciled {
		t.Fatalf("mismatched breakdown must remain unreconciled: %+v", order)
	}
}

func TestValueToMinor(t *testing.T) {
	cases := map[string]int64{
		"0.00":   0,
		"0.5":    50,
		"1":      100,
		"106.00": 10600,
		"106.01": 10601,
	}
	for value, expected := range cases {
		got, err := ValueToMinor(value)
		if err != nil || got != expected {
			t.Fatalf("ValueToMinor(%q) = %d, %v, want %d", value, got, err, expected)
		}
	}
	for _, invalid := range []string{"", "-1.00", "1.005", "abc"} {
		if _, err := ValueToMinor(invalid); err == nil {
			t.Fatalf("ValueToMinor(%q) must fail", invalid)
		}
	}
}

func TestCreateOrderUsesAuthorizeAndOnePurchaseUnit(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/checkout/orders" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("PayPal-Request-Id"); got != "create-order-1" {
			t.Errorf("PayPal-Request-Id = %q", got)
		}
		if got := r.Header.Get("Prefer"); got != "return=representation" {
			t.Errorf("Prefer = %q", got)
		}
		var payload struct {
			Intent        string `json:"intent"`
			PurchaseUnits []struct {
				ReferenceID string `json:"reference_id"`
				CustomID    string `json:"custom_id"`
				Amount      Amount `json:"amount"`
			} `json:"purchase_units"`
			PaymentSource struct {
				PayPal struct {
					ExperienceContext struct {
						UserAction string `json:"user_action"`
					} `json:"experience_context"`
				} `json:"paypal"`
			} `json:"payment_source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode create payload: %v", err)
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		if payload.Intent != "AUTHORIZE" || len(payload.PurchaseUnits) != 1 ||
			payload.PurchaseUnits[0].ReferenceID != "agency-order-1" ||
			payload.PurchaseUnits[0].CustomID != "customer-payment-1" ||
			payload.PurchaseUnits[0].Amount != (Amount{CurrencyCode: "USD", Value: "106.00"}) ||
			payload.PaymentSource.PayPal.ExperienceContext.UserAction != "CONTINUE" {
			t.Errorf("unexpected create payload: %+v", payload)
		}
		writeTestJSON(w, http.StatusCreated, `{
			"id":"PAYPAL-ORDER-1","intent":"AUTHORIZE","status":"CREATED","purchase_units":[{
				"amount":{"currency_code":"USD","value":"106.00"},
				"payee":{"merchant_id":"MERCHANT-1"}
			}],"links":[{"rel":"approve","href":"https://paypal.test/approve/1"}]
		}`)
	}))

	order, err := client.CreateOrder(context.Background(), CreateOrderInput{
		RequestID: "create-order-1", ReferenceID: "agency-order-1",
		CustomID: "customer-payment-1", AmountMinor: 10600,
		ReturnURL: "https://vitlane.test/return", CancelURL: "https://vitlane.test/cancel",
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.ID != "PAYPAL-ORDER-1" || order.AmountMinor != 10600 ||
		order.Currency != "USD" || order.PayeeMerchant != "MERCHANT-1" ||
		order.PayerActionURL != "https://paypal.test/approve/1" {
		t.Fatalf("unexpected order: %+v", order)
	}
}

func TestCreateOrderEmptySuccessIsNonconforming(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusCreated, `{}`)
	}))
	_, err := client.CreateOrder(context.Background(), CreateOrderInput{
		RequestID: "create-order-empty", ReferenceID: "agency-order-1",
		CustomID: "customer-payment-1", AmountMinor: 10600,
		ReturnURL: "https://vitlane.test/return", CancelURL: "https://vitlane.test/cancel",
	})
	if !errors.Is(err, ErrNonconformingResponse) {
		t.Fatalf("empty create success error=%v want nonconforming", err)
	}
}

func TestCreateOrderPreApprovalWithoutHTTPSPayerActionIsNonconforming(t *testing.T) {
	for _, tc := range []struct {
		name  string
		links string
	}{
		{name: "missing", links: `[]`},
		{name: "insecure", links: `[{"rel":"approve","href":"http://paypal.test/approve/1"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeTestJSON(w, http.StatusCreated, `{
					"id":"PAYPAL-ORDER-1","intent":"AUTHORIZE","status":"CREATED",
					"purchase_units":[{"amount":{"currency_code":"USD","value":"106.00"},
					"payee":{"merchant_id":"MERCHANT-1"}}],"links":`+tc.links+`}`)
			}))
			_, err := client.CreateOrder(context.Background(), CreateOrderInput{
				RequestID: "create-order-bad-link", ReferenceID: "agency-order-1",
				CustomID: "customer-payment-1", AmountMinor: 10600,
				ReturnURL: "https://vitlane.test/return", CancelURL: "https://vitlane.test/cancel",
			})
			if !errors.Is(err, ErrNonconformingResponse) {
				t.Fatalf("pre-approval link error=%v want nonconforming", err)
			}
		})
	}
}

func TestAuthorizeOrderAndFreshGetReadAuthorization(t *testing.T) {
	var mu sync.Mutex
	requests := make([]string, 0, 3)
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/v2/payments/authorizations/AUTH-1" {
			writeTestJSON(w, http.StatusOK, `{
				"id":"AUTH-1","status":"CREATED",
				"amount":{"currency_code":"USD","value":"106.00"},
				"expiration_time":"2026-09-25T00:00:00Z"
			}`)
			return
		}
		if r.URL.Path != "/v2/checkout/orders/PAYPAL-ORDER-1/authorize" &&
			r.URL.Path != "/v2/checkout/orders/PAYPAL-ORDER-1" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("PayPal-Request-Id") != "authorize-1" {
			t.Errorf("authorize request id = %q", r.Header.Get("PayPal-Request-Id"))
		}
		writeTestJSON(w, http.StatusCreated, authorizedOrderJSON())
	}))

	order, err := client.AuthorizeOrder(
		context.Background(), "PAYPAL-ORDER-1", "authorize-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	assertAuthorizedOrder(t, order)
	fresh, err := client.GetAuthorizedOrder(context.Background(), "PAYPAL-ORDER-1")
	if err != nil {
		t.Fatal(err)
	}
	assertAuthorizedOrder(t, fresh)
	authorization, err := client.GetAuthorization(context.Background(), "AUTH-1")
	if err != nil {
		t.Fatal(err)
	}
	if authorization.ID != "AUTH-1" || authorization.AmountMinor != 10600 {
		t.Fatalf("unexpected direct authorization: %+v", authorization)
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(requests) != "[POST /v2/checkout/orders/PAYPAL-ORDER-1/authorize GET /v2/checkout/orders/PAYPAL-ORDER-1 GET /v2/payments/authorizations/AUTH-1]" {
		t.Fatalf("unexpected requests: %v", requests)
	}
}

func TestCaptureAuthorizationSupportsMultiplePartialCaptures(t *testing.T) {
	type capturedRequest struct {
		RequestID string
		InvoiceID string
		Amount    Amount
		Final     bool
	}
	var mu sync.Mutex
	requests := make([]capturedRequest, 0, 2)
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost ||
			r.URL.Path != "/v2/payments/authorizations/AUTH-1/capture" {
			http.NotFound(w, r)
			return
		}
		var payload struct {
			Amount       Amount `json:"amount"`
			InvoiceID    string `json:"invoice_id"`
			FinalCapture bool   `json:"final_capture"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode capture payload: %v", err)
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, capturedRequest{
			RequestID: r.Header.Get("PayPal-Request-Id"), InvoiceID: payload.InvoiceID,
			Amount: payload.Amount, Final: payload.FinalCapture,
		})
		captureNumber := len(requests)
		mu.Unlock()
		fee := "1.62"
		net := "28.38"
		if payload.Amount.Value == "76.00" {
			fee, net = "3.64", "72.36"
		}
		writeTestJSON(w, http.StatusCreated, fmt.Sprintf(`{
			"id":"CAP-%d","status":"COMPLETED","invoice_id":%q,
			"final_capture":%t,"create_time":"2026-08-27T00:00:00Z",
			"amount":{"currency_code":"USD","value":%q},
			"seller_receivable_breakdown":{
				"gross_amount":{"currency_code":"USD","value":%q},
				"paypal_fee":{"currency_code":"USD","value":%q},
				"net_amount":{"currency_code":"USD","value":%q}
			}
		}`, captureNumber, payload.InvoiceID, payload.FinalCapture, payload.Amount.Value,
			payload.Amount.Value, fee, net))
	}))

	first, err := client.CaptureAuthorization(context.Background(), CaptureAuthorizationInput{
		AuthorizationID: "AUTH-1", RequestID: "capture-mo-1", InvoiceID: "MO-1",
		AmountMinor: 3000, Currency: "USD", FinalCapture: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.CaptureAuthorization(context.Background(), CaptureAuthorizationInput{
		AuthorizationID: "AUTH-1", RequestID: "capture-mo-2", InvoiceID: "MO-2",
		AmountMinor: 7600, Currency: "USD", FinalCapture: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "CAP-1" || first.FinalCapture || first.ProcessorFeeMinor != 162 ||
		first.NetReceivableMinor != 2838 || !first.EconomicsReconciled ||
		first.SellerReceivableBreakdown == nil {
		t.Fatalf("unexpected first capture: %+v", first)
	}
	if second.ID != "CAP-2" || !second.FinalCapture || second.ProcessorFeeMinor != 364 ||
		second.NetReceivableMinor != 7236 || !second.EconomicsReconciled {
		t.Fatalf("unexpected second capture: %+v", second)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0] != (capturedRequest{
		RequestID: "capture-mo-1", InvoiceID: "MO-1",
		Amount: Amount{CurrencyCode: "USD", Value: "30.00"}, Final: false,
	}) || requests[1] != (capturedRequest{
		RequestID: "capture-mo-2", InvoiceID: "MO-2",
		Amount: Amount{CurrencyCode: "USD", Value: "76.00"}, Final: true,
	}) {
		t.Fatalf("unexpected capture requests: %+v", requests)
	}
}

func TestVoidAndReauthorizeAuthorization(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/payments/authorizations/AUTH-1/void":
			if r.Method != http.MethodPost || r.Header.Get("PayPal-Request-Id") != "void-1" {
				t.Errorf("unexpected void request: %s %q", r.Method, r.Header.Get("PayPal-Request-Id"))
			}
			w.WriteHeader(http.StatusNoContent)
		case "/v2/payments/authorizations/AUTH-1/reauthorize":
			if r.Method != http.MethodPost || r.Header.Get("PayPal-Request-Id") != "reauthorize-1" {
				t.Errorf("unexpected reauthorize request: %s %q", r.Method, r.Header.Get("PayPal-Request-Id"))
			}
			var payload struct {
				Amount Amount `json:"amount"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode reauthorize body: %v", err)
			}
			if payload.Amount.CurrencyCode != "USD" || payload.Amount.Value != "63.75" {
				t.Errorf("unexpected exact reauthorize amount: %+v", payload.Amount)
			}
			writeTestJSON(w, http.StatusCreated, `{
				"id":"AUTH-2","status":"CREATED",
				"amount":{"currency_code":"USD","value":"63.75"},
				"payee":{"merchant_id":"MERCHANT-1"},
				"supplementary_data":{"related_ids":{"order_id":"PAYPAL-ORDER-1"}},
				"expiration_time":"2026-09-25T00:00:00Z"
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	if err := client.VoidAuthorization(context.Background(), "AUTH-1", "void-1"); err != nil {
		t.Fatal(err)
	}
	authorization, err := client.ReauthorizeAuthorization(
		context.Background(), ReauthorizeAuthorizationInput{
			AuthorizationID: "AUTH-1", RequestID: "reauthorize-1",
			AmountMinor: 6375, Currency: "USD", OrderID: "PAYPAL-ORDER-1",
			PayeeMerchant: "MERCHANT-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.ID != "AUTH-2" || authorization.AmountMinor != 6375 ||
		authorization.Currency != "USD" || authorization.ExpirationTime == "" {
		t.Fatalf("unexpected reauthorization: %+v", authorization)
	}
}

func TestReauthorizeRejectsMismatchedResponseAmount(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusCreated, `{
			"id":"AUTH-2","status":"CREATED",
			"amount":{"currency_code":"USD","value":"63.76"}
		}`)
	}))
	authorization, err := client.ReauthorizeAuthorization(
		context.Background(), ReauthorizeAuthorizationInput{
			AuthorizationID: "AUTH-1", RequestID: "reauthorize-1",
			AmountMinor: 6375, Currency: "USD", OrderID: "PAYPAL-ORDER-1",
			PayeeMerchant: "MERCHANT-1",
		},
	)
	if !errors.Is(err, ErrNonconformingResponse) {
		t.Fatalf("error=%v want nonconforming response", err)
	}
	if authorization.ID != "AUTH-2" || authorization.AmountMinor != 6376 {
		t.Fatalf("provider resource identity was lost: %+v", authorization)
	}
}

func TestReauthorizeRejectsPartiallyCapturedResponse(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusCreated, `{
			"id":"AUTH-2","status":"PARTIALLY_CAPTURED",
			"amount":{"currency_code":"USD","value":"63.75"}
		}`)
	}))
	authorization, err := client.ReauthorizeAuthorization(
		context.Background(), ReauthorizeAuthorizationInput{
			AuthorizationID: "AUTH-1", RequestID: "reauthorize-1",
			AmountMinor: 6375, Currency: "USD", OrderID: "PAYPAL-ORDER-1",
			PayeeMerchant: "MERCHANT-1",
		},
	)
	if !errors.Is(err, ErrNonconformingResponse) {
		t.Fatalf("error=%v want nonconforming response", err)
	}
	if authorization.ID != "AUTH-2" || authorization.Status != "PARTIALLY_CAPTURED" {
		t.Fatalf("provider resource identity/status was lost: %+v", authorization)
	}
}

func TestReauthorizeMalformedSuccessPreservesFreshResourceID(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusCreated, `{
			"id":"AUTH-2","status":"CREATED",
			"amount":{"currency_code":"USD"}
		}`)
	}))
	authorization, err := client.ReauthorizeAuthorization(
		context.Background(), ReauthorizeAuthorizationInput{
			AuthorizationID: "AUTH-1", RequestID: "reauthorize-1",
			AmountMinor: 6375, Currency: "USD", OrderID: "PAYPAL-ORDER-1",
			PayeeMerchant: "MERCHANT-1",
		},
	)
	if !errors.Is(err, ErrNonconformingResponse) {
		t.Fatalf("error=%v want nonconforming response", err)
	}
	if authorization.ID != "AUTH-2" {
		t.Fatalf("fresh provider resource identity was lost: %+v", authorization)
	}
}

func TestReauthorizePreviousRequestInProgressIsUnknown(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusConflict, `{
			"name":"RESOURCE_CONFLICT","debug_id":"debug-1",
			"details":[{"issue":"PREVIOUS_REQUEST_IN_PROGRESS"}]
		}`)
	}))
	_, err := client.ReauthorizeAuthorization(
		context.Background(), ReauthorizeAuthorizationInput{
			AuthorizationID: "AUTH-1", RequestID: "reauthorize-1",
			AmountMinor: 6375, Currency: "USD", OrderID: "PAYPAL-ORDER-1",
			PayeeMerchant: "MERCHANT-1",
		},
	)
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("error=%v want outcome unknown", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.IssueCode != "PREVIOUS_REQUEST_IN_PROGRESS" {
		t.Fatalf("provider issue was not preserved: %v", err)
	}
}

func TestAuthorizeRejectsNonconformingResource(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, http.StatusCreated, `{
			"id":"PAYPAL-ORDER-1","intent":"AUTHORIZE","status":"COMPLETED","purchase_units":[{
				"amount":{"currency_code":"USD","value":"106.00"},
				"payee":{"merchant_id":"MERCHANT-1"},
				"payments":{"authorizations":[{
					"id":"AUTH-1","status":"CREATED",
					"amount":{"currency_code":"USD","value":"105.00"}
				}]}
			}]
		}`)
	}))
	_, err := client.AuthorizeOrder(context.Background(), "PAYPAL-ORDER-1", "authorize-1")
	if !errors.Is(err, ErrNonconformingResponse) {
		t.Fatalf("expected nonconforming response, got %v", err)
	}
}

func TestCaptureWriteFiveHundredIsOutcomeUnknown(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "provider failed", http.StatusInternalServerError)
	}))
	_, err := client.CaptureAuthorization(context.Background(), CaptureAuthorizationInput{
		AuthorizationID: "AUTH-1", RequestID: "capture-mo-1", InvoiceID: "MO-1",
		AmountMinor: 3000, Currency: "USD",
	})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("expected unknown outcome, got %v", err)
	}
}

func TestConcurrentIdempotencyRequestInProgressIsOutcomeUnknown(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, http.StatusConflict, `{
			"name":"RESOURCE_CONFLICT","details":[{
				"issue":"PREVIOUS_REQUEST_IN_PROGRESS"
			}],"debug_id":"DEBUG-1"
		}`)
	}))
	_, err := client.CaptureAuthorization(context.Background(), CaptureAuthorizationInput{
		AuthorizationID: "AUTH-1", RequestID: "capture-mo-1", InvoiceID: "MO-1",
		AmountMinor: 3000, Currency: "USD",
	})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("expected request-in-progress to remain unknown, got %v", err)
	}
}

func TestCaptureMalformedSuccessIsNonconforming(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"CAPTURE-1"`))
	}))
	_, err := client.CaptureAuthorization(context.Background(), CaptureAuthorizationInput{
		AuthorizationID: "AUTH-1", RequestID: "capture-mo-1", InvoiceID: "MO-1",
		AmountMinor: 3000, Currency: "USD",
	})
	if !errors.Is(err, ErrNonconformingResponse) {
		t.Fatalf("expected nonconforming malformed success, got %v", err)
	}
}

func TestAuthorizeEmptySuccessIsNonconforming(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	_, err := client.AuthorizeOrder(context.Background(), "PAYPAL-ORDER-1", "authorize-1")
	if !errors.Is(err, ErrNonconformingResponse) {
		t.Fatalf("expected nonconforming empty success, got %v", err)
	}
}

func TestCaptureRejectsAmountOrInvoiceMismatch(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, http.StatusCreated, `{
			"id":"CAP-WRONG","status":"COMPLETED","invoice_id":"OTHER-MO",
			"final_capture":false,
			"amount":{"currency_code":"USD","value":"29.00"}
		}`)
	}))
	capture, err := client.CaptureAuthorization(context.Background(), CaptureAuthorizationInput{
		AuthorizationID: "AUTH-1", RequestID: "capture-mo-1", InvoiceID: "MO-1",
		AmountMinor: 3000, Currency: "USD",
	})
	if !errors.Is(err, ErrNonconformingResponse) || capture.ID != "CAP-WRONG" {
		t.Fatalf("nonconforming capture identity was lost: capture=%+v err=%v", capture, err)
	}
}

func TestGetOrderRejectsMismatchedResourceIdentity(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, http.StatusOK, `{
			"id":"OTHER-ORDER","intent":"AUTHORIZE","status":"APPROVED",
			"purchase_units":[{
				"amount":{"currency_code":"USD","value":"30.00"},
				"payee":{"merchant_id":"MERCHANT-1"}
			}]
		}`)
	}))
	_, err := client.GetAuthorizedOrder(context.Background(), "EXPECTED-ORDER")
	if !errors.Is(err, ErrNonconformingResponse) {
		t.Fatalf("mismatched GET identity was accepted: %v", err)
	}
}

func TestRefundNonconformingSuccessPreservesResourceForReconciliation(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, http.StatusCreated, `{
			"id":"REFUND-1","status":"COMPLETED",
			"amount":{"currency_code":"USD","value":"not-money"}
		}`)
	}))
	refund, err := client.RefundCapture(
		context.Background(), "CAPTURE-1", "refund-mo-1", 3000,
	)
	if !errors.Is(err, ErrNonconformingResponse) || refund.ID != "REFUND-1" {
		t.Fatalf("refund identity was lost: refund=%+v err=%v", refund, err)
	}
}

func TestRefundCaptureAndGetRequireExactParentAndOperationMetadata(t *testing.T) {
	const response = `{
		"id":"REFUND-1","status":"COMPLETED",
		"amount":{"currency_code":"USD","value":"30.00"},
		"invoice_id":"refund-mo-1","custom_id":"refund-mo-1",
		"links":[{"rel":"up","href":"https://api-m.sandbox.paypal.com/v2/payments/captures/CAPTURE-1"}]
	}`
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var payload struct {
				InvoiceID string `json:"invoice_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.InvoiceID != "refund-mo-1" {
				t.Fatalf("refund operation metadata=%+v", payload)
			}
		}
		writeTestJSON(w, http.StatusCreated, response)
	}))
	refund, err := client.RefundCapture(
		context.Background(), "CAPTURE-1", "refund-mo-1", 3000,
	)
	if err != nil || refund.ParentCaptureID != "CAPTURE-1" ||
		refund.InvoiceID != "refund-mo-1" {
		t.Fatalf("exact refund=%+v err=%v", refund, err)
	}
	refund, err = client.GetRefund(context.Background(), "REFUND-1")
	if err != nil || refund.ParentCaptureID != "CAPTURE-1" {
		t.Fatalf("exact refund GET=%+v err=%v", refund, err)
	}
}

func TestRefundCaptureRejectsWrongParentCapture(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusCreated, `{
			"id":"REFUND-1","status":"COMPLETED",
			"amount":{"currency_code":"USD","value":"30.00"},
			"invoice_id":"refund-mo-1","custom_id":"refund-mo-1",
			"links":[{"rel":"up","href":"https://api-m.sandbox.paypal.com/v2/payments/captures/CAPTURE-OTHER"}]
		}`)
	}))
	refund, err := client.RefundCapture(
		context.Background(), "CAPTURE-1", "refund-mo-1", 3000,
	)
	if !errors.Is(err, ErrNonconformingResponse) || refund.ID != "REFUND-1" {
		t.Fatalf("wrong parent refund=%+v err=%v", refund, err)
	}
}

func TestRefundEmptyIdentitySuccessIsNonconforming(t *testing.T) {
	client := newHTTPTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, http.StatusCreated, `{
			"status":"COMPLETED","amount":{"currency_code":"USD","value":"30.00"}
		}`)
	}))
	_, err := client.RefundCapture(
		context.Background(), "CAPTURE-1", "refund-mo-1", 3000,
	)
	if !errors.Is(err, ErrNonconformingResponse) {
		t.Fatalf("expected missing refund identity to be nonconforming, got %v", err)
	}
}

func newHTTPTestClient(t *testing.T, provider http.Handler) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("token method = %s", r.Method)
		}
		writeTestJSON(w, http.StatusOK, `{"access_token":"TOKEN","expires_in":3600}`)
	})
	mux.Handle("/", provider)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := NewClient(Config{
		BaseURL: server.URL, ClientID: "CLIENT", ClientSecret: "SECRET",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeTestJSON(w http.ResponseWriter, status int, payload string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(payload))
}

func authorizedOrderJSON() string {
	return `{
		"id":"PAYPAL-ORDER-1","intent":"AUTHORIZE","status":"COMPLETED","purchase_units":[{
			"amount":{"currency_code":"USD","value":"106.00"},
			"payee":{"merchant_id":"MERCHANT-1"},
			"payments":{"authorizations":[{
				"id":"AUTH-1","status":"CREATED","invoice_id":"ORDER-INVOICE-1",
				"custom_id":"customer-payment-1",
				"amount":{"currency_code":"USD","value":"106.00"},
				"expiration_time":"2026-09-25T00:00:00Z",
				"create_time":"2026-08-27T00:00:00Z"
			}]}
		}]
	}`
}

func assertAuthorizedOrder(t *testing.T, order Order) {
	t.Helper()
	if order.ID != "PAYPAL-ORDER-1" || order.Intent != "AUTHORIZE" || order.Status != "COMPLETED" ||
		order.PayeeMerchant != "MERCHANT-1" || order.AmountMinor != 10600 ||
		order.Currency != "USD" || len(order.Authorizations) != 1 {
		t.Fatalf("unexpected authorized order: %+v", order)
	}
	authorization := order.Authorizations[0]
	if authorization.ID != "AUTH-1" || authorization.Status != "CREATED" ||
		authorization.AmountMinor != 10600 || authorization.Currency != "USD" ||
		authorization.ExpirationTime == "" {
		t.Fatalf("unexpected authorization: %+v", authorization)
	}
}
