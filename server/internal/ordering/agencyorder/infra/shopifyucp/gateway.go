package shopifyucp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

const responseLimit = 4 << 20

type CapabilityVault interface {
	Save(context.Context, string, string, string, string, string, time.Time) (string, error)
	Load(context.Context, string, string, string) (string, error)
}

type GatewayConfig struct {
	HTTPClient           *http.Client
	Tokens               *TokenSource
	Vault                CapabilityVault
	AgentProfileURL      string
	AgentProfileVersion  string
	AgentProfileHash     string
	StorefrontAPIVersion string
	// BuyerContactEmail은 UCP buyer_identity에 싣는 운영(대행) 연락 이메일이다.
	// 고객 이메일·전화는 여기 넣지 않는다 — 견적 단계 PII 유출과 Shop발
	// 예상외 메일·SMS를 만든다. 고객 연락처는 배달 목적의 destination
	// phone_number에 한정한다(운영정합 3차 소유자 결정).
	BuyerContactEmail string
}

type Gateway struct{ config GatewayConfig }

func NewGateway(config GatewayConfig) (*Gateway, error) {
	profile, err := url.Parse(strings.TrimSpace(config.AgentProfileURL))
	if config.HTTPClient == nil || config.Tokens == nil || config.Vault == nil || err != nil ||
		profile.Scheme != "https" || profile.Host == "" || config.AgentProfileVersion == "" || config.AgentProfileHash == "" ||
		len(config.StorefrontAPIVersion) != 7 || config.StorefrontAPIVersion[4] != '-' {
		return nil, fmt.Errorf("Shopify checkout gateway config invalid")
	}
	return &Gateway{config: config}, nil
}

func (g *Gateway) Explore(ctx context.Context, input agencyapp.MerchantRequest) (agencydomain.MerchantCheckout, error) {
	if input.BuyerIP == nil {
		return agencydomain.MerchantCheckout{}, fault.New(fault.InvalidInput, "TRUSTED_BUYER_IP_REQUIRED", false)
	}
	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	buyerContextRef, err := g.config.Vault.Save(
		ctx, input.UserID, input.OrderSheetSessionID, input.ShopDomain,
		"BUYER_CONTEXT", input.BuyerIP.String(), expiresAt,
	)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	cart, err := g.createStorefrontCart(ctx, input)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	safeRef, err := g.config.Vault.Save(ctx, input.UserID, input.OrderSheetSessionID, input.ShopDomain, "STOREFRONT_CART", cart.ID, expiresAt)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	result, err := mapStorefrontCart(input, cart, safeRef)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	result.BuyerContextSafeRef = buyerContextRef
	result.ObservedAt, result.ExpiresAt = time.Now().UTC(), expiresAt
	return result, nil
}

func (g *Gateway) SelectDelivery(ctx context.Context, input agencyapp.MerchantRequest, selections map[string]string) (agencydomain.MerchantCheckout, error) {
	if input.Existing == nil || input.BuyerIP == nil || len(selections) != len(input.Existing.DeliveryGroups) {
		return agencydomain.MerchantCheckout{}, agencydomain.ErrInvalid
	}
	for _, group := range input.Existing.DeliveryGroups {
		selected := selections[group.ID]
		valid := false
		for _, option := range group.Options {
			if option.ID == selected {
				valid = true
				break
			}
		}
		if !valid {
			return agencydomain.MerchantCheckout{}, agencydomain.ErrInvalid
		}
	}
	rawCartID, err := g.config.Vault.Load(ctx, input.UserID, input.Existing.StorefrontCartSafeRef, "STOREFRONT_CART")
	if err != nil {
		return agencydomain.MerchantCheckout{}, fault.Wrap(err, fault.ProviderUnavailable, "STOREFRONT_CART_CAPABILITY_UNAVAILABLE", true)
	}
	cart, err := g.selectStorefrontDelivery(ctx, input, rawCartID, selections)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	result, err := mapStorefrontCart(input, cart, input.Existing.StorefrontCartSafeRef)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	result.ObservedAt, result.ExpiresAt = time.Now().UTC(), input.Existing.ExpiresAt
	return result, nil
}

func (g *Gateway) Preflight(ctx context.Context, input agencyapp.MerchantRequest) (agencydomain.MerchantCheckout, error) {
	if input.Existing == nil || input.BuyerIP == nil {
		return agencydomain.MerchantCheckout{}, agencydomain.ErrInvalid
	}
	rawCartID, err := g.config.Vault.Load(ctx, input.UserID, input.Existing.StorefrontCartSafeRef, "STOREFRONT_CART")
	if err != nil {
		return agencydomain.MerchantCheckout{}, fault.Wrap(err, fault.ProviderUnavailable, "STOREFRONT_CART_CAPABILITY_UNAVAILABLE", true)
	}
	createCheckout := checkoutUpdatePayload(map[string]any{}, input, g.config.BuyerContactEmail)
	createCheckout["cart_id"] = rawCartID
	created, err := g.checkoutResourceCall(ctx, input, "create_checkout", map[string]any{
		"checkout": createCheckout,
	}, sharedhttpclient.ExternalEffect)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	checkout := directObject(created)
	rawCheckoutID := stringValue(checkout["id"])
	if rawCheckoutID == "" {
		return agencydomain.MerchantCheckout{}, fault.New(fault.ProviderRejected, "SHOPIFY_CHECKOUT_RESPONSE_INVALID", false)
	}
	expiresAt := timeValue(checkout["expires_at"], time.Now().Add(20*time.Minute))
	safeRef, err := g.config.Vault.Save(ctx, input.UserID, input.OrderSheetSessionID, input.ShopDomain, "UCP_CHECKOUT", rawCheckoutID, expiresAt)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	continueURL := stringValue(checkout["continue_url"])
	updated, updateErr := g.checkoutResourceCall(ctx, input, "update_checkout", map[string]any{
		"id": rawCheckoutID, "checkout": checkoutUpdatePayload(checkout, input, g.config.BuyerContactEmail),
	}, sharedhttpclient.ExternalEffect)
	if updateErr != nil {
		return agencydomain.MerchantCheckout{}, updateErr
	}
	checkout = directObject(updated)
	continueURL = latestContinueURL(continueURL, checkout)
	first, err := g.checkoutResourceCall(ctx, input, "get_checkout", map[string]any{"id": rawCheckoutID}, sharedhttpclient.ReadOnly)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	firstCheckout := directObject(first)
	continueURL = latestContinueURL(continueURL, firstCheckout)
	second, err := g.checkoutResourceCall(ctx, input, "get_checkout", map[string]any{"id": rawCheckoutID}, sharedhttpclient.ReadOnly)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	secondCheckout := directObject(second)
	continueURL = latestContinueURL(continueURL, secondCheckout)
	stable := checkoutPricingFingerprint(firstCheckout) == checkoutPricingFingerprint(secondCheckout)
	continueRef, continueHash := "", ""
	if normalized, hash, ok := safeContinueURL(continueURL); ok {
		continueRef, err = g.config.Vault.Save(
			ctx, input.UserID, input.OrderSheetSessionID, input.ShopDomain,
			"UCP_CONTINUE_URL", normalized, expiresAt,
		)
		if err != nil {
			return agencydomain.MerchantCheckout{}, err
		}
		continueHash = hash
	}
	result, err := g.mapCheckout(input, secondCheckout, safeRef, continueRef, continueHash, stable)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	return result, nil
}

func (g *Gateway) FinalGet(ctx context.Context, input agencyapp.MerchantRequest) (agencydomain.MerchantCheckout, error) {
	if input.Existing == nil || input.BuyerIP == nil {
		return agencydomain.MerchantCheckout{}, agencydomain.ErrInvalid
	}
	rawID, err := g.config.Vault.Load(ctx, input.UserID, input.Existing.CheckoutSessionSafeRef, "UCP_CHECKOUT")
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	structured, err := g.checkoutResourceCall(ctx, input, "get_checkout", map[string]any{"id": rawID}, sharedhttpclient.ReadOnly)
	if err != nil {
		return agencydomain.MerchantCheckout{}, err
	}
	checkout := directObject(structured)
	continueRef := input.Existing.ContinueURLSafeRef
	continueHash := input.Existing.ContinueURLHash
	if candidate := stringValue(checkout["continue_url"]); candidate != "" {
		normalized, freshHash, ok := safeContinueURL(candidate)
		if !ok {
			continueRef = ""
			continueHash = ""
		} else {
			continueRef, err = g.config.Vault.Save(
				ctx, input.UserID, input.OrderSheetSessionID, input.ShopDomain,
				"UCP_CONTINUE_URL", normalized,
				timeValue(checkout["expires_at"], time.Now().Add(20*time.Minute)),
			)
			if err != nil {
				return agencydomain.MerchantCheckout{}, err
			}
			continueHash = freshHash
		}
	}
	return g.mapCheckout(
		input, checkout, input.Existing.CheckoutSessionSafeRef,
		continueRef, continueHash, true,
	)
}

func (g *Gateway) Cancel(ctx context.Context, input agencyapp.MerchantRequest) error {
	if input.Existing == nil {
		return agencydomain.ErrInvalid
	}
	// Shopify UCP exposes cancel_checkout. Storefront Cart has no cart-level
	// delete/cancel mutation; its opaque local capability is removed only after
	// checkout cancellation succeeds and the remote cart is left to expire.
	if input.Existing.CheckoutSessionSafeRef == "" {
		return nil
	}
	if input.BuyerIP == nil && input.Existing.BuyerContextSafeRef != "" {
		raw, err := g.config.Vault.Load(
			ctx, input.UserID, input.Existing.BuyerContextSafeRef, "BUYER_CONTEXT",
		)
		if err == nil {
			input.BuyerIP = net.ParseIP(strings.TrimSpace(raw))
		}
	}
	if input.BuyerIP == nil {
		return fault.New(fault.ProviderUnavailable, "SHOPIFY_CLEANUP_BUYER_CONTEXT_UNAVAILABLE", true)
	}
	meta := map[string]any{"idempotency-key": deterministicUUID(input.OrderSheetSessionID + ":" + input.ShopDomain + ":cancel")}
	raw, err := g.config.Vault.Load(ctx, input.UserID, input.Existing.CheckoutSessionSafeRef, "UCP_CHECKOUT")
	if err != nil {
		return fault.Wrap(err, fault.ProviderUnavailable, "SHOPIFY_CHECKOUT_CAPABILITY_UNAVAILABLE", true)
	}
	_, err = g.authorizedCall(ctx, input, "cancel_checkout", map[string]any{"id": raw, "meta": meta}, sharedhttpclient.ExternalEffect)
	return err
}

func (g *Gateway) authorizedCall(ctx context.Context, input agencyapp.MerchantRequest, tool string, args map[string]any, effect sharedhttpclient.Effect) (map[string]any, error) {
	token, err := g.config.Tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	structured, status, err := g.call(ctx, input, tool, args, token.AccessToken, effect)
	if err == nil && status == http.StatusUnauthorized {
		g.config.Tokens.Invalidate(token.AccessToken)
		token, err = g.config.Tokens.Token(ctx)
		if err != nil {
			return nil, err
		}
		structured, status, err = g.call(ctx, input, tool, args, token.AccessToken, effect)
	}
	if err != nil {
		return structured, err
	}
	if status < 200 || status >= 300 {
		return nil, sharedhttpclient.StatusFault(status, "", effect == sharedhttpclient.ReadOnly)
	}
	return structured, nil
}

// checkoutResourceCall is the only boundary allowed to recover a Shopify
// checkout resource from an MCP isError result. General MCP calls remain
// fail-closed. A structurally valid checkout with typed observations is still
// useful: quote readiness is calculated from lines/fulfillment/currency/totals,
// while provider status/messages become read-only notices and handling hints.
func (g *Gateway) checkoutResourceCall(
	ctx context.Context,
	input agencyapp.MerchantRequest,
	tool string,
	args map[string]any,
	effect sharedhttpclient.Effect,
) (map[string]any, error) {
	structured, err := g.authorizedCall(ctx, input, tool, args, effect)
	if err == nil {
		return structured, nil
	}
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.ProviderRejected ||
		!recoverableCheckoutResource(directObject(structured)) {
		return structured, err
	}
	return structured, nil
}

func recoverableCheckoutResource(checkout map[string]any) bool {
	if stringValue(checkout["id"]) == "" || stringValue(checkout["status"]) == "" {
		return false
	}
	return len(messageDescriptors(checkout["messages"])) > 0
}

func (g *Gateway) call(ctx context.Context, input agencyapp.MerchantRequest, tool string, args map[string]any, token string, effect sharedhttpclient.Effect) (map[string]any, int, error) {
	meta, _ := args["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["ucp-agent"] = map[string]any{"profile": g.config.AgentProfileURL}
	args["meta"] = meta
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "tools/call", "id": 1,
		"params": map[string]any{"name": tool, "arguments": args}})
	endpoint := "https://" + input.ShopDomain + "/api/ucp/mcp"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Shopify-Buyer-IP", input.BuyerIP.String())
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := sharedhttpclient.Do(ctx, g.config.HTTPClient, request, effect)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.StatusCode, nil
	}
	body, err := sharedhttpclient.ReadBody(response.Body, responseLimit)
	if err != nil {
		return nil, response.StatusCode, err
	}
	var envelope struct {
		Result *struct {
			IsError    bool            `json:"isError"`
			Structured json.RawMessage `json:"structuredContent"`
			Content    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error != nil || envelope.Result == nil {
		return nil, response.StatusCode, fault.New(fault.ProviderRejected, "SHOPIFY_UCP_RESPONSE_INVALID", false)
	}
	structured, err := decodeSuccessfulToolResult(envelope.Result.Structured, envelope.Result.Content)
	if err != nil {
		return nil, response.StatusCode, fault.New(fault.ProviderRejected, "SHOPIFY_UCP_RESPONSE_INVALID", false)
	}
	if envelope.Result.IsError {
		reason := "SHOPIFY_UCP_RESPONSE_INVALID"
		if hasProviderStringValue(structured, "requires_escalation") {
			reason = "SHOPIFY_CHECKOUT_REQUIRES_ESCALATION"
		} else if typed := typedErrorReason(structured); typed != "" {
			// shop이 typed 오류(code·severity)를 준 경우 "형태 오류"로 뭉개지
			// 않고 코드를 그대로 승격한다 — 화면 안내·감사 진단성의 근거다.
			reason = typed
		}
		return structured, response.StatusCode, fault.New(fault.ProviderRejected, reason, false)
	}
	return structured, response.StatusCode, nil
}

// typedErrorReason은 isError 응답의 구조 안에서 첫 typed 오류 코드를 찾아
// SHOPIFY_CHECKOUT_<CODE>로 승격한다. 코드는 [a-z0-9_]만 허용하고 64자로
// 자른다 — provider 문자열을 무검증으로 reason에 싣지 않는다.
func typedErrorReason(value any) string {
	code := firstTypedErrorCode(value)
	if code == "" {
		return ""
	}
	cleaned := make([]rune, 0, len(code))
	for _, r := range strings.ToLower(code) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			cleaned = append(cleaned, r)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	if len(cleaned) > 64 {
		cleaned = cleaned[:64]
	}
	return "SHOPIFY_CHECKOUT_" + strings.ToUpper(string(cleaned))
}

func firstTypedErrorCode(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if stringValue(typed["type"]) == "error" {
			if code := stringValue(typed["code"]); code != "" {
				return code
			}
		}
		for _, key := range []string{"messages", "checkout"} {
			if code := firstTypedErrorCode(typed[key]); code != "" {
				return code
			}
		}
	case []any:
		for _, nested := range typed {
			if code := firstTypedErrorCode(nested); code != "" {
				return code
			}
		}
	}
	return ""
}

func hasProviderStringValue(value any, target string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, nested := range typed {
			if hasProviderStringValue(nested, target) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if hasProviderStringValue(nested, target) {
				return true
			}
		}
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), target)
	}
	return false
}

func decodeSuccessfulToolResult(structured json.RawMessage, content []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) (map[string]any, error) {
	trimmed := bytes.TrimSpace(structured)
	if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var result map[string]any
		if err := json.Unmarshal(trimmed, &result); err != nil || result == nil {
			return nil, agencydomain.ErrInvalid
		}
		return result, nil
	}
	if len(content) == 0 || content[0].Type != "text" || strings.TrimSpace(content[0].Text) == "" {
		return nil, agencydomain.ErrInvalid
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(content[0].Text), &result); err != nil || result == nil {
		return nil, agencydomain.ErrInvalid
	}
	return result, nil
}

func (g *Gateway) mapCheckout(
	input agencyapp.MerchantRequest,
	checkout map[string]any,
	safeRef string,
	continueRef string,
	continueHash string,
	stable bool,
) (agencydomain.MerchantCheckout, error) {
	descriptors := messageDescriptors(checkout["messages"])
	providerNotices := providerNoticeSnapshots(checkout["messages"])
	requirementNotices, requirementSteps := providerRequirementObservations(checkout)
	providerNotices = append(providerNotices, requirementNotices...)
	policyLinks := providerPolicyLinks(input.ShopDomain, checkout)
	status := stringValue(checkout["status"])
	handling := procurementHandling(status, descriptors)
	manualSiteSteps := mergeManualSiteSteps(manualSiteStepsForDescriptors(descriptors), requirementSteps)
	totals, totalsErr := mapTotals(checkout["totals"])
	total, tax, summaryErr := int64(0), int64(0), error(nil)
	if totalsErr == nil {
		total, tax, summaryErr = summarizeTotals(totals)
	}
	structureErr := validateCheckoutStructure(input, checkout)
	readiness := quoteReadiness(stable, totalsErr, summaryErr, structureErr)
	duties := dutiesDisposition(totals)
	scopeHash := ""
	if scope := g.config.Tokens.CurrentScope(); scope != "" {
		sum := sha256.Sum256([]byte(scope))
		scopeHash = hex.EncodeToString(sum[:])
	}
	result := agencydomain.MerchantCheckout{
		MerchantID: input.ShopDomain, ShopDomain: input.ShopDomain,
		StorefrontCartSafeRef: input.Existing.StorefrontCartSafeRef, CheckoutSessionSafeRef: safeRef,
		BuyerContextSafeRef:       input.Existing.BuyerContextSafeRef,
		LineRefs:                  append([]string(nil), input.Existing.LineRefs...),
		DeliveryGroups:            cloneDeliveryGroups(input.Existing.DeliveryGroups),
		DeliveryOptions:           append([]agencydomain.DeliveryOption(nil), input.Existing.DeliveryOptions...),
		SelectedDeliveryOptionRef: input.Existing.SelectedDeliveryOptionRef,
		ProviderStatus:            status, ProviderNotices: providerNotices, PolicyLinks: policyLinks,
		ManualSiteSteps: manualSiteSteps, ProcurementHandling: handling, Totals: totals,
		AuthoritativeTotal: agencydomain.Money{AmountMinor: total, Currency: "USD"}, TaxTotal: agencydomain.Money{AmountMinor: tax, Currency: "USD"},
		DutiesDisposition: duties,
		AuthEvidence: agencydomain.AuthEvidence{Tier: "TOKEN", ScopesHash: scopeHash,
			AgentProfileURL: g.config.AgentProfileURL, AgentProfileVersion: g.config.AgentProfileVersion,
			AgentProfileHash: g.config.AgentProfileHash, CompletionPermission: agencydomain.CompletionNotRequested},
		CompletionRoute: agencydomain.CompletionManualHandoff, QuoteReadiness: readiness,
		ContinueURLSafeRef: continueRef, ContinueURLHash: continueHash,
		QuoteFingerprint: checkoutPricingFingerprint(checkout), ObservedAt: time.Now().UTC(),
		ExpiresAt: timeValue(checkout["expires_at"], time.Now().Add(20*time.Minute)),
	}
	hash, err := shareddomain.CanonicalJSONHash(struct {
		ProviderStatus      string                            `json:"providerStatus"`
		QuoteFingerprint    string                            `json:"quoteFingerprint"`
		QuoteReadiness      string                            `json:"quoteReadiness"`
		ProcurementHandling string                            `json:"procurementHandling"`
		Duties              string                            `json:"duties"`
		Permission          string                            `json:"permission"`
		ProviderNotices     []agencydomain.ProviderNotice     `json:"providerNotices,omitempty"`
		PolicyLinks         []agencydomain.ProviderPolicyLink `json:"policyLinks,omitempty"`
		ManualSiteSteps     []agencydomain.ManualSiteStep     `json:"manualSiteSteps,omitempty"`
	}{status, result.QuoteFingerprint, readiness, handling, duties,
		agencydomain.CompletionNotRequested, providerNotices, policyLinks, manualSiteSteps})
	if err != nil {
		return result, err
	}
	result.EvidenceHash = hash
	return result, nil
}

func cloneDeliveryGroups(source []agencydomain.DeliveryGroup) []agencydomain.DeliveryGroup {
	result := make([]agencydomain.DeliveryGroup, len(source))
	for index := range source {
		result[index] = source[index]
		result[index].LineRefs = append([]string(nil), source[index].LineRefs...)
		result[index].Options = append([]agencydomain.DeliveryOption(nil), source[index].Options...)
	}
	return result
}

func checkoutUpdatePayload(checkout map[string]any, input agencyapp.MerchantRequest, buyerContactEmail string) map[string]any {
	lineItems, _ := checkout["line_items"].([]any)
	ids := make([]string, 0, len(lineItems))
	sanitized := make([]map[string]any, 0, len(input.Lines))
	for index, line := range input.Lines {
		item := map[string]any{"quantity": line.Quantity, "item": map[string]any{"id": line.VariantID}}
		if index < len(lineItems) {
			if raw, ok := lineItems[index].(map[string]any); ok {
				if id := stringValue(raw["id"]); id != "" {
					item["id"] = id
					ids = append(ids, id)
				}
			}
		}
		sanitized = append(sanitized, item)
	}
	first, last := splitName(input.ShippingAddress.RecipientName)
	destination := map[string]any{"first_name": first, "last_name": last,
		"street_address": input.ShippingAddress.AddressLine1, "address_locality": input.ShippingAddress.City,
		"address_region": input.ShippingAddress.Region, "postal_code": input.ShippingAddress.PostalCode,
		"address_country": "US"}
	if input.ShippingAddress.AddressLine2 != "" {
		destination["extended_address"] = input.ShippingAddress.AddressLine2
	}
	if input.ShippingAddress.Phone != "" {
		destination["phone_number"] = input.ShippingAddress.Phone
	}
	methods := make([]map[string]any, 0)
	fulfillment := nestedObject(checkout, "fulfillment")
	if rawMethods, ok := fulfillment["methods"].([]any); ok {
		for _, entry := range rawMethods {
			raw, _ := entry.(map[string]any)
			method := map[string]any{"type": stringValue(raw["type"]), "line_item_ids": ids, "destinations": []any{destination}}
			if method["type"] == "" {
				method["type"] = "shipping"
			}
			if id := stringValue(raw["id"]); id != "" {
				method["id"] = id
			}
			if rawLineIDs, ok := raw["line_item_ids"].([]any); ok && len(rawLineIDs) > 0 {
				method["line_item_ids"] = rawLineIDs
			}
			if rawGroups, ok := raw["groups"].([]any); ok {
				groups := make([]map[string]any, 0, len(rawGroups))
				for _, entry := range rawGroups {
					rawGroup, _ := entry.(map[string]any)
					group := map[string]any{}
					for _, key := range []string{"id", "selected_option_id"} {
						if value := stringValue(rawGroup[key]); value != "" {
							group[key] = value
						}
					}
					if rawGroupLineIDs, ok := rawGroup["line_item_ids"].([]any); ok && len(rawGroupLineIDs) > 0 {
						group["line_item_ids"] = rawGroupLineIDs
					}
					if len(group) > 0 {
						groups = append(groups, group)
					}
				}
				if len(groups) > 0 {
					method["groups"] = groups
				}
			}
			methods = append(methods, method)
		}
	}
	if len(methods) == 0 {
		methods = append(methods, map[string]any{"type": "shipping", "line_item_ids": ids, "destinations": []any{destination}})
	}
	payload := map[string]any{"currency": "USD", "line_items": sanitized,
		"context": map[string]any{"address_country": "US", "address_region": input.ShippingAddress.Region,
			"postal_code": input.ShippingAddress.PostalCode, "currency": "USD", "language": "en-US"},
		"fulfillment": map[string]any{"methods": methods},
		"attribution": map[string]any{"utm_source": "vitlane", "utm_medium": "agentic_commerce"}}
	// buyer(UCP shopping/types/buyer.json)에는 운영(대행) 연락 이메일만 싣는다 —
	// contact-required 정책 shop의 buyer_identity_contact_method_required 거절을
	// 해소한다. 미설정이면 기존과 동일하게 미전송(관대한 shop은 그대로 통과).
	if email := strings.TrimSpace(buyerContactEmail); email != "" {
		payload["buyer"] = map[string]any{"email": email}
	}
	return payload
}

func directObject(value map[string]any) map[string]any {
	if nested := nestedObject(value, "checkout"); len(nested) > 0 {
		return nested
	}
	return value
}
func nestedObject(value map[string]any, key string) map[string]any {
	nested, _ := value[key].(map[string]any)
	return nested
}
func stringValue(value any) string { text, _ := value.(string); return strings.TrimSpace(text) }
func timeValue(value any, fallback time.Time) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, stringValue(value)); err == nil {
		return parsed
	}
	return fallback
}
func splitName(value string) (string, string) {
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return value, "-"
	}
	return strings.Join(fields[:len(fields)-1], " "), fields[len(fields)-1]
}
func mapTotals(value any) ([]agencydomain.Total, error) {
	raw, _ := value.([]any)
	if len(raw) == 0 {
		return nil, agencydomain.ErrInvalid
	}
	result := make([]agencydomain.Total, 0, len(raw))
	for _, entry := range raw {
		object, _ := entry.(map[string]any)
		totalType := stringValue(object["type"])
		amount, err := exactInt64(object["amount"])
		if totalType == "" || err != nil {
			return nil, agencydomain.ErrInvalid
		}
		result = append(result, agencydomain.Total{Type: totalType, AmountMinor: amount, DisplayText: stringValue(object["display_text"])})
	}
	return result, nil
}

func exactInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed ||
			typed < math.MinInt64 || typed > math.MaxInt64 {
			return 0, agencydomain.ErrInvalid
		}
		return int64(typed), nil
	case json.Number:
		return typed.Int64()
	default:
		return 0, agencydomain.ErrInvalid
	}
}

func summarizeTotals(totals []agencydomain.Total) (int64, int64, error) {
	var authoritative, components, tax int64
	totalCount := 0
	for _, item := range totals {
		if item.Type == "total" {
			authoritative = item.AmountMinor
			totalCount++
			continue
		}
		components += item.AmountMinor
		if item.Type == "tax" {
			tax += item.AmountMinor
		}
	}
	// nexus 없는 원격 판매자의 확정 견적에는 tax 항목이 아예 없다(미국 판매세는
	// 배송지 주 nexus가 있을 때만 징수). 구성요소 합이 authoritative total과
	// 정확히 일치할 때만 이를 tax $0 확정 견적으로 수용하고, 합이 어긋나면
	// 미계산 항목이 남았다는 뜻이므로 계속 거절한다. ADR-0047 2026-08-16 개정.
	if totalCount != 1 || authoritative <= 0 || components != authoritative || tax < 0 {
		return 0, 0, fault.New(fault.ProviderRejected, "SHOPIFY_CHECKOUT_TOTAL_MISMATCH", false)
	}
	return authoritative, tax, nil
}

type providerMessageDescriptor struct {
	Type     string `json:"type,omitempty"`
	Severity string `json:"severity,omitempty"`
	Code     string `json:"code,omitempty"`
	SafePath string `json:"safePath,omitempty"`
}

func messageDescriptors(value any) []providerMessageDescriptor {
	raw, _ := value.([]any)
	result := make([]providerMessageDescriptor, 0, len(raw))
	for _, entry := range raw {
		object, _ := entry.(map[string]any)
		descriptor := providerMessageDescriptor{
			Type: safeDescriptorValue(object["type"]), Severity: safeDescriptorValue(object["severity"]),
			Code: safeDescriptorValue(object["code"]), SafePath: safeMessagePath(object["path"]),
		}
		result = append(result, descriptor)
	}
	sort.Slice(result, func(i, j int) bool {
		left, _ := json.Marshal(result[i])
		right, _ := json.Marshal(result[j])
		return bytes.Compare(left, right) < 0
	})
	return result
}

func providerNoticeSnapshots(value any) []agencydomain.ProviderNotice {
	raw, _ := value.([]any)
	result := make([]agencydomain.ProviderNotice, 0, len(raw))
	for _, entry := range raw {
		object, _ := entry.(map[string]any)
		descriptor := agencydomain.ProviderNotice{
			Source:   "MESSAGE",
			Type:     safeDescriptorValue(object["type"]),
			Severity: safeDescriptorValue(object["severity"]),
			Code:     safeDescriptorValue(object["code"]),
			SafePath: safeMessagePath(object["path"]),
		}
		for _, key := range []string{"message", "text", "content", "detail"} {
			if text := safePresentationText(object[key], 1000); text != "" {
				descriptor.Text = text
				break
			}
		}
		descriptor.Presentation, descriptor.Audience = noticePresentation(descriptor.Type, descriptor.Severity, descriptor.Code)
		descriptor.Registered = registeredProviderCode(descriptor.Code) || descriptor.Code == ""
		if descriptor.Type != "" || descriptor.Severity != "" || descriptor.Code != "" || descriptor.Text != "" {
			result = append(result, descriptor)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left, _ := json.Marshal(result[i])
		right, _ := json.Marshal(result[j])
		return bytes.Compare(left, right) < 0
	})
	return result
}

// providerRequirementObservations projects non-core provider requirement arrays
// only as operator read-only evidence. It never accepts or persists an answer,
// and it never participates in quote readiness or ProcurementHandling.
func providerRequirementObservations(checkout map[string]any) ([]agencydomain.ProviderNotice, []agencydomain.ManualSiteStep) {
	notices := make([]agencydomain.ProviderNotice, 0)
	steps := make([]agencydomain.ManualSiteStep, 0)
	seen := make(map[string]struct{})
	for _, root := range []string{"additional_requirements", "buyer_requirements", "requirements"} {
		raw, _ := checkout[root].([]any)
		for _, entry := range raw {
			object, _ := entry.(map[string]any)
			id := safeDescriptorValue(object["id"])
			kind := safeDescriptorValue(object["type"])
			responseType := safeDescriptorValue(object["response_type"])
			if responseType == "" {
				responseType = safeDescriptorValue(object["responseType"])
			}
			prompt := safePresentationText(object["prompt"], 1000)
			if prompt == "" {
				prompt = safePresentationText(object["label"], 1000)
			}
			if id == "" && kind == "" && responseType == "" && prompt == "" {
				continue
			}
			key := root + "\x00" + id + "\x00" + kind + "\x00" + responseType + "\x00" + prompt
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			stepKind, registered := requirementStepKind(id, kind, responseType)
			notices = append(notices, agencydomain.ProviderNotice{
				Source: "REQUIREMENT", Type: kind, Severity: responseType, Code: id,
				SafePath: root, Text: prompt, Presentation: "INTERNAL", Audience: "OPERATOR",
				Registered: registered,
			})
			stepCode := id
			if stepCode == "" {
				stepCode = kind
			}
			if stepCode == "" {
				stepCode = responseType
			}
			if stepCode != "" {
				steps = append(steps, agencydomain.ManualSiteStep{
					Resolution: agencydomain.ManualSiteStepResolution,
					Kind:       stepKind, Code: stepCode, SafePath: root,
				})
			}
		}
	}
	return notices, mergeManualSiteSteps(steps)
}

func requirementStepKind(values ...string) (string, bool) {
	combined := strings.ToLower(strings.Join(values, " "))
	switch {
	case strings.Contains(combined, "captcha"), strings.Contains(combined, "bot_challenge"):
		return "CAPTCHA", true
	case strings.Contains(combined, "3ds"), strings.Contains(combined, "three_ds"),
		strings.Contains(combined, "payment_authentication"):
		return "PAYMENT_AUTHENTICATION", true
	case strings.Contains(combined, "identity"), strings.Contains(combined, "verification"):
		return "IDENTITY_VERIFICATION", true
	case strings.Contains(combined, "password"), strings.Contains(combined, "credential"),
		strings.Contains(combined, "secret"), strings.Contains(combined, "otp"), strings.Contains(combined, "mfa"):
		return "CREDENTIAL_AT_MERCHANT_SITE", true
	default:
		return "UNREGISTERED_PROVIDER_STEP", false
	}
}

func providerPolicyLinks(shopDomain string, checkout map[string]any) []agencydomain.ProviderPolicyLink {
	seen := make(map[string]struct{})
	result := make([]agencydomain.ProviderPolicyLink, 0)
	for _, key := range []string{"policies", "policy_links", "links"} {
		raw, _ := checkout[key].([]any)
		for _, entry := range raw {
			object, _ := entry.(map[string]any)
			kind := ""
			for _, field := range []string{"type", "kind", "rel"} {
				if kind = safeDescriptorValue(object[field]); kind != "" {
					break
				}
			}
			rawURL := ""
			for _, field := range []string{"url", "href"} {
				if rawURL = stringValue(object[field]); rawURL != "" {
					break
				}
			}
			parsed, err := url.Parse(rawURL)
			if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), shopDomain) ||
				kind == "" || len(rawURL) > 2048 {
				continue
			}
			identity := strings.ToUpper(kind) + "\x00" + parsed.String()
			if _, duplicate := seen[identity]; duplicate {
				continue
			}
			seen[identity] = struct{}{}
			label := safePresentationText(object["label"], 200)
			if label == "" {
				label = safePresentationText(object["title"], 200)
			}
			result = append(result, agencydomain.ProviderPolicyLink{
				Kind: strings.ToUpper(kind), Label: label, URL: parsed.String(),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].URL < result[j].URL
	})
	return result
}

var providerCodeKinds = map[string]string{
	"extension_interaction_required":       "EXTENSION_INTERACTION",
	"redirect_to_checkout_required":        "REDIRECT_TO_CHECKOUT",
	"captcha_required":                     "CAPTCHA",
	"captcha_verification_required":        "CAPTCHA",
	"captcha_challenge_required":           "CAPTCHA",
	"bot_challenge_required":               "CAPTCHA",
	"identity_verification_required":       "IDENTITY_VERIFICATION",
	"buyer_identity_verification_required": "IDENTITY_VERIFICATION",
	"three_ds_required":                    "PAYMENT_AUTHENTICATION",
	"3ds_required":                         "PAYMENT_AUTHENTICATION",
	"payment_authentication_required":      "PAYMENT_AUTHENTICATION",
	"address_undeliverable":                "SHIPPING_ADDRESS",
	"delivery_address_invalid":             "SHIPPING_ADDRESS",
	"shipping_address_invalid":             "SHIPPING_ADDRESS",
	"invalid_shipping_address":             "SHIPPING_ADDRESS",
	"postal_code_invalid":                  "SHIPPING_ADDRESS",
	"phone_number_invalid":                 "SHIPPING_ADDRESS",
	"out_of_stock":                         "AVAILABILITY",
	"item_unavailable":                     "AVAILABILITY",
	"product_unavailable":                  "AVAILABILITY",
	"variant_unavailable":                  "AVAILABILITY",
	"sold_out":                             "AVAILABILITY",
}

var customerCorrectionCodes = map[string]struct{}{
	"address_undeliverable": {}, "delivery_address_invalid": {},
	"shipping_address_invalid": {}, "invalid_shipping_address": {},
	"postal_code_invalid": {}, "phone_number_invalid": {},
}

var impossibleCodes = map[string]struct{}{
	"out_of_stock": {}, "item_unavailable": {}, "product_unavailable": {},
	"variant_unavailable": {}, "sold_out": {},
}

func registeredProviderCode(code string) bool {
	_, ok := providerCodeKinds[strings.ToLower(strings.TrimSpace(code))]
	return ok
}

func noticePresentation(messageType, severity, code string) (string, string) {
	combined := strings.ToLower(messageType + " " + severity + " " + code)
	if strings.Contains(combined, "disclosure") {
		return "DISCLOSURE", "CUSTOMER_AND_OPERATOR"
	}
	if strings.Contains(combined, "warning") || strings.Contains(combined, "notice") ||
		strings.Contains(combined, "info") || strings.Contains(combined, "review") {
		return "NOTICE", "CUSTOMER_AND_OPERATOR"
	}
	return "INTERNAL", "OPERATOR"
}

func procurementHandling(status string, descriptors []providerMessageDescriptor) string {
	handling := agencydomain.ProcurementHandlingNormal
	for _, descriptor := range descriptors {
		code := strings.ToLower(strings.TrimSpace(descriptor.Code))
		if _, found := customerCorrectionCodes[code]; found {
			return agencydomain.ProcurementHandlingCustomerCorrection
		}
		if _, found := impossibleCodes[code]; found || strings.EqualFold(descriptor.Severity, "unrecoverable") {
			return agencydomain.ProcurementHandlingImpossible
		}
		if requiresOperatorStep(descriptor) {
			handling = agencydomain.ProcurementHandlingOperatorLater
		}
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "canceled", "cancelled", "completed", "complete_in_progress":
		return agencydomain.ProcurementHandlingImpossible
	case "ready_for_complete", "ready", "":
		return handling
	default:
		return agencydomain.ProcurementHandlingOperatorLater
	}
}

func requiresOperatorStep(descriptor providerMessageDescriptor) bool {
	if strings.TrimSpace(descriptor.Code) != "" || strings.EqualFold(strings.TrimSpace(descriptor.Type), "error") {
		return true
	}
	severity := strings.ToLower(strings.TrimSpace(descriptor.Severity))
	return strings.Contains(severity, "requires_") || strings.Contains(severity, "recoverable") ||
		strings.Contains(severity, "retry")
}

func manualSiteStepsForDescriptors(descriptors []providerMessageDescriptor) []agencydomain.ManualSiteStep {
	steps := make([]agencydomain.ManualSiteStep, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if !requiresOperatorStep(descriptor) {
			continue
		}
		code := strings.ToLower(strings.TrimSpace(descriptor.Code))
		kind := providerCodeKinds[code]
		if _, correction := customerCorrectionCodes[code]; correction {
			continue
		}
		if _, impossible := impossibleCodes[code]; impossible {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(descriptor.Severity), "unrecoverable") {
			continue
		}
		if kind == "" {
			kind = "UNREGISTERED_PROVIDER_STEP"
		}
		steps = append(steps, agencydomain.ManualSiteStep{
			Resolution: agencydomain.ManualSiteStepResolution,
			Kind:       kind, Code: descriptor.Code, SafePath: descriptor.SafePath,
		})
	}
	return mergeManualSiteSteps(steps)
}

func mergeManualSiteSteps(groups ...[]agencydomain.ManualSiteStep) []agencydomain.ManualSiteStep {
	seen := make(map[string]struct{})
	result := make([]agencydomain.ManualSiteStep, 0)
	for _, group := range groups {
		for _, step := range group {
			key := step.Resolution + "\x00" + step.Kind + "\x00" + step.Code + "\x00" + step.SafePath
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, step)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i].Kind + "\x00" + result[i].Code + "\x00" + result[i].SafePath
		right := result[j].Kind + "\x00" + result[j].Code + "\x00" + result[j].SafePath
		return left < right
	})
	return result
}

func safePresentationText(value any, maxRunes int) string {
	raw := strings.TrimSpace(stringValue(value))
	if raw == "" || !utf8.ValidString(raw) || utf8.RuneCountInString(raw) > maxRunes {
		return ""
	}
	for _, char := range raw {
		if char < 0x20 && char != '\n' && char != '\r' && char != '\t' {
			return ""
		}
	}
	return raw
}

func safeDescriptorValue(value any) string {
	raw := strings.TrimSpace(stringValue(value))
	if raw == "" || len(raw) > 128 {
		return ""
	}
	for _, char := range raw {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') &&
			!(char >= '0' && char <= '9') && char != '_' && char != '-' && char != '.' {
			return ""
		}
	}
	return raw
}

func safeMessagePath(value any) string {
	switch typed := value.(type) {
	case string:
		return safeDescriptorValue(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			part := safeDescriptorValue(item)
			if part == "" {
				return ""
			}
			parts = append(parts, part)
		}
		return strings.Join(parts, ".")
	default:
		return ""
	}
}

func quoteReadiness(
	stable bool,
	totalsErr error,
	summaryErr error,
	structureErr error,
) string {
	if !stable || totalsErr != nil || summaryErr != nil || structureErr != nil {
		return agencydomain.PricingUnsafe
	}
	return agencydomain.PricingConfirmed
}

func dutiesDisposition(totals []agencydomain.Total) string {
	for _, total := range totals {
		kind := strings.ToLower(total.Type)
		if (strings.Contains(kind, "dut") || strings.Contains(kind, "import") || strings.Contains(kind, "custom")) && total.AmountMinor != 0 {
			return "CHARGED_OR_CROSS_BORDER"
		}
	}
	return agencydomain.DutiesNoSignalAtPreflight
}

func validateCheckoutStructure(input agencyapp.MerchantRequest, checkout map[string]any) error {
	if currency := strings.ToUpper(stringValue(checkout["currency"])); currency != "" && currency != "USD" {
		return agencydomain.ErrInvalid
	}
	rawLines, _ := checkout["line_items"].([]any)
	if len(rawLines) == 0 || len(rawLines) != len(input.Lines) {
		return agencydomain.ErrInvalid
	}
	expected := make(map[string]int, len(input.Lines))
	for _, line := range input.Lines {
		if line.VariantID == "" || line.Quantity < 1 {
			return agencydomain.ErrInvalid
		}
		expected[line.VariantID] += line.Quantity
	}
	providerLineIDs := make(map[string]struct{}, len(rawLines))
	actual := make(map[string]int, len(rawLines))
	for _, rawLine := range rawLines {
		line, _ := rawLine.(map[string]any)
		lineID := stringValue(line["id"])
		itemID := stringValue(nestedObject(line, "item")["id"])
		quantity, err := exactInt64(line["quantity"])
		if lineID == "" || itemID == "" || err != nil || quantity < 1 {
			return agencydomain.ErrInvalid
		}
		if _, duplicate := providerLineIDs[lineID]; duplicate {
			return agencydomain.ErrInvalid
		}
		providerLineIDs[lineID] = struct{}{}
		actual[itemID] += int(quantity)
	}
	if len(expected) != len(actual) {
		return agencydomain.ErrInvalid
	}
	for variantID, quantity := range expected {
		if actual[variantID] != quantity {
			return agencydomain.ErrInvalid
		}
	}
	rawMethods, _ := nestedObject(checkout, "fulfillment")["methods"].([]any)
	if len(rawMethods) == 0 {
		return agencydomain.ErrInvalid
	}
	covered := make(map[string]bool, len(providerLineIDs))
	observedSelections := make(map[string]bool)
	for _, rawMethod := range rawMethods {
		method, _ := rawMethod.(map[string]any)
		methodID := stringValue(method["id"])
		if methodID == "" || !strings.EqualFold(stringValue(method["type"]), "shipping") {
			return agencydomain.ErrInvalid
		}
		observedSelections[methodID] = true
		lineIDs, _ := method["line_item_ids"].([]any)
		if len(lineIDs) == 0 {
			return agencydomain.ErrInvalid
		}
		for _, rawID := range lineIDs {
			lineID := stringValue(rawID)
			if _, exists := providerLineIDs[lineID]; !exists {
				return agencydomain.ErrInvalid
			}
			covered[lineID] = true
		}
		if rawGroups, ok := method["groups"].([]any); ok {
			for _, rawGroup := range rawGroups {
				group, _ := rawGroup.(map[string]any)
				if selected := stringValue(group["selected_option_id"]); selected != "" {
					observedSelections[selected] = true
				}
			}
		}
	}
	for lineID := range providerLineIDs {
		if !covered[lineID] {
			return agencydomain.ErrInvalid
		}
	}
	for _, group := range input.Existing.DeliveryGroups {
		if group.SelectedOptionRef == "" || !observedSelections[group.SelectedOptionRef] {
			return agencydomain.ErrInvalid
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, stringValue(checkout["expires_at"])); err != nil {
		return agencydomain.ErrInvalid
	}
	return nil
}

func latestContinueURL(current string, checkout map[string]any) string {
	if candidate := stringValue(checkout["continue_url"]); candidate != "" {
		return candidate
	}
	return current
}

func safeContinueURL(raw string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", "", false
	}
	parsed.Fragment = ""
	normalized := parsed.String()
	sum := sha256.Sum256([]byte(normalized))
	return normalized, hex.EncodeToString(sum[:]), true
}

func checkoutPricingFingerprint(value map[string]any) string {
	totals, err := mapTotals(value["totals"])
	if err != nil {
		return "invalid-totals"
	}
	type fingerprintLine struct {
		ID        string `json:"id"`
		VariantID string `json:"variantId"`
		Quantity  int64  `json:"quantity"`
	}
	type fingerprintGroup struct {
		ID               string   `json:"id"`
		LineIDs          []string `json:"lineIds"`
		SelectedOptionID string   `json:"selectedOptionId"`
	}
	type fingerprintMethod struct {
		ID      string             `json:"id"`
		Type    string             `json:"type"`
		LineIDs []string           `json:"lineIds"`
		Groups  []fingerprintGroup `json:"groups"`
	}
	lines := make([]fingerprintLine, 0)
	if rawLines, ok := value["line_items"].([]any); ok {
		for _, rawLine := range rawLines {
			line, _ := rawLine.(map[string]any)
			quantity, _ := exactInt64(line["quantity"])
			lines = append(lines, fingerprintLine{
				ID: stringValue(line["id"]), VariantID: stringValue(nestedObject(line, "item")["id"]), Quantity: quantity,
			})
		}
	}
	sort.Slice(lines, func(i, j int) bool {
		return lines[i].ID+lines[i].VariantID < lines[j].ID+lines[j].VariantID
	})
	methods := make([]fingerprintMethod, 0)
	if rawMethods, ok := nestedObject(value, "fulfillment")["methods"].([]any); ok {
		for _, rawMethod := range rawMethods {
			method, _ := rawMethod.(map[string]any)
			entry := fingerprintMethod{ID: stringValue(method["id"]), Type: stringValue(method["type"])}
			if rawIDs, ok := method["line_item_ids"].([]any); ok {
				for _, rawID := range rawIDs {
					entry.LineIDs = append(entry.LineIDs, stringValue(rawID))
				}
			}
			sort.Strings(entry.LineIDs)
			if rawGroups, ok := method["groups"].([]any); ok {
				for _, rawGroup := range rawGroups {
					group, _ := rawGroup.(map[string]any)
					fingerprint := fingerprintGroup{
						ID:               stringValue(group["id"]),
						SelectedOptionID: stringValue(group["selected_option_id"]),
					}
					if rawIDs, ok := group["line_item_ids"].([]any); ok {
						for _, rawID := range rawIDs {
							fingerprint.LineIDs = append(fingerprint.LineIDs, stringValue(rawID))
						}
					}
					sort.Strings(fingerprint.LineIDs)
					entry.Groups = append(entry.Groups, fingerprint)
				}
				sort.Slice(entry.Groups, func(i, j int) bool { return entry.Groups[i].ID < entry.Groups[j].ID })
			}
			methods = append(methods, entry)
		}
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].ID < methods[j].ID })
	hash, _ := shareddomain.CanonicalJSONHash(struct {
		Currency string               `json:"currency"`
		Lines    []fingerprintLine    `json:"lines"`
		Methods  []fingerprintMethod  `json:"methods"`
		Totals   []agencydomain.Total `json:"totals"`
	}{strings.ToUpper(stringValue(value["currency"])), lines, methods, totals})
	return hash
}
func deterministicUUID(value string) string {
	sum := sha256.Sum256([]byte(value))
	raw := hex.EncodeToString(sum[:16])
	return raw[:8] + "-" + raw[8:12] + "-4" + raw[13:16] + "-a" + raw[17:20] + "-" + raw[20:32]
}
