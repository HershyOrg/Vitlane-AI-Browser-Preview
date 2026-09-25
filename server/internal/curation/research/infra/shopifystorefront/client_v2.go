package shopifystorefront

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

const (
	defaultStorefrontAPIVersionV2 = "2026-07"
	defaultStorefrontBodyLimitV2  = int64(2 << 20)
	maximumStorefrontBodyLimitV2  = int64(8 << 20)
	storefrontSecurityStatusV2    = 430
)

const candidateVariantsQueryV2 = `query CandidateVariants(
  $handle: String!
  $after: String
  $country: CountryCode
) @inContext(country: $country) {
  shop { id primaryDomain { host url } }
  product(handle: $handle) {
    id
    handle
    title
    onlineStoreUrl
    options { name optionValues { name } }
    variants(first: 20, after: $after) {
      nodes {
        id
        title
        availableForSale
        selectedOptions { name value }
        price { amount currencyCode }
        image { url altText }
      }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

type AgentRequestAuthorizerV2 interface {
	AuthorizeAgentRequest(context.Context, *http.Request) error
}

type ClientV2Config struct {
	HTTPClient            *http.Client
	APIVersion            string
	StorefrontAccessToken string
	AgentAuthorizer       AgentRequestAuthorizerV2
	MaxResponseBytes      int64
}

// ClientV2 is a dormant, read-only Storefront GraphQL adapter. Its endpoint
// always comes from reviewed *.myshopify.com evidence, never caller URL text.
type ClientV2 struct {
	httpClient            *http.Client
	apiVersion            string
	storefrontAccessToken string
	agentAuthorizer       AgentRequestAuthorizerV2
	maxResponseBytes      int64
}

var _ researchapp.StorefrontVariantGatewayV2 = (*ClientV2)(nil)

func NewClientV2(config ClientV2Config) (*ClientV2, error) {
	apiVersion := strings.TrimSpace(config.APIVersion)
	if apiVersion == "" {
		apiVersion = defaultStorefrontAPIVersionV2
	}
	if config.HTTPClient == nil || !validStorefrontAPIVersionV2(apiVersion) ||
		strings.ContainsAny(config.StorefrontAccessToken, "\r\n") {
		return nil, fault.New(fault.InvalidInput, "STOREFRONT_CONFIG_INVALID", false)
	}
	limit := config.MaxResponseBytes
	if limit == 0 {
		limit = defaultStorefrontBodyLimitV2
	}
	if limit < 1 || limit > maximumStorefrontBodyLimitV2 {
		return nil, fault.New(fault.InvalidInput, "STOREFRONT_CONFIG_INVALID", false)
	}
	// Redirects must not move a trusted myshopify request to an unreviewed host.
	httpClient := *config.HTTPClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &ClientV2{
		httpClient: &httpClient, apiVersion: apiVersion,
		storefrontAccessToken: strings.TrimSpace(config.StorefrontAccessToken),
		agentAuthorizer:       config.AgentAuthorizer, maxResponseBytes: limit,
	}, nil
}

func (client *ClientV2) LoadVariantPage(
	ctx context.Context,
	query researchapp.StorefrontVariantQueryV2,
) (researchapp.StorefrontVariantPageV2, error) {
	if client == nil || client.httpClient == nil || query.Validate() != nil {
		return researchapp.StorefrontVariantPageV2{}, fault.New(
			fault.InvalidInput, researchapp.VariantChoiceFailureInvalidV2, false,
		)
	}
	if query.ProviderCallAdmission == nil {
		return researchapp.StorefrontVariantPageV2{}, fault.New(
			fault.InvalidInput, researchapp.VariantChoiceFailureInvalidV2, false,
		)
	}
	payload, err := json.Marshal(graphQLRequestV2{
		Query: candidateVariantsQueryV2,
		Variables: graphQLVariablesV2{
			Handle:  query.ProductHandle,
			After:   nullableCursorV2(query.AfterCursor),
			Country: query.Country,
		},
	})
	if err != nil {
		return researchapp.StorefrontVariantPageV2{}, storefrontInternalFailureV2(err)
	}
	endpoint := fmt.Sprintf(
		"https://%s/api/%s/graphql.json",
		query.MyshopifyDomain,
		client.apiVersion,
	)
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, bytes.NewReader(payload),
	)
	if err != nil {
		return researchapp.StorefrontVariantPageV2{}, storefrontInternalFailureV2(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Vitlane-Agent/catalog")
	if client.storefrontAccessToken != "" {
		request.Header.Set(
			"X-Shopify-Storefront-Access-Token",
			client.storefrontAccessToken,
		)
	}
	if client.agentAuthorizer != nil {
		if err := client.agentAuthorizer.AuthorizeAgentRequest(ctx, request); err != nil {
			return researchapp.StorefrontVariantPageV2{}, fault.Wrap(
				err, fault.ProviderRejected,
				string(researchapp.CatalogFailureSecurityRejected), false,
			)
		}
	}
	if !storefrontRequestBodyUnchangedV2(request, payload) ||
		!safeStorefrontAgentRequestV2(request, query.MyshopifyDomain, client.apiVersion) {
		return researchapp.StorefrontVariantPageV2{}, fault.New(
			fault.ProviderRejected,
			string(researchapp.CatalogFailureSecurityRejected), false,
		)
	}

	release, err := query.ProviderCallAdmission.AcquireCatalogProviderCall(ctx)
	if err != nil {
		return researchapp.StorefrontVariantPageV2{}, err
	}
	if release == nil {
		return researchapp.StorefrontVariantPageV2{}, fault.New(
			fault.InternalFailure, "STOREFRONT_CALL_ADMISSION_INVALID", false,
		)
	}
	response, err := sharedhttpclient.Do(
		ctx, client.httpClient, request, sharedhttpclient.ReadOnly,
	)
	release()
	if err != nil {
		return researchapp.StorefrontVariantPageV2{}, remapStorefrontTransportV2(err)
	}
	defer response.Body.Close()
	body, err := sharedhttpclient.ReadBody(response.Body, client.maxResponseBytes)
	if err != nil {
		if errors.Is(err, sharedhttpclient.ErrResponseTooLarge) {
			return researchapp.StorefrontVariantPageV2{}, fault.New(
				fault.ProviderRejected,
				string(researchapp.CatalogFailureResponseTooLarge), false,
			)
		}
		return researchapp.StorefrontVariantPageV2{}, fault.Wrap(
			err, fault.ProviderUnavailable,
			string(researchapp.CatalogFailureUnavailable), true,
		)
	}
	if response.StatusCode != http.StatusOK {
		return researchapp.StorefrontVariantPageV2{}, classifyStorefrontHTTPV2(
			response.StatusCode, response.Header.Get("Retry-After"),
		)
	}
	var decoded graphQLResponseV2
	if err := json.Unmarshal(body, &decoded); err != nil {
		return researchapp.StorefrontVariantPageV2{}, storefrontSchemaFailureV2()
	}
	if len(decoded.Errors) > 0 {
		return researchapp.StorefrontVariantPageV2{}, classifyStorefrontGraphQLErrorsV2(
			decoded.Errors,
		)
	}
	if decoded.Data == nil || decoded.Data.Shop == nil {
		return researchapp.StorefrontVariantPageV2{}, storefrontSchemaFailureV2()
	}
	if decoded.Data.Product == nil {
		return researchapp.StorefrontVariantPageV2{}, fault.New(
			fault.ProviderUnavailable,
			researchapp.VariantResolutionUnavailableV2, true,
		)
	}
	return normalizeStorefrontPageV2(query, *decoded.Data.Shop, *decoded.Data.Product)
}

func storefrontRequestBodyUnchangedV2(request *http.Request, expected []byte) bool {
	if request == nil || request.Body == nil {
		return false
	}
	actual, err := io.ReadAll(io.LimitReader(request.Body, int64(len(expected))+1))
	request.Body = io.NopCloser(bytes.NewReader(actual))
	return err == nil && bytes.Equal(actual, expected)
}

type graphQLRequestV2 struct {
	Query     string             `json:"query"`
	Variables graphQLVariablesV2 `json:"variables"`
}

type graphQLVariablesV2 struct {
	Handle  string  `json:"handle"`
	After   *string `json:"after"`
	Country string  `json:"country"`
}

type graphQLResponseV2 struct {
	Data   *graphQLDataV2   `json:"data"`
	Errors []graphQLErrorV2 `json:"errors"`
}

type graphQLDataV2 struct {
	Shop    *graphQLShopV2    `json:"shop"`
	Product *graphQLProductV2 `json:"product"`
}

type graphQLShopV2 struct {
	ID            string                 `json:"id"`
	PrimaryDomain graphQLPrimaryDomainV2 `json:"primaryDomain"`
}

type graphQLPrimaryDomainV2 struct {
	Host string `json:"host"`
	URL  string `json:"url"`
}

type graphQLProductV2 struct {
	ID             string                     `json:"id"`
	Handle         string                     `json:"handle"`
	Title          string                     `json:"title"`
	OnlineStoreURL *string                    `json:"onlineStoreUrl"`
	Options        []graphQLProductOptionV2   `json:"options"`
	Variants       graphQLVariantConnectionV2 `json:"variants"`
}

type graphQLProductOptionV2 struct {
	Name         string                        `json:"name"`
	OptionValues []graphQLProductOptionValueV2 `json:"optionValues"`
}

type graphQLProductOptionValueV2 struct {
	Name string `json:"name"`
}

type graphQLVariantConnectionV2 struct {
	Nodes    []graphQLVariantV2 `json:"nodes"`
	PageInfo graphQLPageInfoV2  `json:"pageInfo"`
}

type graphQLPageInfoV2 struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

type graphQLVariantV2 struct {
	ID               string                    `json:"id"`
	Title            string                    `json:"title"`
	AvailableForSale bool                      `json:"availableForSale"`
	SelectedOptions  []graphQLSelectedOptionV2 `json:"selectedOptions"`
	Price            graphQLMoneyV2            `json:"price"`
	Image            *graphQLImageV2           `json:"image"`
}

type graphQLSelectedOptionV2 struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type graphQLMoneyV2 struct {
	Amount       string `json:"amount"`
	CurrencyCode string `json:"currencyCode"`
}

type graphQLImageV2 struct {
	URL     string `json:"url"`
	AltText string `json:"altText"`
}

type graphQLErrorV2 struct {
	Message    string                   `json:"message"`
	Extensions graphQLErrorExtensionsV2 `json:"extensions"`
}

type graphQLErrorExtensionsV2 struct {
	Code string `json:"code"`
}

func normalizeStorefrontPageV2(
	query researchapp.StorefrontVariantQueryV2,
	shop graphQLShopV2,
	product graphQLProductV2,
) (researchapp.StorefrontVariantPageV2, error) {
	primaryDomain, ok := normalizedPrimaryDomainV2(shop.PrimaryDomain)
	if !ok || len(product.Variants.Nodes) > researchapp.VariantChoicePageSizeV2 ||
		(product.Variants.PageInfo.HasNextPage &&
			(product.Variants.PageInfo.EndCursor == nil ||
				strings.TrimSpace(*product.Variants.PageInfo.EndCursor) == "")) {
		return researchapp.StorefrontVariantPageV2{}, storefrontSchemaFailureV2()
	}
	if shop.ID != query.ExpectedShopID ||
		primaryDomain != query.ExpectedPrimaryDomain ||
		product.Handle != query.ProductHandle ||
		(query.ExpectedProductID != "" && product.ID != query.ExpectedProductID) {
		return researchapp.StorefrontVariantPageV2{}, fault.New(
			fault.ProviderRejected,
			researchapp.VariantPageCorrelationMismatchV2, false,
		)
	}
	if product.OnlineStoreURL == nil ||
		strings.TrimSpace(*product.OnlineStoreURL) == "" {
		return researchapp.StorefrontVariantPageV2{}, fault.New(
			fault.ProviderUnavailable,
			researchapp.VariantResolutionUnavailableV2, true,
		)
	}
	options := make([]researchapp.StorefrontProductOptionV2, len(product.Options))
	for index, option := range product.Options {
		values := make([]string, len(option.OptionValues))
		for valueIndex, value := range option.OptionValues {
			values[valueIndex] = value.Name
		}
		options[index] = researchapp.StorefrontProductOptionV2{
			Name: option.Name, Values: values,
		}
	}
	rows := make([]researchapp.StorefrontVariantRowV2, len(product.Variants.Nodes))
	for index, variant := range product.Variants.Nodes {
		price, err := shareddomain.NewMoney(
			variant.Price.Amount, variant.Price.CurrencyCode,
		)
		if err != nil || price.Sign() < 0 || string(price.Currency) != query.Currency {
			return researchapp.StorefrontVariantPageV2{}, storefrontSchemaFailureV2()
		}
		selected := make([]researchdomain.VariantOptionSelectionV2, len(variant.SelectedOptions))
		for optionIndex, option := range variant.SelectedOptions {
			selected[optionIndex] = researchdomain.VariantOptionSelectionV2{
				Name: option.Name, Value: option.Value,
			}
		}
		var media *researchapp.StorefrontVariantMediaV2
		if variant.Image != nil {
			if !validStorefrontMediaURLV2(variant.Image.URL) {
				return researchapp.StorefrontVariantPageV2{}, storefrontSchemaFailureV2()
			}
			media = &researchapp.StorefrontVariantMediaV2{
				URL: variant.Image.URL, AltText: variant.Image.AltText,
			}
		}
		rows[index] = researchapp.StorefrontVariantRowV2{
			VariantID: variant.ID, Title: variant.Title,
			SelectedOptions: selected, Price: price,
			AvailableForSale: variant.AvailableForSale, Media: media,
		}
	}
	endCursor := ""
	if product.Variants.PageInfo.EndCursor != nil {
		endCursor = strings.TrimSpace(*product.Variants.PageInfo.EndCursor)
	}
	return researchapp.StorefrontVariantPageV2{
		ShopID: shop.ID, PrimaryDomain: primaryDomain,
		ProductID: product.ID, ProductHandle: product.Handle,
		ProductTitle:   strings.TrimSpace(product.Title),
		ProductURL:     strings.TrimSpace(*product.OnlineStoreURL),
		ProductOptions: options, Rows: rows,
		HasNextPage: product.Variants.PageInfo.HasNextPage,
		EndCursor:   endCursor,
	}, nil
}

func normalizedPrimaryDomainV2(value graphQLPrimaryDomainV2) (string, bool) {
	host := strings.ToLower(strings.TrimSpace(value.Host))
	parsed, err := url.Parse(strings.TrimSpace(value.URL))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil ||
		parsed.Port() != "" || strings.ToLower(parsed.Hostname()) != host ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", false
	}
	return host, host != ""
}

func nullableCursorV2(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func safeStorefrontAgentRequestV2(
	request *http.Request,
	domain string,
	apiVersion string,
) bool {
	if request == nil || request.URL == nil || request.Method != http.MethodPost ||
		request.URL.Scheme != "https" || request.URL.User != nil ||
		strings.ToLower(request.URL.Hostname()) != domain || request.URL.Port() != "" ||
		(request.Host != "" && !strings.EqualFold(request.Host, request.URL.Host)) ||
		len(request.Header.Values("Host")) > 0 || request.URL.Opaque != "" ||
		request.URL.RawPath != "" ||
		request.URL.Path != "/api/"+apiVersion+"/graphql.json" ||
		request.URL.RawQuery != "" || request.URL.Fragment != "" ||
		request.Header.Get("Content-Type") != "application/json" {
		return false
	}
	for _, header := range []string{
		"Shopify-Storefront-Buyer-IP", "X-Shopify-Storefront-Buyer-IP",
		"X-Forwarded-For", "Forwarded", "True-Client-IP", "CF-Connecting-IP",
		"X-Real-IP", "Authorization", "Cookie", "X-Shopify-Customer-Access-Token",
	} {
		if len(request.Header.Values(header)) > 0 {
			return false
		}
	}
	return true
}

func validStorefrontMediaURLV2(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" &&
		parsed.User == nil
}

func validStorefrontAPIVersionV2(value string) bool {
	if len(value) != 7 || value[4] != '-' {
		return false
	}
	for index, current := range value {
		if index == 4 {
			continue
		}
		if current < '0' || current > '9' {
			return false
		}
	}
	return true
}

func classifyStorefrontGraphQLErrorsV2(errors []graphQLErrorV2) error {
	classification := ""
	for _, providerError := range errors {
		code := strings.ToUpper(strings.TrimSpace(providerError.Extensions.Code))
		current := "UNKNOWN"
		switch code {
		case "THROTTLED":
			current = "THROTTLED"
		case "INTERNAL_SERVER_ERROR":
			current = "INTERNAL"
		case "MAX_COMPLEXITY_EXCEEDED":
			current = "MAX_COMPLEXITY"
		case "ACCESS_DENIED":
			current = "ACCESS_DENIED"
		}
		if classification == "" {
			classification = current
		} else if classification != current {
			classification = "UNKNOWN"
		}
	}
	switch classification {
	case "THROTTLED":
		return fault.New(
			fault.RateLimited, string(researchapp.CatalogFailureRateLimited), true,
		)
	case "INTERNAL":
		return fault.New(
			fault.ProviderUnavailable,
			string(researchapp.CatalogFailureUnavailable), true,
		)
	case "MAX_COMPLEXITY":
		return fault.New(
			fault.ProviderRejected,
			string(researchapp.CatalogFailureValidation), false,
		)
	case "ACCESS_DENIED":
		return fault.New(
			fault.ProviderRejected,
			string(researchapp.CatalogFailureProfileOrAuth), false,
		)
	default:
		return fault.New(
			fault.ProviderRejected,
			string(researchapp.CatalogFailureProtocolRejected), false,
		)
	}
}

func classifyStorefrontHTTPV2(status int, retryAfter string) error {
	switch {
	case status == http.StatusTooManyRequests:
		failure := fault.New(
			fault.RateLimited, string(researchapp.CatalogFailureRateLimited), true,
		)
		failure.RetryAfter = sharedhttpclient.ParseRetryAfter(retryAfter, time.Now())
		return failure
	case status == storefrontSecurityStatusV2:
		return fault.New(
			fault.ProviderRejected,
			string(researchapp.CatalogFailureSecurityRejected), false,
		)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fault.New(
			fault.ProviderRejected,
			string(researchapp.CatalogFailureProfileOrAuth), false,
		)
	case status >= 500:
		return fault.New(
			fault.ProviderUnavailable,
			string(researchapp.CatalogFailureUnavailable), true,
		)
	default:
		return fault.New(
			fault.ProviderRejected,
			string(researchapp.CatalogFailureProtocolRejected), false,
		)
	}
}

func remapStorefrontTransportV2(err error) error {
	classified, ok := fault.As(err)
	if !ok {
		return fault.Wrap(
			err, fault.ProviderUnavailable,
			string(researchapp.CatalogFailureUnavailable), true,
		)
	}
	switch classified.Code {
	case fault.CallerCancelled:
		return err
	case fault.DeadlineExceeded:
		return fault.Wrap(
			err, fault.DeadlineExceeded,
			string(researchapp.CatalogFailureUnavailable), true,
		)
	default:
		return fault.Wrap(
			err, fault.ProviderUnavailable,
			string(researchapp.CatalogFailureUnavailable), true,
		)
	}
}

func storefrontSchemaFailureV2() error {
	return fault.New(
		fault.ProviderRejected,
		string(researchapp.CatalogFailureSchemaMismatch), false,
	)
}

func storefrontInternalFailureV2(err error) error {
	return fault.Wrap(err, fault.InternalFailure, "STOREFRONT_INTERNAL_FAILURE", false)
}
