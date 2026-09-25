package shopifyucp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	agencyapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	agencydomain "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

const storefrontCartProjection = `
fragment VitlaneCartDelivery on Cart {
  id
  ... @defer {
    deliveryGroups(first: 20, withCarrierRates: true) {
      nodes {
        id
        cartLines(first: 50) { nodes { merchandise { ... on ProductVariant { id } } } }
        deliveryOptions { handle title estimatedCost { amount currencyCode } }
        selectedDeliveryOption { handle title estimatedCost { amount currencyCode } }
      }
    }
  }
}`

const storefrontCartCreateMutation = `mutation VitlaneCartCreate($input: CartInput!) {
  cartCreate(input: $input) { cart { id } userErrors { code } warnings { code } }
}`

const storefrontAddressMutation = `mutation VitlaneCartAddress($id: ID!, $addresses: [CartSelectableAddressInput!]!) {
  cartDeliveryAddressesAdd(cartId: $id, addresses: $addresses) {
    cart { ...VitlaneCartDelivery } userErrors { code } warnings { code }
  }
}` + storefrontCartProjection

const storefrontSelectDeliveryMutation = `mutation VitlaneCartDeliverySelection($id: ID!, $selections: [CartSelectedDeliveryOptionInput!]!) {
  cartSelectedDeliveryOptionsUpdate(cartId: $id, selectedDeliveryOptions: $selections) {
    cart { ...VitlaneCartDelivery } userErrors { code } warnings { code }
  }
}` + storefrontCartProjection

type storefrontGraphQLError struct {
	Message string `json:"message"`
}

type storefrontUserError struct {
	Code string `json:"code"`
}

type storefrontMoney struct {
	Amount       string `json:"amount"`
	CurrencyCode string `json:"currencyCode"`
}

type storefrontDeliveryOption struct {
	Handle        string          `json:"handle"`
	Title         string          `json:"title"`
	EstimatedCost storefrontMoney `json:"estimatedCost"`
}

type storefrontDeliveryGroup struct {
	ID        string `json:"id"`
	CartLines struct {
		Nodes []struct {
			Merchandise struct {
				ID string `json:"id"`
			} `json:"merchandise"`
		} `json:"nodes"`
	} `json:"cartLines"`
	DeliveryOptions        []storefrontDeliveryOption `json:"deliveryOptions"`
	SelectedDeliveryOption *storefrontDeliveryOption  `json:"selectedDeliveryOption"`
}

type storefrontCart struct {
	ID             string `json:"id"`
	DeliveryGroups struct {
		Nodes []storefrontDeliveryGroup `json:"nodes"`
	} `json:"deliveryGroups"`
}

func (g *Gateway) createStorefrontCart(ctx context.Context, input agencyapp.MerchantRequest) (storefrontCart, error) {
	lines := make([]map[string]any, 0, len(input.Lines))
	for _, line := range input.Lines {
		lines = append(lines, map[string]any{"merchandiseId": line.VariantID, "quantity": line.Quantity})
	}
	var created struct {
		CartCreate struct {
			Cart       *storefrontCart       `json:"cart"`
			UserErrors []storefrontUserError `json:"userErrors"`
		} `json:"cartCreate"`
	}
	// buyerIdentity에는 운영(대행) 연락 이메일만 싣는다 — Shopify는 checkout의
	// contact method를 이 cart buyerIdentity에서 검증한다(gateway.go buyer 주석
	// 참조). 고객 이메일·전화는 넣지 않는다.
	buyerIdentity := map[string]any{"countryCode": "US"}
	if email := strings.TrimSpace(g.config.BuyerContactEmail); email != "" {
		buyerIdentity["email"] = email
	}
	if err := g.storefrontGraphQL(ctx, input, storefrontCartCreateMutation, map[string]any{
		"input": map[string]any{"lines": lines, "buyerIdentity": buyerIdentity},
	}, &created); err != nil {
		return storefrontCart{}, err
	}
	if created.CartCreate.Cart == nil || created.CartCreate.Cart.ID == "" || len(created.CartCreate.UserErrors) > 0 {
		return storefrontCart{}, fault.New(fault.ProviderRejected, "SHOPIFY_STOREFRONT_CART_CREATE_REJECTED", false)
	}
	first, last := splitName(input.ShippingAddress.RecipientName)
	address := map[string]any{
		"firstName": first, "lastName": last,
		"address1": input.ShippingAddress.AddressLine1,
		"city":     input.ShippingAddress.City, "provinceCode": input.ShippingAddress.Region,
		"zip": input.ShippingAddress.PostalCode, "countryCode": "US",
	}
	if input.ShippingAddress.AddressLine2 != "" {
		address["address2"] = input.ShippingAddress.AddressLine2
	}
	if input.ShippingAddress.Phone != "" {
		address["phone"] = input.ShippingAddress.Phone
	}
	var addressed struct {
		CartDeliveryAddressesAdd struct {
			Cart       *storefrontCart       `json:"cart"`
			UserErrors []storefrontUserError `json:"userErrors"`
		} `json:"cartDeliveryAddressesAdd"`
	}
	if err := g.storefrontGraphQL(ctx, input, storefrontAddressMutation, map[string]any{
		"id": created.CartCreate.Cart.ID,
		"addresses": []any{map[string]any{"selected": true, "oneTimeUse": true,
			"address": map[string]any{"deliveryAddress": address}}},
	}, &addressed); err != nil {
		return storefrontCart{}, err
	}
	if addressed.CartDeliveryAddressesAdd.Cart == nil || len(addressed.CartDeliveryAddressesAdd.UserErrors) > 0 {
		return storefrontCart{}, fault.New(fault.ProviderRejected, "SHOPIFY_STOREFRONT_ADDRESS_REJECTED", false)
	}
	return *addressed.CartDeliveryAddressesAdd.Cart, nil
}

func (g *Gateway) selectStorefrontDelivery(ctx context.Context, input agencyapp.MerchantRequest, rawCartID string, selections map[string]string) (storefrontCart, error) {
	values := make([]map[string]string, 0, len(selections))
	for groupID, optionID := range selections {
		values = append(values, map[string]string{"deliveryGroupId": groupID, "deliveryOptionHandle": optionID})
	}
	var selected struct {
		CartSelectedDeliveryOptionsUpdate struct {
			Cart       *storefrontCart       `json:"cart"`
			UserErrors []storefrontUserError `json:"userErrors"`
		} `json:"cartSelectedDeliveryOptionsUpdate"`
	}
	if err := g.storefrontGraphQL(ctx, input, storefrontSelectDeliveryMutation, map[string]any{
		"id": rawCartID, "selections": values,
	}, &selected); err != nil {
		return storefrontCart{}, err
	}
	if selected.CartSelectedDeliveryOptionsUpdate.Cart == nil || len(selected.CartSelectedDeliveryOptionsUpdate.UserErrors) > 0 {
		return storefrontCart{}, fault.New(fault.ProviderRejected, "SHOPIFY_DELIVERY_SELECTION_REJECTED", false)
	}
	return *selected.CartSelectedDeliveryOptionsUpdate.Cart, nil
}

func (g *Gateway) storefrontGraphQL(ctx context.Context, input agencyapp.MerchantRequest, query string, variables map[string]any, target any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	endpoint := "https://" + input.ShopDomain + "/api/" + g.config.StorefrontAPIVersion + "/graphql.json"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "multipart/mixed; deferSpec=20220824, application/json")
	response, err := sharedhttpclient.Do(ctx, g.config.HTTPClient, request, sharedhttpclient.ExternalEffect)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return sharedhttpclient.StatusFault(response.StatusCode, response.Header.Get("Retry-After"), false)
	}
	body, err := sharedhttpclient.ReadBody(response.Body, responseLimit)
	if err != nil {
		return err
	}
	data, graphQLErrors, err := decodeStorefrontResponse(response.Header.Get("Content-Type"), body)
	if err != nil || len(graphQLErrors) > 0 || len(data) == 0 {
		return fault.New(fault.ProviderRejected, "SHOPIFY_STOREFRONT_RESPONSE_INVALID", false)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fault.New(fault.ProviderRejected, "SHOPIFY_STOREFRONT_RESPONSE_INVALID", false)
	}
	return nil
}

type storefrontIncrementalPatch struct {
	Path   []any                    `json:"path"`
	Data   map[string]any           `json:"data"`
	Errors []storefrontGraphQLError `json:"errors"`
}

type storefrontResponseChunk struct {
	Data        map[string]any               `json:"data"`
	Errors      []storefrontGraphQLError     `json:"errors"`
	Incremental []storefrontIncrementalPatch `json:"incremental"`
}

func decodeStorefrontResponse(contentType string, body []byte) (json.RawMessage, []storefrontGraphQLError, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, nil, err
	}
	if mediaType != "multipart/mixed" {
		var chunk storefrontResponseChunk
		if err := json.Unmarshal(body, &chunk); err != nil {
			return nil, nil, err
		}
		data, err := json.Marshal(chunk.Data)
		return data, chunk.Errors, err
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, nil, fmt.Errorf("Shopify deferred response boundary missing")
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	root := map[string]any{}
	var graphQLErrors []storefrontGraphQLError
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, graphQLErrors, err
		}
		partBody, err := io.ReadAll(part)
		if err != nil {
			return nil, graphQLErrors, err
		}
		var chunk storefrontResponseChunk
		if err := json.Unmarshal(partBody, &chunk); err != nil {
			return nil, graphQLErrors, err
		}
		mergeStorefrontObject(root, chunk.Data)
		graphQLErrors = append(graphQLErrors, chunk.Errors...)
		for _, patch := range chunk.Incremental {
			graphQLErrors = append(graphQLErrors, patch.Errors...)
			if err := applyStorefrontPatch(root, patch.Path, patch.Data); err != nil {
				return nil, graphQLErrors, err
			}
		}
	}
	if len(root) == 0 {
		return nil, graphQLErrors, fmt.Errorf("Shopify deferred response data missing")
	}
	data, err := json.Marshal(root)
	return data, graphQLErrors, err
}

func applyStorefrontPatch(root map[string]any, path []any, patch map[string]any) error {
	if len(path) == 0 {
		mergeStorefrontObject(root, patch)
		return nil
	}
	cursor := root
	for _, component := range path {
		key, ok := component.(string)
		if !ok || strings.TrimSpace(key) == "" {
			return fmt.Errorf("Shopify deferred response path invalid")
		}
		next, found := cursor[key]
		if !found {
			child := map[string]any{}
			cursor[key] = child
			cursor = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("Shopify deferred response path type invalid")
		}
		cursor = child
	}
	mergeStorefrontObject(cursor, patch)
	return nil
}

func mergeStorefrontObject(target, source map[string]any) {
	for key, value := range source {
		if sourceObject, ok := value.(map[string]any); ok {
			if targetObject, found := target[key].(map[string]any); found {
				mergeStorefrontObject(targetObject, sourceObject)
				continue
			}
		}
		target[key] = value
	}
}

func mapStorefrontCart(input agencyapp.MerchantRequest, cart storefrontCart, safeRef string) (agencydomain.MerchantCheckout, error) {
	lineByVariant := make(map[string]string, len(input.Lines))
	lineRefs := make([]string, 0, len(input.Lines))
	for _, line := range input.Lines {
		lineByVariant[line.VariantID] = line.LineID
		lineRefs = append(lineRefs, line.LineID)
	}
	groups := make([]agencydomain.DeliveryGroup, 0, len(cart.DeliveryGroups.Nodes))
	flat := make([]agencydomain.DeliveryOption, 0)
	for _, source := range cart.DeliveryGroups.Nodes {
		group := agencydomain.DeliveryGroup{ID: source.ID}
		for _, cartLine := range source.CartLines.Nodes {
			if lineID := lineByVariant[cartLine.Merchandise.ID]; lineID != "" {
				group.LineRefs = append(group.LineRefs, lineID)
			}
		}
		for _, sourceOption := range source.DeliveryOptions {
			minor, err := storefrontUSDMinor(sourceOption.EstimatedCost)
			if err != nil || sourceOption.Handle == "" {
				return agencydomain.MerchantCheckout{}, fault.New(fault.ProviderRejected, "SHOPIFY_DELIVERY_RESPONSE_INVALID", false)
			}
			option := agencydomain.DeliveryOption{ID: sourceOption.Handle, Title: sourceOption.Title, AmountMinor: minor, Currency: "USD"}
			group.Options = append(group.Options, option)
			flat = append(flat, option)
		}
		if source.SelectedDeliveryOption != nil {
			group.SelectedOptionRef = source.SelectedDeliveryOption.Handle
		} else if len(group.Options) == 1 {
			group.SelectedOptionRef = group.Options[0].ID
		}
		if group.ID == "" || len(group.LineRefs) == 0 || len(group.Options) == 0 {
			return agencydomain.MerchantCheckout{}, fault.New(fault.ProviderRejected, "SHOPIFY_DELIVERY_RESPONSE_INVALID", false)
		}
		groups = append(groups, group)
	}
	if len(groups) == 0 {
		return agencydomain.MerchantCheckout{}, fault.New(fault.ProviderRejected, "SHOPIFY_DELIVERY_OPTIONS_UNAVAILABLE", false)
	}
	return agencydomain.MerchantCheckout{
		MerchantID: input.ShopDomain, ShopDomain: input.ShopDomain,
		StorefrontCartSafeRef: safeRef, LineRefs: lineRefs,
		DeliveryGroups: groups, DeliveryOptions: flat, ProviderStatus: "cart_ready",
		QuoteReadiness:      agencydomain.PricingEstimated,
		ProcurementHandling: agencydomain.ProcurementHandlingNormal,
	}, nil
}

func storefrontUSDMinor(value storefrontMoney) (int64, error) {
	if value.CurrencyCode != "USD" {
		return 0, fmt.Errorf("unsupported storefront currency")
	}
	parts := strings.Split(value.Amount, ".")
	if len(parts) > 2 || len(parts) == 0 {
		return 0, fmt.Errorf("invalid storefront amount")
	}
	dollars, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || dollars < 0 {
		return 0, fmt.Errorf("invalid storefront amount")
	}
	cents := int64(0)
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) > 2 || fraction == "" {
			return 0, fmt.Errorf("invalid storefront amount")
		}
		if len(fraction) == 1 {
			fraction += "0"
		}
		cents, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid storefront amount")
		}
	}
	return dollars*100 + cents, nil
}
