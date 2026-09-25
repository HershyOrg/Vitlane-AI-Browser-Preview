package shopifyucp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

type liveProbeTransport struct {
	base        http.RoundTripper
	mu          sync.Mutex
	host        string
	path        string
	status      int
	code        string
	message     string
	contentType string
	shape       string
}

func (t *liveProbeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if response != nil {
		code, message, shape := "", "", "body_unavailable"
		if response.Body != nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			if readErr == nil {
				response.Body.Close()
				response.Body = io.NopCloser(strings.NewReader(string(body)))
				shape = safeMCPResponseShape(body)
				var envelope struct {
					Error struct {
						Code    any    `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if response.StatusCode >= 400 && json.Unmarshal(body, &envelope) == nil {
					code, message = fmt.Sprint(envelope.Error.Code), strings.TrimSpace(envelope.Error.Message)
				}
			}
		}
		t.mu.Lock()
		t.host, t.path, t.status, t.code, t.message = request.URL.Host, request.URL.Path, response.StatusCode, code, message
		t.contentType, t.shape = response.Header.Get("Content-Type"), shape
		t.mu.Unlock()
	}
	return response, err
}

func (t *liveProbeTransport) last() (string, string, int, string, string, string, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.host, t.path, t.status, t.code, t.message, t.contentType, t.shape
}

func safeMCPResponseShape(body []byte) string {
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "non_json"
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		return fmt.Sprintf("result=%T error=%T", envelope["result"], envelope["error"])
	}
	content, _ := result["content"].([]any)
	firstContentType, firstTextJSONType, firstTextSignals := "missing", "missing", "none"
	if len(content) > 0 {
		first, _ := content[0].(map[string]any)
		firstContentType = fmt.Sprint(first["type"])
		if text, ok := first["text"].(string); ok {
			var decoded any
			if json.Unmarshal([]byte(text), &decoded) == nil {
				firstTextJSONType = fmt.Sprintf("%T", decoded)
			} else {
				firstTextJSONType = "non_json"
			}
			lower := strings.ToLower(text)
			signals := make([]string, 0)
			for _, signal := range []string{
				"accepting", "address", "agent profile", "allowed", "browser", "buyer ip", "capability", "cart", "checkout",
				"closed", "continue", "create", "currency", "disabled", "discovery", "eligible", "error", "escalation",
				"failed", "fulfillment", "human", "initiate", "issue", "line item", "merchant", "occurred", "order",
				"permission", "postal", "product", "rate limit", "requires_escalation", "restricted", "schema",
				"shipping", "tax", "unauthorized", "unavailable", "unable", "unexpected", "unsupported", "update", "variant", "wrong",
				"invalid",
			} {
				if strings.Contains(lower, signal) {
					signals = append(signals, strings.ReplaceAll(signal, " ", "_"))
				}
			}
			if len(signals) > 0 {
				firstTextSignals = strings.Join(signals, ",")
			}
			if strings.Contains(lower, "https://") || strings.Contains(lower, "http://") {
				if firstTextSignals == "none" {
					firstTextSignals = "url"
				} else {
					firstTextSignals += ",url"
				}
			}
		}
	}
	return fmt.Sprintf("is_error=%v structured=%T content_count=%d first_content_type=%s first_text_json=%s first_text_signals=%s",
		result["isError"], result["structuredContent"], len(content), firstContentType, firstTextJSONType, firstTextSignals)
}

// TestLiveShopifyAgentToken verifies the real client-credentials response
// contract without printing the bearer token or credential values. Unlike the
// checkout conformance gate, it does not contact a merchant Shop.
func TestLiveShopifyAgentToken(t *testing.T) {
	if os.Getenv("SHOPIFY_AGENCY_ORDER_TOKEN_LIVE") != "1" {
		t.Skip("set SHOPIFY_AGENCY_ORDER_TOKEN_LIVE=1 to run the Shopify token contract gate")
	}
	clientID := strings.TrimSpace(os.Getenv("SHOPIFY_DEV_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("SHOPIFY_DEV_CLIENT_SECRET"))
	if clientID == "" || clientSecret == "" {
		t.Fatal("Shopify client credentials are required")
	}
	transport, err := sharedhttpclient.NewTransport(sharedhttpclient.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	client, err := sharedhttpclient.NewClient(transport, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := NewTokenSource(client, "https://api.shopify.com/auth/access_token", clientID, clientSecret)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token.Scope == "" || !time.Now().Add(5*time.Minute).Before(token.ExpiresAt) {
		t.Fatal("Shopify token claims are missing usable scopes or expiry")
	}
	t.Logf("Shopify token scope=%q", token.Scope)
}

func TestLiveShopifyCheckoutToolSchemas(t *testing.T) {
	if os.Getenv("SHOPIFY_AGENCY_ORDER_SCHEMA_LIVE") != "1" {
		t.Skip("set SHOPIFY_AGENCY_ORDER_SCHEMA_LIVE=1 to read the live checkout tool schemas")
	}
	required := func(name string) string {
		t.Helper()
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required", name)
		}
		return value
	}
	clientID := required("SHOPIFY_DEV_CLIENT_ID")
	clientSecret := required("SHOPIFY_DEV_CLIENT_SECRET")
	shopDomain := strings.ToLower(required("SHOPIFY_AGENCY_ORDER_LIVE_SHOP"))
	buyerIP := net.ParseIP(required("SHOPIFY_AGENCY_ORDER_LIVE_BUYER_IP"))
	if buyerIP == nil || buyerIP.IsLoopback() || buyerIP.IsPrivate() || buyerIP.IsUnspecified() {
		t.Fatal("SHOPIFY_AGENCY_ORDER_LIVE_BUYER_IP must be an explicitly approved public IP")
	}
	transport, err := sharedhttpclient.NewTransport(sharedhttpclient.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	client, err := sharedhttpclient.NewClient(transport, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := NewTokenSource(client, "https://api.shopify.com/auth/access_token", clientID, clientSecret)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"jsonrpc":"2.0","method":"tools/list","id":1,"params":{}}`)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://"+shopDomain+"/api/ucp/mcp", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	request.Header.Set("Shopify-Buyer-IP", buyerIP.String())
	response, err := sharedhttpclient.Do(context.Background(), client, request, sharedhttpclient.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := sharedhttpclient.ReadBody(response.Body, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &envelope) != nil || envelope.Error != nil {
		t.Fatalf("tools/list failed status=%d", response.StatusCode)
	}
	schemas := make(map[string]map[string]any)
	for _, tool := range envelope.Result.Tools {
		switch tool.Name {
		case "create_checkout", "update_checkout", "get_checkout", "cancel_checkout":
			var schema map[string]any
			if json.Unmarshal(tool.InputSchema, &schema) != nil {
				t.Fatalf("tool %s returned an invalid schema", tool.Name)
			}
			schemas[tool.Name] = schema
		}
	}
	if len(schemas) != 4 {
		t.Fatalf("expected four checkout schemas, found %d", len(schemas))
	}
	assertSchemaRequired(t, schemas["create_checkout"], "meta", "checkout")
	assertSchemaRequired(t, schemas["update_checkout"], "meta", "checkout", "id")
	assertSchemaRequired(t, schemas["get_checkout"], "meta", "id")
	assertSchemaRequired(t, schemas["cancel_checkout"], "meta", "id")
	properties, _ := schemas["create_checkout"]["properties"].(map[string]any)
	checkout, _ := properties["checkout"].(map[string]any)
	checkoutProperties, _ := checkout["properties"].(map[string]any)
	if checkoutProperties["cart_id"] == nil || properties["cart_id"] != nil {
		t.Fatal("live create_checkout schema no longer nests cart_id under checkout")
	}
	assertSchemaRequired(t, checkout, "line_items")
	t.Log("live checkout tool schemas matched the reviewed argument contract")
}

func assertSchemaRequired(t *testing.T, schema map[string]any, expected ...string) {
	t.Helper()
	raw, _ := schema["required"].([]any)
	required := make(map[string]bool, len(raw))
	for _, entry := range raw {
		required[fmt.Sprint(entry)] = true
	}
	for _, field := range expected {
		if !required[field] {
			t.Fatalf("schema is missing required field %s", field)
		}
	}
}

// TestLiveAgencyOrderShopifyProbe exercises the same non-completing checkout
// path against a candidate Shop without treating catalog origin hints or token
// presence as domestic/completion approval. It is discovery evidence only and
// can never replace TestLiveAgencyOrderShopifyPreflight as a release gate.
func TestLiveAgencyOrderShopifyProbe(t *testing.T) {
	if os.Getenv("SHOPIFY_AGENCY_ORDER_PROBE_LIVE") != "1" {
		t.Skip("set SHOPIFY_AGENCY_ORDER_PROBE_LIVE=1 to probe a candidate Shop")
	}
	required := func(name string) string {
		t.Helper()
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required", name)
		}
		return value
	}
	clientID := required("SHOPIFY_DEV_CLIENT_ID")
	clientSecret := required("SHOPIFY_DEV_CLIENT_SECRET")
	shopDomain := strings.ToLower(required("SHOPIFY_AGENCY_ORDER_LIVE_SHOP"))
	variantID := required("SHOPIFY_AGENCY_ORDER_LIVE_VARIANT_ID")
	profileURL := required("SHOPIFY_AGENCY_ORDER_LIVE_AGENT_PROFILE_URL")
	buyerIP := net.ParseIP(required("SHOPIFY_AGENCY_ORDER_LIVE_BUYER_IP"))
	if buyerIP == nil || buyerIP.IsLoopback() || buyerIP.IsPrivate() || buyerIP.IsUnspecified() {
		t.Fatal("SHOPIFY_AGENCY_ORDER_LIVE_BUYER_IP must be an explicitly approved public IP")
	}
	parsedProfile, err := url.Parse(profileURL)
	if err != nil || parsedProfile.Scheme != "https" || parsedProfile.Host == "" {
		t.Fatal("SHOPIFY_AGENCY_ORDER_LIVE_AGENT_PROFILE_URL must be public HTTPS")
	}

	transport, err := sharedhttpclient.NewTransport(sharedhttpclient.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	recorder := &liveProbeTransport{base: transport}
	client, err := sharedhttpclient.NewClient(recorder, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := NewTokenSource(client, "https://api.shopify.com/auth/access_token", clientID, clientSecret)
	if err != nil {
		t.Fatal(err)
	}
	vault := &gatewayVault{values: map[string]string{}}
	gateway, err := NewGateway(GatewayConfig{
		HTTPClient: client, Tokens: tokens, Vault: vault,
		AgentProfileURL: profileURL, AgentProfileVersion: "2026-04-08",
		AgentProfileHash: "DISCOVERY_ONLY_NOT_RELEASE_EVIDENCE", StorefrontAPIVersion: "2026-07",
		BuyerContactEmail: strings.TrimSpace(os.Getenv("AGENCY_ORDER_BUYER_CONTACT_EMAIL")),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := agencyapp.MerchantRequest{
		OrderSheetSessionID: "live-probe-" + fmt.Sprint(time.Now().UnixNano()),
		UserID:              "live-probe", ShopDomain: shopDomain, BuyerIP: buyerIP,
		Lines: []agencydomain.ExactLine{{LineID: "line-1", VariantID: variantID, ShopDomain: shopDomain, Quantity: 1}},
		ShippingAddress: agencyapp.ShippingAddress{
			RecipientName: "Vitlane Review", AddressLine1: "131 Greene Street",
			City: "New York", Region: "NY", PostalCode: "10012", Country: "US",
			Phone: "+12025550123",
		},
	}
	latest := agencydomain.MerchantCheckout{}
	t.Cleanup(func() {
		if _, found := vault.values["safe:UCP_CHECKOUT"]; found && latest.CheckoutSessionSafeRef == "" {
			latest.CheckoutSessionSafeRef = "safe:UCP_CHECKOUT"
		}
		if latest.CheckoutSessionSafeRef == "" {
			return
		}
		request.Existing = &latest
		if err := gateway.Cancel(context.Background(), request); err != nil {
			t.Errorf("cancel probed checkout: %v", err)
		}
	})

	explored, err := gateway.Explore(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	latest = explored
	if len(explored.DeliveryGroups) == 0 {
		t.Fatal("candidate Storefront Cart returned no delivery groups")
	}
	selections := make(map[string]string, len(explored.DeliveryGroups))
	for _, group := range explored.DeliveryGroups {
		if len(group.Options) == 0 {
			t.Fatalf("delivery group %s returned no options", group.ID)
		}
		selections[group.ID] = group.Options[0].ID
	}
	request.Existing = &latest
	selected, err := gateway.SelectDelivery(context.Background(), request, selections)
	if err != nil {
		t.Fatal(err)
	}
	latest = selected
	t.Logf("candidate Storefront Cart observed shop=%s delivery_groups=%d selected_groups=%d", shopDomain, len(explored.DeliveryGroups), len(selections))
	request.Existing = &latest
	preflight, err := gateway.Preflight(context.Background(), request)
	if err != nil {
		host, path, status, code, message, contentType, shape := recorder.last()
		t.Fatalf("%v (last_provider_response=%s%s status=%d code=%s message=%q content_type=%q shape=%q)",
			err, host, path, status, code, message, contentType, shape)
	}
	latest = preflight
	t.Logf("candidate quote classification shop=%s status=%s quote=%s handling=%s continue_capability=%t notices=%#v",
		shopDomain, preflight.ProviderStatus, preflight.QuoteReadiness,
		preflight.ProcurementHandling, preflight.ContinueURLSafeRef != "", preflight.ProviderNotices)
	request.Existing = &latest
	final, err := gateway.FinalGet(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	latest = final
	if final.EvidenceHash != preflight.EvidenceHash || final.AuthoritativeTotal != preflight.AuthoritativeTotal || final.TaxTotal != preflight.TaxTotal {
		t.Fatalf("candidate final snapshot changed status=%s->%s quote=%s->%s handling=%s->%s quote_equal=%t totals_equal=%t notices_equal=%t expiry_equal=%t continue_equal=%t total_equal=%t tax_equal=%t",
			preflight.ProviderStatus, final.ProviderStatus,
			preflight.QuoteReadiness, final.QuoteReadiness,
			preflight.ProcurementHandling, final.ProcurementHandling,
			preflight.QuoteFingerprint == final.QuoteFingerprint,
			reflect.DeepEqual(preflight.Totals, final.Totals),
			reflect.DeepEqual(preflight.ProviderNotices, final.ProviderNotices),
			preflight.ExpiresAt.Equal(final.ExpiresAt),
			preflight.ContinueURLHash == final.ContinueURLHash,
			preflight.AuthoritativeTotal == final.AuthoritativeTotal,
			preflight.TaxTotal == final.TaxTotal)
	}
	t.Logf("candidate probe observed shop=%s delivery_groups=%d status=%s readiness=%s total_positive=%t tax_present=%t messages=%d",
		shopDomain, len(explored.DeliveryGroups), final.ProviderStatus, final.QuoteReadiness,
		final.AuthoritativeTotal.AmountMinor > 0, hasTotalType(final.Totals, "tax"), len(final.ProviderNotices))
}

// TestLiveAgencyOrderShopifyPreflight is the opt-in get_checkout release gate.
// It creates a Storefront Cart, applies
// a full US address, selects every delivery group, creates/reads a UCP checkout
// twice, verifies stable tax/total and always cancels the checkout. It never
// calls complete_checkout.
//
// The test intentionally requires the buyer IP as an explicit environment
// input because running it discloses that IP to the selected Shopify Shop.
func TestLiveAgencyOrderShopifyPreflight(t *testing.T) {
	if os.Getenv("SHOPIFY_AGENCY_ORDER_LIVE") != "1" {
		t.Skip("set SHOPIFY_AGENCY_ORDER_LIVE=1 to run the reviewed live conformance gate")
	}
	required := func(name string) string {
		t.Helper()
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required", name)
		}
		return value
	}
	clientID := required("SHOPIFY_DEV_CLIENT_ID")
	clientSecret := required("SHOPIFY_DEV_CLIENT_SECRET")
	shopDomain := strings.ToLower(required("SHOPIFY_AGENCY_ORDER_LIVE_SHOP"))
	variantID := required("SHOPIFY_AGENCY_ORDER_LIVE_VARIANT_ID")
	profileURL := required("SHOPIFY_AGENCY_ORDER_LIVE_AGENT_PROFILE_URL")
	buyerIP := net.ParseIP(required("SHOPIFY_AGENCY_ORDER_LIVE_BUYER_IP"))
	if buyerIP == nil || buyerIP.IsLoopback() || buyerIP.IsPrivate() || buyerIP.IsUnspecified() {
		t.Fatal("SHOPIFY_AGENCY_ORDER_LIVE_BUYER_IP must be an explicitly approved public IP")
	}
	parsedProfile, err := url.Parse(profileURL)
	if err != nil || parsedProfile.Scheme != "https" || parsedProfile.Host == "" {
		t.Fatal("SHOPIFY_AGENCY_ORDER_LIVE_AGENT_PROFILE_URL must be public HTTPS")
	}

	transport, err := sharedhttpclient.NewTransport(sharedhttpclient.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	client, err := sharedhttpclient.NewClient(transport, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	profileHash := fetchLiveProfileHash(t, client, profileURL)
	tokens, err := NewTokenSource(
		client, "https://api.shopify.com/auth/access_token", clientID, clientSecret,
	)
	if err != nil {
		t.Fatal(err)
	}
	vault := &gatewayVault{values: map[string]string{}}
	gateway, err := NewGateway(GatewayConfig{
		HTTPClient: client, Tokens: tokens, Vault: vault,
		AgentProfileURL: profileURL, AgentProfileVersion: "2026-04-08",
		AgentProfileHash: profileHash, StorefrontAPIVersion: "2026-07",
		BuyerContactEmail: strings.TrimSpace(os.Getenv("AGENCY_ORDER_BUYER_CONTACT_EMAIL")),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := agencyapp.MerchantRequest{
		OrderSheetSessionID: "live-conformance-" + fmt.Sprint(time.Now().UnixNano()),
		UserID:              "live-conformance", ShopDomain: shopDomain, BuyerIP: buyerIP,
		Lines: []agencydomain.ExactLine{{
			LineID: "line-1", VariantID: variantID, ShopDomain: shopDomain, Quantity: 1,
		}},
		ShippingAddress: agencyapp.ShippingAddress{
			RecipientName: "Vitlane Review", AddressLine1: "131 Greene Street",
			City: "New York", Region: "NY", PostalCode: "10012", Country: "US",
			Phone: "+12025550123",
		},
	}
	latest := agencydomain.MerchantCheckout{}
	t.Cleanup(func() {
		if _, found := vault.values["safe:UCP_CHECKOUT"]; found && latest.CheckoutSessionSafeRef == "" {
			latest.CheckoutSessionSafeRef = "safe:UCP_CHECKOUT"
		}
		if latest.CheckoutSessionSafeRef == "" {
			return
		}
		request.Existing = &latest
		if err := gateway.Cancel(context.Background(), request); err != nil {
			t.Errorf("cancel live checkout: %v", err)
		}
	})

	explored, err := gateway.Explore(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	latest = explored
	if len(explored.DeliveryGroups) == 0 {
		t.Fatal("live Storefront Cart returned no delivery groups")
	}
	selections := make(map[string]string, len(explored.DeliveryGroups))
	for _, group := range explored.DeliveryGroups {
		if len(group.Options) == 0 {
			t.Fatalf("delivery group %s returned no options", group.ID)
		}
		selections[group.ID] = group.Options[0].ID
	}
	request.Existing = &latest
	selected, err := gateway.SelectDelivery(context.Background(), request, selections)
	if err != nil {
		t.Fatal(err)
	}
	latest = selected
	if !selected.DeliverySelectionComplete() {
		t.Fatal("delivery selection did not cover every group")
	}
	request.Existing = &latest
	preflight, err := gateway.Preflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	latest = preflight
	if preflight.QuoteReadiness != agencydomain.PricingConfirmed ||
		(preflight.ProcurementHandling != agencydomain.ProcurementHandlingNormal &&
			preflight.ProcurementHandling != agencydomain.ProcurementHandlingOperatorLater) ||
		preflight.ContinueURLSafeRef == "" || preflight.ContinueURLHash == "" ||
		preflight.QuoteFingerprint == "" ||
		preflight.DutiesDisposition != agencydomain.DutiesNoSignalAtPreflight ||
		preflight.AuthEvidence.CompletionPermission != agencydomain.CompletionNotRequested ||
		preflight.CompletionRoute != agencydomain.CompletionManualHandoff ||
		// 명시적 tax 항목 또는 구조 일관 tax $0 견적(ADR-0047 2026-08-16 개정)은
		// summarizeTotals가 검증하며 PricingConfirmed에 반영된다.
		preflight.AuthoritativeTotal.AmountMinor <= 0 {
		t.Fatalf("live checkout did not satisfy the quote gate: status=%s readiness=%s handling=%s duties=%s completion=%s total=%d tax_present=%t",
			preflight.ProviderStatus, preflight.QuoteReadiness,
			preflight.ProcurementHandling,
			preflight.DutiesDisposition, preflight.AuthEvidence.CompletionPermission,
			preflight.AuthoritativeTotal.AmountMinor, hasTotalType(preflight.Totals, "tax"))
	}
	request.Existing = &latest
	final, err := gateway.FinalGet(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	latest = final
	if final.EvidenceHash != preflight.EvidenceHash ||
		final.AuthoritativeTotal != preflight.AuthoritativeTotal ||
		final.TaxTotal != preflight.TaxTotal {
		t.Fatal("final get changed the reviewed tax/total snapshot")
	}
}

func fetchLiveProfileHash(t *testing.T, client *http.Client, profileURL string) string {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, profileURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := sharedhttpclient.Do(context.Background(), client, request, sharedhttpclient.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("agent profile is not public JSON: status=%d content_type=%q",
			response.StatusCode, response.Header.Get("Content-Type"))
	}
	body, err := sharedhttpclient.ReadBody(response.Body, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var profile map[string]any
	if err := json.Unmarshal(body, &profile); err != nil {
		t.Fatal(err)
	}
	ucp, ok := profile["ucp"].(map[string]any)
	if !ok || ucp["version"] != "2026-04-08" {
		t.Fatal("agent profile does not declare UCP 2026-04-08")
	}
	hash, err := shareddomain.CanonicalJSONHash(profile)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func hasTotalType(totals []agencydomain.Total, target string) bool {
	for _, total := range totals {
		if total.Type == target {
			return true
		}
	}
	return false
}
