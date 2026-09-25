package shopifystorefront

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

const storefrontProductIdentityQueryV2 = `query CandidateIdentity(
  $handle: String!
  $country: CountryCode
) @inContext(country: $country) {
  shop { id primaryDomain { host url } }
  product(handle: $handle) { id handle title onlineStoreUrl }
}`

const storefrontVariantIdentityQueryV2 = `query CandidateVariantIdentity(
  $handle: String!
  $variantId: ID!
  $country: CountryCode
) @inContext(country: $country) {
  shop { id primaryDomain { host url } }
  product(handle: $handle) { id handle title onlineStoreUrl }
  node(id: $variantId) {
    ... on ProductVariant { id product { id handle } }
  }
}`

var storefrontHandlePatternV2 = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

type storefrontIdentityInputV2 struct {
	IncludeVariants       bool
	Currency              string
	MyshopifyDomain       string
	ProductHandle         string
	VariantID             string
	Country               string
	ProviderCallAdmission researchapp.CatalogProviderCallAdmissionV2
}

type storefrontIdentityV2 struct {
	FirstPage       *researchapp.StorefrontVariantPageV2
	ShopID          string
	PrimaryDomain   string
	ProductID       string
	ProductHandle   string
	ProductTitle    string
	ProductURL      string
	VerifiedVariant string
}

type identityGraphQLRequestV2 struct {
	Query     string                     `json:"query"`
	Variables identityGraphQLVariablesV2 `json:"variables"`
}

type identityGraphQLVariablesV2 struct {
	Handle    string `json:"handle"`
	Country   string `json:"country"`
	VariantID string `json:"variantId,omitempty"`
}

type identityGraphQLResponseV2 struct {
	Data   *identityGraphQLDataV2 `json:"data"`
	Errors []graphQLErrorV2       `json:"errors"`
}

type identityGraphQLDataV2 struct {
	Shop    *graphQLShopV2            `json:"shop"`
	Product *identityGraphQLProductV2 `json:"product"`
	Node    *identityGraphQLVariantV2 `json:"node"`
}

type identityGraphQLProductV2 struct {
	Options        []graphQLProductOptionV2    `json:"options"`
	Variants       *graphQLVariantConnectionV2 `json:"variants"`
	ID             string                      `json:"id"`
	Handle         string                      `json:"handle"`
	Title          string                      `json:"title"`
	OnlineStoreURL *string                     `json:"onlineStoreUrl"`
}

type identityGraphQLVariantV2 struct {
	ID      string `json:"id"`
	Product struct {
		ID     string `json:"id"`
		Handle string `json:"handle"`
	} `json:"product"`
}

func (client *ClientV2) resolveIdentityV2(
	ctx context.Context,
	input storefrontIdentityInputV2,
) (storefrontIdentityV2, error) {
	input.MyshopifyDomain = strings.ToLower(strings.TrimSpace(input.MyshopifyDomain))
	input.ProductHandle = strings.TrimSpace(input.ProductHandle)
	input.VariantID = strings.TrimSpace(input.VariantID)
	input.Country = strings.ToUpper(strings.TrimSpace(input.Country))
	if client == nil || client.httpClient == nil ||
		input.ProviderCallAdmission == nil ||
		!strings.HasSuffix(input.MyshopifyDomain, ".myshopify.com") ||
		!storefrontHandlePatternV2.MatchString(input.ProductHandle) ||
		len(input.Country) != 2 ||
		(input.VariantID != "" && !validShopifyNumericGIDV2(input.VariantID, "ProductVariant")) {
		return storefrontIdentityV2{}, fault.New(
			fault.InvalidInput, researchapp.VariantChoiceFailureInvalidV2, false,
		)
	}
	query := storefrontProductIdentityQueryV2
	if input.VariantID != "" {
		query = storefrontVariantIdentityQueryV2
	}
	if input.IncludeVariants {
		query = strings.Replace(query, "product(handle: $handle) { id handle title onlineStoreUrl }", `product(handle: $handle) {
   id handle title onlineStoreUrl
   options { name optionValues { name } }
   variants(first: 20) {
    nodes {id title availableForSale selectedOptions {name value} price {amount currencyCode} image {url altText}}
    pageInfo {hasNextPage endCursor}
   }
  }`, 1)
	}
	payload, err := json.Marshal(identityGraphQLRequestV2{
		Query: query,
		Variables: identityGraphQLVariablesV2{
			Handle: input.ProductHandle, Country: input.Country,
			VariantID: input.VariantID,
		},
	})
	if err != nil {
		return storefrontIdentityV2{}, storefrontInternalFailureV2(err)
	}
	endpoint := "https://" + input.MyshopifyDomain + "/api/" + client.apiVersion + "/graphql.json"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return storefrontIdentityV2{}, storefrontInternalFailureV2(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Vitlane-Agent/catalog")
	if client.storefrontAccessToken != "" {
		request.Header.Set("X-Shopify-Storefront-Access-Token", client.storefrontAccessToken)
	}
	if client.agentAuthorizer != nil {
		if err := client.agentAuthorizer.AuthorizeAgentRequest(ctx, request); err != nil {
			return storefrontIdentityV2{}, fault.Wrap(
				err, fault.ProviderRejected,
				string(researchapp.CatalogFailureSecurityRejected), false,
			)
		}
	}
	if !identityRequestBodyUnchangedV2(request, payload) ||
		!safeStorefrontAgentRequestV2(request, input.MyshopifyDomain, client.apiVersion) {
		return storefrontIdentityV2{}, fault.New(
			fault.ProviderRejected, string(researchapp.CatalogFailureSecurityRejected), false,
		)
	}
	release, err := input.ProviderCallAdmission.AcquireCatalogProviderCall(ctx)
	if err != nil {
		return storefrontIdentityV2{}, err
	}
	if release == nil {
		return storefrontIdentityV2{}, fault.New(
			fault.InternalFailure, "STOREFRONT_CALL_ADMISSION_INVALID", false,
		)
	}
	response, err := sharedhttpclient.Do(ctx, client.httpClient, request, sharedhttpclient.ReadOnly)
	release()
	if err != nil {
		return storefrontIdentityV2{}, remapStorefrontTransportV2(err)
	}
	defer response.Body.Close()
	body, err := sharedhttpclient.ReadBody(response.Body, client.maxResponseBytes)
	if err != nil {
		if errors.Is(err, sharedhttpclient.ErrResponseTooLarge) {
			return storefrontIdentityV2{}, fault.New(
				fault.ProviderRejected, string(researchapp.CatalogFailureResponseTooLarge), false,
			)
		}
		return storefrontIdentityV2{}, fault.Wrap(
			err, fault.ProviderUnavailable, string(researchapp.CatalogFailureUnavailable), true,
		)
	}
	if response.StatusCode != http.StatusOK {
		return storefrontIdentityV2{}, classifyStorefrontHTTPV2(
			response.StatusCode, response.Header.Get("Retry-After"),
		)
	}
	var decoded identityGraphQLResponseV2
	if err := json.Unmarshal(body, &decoded); err != nil {
		return storefrontIdentityV2{}, storefrontSchemaFailureV2()
	}
	if len(decoded.Errors) > 0 {
		return storefrontIdentityV2{}, classifyStorefrontGraphQLErrorsV2(decoded.Errors)
	}
	if decoded.Data == nil || decoded.Data.Shop == nil || decoded.Data.Product == nil {
		return storefrontIdentityV2{}, fault.New(
			fault.ProviderUnavailable, researchapp.VariantResolutionUnavailableV2, true,
		)
	}
	primaryDomain, ok := normalizedPrimaryDomainV2(decoded.Data.Shop.PrimaryDomain)
	product := decoded.Data.Product
	if !ok || !validShopifyNumericGIDV2(decoded.Data.Shop.ID, "Shop") ||
		!validShopifyNumericGIDV2(product.ID, "Product") ||
		product.Handle != input.ProductHandle || strings.TrimSpace(product.Title) == "" ||
		product.OnlineStoreURL == nil ||
		!validIdentityProductURLV2(
			*product.OnlineStoreURL, input.MyshopifyDomain, primaryDomain, input.ProductHandle,
		) {
		return storefrontIdentityV2{}, fault.New(
			fault.ProviderRejected, researchapp.VariantIdentityCorrelationMismatchV2, false,
		)
	}
	verifiedVariant := ""
	if input.VariantID != "" {
		if decoded.Data.Node == nil || decoded.Data.Node.ID != input.VariantID ||
			decoded.Data.Node.Product.ID != product.ID ||
			decoded.Data.Node.Product.Handle != product.Handle {
			return storefrontIdentityV2{}, fault.New(
				fault.ProviderRejected, researchapp.VariantIdentityCorrelationMismatchV2, false,
			)
		}
		verifiedVariant = input.VariantID
	}
	var firstPage *researchapp.StorefrontVariantPageV2
	if input.IncludeVariants {
		if product.Variants == nil {
			return storefrontIdentityV2{}, storefrontSchemaFailureV2()
		}
		page, err := normalizeStorefrontPageV2(researchapp.StorefrontVariantQueryV2{ExpectedShopID: decoded.Data.Shop.ID, ExpectedPrimaryDomain: primaryDomain, ExpectedProductID: product.ID, ProductHandle: product.Handle, Country: input.Country, Currency: input.Currency}, *decoded.Data.Shop, graphQLProductV2{ID: product.ID, Handle: product.Handle, Title: product.Title, OnlineStoreURL: product.OnlineStoreURL, Options: product.Options, Variants: *product.Variants})
		if err != nil {
			return storefrontIdentityV2{}, err
		}
		firstPage = &page
	}
	return storefrontIdentityV2{
		FirstPage: firstPage,
		ShopID:    decoded.Data.Shop.ID, PrimaryDomain: primaryDomain,
		ProductID: product.ID, ProductHandle: product.Handle,
		ProductTitle:    strings.TrimSpace(product.Title),
		ProductURL:      strings.TrimSpace(*product.OnlineStoreURL),
		VerifiedVariant: verifiedVariant,
	}, nil
}

func identityRequestBodyUnchangedV2(request *http.Request, expected []byte) bool {
	if request == nil || request.Body == nil {
		return false
	}
	actual, err := io.ReadAll(io.LimitReader(request.Body, int64(len(expected))+1))
	request.Body = io.NopCloser(bytes.NewReader(actual))
	return err == nil && bytes.Equal(actual, expected)
}

func validShopifyNumericGIDV2(value, kind string) bool {
	prefix := "gid://shopify/" + kind + "/"
	identifier := strings.TrimPrefix(strings.TrimSpace(value), prefix)
	if identifier == "" || prefix+identifier != strings.TrimSpace(value) {
		return false
	}
	for _, character := range identifier {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validIdentityProductURLV2(raw, myshopifyDomain, primaryDomain, handle string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" ||
		parsed.Opaque != "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return (host == myshopifyDomain || host == primaryDomain) &&
		strings.Trim(parsed.Path, "/") == "products/"+handle
}
