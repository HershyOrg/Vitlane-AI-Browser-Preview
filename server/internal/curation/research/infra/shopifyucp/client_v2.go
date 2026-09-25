package shopifyucp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

const (
	Provider               = researchapp.CatalogProviderShopifyGlobalV2
	defaultResponseLimitV2 = int64(4 << 20)
	maximumResponseLimitV2 = int64(16 << 20)
)

type GatewayV2Config struct {
	Endpoint         string
	ProfileURL       string
	HTTPClient       *http.Client
	ExpectedVersion  string
	LookupBatchSize  int
	MaxResponseBytes int64
	Tokens           CatalogAccessTokenSourceV2
}

// CatalogAccessTokenSourceV2 keeps the Catalog adapter independent from the
// checkout product's concrete token cache. The composition root may share one
// Shopify agent token source across both adapters.
type CatalogAccessTokenSourceV2 interface {
	AccessToken(context.Context) (string, error)
	InvalidateAccessToken(string)
}

// GatewayV2 is intentionally not wired into the legacy Research service. It
// is the dormant Phase 8 provider-conformance adapter.
type GatewayV2 struct {
	endpoint         string
	profileURL       string
	httpClient       *http.Client
	expectedVersion  string
	lookupBatchSize  int
	maxResponseBytes int64
	tokens           CatalogAccessTokenSourceV2
	now              func() time.Time
	rateLimitMu      sync.Mutex
	providerRetryAt  time.Time
	nextRequestID    atomic.Int64
}

var _ researchapp.CatalogGatewayV2 = (*GatewayV2)(nil)
var _ researchapp.CatalogOfferLookupGatewayV2 = (*GatewayV2)(nil)

func NewGatewayV2(config GatewayV2Config) (*GatewayV2, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	profileURL := strings.TrimSpace(config.ProfileURL)
	if !validEndpointV2(endpoint) || !validProfileURLV2(profileURL) || config.HTTPClient == nil {
		return nil, fault.New(fault.InvalidInput, "SHOPIFY_V2_CONFIG_INVALID", false)
	}
	expectedVersion := strings.TrimSpace(config.ExpectedVersion)
	if expectedVersion == "" {
		expectedVersion = ucpCatalogVersionV2
	}
	batchSize := config.LookupBatchSize
	if batchSize == 0 {
		batchSize = researchapp.CatalogLookupDefaultBatchSize
	}
	if batchSize < 1 || batchSize > researchapp.CatalogLookupMaxBatchSize {
		return nil, fault.New(fault.InvalidInput, "SHOPIFY_V2_BATCH_SIZE_INVALID", false)
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = defaultResponseLimitV2
	}
	if maxResponseBytes < 1 || maxResponseBytes > maximumResponseLimitV2 {
		return nil, fault.New(fault.InvalidInput, "SHOPIFY_V2_RESPONSE_LIMIT_INVALID", false)
	}
	return &GatewayV2{
		endpoint: endpoint, profileURL: profileURL, httpClient: config.HTTPClient,
		expectedVersion: expectedVersion, lookupBatchSize: batchSize,
		maxResponseBytes: maxResponseBytes, tokens: config.Tokens, now: time.Now,
	}, nil
}

type catalogCallResultV2 struct {
	content    *wireStructuredContentV2
	outcome    researchapp.CatalogOutcome
	messages   []researchapp.CatalogProviderMessage
	requestRef string
}

func (gateway *GatewayV2) callCatalogV2(
	ctx context.Context,
	toolName string,
	catalog any,
	expectedCapability string,
) (catalogCallResultV2, error) {
	if err := gateway.providerRateLimitCooldownV2(); err != nil {
		return catalogCallResultV2{}, err
	}
	requestID := gateway.nextRequestID.Add(1)
	payload, err := json.Marshal(wireRPCRequestV2{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  "tools/call",
		Params: wireCallParamsV2{
			Name: toolName,
			Arguments: wireCallArgumentsV2{
				Meta: map[string]wireAgentProfileV2{
					"ucp-agent": {Profile: gateway.profileURL},
				},
				Catalog: catalog,
			},
		},
	})
	if err != nil {
		return catalogCallResultV2{}, fault.Wrap(
			err, fault.InternalFailure, "SHOPIFY_V2_REQUEST_ENCODE_FAILED", false,
		)
	}
	accessToken := ""
	if gateway.tokens != nil {
		accessToken, err = gateway.tokens.AccessToken(ctx)
		if err != nil {
			gateway.noteProviderRateLimitV2(err)
			return catalogCallResultV2{}, err
		}
	}
	var response catalogHTTPResponseV2
	for attempt := 0; attempt < 2; attempt++ {
		response, err = gateway.doCatalogRequestV2(ctx, payload, accessToken)
		if err != nil {
			return catalogCallResultV2{}, err
		}
		if response.status != http.StatusUnauthorized || gateway.tokens == nil || attempt > 0 {
			break
		}
		gateway.tokens.InvalidateAccessToken(accessToken)
		accessToken, err = gateway.tokens.AccessToken(ctx)
		if err != nil {
			gateway.noteProviderRateLimitV2(err)
			return catalogCallResultV2{}, err
		}
	}
	if response.status != http.StatusOK {
		classified := classifyHTTPFailureV2(response.status, response.retryAfter, response.body)
		gateway.noteProviderRateLimitV2(classified)
		return catalogCallResultV2{}, classified
	}

	var decoded wireRPCResponseV2
	if err := json.Unmarshal(response.body, &decoded); err != nil {
		return catalogCallResultV2{}, schemaMismatchV2()
	}
	if decoded.JSONRPC != "2.0" || !rpcIDMatchesV2(decoded.ID, requestID) ||
		(decoded.Result == nil) == (decoded.Error == nil) {
		return catalogCallResultV2{}, schemaMismatchV2()
	}
	if decoded.Error != nil {
		classified := classifyRPCFailureV2(decoded.Error)
		gateway.noteProviderRateLimitV2(classified)
		return catalogCallResultV2{}, classified
	}
	if decoded.Result.StructuredContent == nil {
		return catalogCallResultV2{}, schemaMismatchV2()
	}
	content := decoded.Result.StructuredContent
	outcome, messages, err := gateway.validateEnvelopeV2(content, expectedCapability)
	if err != nil {
		return catalogCallResultV2{}, err
	}
	return catalogCallResultV2{
		content: content, outcome: outcome, messages: messages,
		requestRef: "shopify-ucp-rpc:" + strconv.FormatInt(requestID, 10),
	}, nil
}

type catalogHTTPResponseV2 struct {
	status     int
	retryAfter string
	body       []byte
}

func (gateway *GatewayV2) doCatalogRequestV2(
	ctx context.Context,
	payload []byte,
	accessToken string,
) (catalogHTTPResponseV2, error) {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, gateway.endpoint, bytes.NewReader(payload),
	)
	if err != nil {
		return catalogHTTPResponseV2{}, fault.Wrap(
			err, fault.InternalFailure, "SHOPIFY_V2_REQUEST_CREATE_FAILED", false,
		)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	response, err := sharedhttpclient.Do(
		ctx, gateway.httpClient, request, sharedhttpclient.ReadOnly,
	)
	if err != nil {
		return catalogHTTPResponseV2{}, remapTransportErrorV2(err)
	}
	defer response.Body.Close()
	body, err := sharedhttpclient.ReadBody(response.Body, gateway.maxResponseBytes)
	if err != nil {
		if errors.Is(err, sharedhttpclient.ErrResponseTooLarge) {
			return catalogHTTPResponseV2{}, newCatalogFaultV2(
				fault.ProviderRejected, researchapp.CatalogFailureResponseTooLarge, false, 0,
			)
		}
		return catalogHTTPResponseV2{}, fault.Wrap(
			err, fault.ProviderUnavailable,
			string(researchapp.CatalogFailureUnavailable), true,
		)
	}
	return catalogHTTPResponseV2{
		status: response.StatusCode, retryAfter: response.Header.Get("Retry-After"), body: body,
	}, nil
}

func (gateway *GatewayV2) providerRateLimitCooldownV2() error {
	gateway.rateLimitMu.Lock()
	defer gateway.rateLimitMu.Unlock()
	now := gateway.now()
	if !gateway.providerRetryAt.After(now) {
		return nil
	}
	return newCatalogFaultV2(
		fault.RateLimited, researchapp.CatalogFailureRateLimited,
		true, gateway.providerRetryAt.Sub(now),
	)
}

func (gateway *GatewayV2) noteProviderRateLimitV2(err error) {
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.RateLimited {
		return
	}
	retryAfter := failure.RetryAfter
	if retryAfter <= 0 {
		retryAfter = time.Second
	}
	gateway.rateLimitMu.Lock()
	defer gateway.rateLimitMu.Unlock()
	retryAt := gateway.now().Add(retryAfter)
	if retryAt.After(gateway.providerRetryAt) {
		gateway.providerRetryAt = retryAt
	}
}

func (gateway *GatewayV2) validateEnvelopeV2(
	content *wireStructuredContentV2,
	expectedCapability string,
) (researchapp.CatalogOutcome, []researchapp.CatalogProviderMessage, error) {
	if strings.TrimSpace(content.UCP.Version) != gateway.expectedVersion ||
		!capabilityHasVersionV2(
			content.UCP.Capabilities, expectedCapability, gateway.expectedVersion,
		) {
		return "", nil, schemaMismatchV2()
	}
	messages, err := normalizeMessagesV2(content.Messages)
	if err != nil {
		return "", nil, err
	}
	if content.UCP.Status == nil || *content.UCP.Status == "success" {
		if content.Products == nil {
			return "", nil, schemaMismatchV2()
		}
		return researchapp.CatalogOutcomeSuccess, messages, nil
	}
	if *content.UCP.Status == "error" {
		if len(messages) == 0 {
			return "", nil, schemaMismatchV2()
		}
		return researchapp.CatalogOutcomeBusinessError, messages, nil
	}
	return "", nil, schemaMismatchV2()
}

func normalizeMessagesV2(
	messages []wireMessageV2,
) ([]researchapp.CatalogProviderMessage, error) {
	normalized := make([]researchapp.CatalogProviderMessage, 0, len(messages))
	for _, message := range messages {
		if strings.TrimSpace(message.Content) == "" ||
			(message.ContentType != "" && message.ContentType != "plain" &&
				message.ContentType != "markdown") {
			return nil, schemaMismatchV2()
		}
		switch message.Type {
		case "error":
			if strings.TrimSpace(message.Code) == "" || strings.TrimSpace(message.Severity) == "" {
				return nil, schemaMismatchV2()
			}
		case "warning":
			if strings.TrimSpace(message.Code) == "" {
				return nil, schemaMismatchV2()
			}
		case "info":
		default:
			return nil, schemaMismatchV2()
		}
		normalized = append(normalized, researchapp.CatalogProviderMessage{
			Type: message.Type, Code: message.Code, Path: message.Path,
			ContentType: message.ContentType, Content: message.Content,
			Severity: message.Severity, Presentation: message.Presentation,
			ImageURL: message.ImageURL, URL: message.URL,
		})
	}
	return normalized, nil
}

func capabilityHasVersionV2(
	capabilities map[string][]wireCapabilityV2,
	name string,
	version string,
) bool {
	for _, capability := range capabilities[name] {
		if capability.Version == version {
			return true
		}
	}
	return false
}

func rpcIDMatchesV2(raw json.RawMessage, expected int64) bool {
	return string(bytes.TrimSpace(raw)) == strconv.FormatInt(expected, 10)
}

func schemaMismatchV2() error {
	return newCatalogFaultV2(
		fault.ProviderRejected, researchapp.CatalogFailureSchemaMismatch, false, 0,
	)
}

func invalidCatalogRequestV2() error {
	return newCatalogFaultV2(
		fault.InvalidInput, researchapp.CatalogFailureRequestInvalid, false, 0,
	)
}

func validEndpointV2(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.Hostname() != "" && parsed.User == nil
}

func validProfileURLV2(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" &&
		parsed.User == nil
}
