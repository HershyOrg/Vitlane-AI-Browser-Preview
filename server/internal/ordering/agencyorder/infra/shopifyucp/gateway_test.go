package shopifyucp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

type gatewayRoundTrip func(*http.Request) (*http.Response, error)

func (f gatewayRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type gatewayVault struct {
	values map[string]string
}

func (v *gatewayVault) Save(_ context.Context, _, _, _, kind, raw string, _ time.Time) (string, error) {
	safe := "safe:" + kind
	v.values[safe] = raw
	return safe, nil
}
func (v *gatewayVault) Load(_ context.Context, _ string, safe, _ string) (string, error) {
	return v.values[safe], nil
}

func TestGatewayStorefrontDeliveryAndCheckoutPreflightNeverCompletes(t *testing.T) {
	var toolNames []string
	var storefrontCalls int
	var updateArguments map[string]any
	client := &http.Client{Transport: gatewayRoundTrip(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if request.URL.Host == "api.shopify.com" {
			tokenValue := testShopifyJWT(t, time.Now().Add(time.Hour), "checkout:write", "gateway")
			return gatewayResponse(http.StatusOK, `{"access_token":"`+tokenValue+`"}`), nil
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if strings.HasSuffix(request.URL.Path, "/graphql.json") {
			storefrontCalls++
			variables := payload["variables"].(map[string]any)
			switch storefrontCalls {
			case 1:
				return gatewayResponse(http.StatusOK, `{"data":{"cartCreate":{"cart":{"id":"gid://shopify/Cart/cart-1"},"userErrors":[],"warnings":[]}}}`), nil
			case 2:
				address := variables["addresses"].([]any)[0].(map[string]any)["address"].(map[string]any)["deliveryAddress"].(map[string]any)
				if address["address1"] != "131 Greene Street" || address["zip"] != "10012" || address["countryCode"] != "US" {
					t.Fatalf("full address missing: %#v", address)
				}
				return gatewayResponse(http.StatusOK, storefrontCartBody("")), nil
			case 3:
				selection := variables["selections"].([]any)[0].(map[string]any)
				if selection["deliveryGroupId"] != "group-1" || selection["deliveryOptionHandle"] != "ground" {
					t.Fatalf("selection=%#v", selection)
				}
				return gatewayResponse(http.StatusOK, storefrontCartBody("ground")), nil
			default:
				t.Fatalf("unexpected storefront call %d", storefrontCalls)
			}
		}
		params := payload["params"].(map[string]any)
		name := params["name"].(string)
		toolNames = append(toolNames, name)
		arguments := params["arguments"].(map[string]any)
		if request.Header.Get("Shopify-Buyer-IP") != "203.0.113.9" {
			t.Fatalf("buyer IP missing for %s", name)
		}
		switch name {
		case "create_checkout":
			checkout, ok := arguments["checkout"].(map[string]any)
			if !ok || checkout["cart_id"] != "gid://shopify/Cart/cart-1" {
				t.Fatalf("cart handoff=%#v", arguments)
			}
			lineItems, _ := checkout["line_items"].([]any)
			fulfillment, _ := checkout["fulfillment"].(map[string]any)
			methods, _ := fulfillment["methods"].([]any)
			if len(lineItems) != 1 || len(methods) != 1 || arguments["cart_id"] != nil {
				t.Fatalf("create checkout contract=%#v", arguments)
			}
			return gatewayResponse(http.StatusOK, ucpBody(`{"id":"gid://shopify/Checkout/checkout-1","status":"incomplete","line_items":[{"id":"line-provider-1","item":{"id":"gid://shopify/ProductVariant/1"},"quantity":1}],"fulfillment":{"methods":[{"id":"ground","type":"shipping","line_item_ids":["line-provider-1"],"groups":[{"id":"group-1","line_item_ids":["line-provider-1"],"selected_option_id":"ground"}]}]},"totals":[{"type":"total","amount":3200}],"messages":[]}`)), nil
		case "update_checkout":
			updateArguments = arguments
			return gatewayResponse(http.StatusOK, ucpBody(readyCheckoutJSON())), nil
		case "get_checkout":
			return gatewayResponse(http.StatusOK, ucpBody(readyCheckoutJSON())), nil
		case "cancel_checkout":
			return gatewayResponse(http.StatusOK, ucpBody(`{"status":"canceled"}`)), nil
		default:
			t.Fatalf("prohibited or unknown tool %q", name)
		}
		return nil, nil
	})}
	tokens, err := NewTokenSource(client, "https://api.shopify.com/auth/access_token", "client", "secret")
	if err != nil {
		t.Fatal(err)
	}
	vault := &gatewayVault{values: map[string]string{}}
	gateway, err := NewGateway(GatewayConfig{
		HTTPClient: client, Tokens: tokens, Vault: vault,
		AgentProfileURL:     "https://app.vitlane.test/.well-known/ucp-agent.json",
		AgentProfileVersion: "2026-04-08", AgentProfileHash: "profile-hash",
		StorefrontAPIVersion: "2026-07",
	})
	if err != nil {
		t.Fatal(err)
	}
	input := agencyapp.MerchantRequest{
		OrderSheetSessionID: "sheet-1", UserID: "user-1", ShopDomain: "shop.example",
		BuyerIP:         net.ParseIP("203.0.113.9"),
		Lines:           []agencydomain.ExactLine{{LineID: "line-1", VariantID: "gid://shopify/ProductVariant/1", Quantity: 1}},
		ShippingAddress: agencyapp.ShippingAddress{RecipientName: "Jane Smith", AddressLine1: "131 Greene Street", City: "New York", Region: "NY", PostalCode: "10012", Country: "US", Phone: "+12025550123"},
	}
	explored, err := gateway.Explore(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if explored.DeliverySelectionComplete() || len(explored.DeliveryGroups) != 1 || len(explored.DeliveryGroups[0].Options) != 2 ||
		explored.QuoteReadiness != agencydomain.PricingEstimated ||
		explored.ProcurementHandling != agencydomain.ProcurementHandlingNormal {
		t.Fatalf("explored=%#v", explored)
	}
	input.Existing = &explored
	selected, err := gateway.SelectDelivery(context.Background(), input, map[string]string{"group-1": "ground"})
	if err != nil || !selected.DeliverySelectionComplete() {
		t.Fatalf("selected=%#v err=%v", selected, err)
	}
	input.Existing = &selected
	preflight, err := gateway.Preflight(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.ProviderStatus != "ready_for_complete" || preflight.TaxTotal.AmountMinor != 244 ||
		preflight.AuthoritativeTotal.AmountMinor != 3244 || preflight.QuoteReadiness != agencydomain.PricingConfirmed ||
		preflight.ProcurementHandling != agencydomain.ProcurementHandlingNormal ||
		preflight.ContinueURLSafeRef != "safe:UCP_CONTINUE_URL" || preflight.QuoteFingerprint == "" {
		t.Fatalf("preflight=%#v", preflight)
	}
	if !preflight.DeliverySelectionComplete() ||
		preflight.DeliveryGroups[0].SelectedOptionRef != "ground" {
		t.Fatalf("preflight lost Storefront delivery selection: %#v", preflight.DeliveryGroups)
	}
	if preflight.DutiesDisposition != agencydomain.DutiesNoSignalAtPreflight ||
		preflight.CompletionRoute != agencydomain.CompletionManualHandoff ||
		preflight.AuthEvidence.CompletionPermission != agencydomain.CompletionNotRequested {
		t.Fatalf("Step 2 handoff evidence=%#v", preflight)
	}
	input.Existing = &preflight
	final, err := gateway.FinalGet(context.Background(), input)
	if err != nil || final.QuoteFingerprint != preflight.QuoteFingerprint ||
		final.EvidenceHash != preflight.EvidenceHash || final.ContinueURLSafeRef != "safe:UCP_CONTINUE_URL" {
		t.Fatalf("final=%#v err=%v", final, err)
	}
	checkout := updateArguments["checkout"].(map[string]any)
	method := checkout["fulfillment"].(map[string]any)["methods"].([]any)[0].(map[string]any)
	group := method["groups"].([]any)[0].(map[string]any)
	destination := method["destinations"].([]any)[0].(map[string]any)
	if method["id"] != "ground" || destination["street_address"] != "131 Greene Street" ||
		destination["address_country"] != "US" || destination["phone_number"] != "+12025550123" ||
		group["selected_option_id"] != "ground" {
		t.Fatalf("update checkout=%#v", checkout)
	}
	input.Existing = &final
	if err := gateway.Cancel(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	for _, name := range toolNames {
		if name == "complete_checkout" || name == "cancel_cart" {
			t.Fatalf("prohibited tool called: %s", name)
		}
	}
}

func TestSummarizeTotalsPreservesAutomaticDiscountAndRejectsMismatch(t *testing.T) {
	totals := []agencydomain.Total{
		{Type: "subtotal", AmountMinor: 2000},
		{Type: "discount", AmountMinor: -200},
		{Type: "fulfillment", AmountMinor: 300},
		{Type: "tax", AmountMinor: 150},
		{Type: "total", AmountMinor: 2250},
	}
	total, tax, err := summarizeTotals(totals)
	if err != nil || total != 2250 || tax != 150 {
		t.Fatalf("automatic discount was not preserved: total=%d tax=%d err=%v", total, tax, err)
	}
	totals[len(totals)-1].AmountMinor++
	if _, _, err := summarizeTotals(totals); err == nil {
		t.Fatal("mismatched authoritative total was accepted")
	}
}

func TestSummarizeTotalsAcceptsConsistentZeroTaxQuote(t *testing.T) {
	// ADR-0047 2026-08-16 개정: nexus 없는 원격 판매자는 tax 항목 없이 견적을
	// 확정한다. 구성요소 합 == total일 때만 tax $0으로 수용한다.
	totals := []agencydomain.Total{
		{Type: "subtotal", AmountMinor: 2000},
		{Type: "fulfillment", AmountMinor: 300},
		{Type: "total", AmountMinor: 2300},
	}
	if total, tax, err := summarizeTotals(totals); err != nil || total != 2300 || tax != 0 {
		t.Fatalf("consistent zero-tax quote was rejected: total=%d tax=%d err=%v", total, tax, err)
	}
	// 합이 어긋나는 tax 부재는 미계산 항목이 있다는 뜻이므로 계속 거절한다.
	inconsistent := []agencydomain.Total{
		{Type: "subtotal", AmountMinor: 2000},
		{Type: "fulfillment", AmountMinor: 300},
		{Type: "total", AmountMinor: 2450},
	}
	if _, _, err := summarizeTotals(inconsistent); err == nil {
		t.Fatal("inconsistent tax-free totals were accepted")
	}
	totals = append(totals[:2], agencydomain.Total{Type: "tax", AmountMinor: 0}, totals[2])
	if total, tax, err := summarizeTotals(totals); err != nil || total != 2300 || tax != 0 {
		t.Fatalf("explicit zero tax should remain authoritative: total=%d tax=%d err=%v", total, tax, err)
	}
}

func TestUnknownProviderMessageBecomesReadOnlyOperatorObservation(t *testing.T) {
	raw := []any{map[string]any{"type": "error", "severity": "requires_buyer_input", "code": "future_shopify_code", "content": "buyer review required"}}
	descriptors := messageDescriptors(raw)
	encoded, err := json.Marshal(descriptors)
	if err != nil || strings.Contains(string(encoded), "buyer review required") {
		t.Fatalf("free-form content leaked into descriptor: %s err=%v", encoded, err)
	}
	notices := providerNoticeSnapshots(raw)
	if got := procurementHandling("requires_escalation", descriptors); got != agencydomain.ProcurementHandlingOperatorLater ||
		len(notices) != 1 || notices[0].Registered || notices[0].Audience != "OPERATOR" {
		t.Fatalf("handling=%s notices=%#v", got, notices)
	}
}

func TestIsErrorCheckoutResourceAllowsUnregisteredTypedCode(t *testing.T) {
	checkout := map[string]any{
		"id":     "gid://shopify/Checkout/unknown-code",
		"status": "requires_escalation",
		"messages": []any{map[string]any{
			"type": "error", "severity": "requires_buyer_input", "code": "future_shopify_code",
		}},
	}
	if !recoverableCheckoutResource(checkout) {
		t.Fatal("valid checkout resource with an unregistered typed code was rejected")
	}
	delete(checkout, "id")
	if recoverableCheckoutResource(checkout) {
		t.Fatal("checkout resource without identity was accepted")
	}
}

func TestProviderPresentationEvidenceIsSanitizedAndStructured(t *testing.T) {
	checkout := map[string]any{
		"messages": []any{map[string]any{
			"type": "notice", "severity": "requires_buyer_review",
			"code": "review_return_policy", "message": "Review returns before purchase.",
		}},
		"policies": []any{
			map[string]any{"type": "return", "label": "Returns", "url": "https://shop.example/policies/returns"},
			map[string]any{"type": "privacy", "label": "Off-site", "url": "https://evil.example/privacy"},
		},
	}
	notices := providerNoticeSnapshots(checkout["messages"])
	links := providerPolicyLinks("shop.example", checkout)
	if len(notices) != 1 || notices[0].Text != "Review returns before purchase." ||
		notices[0].Presentation != "NOTICE" || notices[0].Audience != "CUSTOMER_AND_OPERATOR" ||
		len(links) != 1 || links[0].Kind != "RETURN" {
		t.Fatalf("notices=%#v links=%#v", notices, links)
	}
}

func TestInformationalNoticeDoesNotRequestOperatorFollowUp(t *testing.T) {
	descriptors := messageDescriptors([]any{map[string]any{
		"type": "warning", "message": "Merchant informational notice.",
	}})
	if got := procurementHandling("ready_for_complete", descriptors); got != agencydomain.ProcurementHandlingNormal {
		t.Fatalf("informational notice handling=%s", got)
	}
	if steps := manualSiteStepsForDescriptors(descriptors); len(steps) != 0 {
		t.Fatalf("informational notice created manual steps: %#v", steps)
	}
}

func TestUnspecifiedRequirementArraysAreReadOnlyAndNeverBecomeAnswers(t *testing.T) {
	var checkout map[string]any
	if err := json.Unmarshal([]byte(readyCheckoutJSON()), &checkout); err != nil {
		t.Fatal(err)
	}
	checkout["additional_requirements"] = []any{
		map[string]any{
			"id": "engraving", "type": "buyer_input", "prompt": "Engraving",
			"response_type": "text", "required": false,
		},
		map[string]any{
			"id": "merchant-secret", "type": "buyer_input", "prompt": "Enter merchant password",
			"response_type": "password", "required": true,
		},
	}
	existing := agencydomain.MerchantCheckout{
		StorefrontCartSafeRef: "safe:STOREFRONT_CART",
		BuyerContextSafeRef:   "safe:BUYER_CONTEXT", LineRefs: []string{"line-1"},
		DeliveryGroups: []agencydomain.DeliveryGroup{{
			ID: "group-1", LineRefs: []string{"line-1"},
			Options:           []agencydomain.DeliveryOption{{ID: "ground", Currency: "USD"}},
			SelectedOptionRef: "ground",
		}},
	}
	gateway := &Gateway{config: GatewayConfig{
		Tokens: &TokenSource{}, AgentProfileURL: "https://app.vitlane.test/.well-known/ucp-agent.json",
		AgentProfileVersion: "2026-04-08", AgentProfileHash: "profile-hash",
	}}
	result, err := gateway.mapCheckout(agencyapp.MerchantRequest{
		ShopDomain: "shop.example", Existing: &existing,
		Lines: []agencydomain.ExactLine{{
			LineID: "line-1", VariantID: "gid://shopify/ProductVariant/1", Quantity: 1,
		}},
	}, checkout, "safe:UCP_CHECKOUT", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if result.QuoteReadiness != agencydomain.PricingConfirmed ||
		result.ProcurementHandling != agencydomain.ProcurementHandlingNormal {
		t.Fatalf("unsupported arrays affected quote behavior: %#v", result)
	}
	if len(result.ProviderNotices) != 2 || result.ProviderNotices[0].Source != "REQUIREMENT" ||
		result.ProviderNotices[0].Audience != "OPERATOR" {
		t.Fatalf("requirement observations=%#v", result.ProviderNotices)
	}
	if len(result.ManualSiteSteps) != 2 || result.ManualSiteSteps[0].Kind != "CREDENTIAL_AT_MERCHANT_SITE" {
		t.Fatalf("requirement steps=%#v", result.ManualSiteSteps)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "additionalRequirementAnswers") || strings.Contains(string(encoded), "\"answer\"") {
		t.Fatalf("provider requirement became an answer contract: %s", encoded)
	}
}

func TestTypedBuyerInputDoesNotChangeConfirmedQuote(t *testing.T) {
	descriptors := messageDescriptors([]any{
		map[string]any{"type": "error", "severity": "recoverable", "code": "delivery_phone_number_required"},
		map[string]any{"type": "error", "severity": "requires_buyer_input", "code": "future_buyer_input"},
	})
	if handling := procurementHandling("requires_escalation", descriptors); handling != agencydomain.ProcurementHandlingOperatorLater {
		t.Fatalf("handling=%s", handling)
	}
	if readiness := quoteReadiness(true, nil, nil, nil); readiness != agencydomain.PricingConfirmed {
		t.Fatalf("readiness=%s", readiness)
	}
}

func TestExtensionInteractionBuyerInputConfirmsPriceEvidence(t *testing.T) {
	// ADR-0049: extension_interaction_required는 운영자 수동 인계로 해소되는
	// Shop 특성 신호이므로 escalation 축만 남기고 가격 축은 증거로 판정한다.
	descriptors := messageDescriptors([]any{map[string]any{
		"type": "error", "severity": "requires_buyer_input",
		"code": "extension_interaction_required", "path": []any{"checkout"},
	}})
	if handling := procurementHandling("requires_escalation", descriptors); handling != agencydomain.ProcurementHandlingOperatorLater {
		t.Fatalf("handling=%s", handling)
	}
	if readiness := quoteReadiness(true, nil, nil, nil); readiness != agencydomain.PricingConfirmed {
		t.Fatalf("readiness=%s", readiness)
	}
	// 가격 증거가 무너지면(불안정 fingerprint) 해소 가능 신호라도 UNSAFE다.
	if readiness := quoteReadiness(false, nil, nil, nil); readiness != agencydomain.PricingUnsafe {
		t.Fatalf("unstable readiness=%s", readiness)
	}
	// CAPTCHA/identity verification은 운영자가 merchant site에서 직접
	// 처리하므로 자동화 blocker가 아니라 명시적 MANUAL_SITE_STEP이다.
	mixed := messageDescriptors([]any{
		map[string]any{"type": "error", "severity": "requires_buyer_input", "code": "extension_interaction_required"},
		map[string]any{"type": "error", "severity": "requires_buyer_input", "code": "identity_verification_required"},
	})
	if len(manualSiteStepsForDescriptors(mixed)) != 2 {
		t.Fatal("recognized manual-site steps were treated as automation blockers")
	}
}

func TestCustomerCorrectionAndImpossibleCodesHaveExactHandling(t *testing.T) {
	for code, want := range map[string]string{
		"address_undeliverable": agencydomain.ProcurementHandlingCustomerCorrection,
		"out_of_stock":          agencydomain.ProcurementHandlingImpossible,
	} {
		descriptors := messageDescriptors([]any{map[string]any{
			"type": "error", "severity": "requires_buyer_input", "code": code,
		}})
		if got := procurementHandling("requires_escalation", descriptors); got != want {
			t.Fatalf("code=%s handling=%s want=%s", code, got, want)
		}
	}
}

func TestPricingFingerprintIgnoresRotatingHandoffMetadata(t *testing.T) {
	var first, second map[string]any
	if err := json.Unmarshal([]byte(readyCheckoutJSON()), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(readyCheckoutJSON()), &second); err != nil {
		t.Fatal(err)
	}
	second["expires_at"] = "2026-08-16T02:00:00Z"
	second["continue_url"] = "https://shop.example/checkouts/continue?key=rotated-token"
	second["status"] = "requires_escalation"
	second["messages"] = []any{map[string]any{"type": "error", "severity": "requires_buyer_input", "code": "future_code"}}
	if checkoutPricingFingerprint(first) != checkoutPricingFingerprint(second) {
		t.Fatal("rotating expiry/continue metadata changed the price fingerprint")
	}
	second["totals"].([]any)[3].(map[string]any)["amount"] = float64(3245)
	if checkoutPricingFingerprint(first) == checkoutPricingFingerprint(second) {
		t.Fatal("authoritative total change did not change the price fingerprint")
	}
}

func TestMapCheckoutTreatsContinueURLAsOptionalReference(t *testing.T) {
	var checkout map[string]any
	if err := json.Unmarshal([]byte(readyCheckoutJSON()), &checkout); err != nil {
		t.Fatal(err)
	}
	existing := agencydomain.MerchantCheckout{
		StorefrontCartSafeRef: "safe:STOREFRONT_CART",
		BuyerContextSafeRef:   "safe:BUYER_CONTEXT", LineRefs: []string{"line-1"},
		DeliveryGroups: []agencydomain.DeliveryGroup{{
			ID: "group-1", LineRefs: []string{"line-1"},
			Options:           []agencydomain.DeliveryOption{{ID: "ground", Currency: "USD"}},
			SelectedOptionRef: "ground",
		}},
	}
	gateway := &Gateway{config: GatewayConfig{
		Tokens: &TokenSource{}, AgentProfileURL: "https://app.vitlane.test/.well-known/ucp-agent.json",
		AgentProfileVersion: "2026-04-08", AgentProfileHash: "profile-hash",
	}}
	result, err := gateway.mapCheckout(agencyapp.MerchantRequest{
		ShopDomain: "shop.example", Existing: &existing,
		Lines: []agencydomain.ExactLine{{
			LineID: "line-1", VariantID: "gid://shopify/ProductVariant/1", Quantity: 1,
		}},
	}, checkout, "safe:UCP_CHECKOUT", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProcurementHandling != agencydomain.ProcurementHandlingNormal ||
		result.ContinueURLSafeRef != "" || result.ContinueURLHash != "" {
		t.Fatalf("optional continue URL changed readiness: %#v", result)
	}
}

func TestDutiesDispositionUsesOnlyActualPriceComponents(t *testing.T) {
	if got := dutiesDisposition([]agencydomain.Total{{Type: "duty", AmountMinor: 125}}); got != "CHARGED_OR_CROSS_BORDER" {
		t.Fatalf("positive duty disposition=%q", got)
	}
	if got := dutiesDisposition([]agencydomain.Total{{Type: "duties", AmountMinor: 0}}); got != agencydomain.DutiesNoSignalAtPreflight {
		t.Fatalf("explicit zero duty disposition=%q", got)
	}
}

func TestCallAcceptsStructuredOrTextJSONAndFailClosesIsError(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantReason string
	}{
		{name: "structured", body: `{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"checkout":{"status":"ready_for_complete"}}}}`},
		{name: "text fallback", body: `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"checkout\":{\"status\":\"ready_for_complete\"}}"}]}}`},
		{name: "provider tool error", body: `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"structuredContent":{"checkout":{"status":"ready_for_complete"}},"content":[{"type":"text","text":"{\"checkout\":{}}"}]}}`, wantReason: "SHOPIFY_UCP_RESPONSE_INVALID"},
		{name: "provider escalation", body: `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"structuredContent":{"checkout":{"status":"requires_escalation"}},"content":[{"type":"text","text":"{\"checkout\":{}}"}]}}`, wantReason: "SHOPIFY_CHECKOUT_REQUIRES_ESCALATION"},
		{name: "malformed text", body: `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"not-json"}]}}`, wantReason: "SHOPIFY_UCP_RESPONSE_INVALID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: gatewayRoundTrip(func(*http.Request) (*http.Response, error) {
				return gatewayResponse(http.StatusOK, test.body), nil
			})}
			gateway := &Gateway{config: GatewayConfig{
				HTTPClient: client, AgentProfileURL: "https://app.vitlane.test/.well-known/ucp-agent.json",
			}}
			result, _, err := gateway.call(
				context.Background(),
				agencyapp.MerchantRequest{ShopDomain: "shop.example", BuyerIP: net.ParseIP("203.0.113.9")},
				"get_checkout", map[string]any{"id": "checkout-1"}, "token", sharedhttpclient.ReadOnly,
			)
			if test.wantReason != "" {
				if err == nil {
					t.Fatalf("result=%#v", result)
				}
				failure, ok := fault.As(err)
				if !ok || failure.Reason != test.wantReason {
					t.Fatalf("reason=%#v err=%v", failure, err)
				}
				if test.name != "malformed text" && result == nil {
					t.Fatal("structured error payload was discarded")
				}
				return
			}
			if err != nil || stringValue(nestedObject(result, "checkout")["status"]) != "ready_for_complete" {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestPreflightAcceptsStableBuyerReviewQuoteAndPreservesManualHandoff(t *testing.T) {
	cancelCalls := 0
	checkoutCalls := 0
	tokenValue := testShopifyJWT(t, time.Now().Add(time.Hour), "checkout", "escalation")
	client := &http.Client{Transport: gatewayRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "api.shopify.com" {
			return gatewayResponse(http.StatusOK, `{"access_token":"`+tokenValue+`"}`), nil
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		params := payload["params"].(map[string]any)
		arguments := params["arguments"].(map[string]any)
		switch params["name"] {
		case "create_checkout":
			checkoutCalls++
			checkout := arguments["checkout"].(map[string]any)
			if checkout["cart_id"] != "gid://shopify/Cart/cart-1" || arguments["cart_id"] != nil {
				t.Fatalf("create arguments=%#v", arguments)
			}
			return gatewayResponse(http.StatusOK, ucpErrorBody(reviewCheckoutJSON())), nil
		case "update_checkout", "get_checkout":
			checkoutCalls++
			return gatewayResponse(http.StatusOK, ucpErrorBody(reviewCheckoutJSON())), nil
		case "cancel_checkout":
			cancelCalls++
			if arguments["id"] != "gid://shopify/Checkout/checkout-1" {
				t.Fatalf("cancel arguments=%#v", arguments)
			}
			return gatewayResponse(http.StatusOK, ucpBody(`{"status":"canceled"}`)), nil
		default:
			t.Fatalf("unexpected tool=%v", params["name"])
		}
		return nil, nil
	})}
	tokens, err := NewTokenSource(client, "https://api.shopify.com/auth/access_token", "client", "secret")
	if err != nil {
		t.Fatal(err)
	}
	vault := &gatewayVault{values: map[string]string{
		"safe:STOREFRONT_CART": "gid://shopify/Cart/cart-1",
	}}
	gateway, err := NewGateway(GatewayConfig{
		HTTPClient: client, Tokens: tokens, Vault: vault,
		AgentProfileURL:     "https://app.vitlane.test/.well-known/ucp-agent.json",
		AgentProfileVersion: "2026-04-08", AgentProfileHash: "profile-hash",
		StorefrontAPIVersion: "2026-07",
	})
	if err != nil {
		t.Fatal(err)
	}
	existing := agencydomain.MerchantCheckout{
		StorefrontCartSafeRef: "safe:STOREFRONT_CART",
		BuyerContextSafeRef:   "safe:BUYER_CONTEXT",
		LineRefs:              []string{"line-1"},
		DeliveryGroups: []agencydomain.DeliveryGroup{{
			ID: "group-1", SelectedOptionRef: "ground",
		}},
	}
	input := agencyapp.MerchantRequest{
		OrderSheetSessionID: "sheet-1", UserID: "user-1", ShopDomain: "shop.example",
		BuyerIP: net.ParseIP("203.0.113.9"), Existing: &existing,
		Lines:           []agencydomain.ExactLine{{LineID: "line-1", VariantID: "gid://shopify/ProductVariant/1", Quantity: 1}},
		ShippingAddress: agencyapp.ShippingAddress{RecipientName: "Jane Smith", AddressLine1: "131 Greene Street", City: "New York", Region: "NY", PostalCode: "10012", Country: "US"},
	}
	preflight, err := gateway.Preflight(context.Background(), input)
	if err != nil || preflight.ProviderStatus != "requires_escalation" ||
		preflight.QuoteReadiness != agencydomain.PricingConfirmed ||
		preflight.ProcurementHandling != agencydomain.ProcurementHandlingOperatorLater ||
		len(preflight.ProviderNotices) != 2 || len(preflight.ManualSiteSteps) != 2 ||
		preflight.CheckoutSessionSafeRef != "safe:UCP_CHECKOUT" ||
		preflight.ContinueURLSafeRef != "safe:UCP_CONTINUE_URL" {
		t.Fatalf("preflight=%#v err=%v", preflight, err)
	}
	if checkoutCalls != 4 {
		t.Fatalf("create/update/get/get calls=%d", checkoutCalls)
	}
	encoded, err := json.Marshal(preflight)
	if err != nil || strings.Contains(string(encoded), "secret-token") ||
		!strings.Contains(string(encoded), "Operator must review the purchase") {
		t.Fatalf("unsafe provider data leaked: %s err=%v", encoded, err)
	}
	if vault.values["safe:UCP_CONTINUE_URL"] != "https://shop.example/checkouts/continue?key=secret-token" {
		t.Fatalf("continue capability not vaulted: %#v", vault.values)
	}
	input.Existing = &preflight
	if err := gateway.Cancel(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if cancelCalls != 1 {
		t.Fatalf("cancel calls=%d", cancelCalls)
	}
}

func storefrontCartBody(selected string) string {
	selectedJSON := "null"
	if selected != "" {
		selectedJSON = `{"handle":"ground","title":"Ground","estimatedCost":{"amount":"5.00","currencyCode":"USD"}}`
	}
	return `{"data":{"cartDeliveryAddressesAdd":{"cart":{"id":"gid://shopify/Cart/cart-1","deliveryGroups":{"nodes":[{"id":"group-1","cartLines":{"nodes":[{"merchandise":{"id":"gid://shopify/ProductVariant/1"}}]},"deliveryOptions":[{"handle":"ground","title":"Ground","estimatedCost":{"amount":"5.00","currencyCode":"USD"}},{"handle":"express","title":"Express","estimatedCost":{"amount":"12.00","currencyCode":"USD"}}],"selectedDeliveryOption":` + selectedJSON + `}]}},"userErrors":[],"warnings":[]},"cartSelectedDeliveryOptionsUpdate":{"cart":{"id":"gid://shopify/Cart/cart-1","deliveryGroups":{"nodes":[{"id":"group-1","cartLines":{"nodes":[{"merchandise":{"id":"gid://shopify/ProductVariant/1"}}]},"deliveryOptions":[{"handle":"ground","title":"Ground","estimatedCost":{"amount":"5.00","currencyCode":"USD"}},{"handle":"express","title":"Express","estimatedCost":{"amount":"12.00","currencyCode":"USD"}}],"selectedDeliveryOption":` + selectedJSON + `}]}},"userErrors":[],"warnings":[]}}}`
}

func readyCheckoutJSON() string {
	return `{"id":"gid://shopify/Checkout/checkout-1","status":"ready_for_complete","currency":"USD","continue_url":"https://shop.example/checkouts/continue?key=secret-token","line_items":[{"id":"line-provider-1","item":{"id":"gid://shopify/ProductVariant/1"},"quantity":1}],"fulfillment":{"methods":[{"id":"ground","type":"shipping","line_item_ids":["line-provider-1"],"groups":[{"id":"group-1","line_item_ids":["line-provider-1"],"selected_option_id":"ground"}]}]},"totals":[{"type":"subtotal","amount":2500},{"type":"fulfillment","amount":500},{"type":"tax","amount":244},{"type":"total","amount":3244}],"messages":[],"expires_at":"2026-08-16T01:00:00Z"}`
}

func reviewCheckoutJSON() string {
	return `{"id":"gid://shopify/Checkout/checkout-1","status":"requires_escalation","currency":"USD","continue_url":"https://shop.example/checkouts/continue?key=secret-token","line_items":[{"id":"line-provider-1","item":{"id":"gid://shopify/ProductVariant/1"},"quantity":1}],"fulfillment":{"methods":[{"id":"ground","type":"shipping","line_item_ids":["line-provider-1"],"groups":[{"id":"group-1","line_item_ids":["line-provider-1"],"selected_option_id":"ground"}]}]},"totals":[{"type":"subtotal","amount":2500},{"type":"fulfillment","amount":500},{"type":"tax","amount":244},{"type":"total","amount":3244}],"messages":[{"type":"error","severity":"requires_buyer_input","code":"extension_interaction_required","path":["checkout"],"content":"Operator must review the purchase"},{"type":"error","severity":"requires_buyer_input","code":"redirect_to_checkout_required","path":["checkout"]}],"expires_at":"2026-08-16T01:00:00Z"}`
}

func ucpBody(structured string) string {
	return `{"jsonrpc":"2.0","id":1,"result":{"structuredContent":` + structured + `}}`
}

func ucpErrorBody(structured string) string {
	return `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"structuredContent":{"checkout":` + structured + `}}}`
}

func gatewayResponse(status int, body string) *http.Response {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

// buyer_identity는 운영(대행) 연락 이메일만 싣는다(운영정합 3차 소유자 결정) —
// contact-required 정책 shop의 buyer_identity_contact_method_required 해소.
// 미설정이면 기존과 동일하게 미전송이고, 고객 연락처는 destination에 한정된다.
func TestCheckoutUpdatePayloadBuyerIdentity(t *testing.T) {
	input := agencyapp.MerchantRequest{
		Lines: []agencydomain.ExactLine{{LineID: "line-1", VariantID: "gid://shopify/ProductVariant/1", Quantity: 1}},
		ShippingAddress: agencyapp.ShippingAddress{
			RecipientName: "Jane Smith", AddressLine1: "131 Greene Street",
			City: "New York", Region: "NY", PostalCode: "10012", Country: "US",
			Phone: "+12025550123",
		},
	}
	withEmail := checkoutUpdatePayload(map[string]any{}, input, " ops@vitlane.com ")
	buyer, ok := withEmail["buyer"].(map[string]any)
	if !ok || buyer["email"] != "ops@vitlane.com" || len(buyer) != 1 {
		t.Fatalf("buyer=%#v", withEmail["buyer"])
	}
	without := checkoutUpdatePayload(map[string]any{}, input, "")
	if _, exists := without["buyer"]; exists {
		t.Fatalf("buyer must be omitted when unset: %#v", without["buyer"])
	}
	// 고객 배달 연락처(destination.phone_number)는 buyer_identity와 무관하게 유지.
	methods := withEmail["fulfillment"].(map[string]any)["methods"].([]map[string]any)
	destination := methods[0]["destinations"].([]any)[0].(map[string]any)
	if destination["phone_number"] != "+12025550123" {
		t.Fatalf("destination=%#v", destination)
	}
}

// typed isError 응답은 "형태 오류"로 뭉개지 않고 코드를 reason으로 승격한다.
func TestTypedErrorReasonPromotesShopCode(t *testing.T) {
	structured := map[string]any{"checkout": map[string]any{
		"id": "chk-1", "status": "error",
		"messages": []any{map[string]any{
			"type": "error", "code": "buyer_identity_contact_method_required",
			"content": "Missing a valid contact method.", "severity": "recoverable",
		}},
	}}
	if reason := typedErrorReason(structured); reason != "SHOPIFY_CHECKOUT_BUYER_IDENTITY_CONTACT_METHOD_REQUIRED" {
		t.Fatalf("reason=%q", reason)
	}
	if reason := typedErrorReason(map[string]any{"messages": []any{map[string]any{"type": "warning", "code": "x"}}}); reason != "" {
		t.Fatalf("non-error promoted: %q", reason)
	}
	// provider 문자열은 무검증으로 싣지 않는다 — 허용 밖 문자는 걸러진다.
	if reason := typedErrorReason(map[string]any{"messages": []any{map[string]any{
		"type": "error", "code": "Weird-Code! DROP TABLE",
	}}}); reason != "SHOPIFY_CHECKOUT_WEIRDCODE_DROPTABLE" && reason != "SHOPIFY_CHECKOUT_WEIRDCODEDROPTABLE" {
		t.Fatalf("sanitized=%q", reason)
	}
}
