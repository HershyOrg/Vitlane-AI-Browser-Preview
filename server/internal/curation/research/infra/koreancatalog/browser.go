package koreancatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

const browserDiscoveryAPIID = "BROWSER_MERCHANT"

type browserDiscoveryRequest struct {
	Query  string `json:"query"`
	Source string `json:"source"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type browserDiscoveryResult struct {
	Source    string `json:"source"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Context   string `json:"context"`
	PriceText string `json:"priceText,omitempty"`
}

type browserDiscoveryResponse struct {
	ProtocolVersion int                      `json:"protocolVersion"`
	Results         []browserDiscoveryResult `json:"results"`
	Coverage        struct {
		Source     string `json:"source"`
		Status     string `json:"status"`
		ReasonCode string `json:"reasonCode,omitempty"`
		Count      int    `json:"count"`
	} `json:"coverage"`
}

var browserWonPrice = regexp.MustCompile(`(?:₩\s*(?:[1-9][0-9]{2,8}|[1-9][0-9]{0,2}(?:,[0-9]{3})+)\s*원?|(?:[1-9][0-9]{0,2}(?:,[0-9]{3})+|[1-9][0-9]{2,8})\s*원)`)

// searchViaBrowser asks the local browser service to read one merchant's
// anonymous public search page. The returned DOM text is untrusted discovery
// evidence: the server derives the merchant identity from the URL, rebuilds
// its canonical URL and rejects everything outside the Korean mall registry.
func (g *Gateway) searchViaBrowser(
	ctx context.Context,
	mall researchdomain.KRMall,
	query string,
	limit int,
	offset int,
) (webSearch, error) {
	result := webSearch{Products: []researchdomain.ExternalProductObservation{}}
	if !mall.BrowserSearch {
		return result, safeFailure("CATALOG_API_INVALID")
	}
	limit = max(1, min(20, limit))
	response, err := g.browserDiscover(ctx, browserDiscoveryRequest{
		Query: query, Source: string(mall.Source), Limit: limit, Offset: max(0, min(400, offset)),
	})
	if err != nil {
		return result, err
	}
	result.DiscoveryCompleted = true
	result.Items = len(response.Results)
	seen := map[string]bool{}
	for _, item := range response.Results {
		if item.Source != string(mall.Source) {
			result.Rejected++
			continue
		}
		ref, refErr := researchdomain.SourceProductFromURL(item.URL)
		if refErr != nil || ref.Source != mall.Source || seen[ref.IdentityKey()] {
			result.Rejected++
			continue
		}
		canonical, canonicalErr := ref.ExternalProductURL()
		if canonicalErr != nil {
			result.Rejected++
			continue
		}
		title := cleanTitle(item.Title)
		if title == "" {
			result.Rejected++
			continue
		}
		seen[ref.IdentityKey()] = true
		price := researchdomain.VariantObservedPrice{
			Kind: "UNKNOWN", ReasonCode: "BROWSER_SEARCH_PRICE_NOT_REPORTED",
		}
		if token := browserWonPrice.FindString(strings.TrimSpace(item.PriceText)); token != "" {
			price = observedPrice(token, "KRW")
		}
		observation := researchdomain.ExternalProductObservation{
			SchemaVersion: "vitlane.external-product-observation.v1",
			ProductRef:    ref,
			ProductURL:    canonical,
			Title:         title,
			Description:   cleanTitle(item.Context),
			Price:         price,
			PriceScope:    "PRODUCT",
			Seller:        researchdomain.ObservedSeller{Kind: "UNKNOWN"},
			Provenance: researchdomain.ProductProvenance{
				APIProvider:      "Vitlane Browser Fork",
				APIProduct:       "Ephemeral public merchant search v1",
				DiscoveryChannel: "SERVER_BROWSER_PUBLIC",
				Country:          "KR",
				QueryLanguage:    "ko",
				ProviderLookupID: ref.IdentityKey(),
			},
			ObservedAt: time.Now().UTC(),
		}
		if observation.Validate() != nil {
			result.Rejected++
			continue
		}
		result.Products = append(result.Products, observation)
	}
	return result, nil
}

func (g *Gateway) browserDiscover(
	ctx context.Context,
	payload browserDiscoveryRequest,
) (response browserDiscoveryResponse, err error) {
	if !g.Configured(browserDiscoveryAPIID) {
		return response, safeFailure("CATALOG_API_NOT_CONFIGURED")
	}
	release, err := acquireRouteSlot(ctx)
	if err != nil {
		return response, err
	}
	defer release()
	call, err := g.reserve(ctx, browserDiscoveryAPIID, "SEARCH")
	if err != nil {
		return response, err
	}
	status, retry := 0, 0
	defer func() {
		code := "SUCCESS"
		if err != nil {
			code = "CATALOG_INTERNAL_ERROR"
			if f, ok := fault.As(err); ok {
				code = f.Reason
			}
		}
		finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if completeErr := g.config.Control.CompleteProviderCall(
			finalCtx, call, code, status, retry, time.Now().UTC(),
		); completeErr != nil && err == nil {
			err = safeFailure("CATALOG_CALL_FINALIZATION_FAILED")
		}
	}()

	body, encodeErr := json.Marshal(payload)
	if encodeErr != nil {
		return response, safeFailure("CATALOG_REQUEST_INVALID")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, requestErr := http.NewRequestWithContext(
		requestCtx, http.MethodPost,
		strings.TrimRight(g.config.BrowserBaseURL, "/")+"/v1/discover",
		bytes.NewReader(body),
	)
	if requestErr != nil {
		return response, safeFailure("CATALOG_REQUEST_INVALID")
	}
	req.Header.Set("Authorization", "Bearer "+g.config.BrowserToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	upstream, callErr := sharedhttpclient.Do(requestCtx, g.client, req, sharedhttpclient.ReadOnly)
	if callErr != nil {
		if errors.Is(requestCtx.Err(), context.Canceled) {
			return response, safeFailure("CATALOG_REQUEST_CANCELLED")
		}
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) || fault.CodeOf(callErr) == fault.DeadlineExceeded {
			return response, safeFailure("CATALOG_TIMEOUT")
		}
		return response, safeFailure("CATALOG_NETWORK_FAILED")
	}
	defer upstream.Body.Close()
	status = upstream.StatusCode
	if seconds, parseErr := strconv.Atoi(upstream.Header.Get("Retry-After")); parseErr == nil && seconds > 0 {
		retry = min(seconds, 300)
	}
	if status == http.StatusTooManyRequests {
		return response, safeFailure("CATALOG_UPSTREAM_RATE_LIMITED")
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return response, safeFailure("CATALOG_AUTH_REJECTED")
	}
	if status < 200 || status >= 300 {
		return response, safeFailure("CATALOG_UPSTREAM_FAILED")
	}
	mediaType, _, mediaErr := mime.ParseMediaType(upstream.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		return response, safeFailure("CATALOG_CONTENT_TYPE_INVALID")
	}
	raw, readErr := io.ReadAll(io.LimitReader(upstream.Body, (64<<10)+1))
	if readErr != nil || len(raw) > 64<<10 {
		return response, safeFailure("CATALOG_BODY_INVALID")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(&response); decodeErr != nil {
		return response, safeFailure("CATALOG_SCHEMA_MISMATCH")
	}
	if decoder.Decode(&struct{}{}) != io.EOF || response.ProtocolVersion != 1 ||
		response.Coverage.Source != payload.Source || response.Coverage.Count != len(response.Results) ||
		len(response.Results) > payload.Limit {
		return response, safeFailure("CATALOG_SCHEMA_MISMATCH")
	}
	switch response.Coverage.Status {
	case "SUCCEEDED":
		if len(response.Results) == 0 {
			return response, safeFailure("CATALOG_SCHEMA_MISMATCH")
		}
	case "EMPTY":
		if len(response.Results) != 0 {
			return response, safeFailure("CATALOG_SCHEMA_MISMATCH")
		}
	case "FAILED":
		return response, safeFailure("CATALOG_UPSTREAM_FAILED")
	default:
		return response, safeFailure("CATALOG_SCHEMA_MISMATCH")
	}
	return response, nil
}
