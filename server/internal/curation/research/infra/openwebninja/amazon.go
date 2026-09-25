// Package openwebninja adapts the private data vendor to the public Amazon source.
package openwebninja

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

const endpoint = "https://api.openwebninja.com"
const maxBody = 2 << 20
const observationTTL = 15 * time.Minute

type Config struct {
	APIKey string
	Client *http.Client
	Usage  researchapp.CatalogAPIUsageRepository
	// Endpoint is for an isolated test server; production composition uses the fixed default.
	Endpoint string
}
type Gateway struct {
	wait       func(context.Context, time.Duration) error
	credential string
	client     *http.Client
	endpoint   string
	usage      researchapp.CatalogAPIUsageRepository
	quotaMu    sync.Mutex
}

func New(c Config) (*Gateway, error) {
	if c.APIKey == "" || c.Client == nil || c.Usage == nil {
		return nil, fault.New(fault.InvalidInput, "AMAZON_CONFIG_INVALID", false)
	}
	base := endpoint
	if c.Endpoint != "" {
		parsed, err := url.Parse(c.Endpoint)
		if err != nil || parsed.Scheme != "http" || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") || parsed.User != nil {
			return nil, failure("AMAZON_CONFIG_INVALID")
		}
		base = strings.TrimRight(c.Endpoint, "/")
	}
	client := *c.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Gateway{credential: c.APIKey, client: &client, endpoint: base, usage: c.Usage, wait: waitFor}, nil
}
func failure(code string) error {
	class := fault.ProviderUnavailable
	retry := false
	switch code {
	case "AMAZON_AUTH_REJECTED":
		class = fault.ProviderRejected
	case "AMAZON_QUOTA_EXHAUSTED":
		class = fault.QuotaExceeded
	case "AMAZON_RATE_LIMITED":
		class = fault.RateLimited
		retry = true
	case "AMAZON_TIMEOUT":
		class = fault.DeadlineExceeded
		retry = true
	case "AMAZON_NETWORK_FAILED", "AMAZON_UPSTREAM_FAILED":
		retry = true
	case "AMAZON_SCHEMA_MISMATCH", "AMAZON_ASIN_MISMATCH":
		class = fault.ProviderRejected
	}
	return fault.New(class, code, retry)
}
func safeReason(err error) string {
	for _, code := range []string{"AMAZON_AUTH_REJECTED", "AMAZON_QUOTA_EXHAUSTED", "AMAZON_RATE_LIMITED", "AMAZON_TIMEOUT", "AMAZON_NETWORK_FAILED", "AMAZON_UPSTREAM_FAILED", "AMAZON_SCHEMA_MISMATCH", "AMAZON_ASIN_MISMATCH", "AMAZON_PRODUCT_UNRESOLVED", "AMAZON_REQUEST_CANCELLED"} {
		if strings.Contains(err.Error(), code) {
			return code
		}
	}
	return "AMAZON_INTERNAL_ERROR"
}
func (g *Gateway) get(ctx context.Context, path string, query url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.endpoint+path+"?"+query.Encode(), nil)
	if err != nil {
		return failure("AMAZON_INTERNAL_ERROR")
	}
	req.Header.Set("x-api-key", g.credential)
	effect := sharedhttpclient.ExternalEffect
	if path == "/usage" {
		effect = sharedhttpclient.ReadOnly
	}
	response, err := sharedhttpclient.Do(ctx, g.client, req, effect)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return failure("AMAZON_TIMEOUT")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return fault.New(fault.CallerCancelled, "AMAZON_REQUEST_CANCELLED", false)
		}
		return failure("AMAZON_NETWORK_FAILED")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return failure("AMAZON_AUTH_REJECTED")
	case http.StatusTooManyRequests:
		return failure("AMAZON_RATE_LIMITED")
	case http.StatusNotFound:
		return failure("AMAZON_PRODUCT_UNRESOLVED")
	default:
		return failure("AMAZON_UPSTREAM_FAILED")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return failure("AMAZON_NETWORK_FAILED")
	}
	if len(body) > maxBody {
		return failure("AMAZON_SCHEMA_MISMATCH")
	}
	var envelope struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Status != "OK" || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return failure("AMAZON_SCHEMA_MISMATCH")
	}
	if json.Unmarshal(envelope.Data, out) != nil {
		return failure("AMAZON_SCHEMA_MISMATCH")
	}
	return nil
}
func (g *Gateway) RefreshAmazonUsage(ctx context.Context) (result researchapp.CatalogAPIUsage, err error) {
	g.quotaMu.Lock()
	defer g.quotaMu.Unlock()
	started := time.Now().UTC()
	defer func() {
		if ledger, ok := g.usage.(interface {
			RecordAmazonQuotaCall(context.Context, string, time.Time, time.Time) error
		}); ok {
			outcome := "SUCCESS"
			if err != nil {
				outcome = safeReason(err)
			}
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			if recordErr := ledger.RecordAmazonQuotaCall(final, outcome, started, time.Now().UTC()); err == nil && recordErr != nil {
				err = recordErr
			}
		}
	}()
	var data struct {
		Plan struct {
			IsFree bool `json:"is_free"`
		} `json:"plan"`
		Quotas []struct {
			Limit     int64     `json:"limit"`
			Used      int64     `json:"used"`
			Remaining int64     `json:"remaining"`
			ResetAt   time.Time `json:"reset_at"`
		} `json:"quotas"`
	}
	err = g.get(ctx, "/usage", url.Values{"api_id": {"realtime_amazon_data"}}, &data)
	if err == nil && (len(data.Quotas) != 1 || data.Quotas[0].Limit < 0 || data.Quotas[0].Used < 0 || data.Quotas[0].Remaining < 0 || data.Quotas[0].ResetAt.IsZero()) {
		err = failure("AMAZON_SCHEMA_MISMATCH")
	}
	if err != nil {
		final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = g.usage.RecordAmazonQuotaFailure(final, safeReason(err))
		return researchapp.CatalogAPIUsage{}, err
	}
	q := data.Quotas[0]
	err = g.usage.SaveAmazonQuota(ctx, researchapp.CatalogAPIQuota{Limit: q.Limit, Used: q.Used, Remaining: q.Remaining, ResetAt: q.ResetAt, ObservedAt: time.Now().UTC(), IsFree: data.Plan.IsFree}, started)
	if err != nil {
		return researchapp.CatalogAPIUsage{}, err
	}
	return g.usage.ReadAmazonUsage(ctx)
}
func (g *Gateway) start(ctx context.Context, operation string) (string, error) {
	u, err := g.usage.ReadAmazonUsage(ctx)
	if err != nil {
		return "", err
	}
	if u.Quota == nil || time.Now().After(u.Quota.ResetAt) || time.Since(u.Quota.ObservedAt) > 5*time.Minute {
		if _, err = g.RefreshAmazonUsage(ctx); err != nil {
			return "", err
		}
	}
	id, err := g.usage.ReserveAmazonCall(ctx, operation, time.Now().UTC())
	// Another Round holds one of the two Amazon slots or fired within the last
	// second. That clears in about a second, so wait once instead of dropping
	// Amazon from this Round; longer holds still skip the source.
	if f, ok := fault.As(err); ok && f.Reason == "AMAZON_RATE_LIMITED" && f.RetryAfter > 0 && f.RetryAfter <= maximumAdmissionWait {
		if waitErr := g.wait(ctx, f.RetryAfter); waitErr != nil {
			return "", fault.New(fault.CallerCancelled, "AMAZON_REQUEST_CANCELLED", false)
		}
		return g.usage.ReserveAmazonCall(ctx, operation, time.Now().UTC())
	}
	return id, err
}

const maximumAdmissionWait = 5 * time.Second

func waitFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (g *Gateway) finish(ctx context.Context, id string, err error) error {
	code := "SUCCESS"
	if err != nil {
		code = safeReason(err)
	}
	final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	recordErr := g.usage.CompleteAmazonCall(final, id, code, time.Now().UTC())
	if recordErr != nil {
		return fault.New(fault.InternalFailure, "AMAZON_USAGE_RECORD_FAILED", false)
	}
	return err
}

type product struct {
	Description  string          `json:"product_description"`
	About        []string        `json:"about_product"`
	ASIN         string          `json:"asin"`
	Title        string          `json:"product_title"`
	Price        *string         `json:"product_price"`
	Currency     string          `json:"currency"`
	Country      string          `json:"country"`
	URL          string          `json:"product_url"`
	Photo        string          `json:"product_photo"`
	Availability string          `json:"product_availability"`
	Variations   json.RawMessage `json:"product_variations"`
}

var priceFormat = regexp.MustCompile(`^\$?(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)(?:\.[0-9]{1,2})?$`)

func observedPrice(raw *string, currency string) researchdomain.VariantObservedPrice {
	unknown := researchdomain.VariantObservedPrice{Kind: "UNKNOWN", ReasonCode: "PRICE_NOT_OBSERVED"}
	if raw == nil || currency != "USD" || !priceFormat.MatchString(strings.TrimSpace(*raw)) {
		return unknown
	}
	n, ok := new(big.Rat).SetString(strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(*raw), "$"), ",", ""))
	if !ok {
		return unknown
	}
	n.Mul(n, big.NewRat(100, 1))
	if !n.IsInt() || !n.Num().IsInt64() {
		return unknown
	}
	amount := n.Num().Int64()
	if amount < 0 || amount > 9007199254740991 {
		return unknown
	}
	return researchdomain.VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &amount, Currency: "USD"}
}
func normalize(p product, market string, now time.Time) (researchapp.CatalogProductObservation, error) {
	ref := researchdomain.SourceProductRef{Source: researchdomain.SourceAmazon, Marketplace: market, AnchorASIN: p.ASIN}
	variant := researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: market, ASIN: p.ASIN}
	if ref.Validate() != nil || strings.TrimSpace(p.Title) == "" || (p.Country != "" && p.Country != market) {
		return researchapp.CatalogProductObservation{}, failure("AMAZON_SCHEMA_MISMATCH")
	}
	parsed, err := url.Parse(p.URL)
	if err != nil || parsed.User != nil || parsed.Scheme != "https" || (parsed.Host != "www.amazon.com" && parsed.Host != "amazon.com") || strings.TrimRight(parsed.Path, "/") != "/dp/"+p.ASIN {
		return researchapp.CatalogProductObservation{}, failure("AMAZON_SCHEMA_MISMATCH")
	}
	canonical, _ := variant.ExternalURL()
	availability := "UNKNOWN"
	switch strings.ToLower(strings.TrimSpace(p.Availability)) {
	case "in stock", "in stock.":
		availability = "AVAILABLE"
	case "currently unavailable.", "currently unavailable", "out of stock":
		availability = "UNAVAILABLE"
	}
	observation := researchdomain.VariantObservation{ObservationID: "amazon:" + p.ASIN + ":" + now.Format(time.RFC3339Nano), VariantRef: variant, Price: observedPrice(p.Price, p.Currency), Availability: availability, DeliveryEligibility: "UNCONFIRMED", Seller: researchdomain.ObservedSeller{Kind: "UNKNOWN"}, PurchaseRoute: "EXTERNAL", ProductURL: canonical, ObservedAt: now, RefreshAfter: now.Add(observationTTL)}
	if err := observation.Validate(); err != nil {
		return researchapp.CatalogProductObservation{}, failure("AMAZON_SCHEMA_MISMATCH")
	}
	result := researchapp.CatalogProductObservation{Description: researchapp.CatalogDescription{Plain: p.Description + "\n" + strings.Join(p.About, "; ")}, ProviderProductID: ref.IdentityKey(), SourceProductRef: &ref, VariantObservation: &observation, Title: p.Title, Locator: &researchapp.CatalogProductLocator{Kind: researchapp.CatalogLocatorProductURL, ProductURL: &researchapp.CatalogProductURLLocator{CanonicalURL: canonical}}}
	if observation.Price.AmountMinor != nil {
		money := researchapp.CatalogMoney{AmountMinor: *observation.Price.AmountMinor, Currency: p.Currency}
		result.PriceRange = researchapp.CatalogPriceRange{Minimum: money, Maximum: money}
	}
	if photo, e := url.Parse(p.Photo); e == nil && photo.Scheme == "https" && photo.User == nil && photo.Host == "m.media-amazon.com" {
		result.Media = []researchapp.CatalogMedia{{Type: "IMAGE", URL: p.Photo}}
	}
	return result, nil
}
func (g *Gateway) SearchAmazon(ctx context.Context, r researchapp.AmazonSearchRequest) (result researchapp.AmazonSearchResult, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if r.Marketplace != "US" || strings.TrimSpace(r.Query) == "" {
		return result, failure("AMAZON_MARKET_UNSUPPORTED")
	}
	id, err := g.start(ctx, "SEARCH")
	if err != nil {
		return result, err
	}
	defer func() { err = g.finish(ctx, id, err) }()
	var data struct {
		Country  string    `json:"country"`
		Products []product `json:"products"`
	}
	page := max(1, min(100, r.Page))
	if err = g.get(ctx, "/realtime-amazon-data/search", url.Values{"query": {r.Query}, "country": {"US"}, "page": {strconv.Itoa(page)}}, &data); err != nil {
		return result, err
	}
	if data.Country != "US" || data.Products == nil {
		return result, failure("AMAZON_SCHEMA_MISMATCH")
	}
	result.RawCount = len(data.Products)
	result.PaginationKnown = true
	result.HasNextPage = len(data.Products) > 0 && page < 100
	result.Products = []researchapp.CatalogProductObservation{}
	seen := map[string]bool{}
	for _, raw := range data.Products {
		p, e := normalize(raw, "US", time.Now().UTC())
		if e != nil {
			result.Partial = true
			continue
		}
		if seen[p.ProviderProductID] {
			continue
		}
		seen[p.ProviderProductID] = true
		p.ProviderOrder = len(result.Products)
		result.Products = append(result.Products, p)
	}
	return result, nil
}
func (g *Gateway) LookupAmazon(ctx context.Context, ref researchdomain.SourceVariantRef) (result researchapp.AmazonProductDetail, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if ref.Validate() != nil || ref.Source != researchdomain.SourceAmazon {
		return result, failure("AMAZON_REFERENCE_INVALID")
	}
	id, err := g.start(ctx, "DETAIL")
	if err != nil {
		return result, err
	}
	defer func() { err = g.finish(ctx, id, err) }()
	var raw product
	if err = g.get(ctx, "/realtime-amazon-data/product-details", url.Values{"asin": {ref.ASIN}, "country": {ref.Marketplace}}, &raw); err != nil {
		return result, err
	}
	if raw.ASIN != ref.ASIN {
		return result, failure("AMAZON_ASIN_MISMATCH")
	}
	result.Product, err = normalize(raw, ref.Marketplace, time.Now().UTC())
	if err != nil {
		return result, err
	}
	result.Variants, result.RelationStatus, result.Truncated = parseVariants(raw.Variations)
	return result, nil
}
func parseVariants(raw json.RawMessage) ([]researchapp.AmazonVariantOption, string, bool) {
	result := []researchapp.AmazonVariantOption{}
	if len(raw) == 0 || string(raw) == "null" {
		return result, "UNKNOWN", false
	}
	var axes map[string][]struct {
		ASIN      string `json:"asin"`
		Value     string `json:"value"`
		Available *bool  `json:"is_available"`
	}
	if json.Unmarshal(raw, &axes) != nil || axes == nil {
		return result, "PARTIAL", false
	}
	names := make([]string, 0, len(axes))
	for name := range axes {
		names = append(names, name)
	}
	sort.Strings(names)
	byID := map[string]int{}
	partial := false
	truncated := false
	for _, name := range names {
		for _, row := range axes[name] {
			ref := researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: row.ASIN}
			if ref.Validate() != nil || strings.TrimSpace(row.Value) == "" {
				partial = true
				continue
			}
			if index, ok := byID[row.ASIN]; ok {
				result[index].Labels = append(result[index].Labels, researchapp.LiveVariantOptionV2{Name: name, Value: row.Value})
				if row.Available != nil && !*row.Available {
					result[index].Available = row.Available
				}
				continue
			}
			if len(result) >= 50 {
				truncated = true
				continue
			}
			byID[row.ASIN] = len(result)
			result = append(result, researchapp.AmazonVariantOption{ASIN: row.ASIN, Labels: []researchapp.LiveVariantOptionV2{{Name: name, Value: row.Value}}, Available: row.Available})
		}
	}
	status := "RELATED_REFS"
	if len(result) == 0 {
		status = "EMPTY_OBSERVED"
	}
	if partial {
		status = "PARTIAL"
	}
	return result, status, truncated
}

func (g *Gateway) DescribeDiscovery() researchapp.DiscoveryDescriptor {
	return researchapp.EnglishDiscoveryDescriptor("AMAZON")
}

func (v *product) UnmarshalJSON(raw []byte) error {
	type fields product
	var decoded fields
	if err := json.Unmarshal(raw, &decoded); err != nil {
		*v = product{}
		return nil
	}
	*v = product(decoded)
	return nil
}
