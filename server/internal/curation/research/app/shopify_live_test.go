package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchshopify "github.com/vitlane/vitlane/server/internal/curation/research/infra/shopifyucp"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

// liveShopifyTokens exchanges the Shopify dev client credentials for an agent
// token. Nothing it holds is ever logged.
type liveShopifyTokens struct {
	client           *http.Client
	clientID, secret string
	mu               sync.Mutex
	token            string
}

func (s *liveShopifyTokens) AccessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" {
		return s.token, nil
	}
	payload, _ := json.Marshal(map[string]string{"client_id": s.clientID, "client_secret": s.secret, "grant_type": "client_credentials"})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.shopify.com/auth/access_token", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return "", errors.New("shopify token request failed")
	}
	defer response.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&body) != nil || body.AccessToken == "" {
		return "", fmt.Errorf("shopify token exchange: status %d", response.StatusCode)
	}
	s.token = body.AccessToken
	return s.token, nil
}

func (s *liveShopifyTokens) InvalidateAccessToken(string) {
	s.mu.Lock()
	s.token = ""
	s.mu.Unlock()
}

type liveClock struct{}

func (liveClock) Now() time.Time { return time.Now().UTC() }

// liveAdmission lets the adapter make as many provider calls as a lookup needs; the test counts them.
type liveAdmission struct{ calls int }

func (a *liveAdmission) AcquireCatalogProviderCall(context.Context) (func(), error) {
	a.calls++
	return func() {}, nil
}

// measuredShopifyGateway counts what actually went to Shopify and how long it took.
type measuredShopifyGateway struct {
	inner    *researchshopify.GatewayV2
	searches []researchapp.CatalogProductSearchRequest
	raw      []int
	took     []time.Duration
	lookups  int
	messages []researchapp.CatalogProviderMessage
	applied  string
}

func (g *measuredShopifyGateway) SearchProducts(ctx context.Context, request researchapp.CatalogProductSearchRequest) (researchapp.CatalogProductSearchResult, error) {
	started := time.Now()
	result, err := g.inner.SearchProducts(ctx, request)
	g.searches = append(g.searches, request)
	g.raw = append(g.raw, len(result.Products))
	g.took = append(g.took, time.Since(started))
	g.messages = result.Messages
	g.applied = fmt.Sprintf("verified=%t price=%v currency=%q disqualifying=%d", result.AppliedFilters.Verified, result.AppliedFilters.Price != nil, result.AppliedFilters.PriceCurrency, len(result.AppliedFilters.DisqualifyingMessageHashes))
	return result, err
}
func (g *measuredShopifyGateway) LookupMedia(ctx context.Context, request researchapp.CatalogMediaLookupRequest) (researchapp.CatalogMediaLookupResult, error) {
	g.lookups++
	return g.inner.LookupMedia(ctx, request)
}
func (g *measuredShopifyGateway) LookupOffers(ctx context.Context, request researchapp.CatalogOfferLookupRequest) (researchapp.CatalogOfferLookupResult, error) {
	g.lookups++
	return g.inner.LookupOffers(ctx, request)
}

// TestLiveShopifySearchPlan runs the production search path against the real
// Shopify Global Catalog. It is a gate for people, not for CI: it spends real
// provider calls, so it runs only when asked for.
//
//	SHOPIFY_CATALOG_SEARCH_LIVE=1 SHOPIFY_DEV_CLIENT_ID=… SHOPIFY_DEV_CLIENT_SECRET=… \
//	  go test ./internal/curation/research/app/ -run TestLiveShopifySearchPlan -v -count=1
func TestLiveShopifySearchPlan(t *testing.T) {
	if os.Getenv("SHOPIFY_CATALOG_SEARCH_LIVE") != "1" {
		t.Skip("set SHOPIFY_CATALOG_SEARCH_LIVE=1 to search the real Shopify catalog")
	}
	clientID, secret := strings.TrimSpace(os.Getenv("SHOPIFY_DEV_CLIENT_ID")), strings.TrimSpace(os.Getenv("SHOPIFY_DEV_CLIENT_SECRET"))
	if clientID == "" || secret == "" {
		t.Skip("SHOPIFY_DEV_CLIENT_ID and SHOPIFY_DEV_CLIENT_SECRET are required")
	}
	client, err := sharedhttpclient.NewClient(http.DefaultTransport, 25*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := researchshopify.NewGatewayV2(researchshopify.GatewayV2Config{
		Endpoint:   "https://catalog.shopify.com/api/ucp/mcp",
		ProfileURL: "https://shopify.dev/ucp/agent-profiles/2026-04-08/valid-with-capabilities.json",
		HTTPClient: client, ExpectedVersion: "2026-04-08",
		LookupBatchSize: researchapp.CatalogLookupDefaultBatchSize,
		Tokens:          &liveShopifyTokens{client: client, clientID: clientID, secret: secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	maximum, err := shareddomain.NewMoney("100", "USD")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, query, productType string
		seeds                    []string
		maximum                  *shareddomain.Money
	}{
		// The two shapes that failed before any request was sent (ADR-0087).
		{name: "one word", query: "sunscreen", productType: "sunscreen", seeds: []string{"sunscreen", "mineral sunscreen spf 50"}},
		{name: "two words", query: "camping chair", productType: "camping chair", seeds: []string{"camping chair", "folding camp chair"}},
		// A product type whose whole phrase rarely appears contiguously in a title.
		{name: "loose phrase", query: "wireless earbuds", productType: "wireless earbuds", seeds: []string{"wireless earbuds", "bluetooth earbuds"}},
		// A longer phrase with a budget: the price filter and its proof.
		{name: "budget", query: "lightweight trail running shoes", productType: "trail running shoes", seeds: []string{"lightweight trail running shoes", "trail running shoes"}, maximum: &maximum},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			gateway := &measuredShopifyGateway{inner: inner}
			service, err := researchapp.NewLiveCatalogReviewServiceV2(gateway, liveClock{}, researchapp.LiveCatalogReviewConfigV2{
				MaximumCallsPerWindow: 30, Window: time.Minute, MaximumConcurrent: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			profile := researchapp.CatalogTargetSearchProfileV2{
				TargetID: "live-target", NormalizedIntent: test.query, TargetHash: "live-profile",
				Market: researchapp.CatalogMarketContextV2{Country: "US", Currency: "USD"}, MaximumPrice: test.maximum,
			}
			input := researchapp.CatalogWorkspaceSearchInputV2{
				UserID: "live-user", CurationID: "live-curation", TargetID: "live-target",
				Mode: researchapp.CatalogResearchAppendV2, QuerySeeds: test.seeds,
				Criteria: &curationdomain.TargetCriteriaSetV1{Subject: curationdomain.ResearchSubject{ProductType: test.productType}},
				Search:   researchapp.LiveCatalogReviewSearchInputV2{Query: test.query, Limit: researchapp.CandidateUpdateSize},
			}
			seen := map[string]bool{}
			for round := 1; round <= 2; round++ {
				started := time.Now()
				result, err := researchapp.SearchWorkspacePlanForProfileForTestV2(context.Background(), service, input, profile)
				if err != nil {
					for _, message := range gateway.messages {
						content := message.Content
						if len(content) > 240 {
							content = content[:240]
						}
						t.Logf("    provider message: type=%q code=%q path=%q severity=%q content=%q", message.Type, message.Code, message.Path, message.Severity, content)
					}
					if len(gateway.searches) > 0 {
						last := gateway.searches[len(gateway.searches)-1]
						t.Logf("    request filters: price=%+v shipsTo=%+v conditions=%v categories=%v | applied proof: %s", last.Filters.Price, last.Filters.ShipsTo, last.Filters.Conditions, last.Filters.Categories, gateway.applied)
					}
					t.Fatalf("round %d: %v", round, err)
				}
				request := gateway.searches[len(gateway.searches)-1]
				fresh, wholePhrase, overBudget := 0, 0, 0
				for _, product := range result.Search.Products {
					if !seen[product.ProviderProductID] {
						fresh++
					}
					seen[product.ProviderProductID] = true
					if researchapp.ListingMentionsForTestV2(product, test.productType) {
						wholePhrase++
					}
					if test.maximum != nil && product.PriceRange.Minimum.AmountMinor > 10000 {
						overBudget++
					}
				}
				next := result.NextProgress["SHOPIFY"]
				t.Logf("round %d: asked %q page-cursor=%t limit=%d | shopify returned %d in %s | admitted %d (new %d) | would pass the old whole-phrase guard %d | over budget %d | lookups so far %d | total %s | next: %q page %d cursor=%t",
					round, request.Query, request.Cursor != "", request.Limit, gateway.raw[len(gateway.raw)-1], gateway.took[len(gateway.took)-1].Round(time.Millisecond),
					len(result.Search.Products), fresh, wholePhrase, overBudget, gateway.lookups, time.Since(started).Round(time.Millisecond),
					next.Query, next.Page, next.Cursor != "")
				for index, product := range result.Search.Products {
					if index < 5 {
						t.Logf("    %d. %s | %d-%d %s", index+1, product.Title, product.PriceRange.Minimum.AmountMinor, product.PriceRange.Maximum.AmountMinor, product.PriceRange.Minimum.Currency)
					}
				}
				if request.Query != test.query && round == 1 {
					t.Fatalf("the first request must ask the plan's primary phrase, asked %q", request.Query)
				}
				if overBudget > 0 {
					t.Fatalf("a product over the budget was admitted")
				}
				input.Progress = result.NextProgress
				if round == 1 {
					liveLookupSavedProducts(t, gateway, result.Search.Products)
				}
				time.Sleep(2500 * time.Millisecond)
			}
		})
	}
}

// liveLookupSavedProducts does what the screen does for a saved candidate: asks
// Shopify again by the product's own locator and reads today's name and price.
func liveLookupSavedProducts(t *testing.T, gateway *measuredShopifyGateway, products []researchapp.CatalogProductObservation) {
	t.Helper()
	inputs := make([]researchapp.CatalogOfferLookupInput, 0, 3)
	byDraft := map[string]researchapp.CatalogProductObservation{}
	kinds := map[researchapp.CatalogLocatorKind]int{}
	for _, product := range products {
		if product.Locator == nil {
			continue
		}
		kinds[product.Locator.Kind]++
		// The same identifier the saved workspace uses: the product URL, else the merchant variant.
		identifier := (researchapp.CatalogCandidateReferenceV2{Locator: *product.Locator}).LookupIdentifier()
		if identifier == "" || len(inputs) == 3 {
			continue
		}
		draft := fmt.Sprintf("candidate:%d", len(inputs)+1)
		inputs = append(inputs, researchapp.CatalogOfferLookupInput{DraftID: draft, Identifier: identifier})
		byDraft[draft] = product
	}
	t.Logf("    locators of the admitted products: %v", kinds)
	if len(inputs) == 0 {
		t.Log("    lookup: no product carried a usable locator")
		return
	}
	started := time.Now()
	result, err := gateway.LookupOffers(context.Background(), researchapp.CatalogOfferLookupRequest{
		Inputs:  inputs,
		Context: researchapp.CatalogBuyerContext{Country: "US", Currency: "USD", Language: "en", Intent: "render saved research workspace"},
		// The app's rate guard stands here in production; the test only counts.
		ProviderCallAdmission: &liveAdmission{},
	})
	if err != nil {
		t.Fatalf("lookup by saved locator: %v", err)
	}
	t.Logf("    lookup of %d saved products by locator: %d matched, %d unresolved, %d provider calls, %s", len(inputs), len(result.Matches), len(result.UnresolvedIDs), result.ProviderCallCount, time.Since(started).Round(time.Millisecond))
	for _, match := range result.Matches {
		searched := byDraft[match.DraftID]
		same := match.Product.PriceRange.Minimum.AmountMinor == searched.PriceRange.Minimum.AmountMinor
		t.Logf("      %s: %q %d %s (search said %d) same-price=%t variant=%q", match.Match, match.Product.Title, match.Product.PriceRange.Minimum.AmountMinor, match.Product.PriceRange.Minimum.Currency, searched.PriceRange.Minimum.AmountMinor, same, match.Variant.Title)
		if match.Product.PriceRange.Minimum.Currency == "" || match.Product.Title == "" {
			t.Fatalf("a matched product must carry today's name and price: %+v", match.Product)
		}
	}
}
