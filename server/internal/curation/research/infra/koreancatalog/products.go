package koreancatalog

import (
	"bytes"
	"context"
	"encoding/json"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"golang.org/x/net/html"
	stdhtml "html"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type productSummary struct {
	ID     string   `json:"product_id"`
	Title  string   `json:"product_title"`
	Photos []string `json:"product_photos"`
	Store  string   `json:"store_name"`
}

func (g *Gateway) searchProducts(ctx context.Context, query string, limits ...int) ([]researchdomain.ExternalProductObservation, error) {
	products := []researchdomain.ExternalProductObservation{}
	if err := g.ensureQuota(ctx, "OWN_PRODUCT"); err != nil {
		return products, err
	}
	var search struct {
		Status string `json:"status"`
		Data   struct {
			Products tolerantItems[productSummary] `json:"products"`
		} `json:"data"`
	}
	if _, err := g.get(ctx, "OWN_PRODUCT", "SEARCH", "https://api.openwebninja.com", "/realtime-product-search/v2/search", url.Values{"q": {query}, "country": {"kr"}, "language": {"ko"}}, &search); err != nil {
		return products, err
	}
	if search.Status != "OK" {
		return products, safeFailure("CATALOG_SCHEMA_MISMATCH")
	}
	limit := g.detailLimit()
	if len(limits) > 0 {
		limit = min(limit, limits[0])
	}
	summaries := search.Data.Products[:min(len(search.Data.Products), limit)]
	type detailResult struct {
		products []researchdomain.ExternalProductObservation
		err      error
	}
	results := make([]detailResult, len(summaries))
	parallel := 2
	if usage, err := g.Usage(ctx, "OWN_PRODUCT", false); err == nil && usage.MaxConcurrent > 0 {
		parallel = usage.MaxConcurrent
	}
	var wg sync.WaitGroup
	jobs := make(chan int)
	for n := 0; n < min(parallel, len(summaries)); n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				p := summaries[i]
				if p.ID == "" || len(p.ID) > 8192 {
					continue
				}
				results[i].products, results[i].err = g.searchProductDetail(ctx, p)
			}
		}()
	}
	for i := range summaries {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	var detailErr error
	for _, result := range results {
		products = append(products, result.products...)
		if result.err != nil {
			detailErr = result.err
		}
	}
	if detailErr != nil {
		return products, &partialDiscoveryError{cause: detailErr}
	}
	return products, nil
}

func (g *Gateway) searchProductDetail(ctx context.Context, p productSummary) ([]researchdomain.ExternalProductObservation, error) {
	products := []researchdomain.ExternalProductObservation{}
	var detail struct {
		Status string `json:"status"`
		Data   struct {
			Title  string   `json:"product_title"`
			Photos []string `json:"product_photos"`
			Offers []struct {
				URL   string `json:"offer_page_url"`
				Price string `json:"price"`
			} `json:"offers"`
		} `json:"data"`
	}
	if _, err := g.get(ctx, "OWN_PRODUCT", "DETAIL", "https://api.openwebninja.com", "/realtime-product-search/v2/product-details", url.Values{"product_id": {p.ID}, "country": {"kr"}, "language": {"ko"}}, &detail); err != nil {
		return products, err
	}
	if detail.Status != "OK" {
		return products, safeFailure("CATALOG_SCHEMA_MISMATCH")
	}
	title := p.Title
	if detail.Data.Title != "" {
		title = detail.Data.Title
	}
	photos := p.Photos
	if len(detail.Data.Photos) > 0 {
		photos = detail.Data.Photos
	}
	for _, offer := range detail.Data.Offers {
		// Every registered Korean mall is an original product identity;
		// Google-side merchants that are not registered are left out.
		ref, err := researchdomain.SourceProductFromURL(offer.URL)
		if err != nil {
			continue
		}
		canonical, _ := ref.ExternalProductURL()
		o := researchdomain.ExternalProductObservation{SchemaVersion: "vitlane.external-product-observation.v1", ProductRef: ref, ProductURL: canonical, Title: cleanTitle(title), Price: observedPrice(offer.Price, ""), PriceScope: "PRODUCT", Seller: researchdomain.ObservedSeller{Kind: "UNKNOWN"}, Provenance: researchdomain.ProductProvenance{APIProvider: "OpenWebNinja", APIProduct: "Real-Time Product Search v2", DiscoveryChannel: "GOOGLE_SHOPPING", Country: "KR", QueryLanguage: "ko", ProviderLookupID: p.ID}, ObservedAt: time.Now().UTC()}
		if len(photos) > 0 {
			o.ImageURL = safeImage(photos[0])
		}
		if ref.Source == researchdomain.SourceCoupang {
			parsed, _ := url.Parse(offer.URL)
			o.OriginalItemID = parsed.Query().Get("itemId")
			o.OriginalListingID = parsed.Query().Get("vendorItemId")
		}
		if o.Validate() == nil {
			products = append(products, o)
		}
	}
	return products, nil
}

// webSearch keeps what the provider returned apart from what became a
// product observation. Items drive paging; Rejected counts documents that were
// never products of the asked mall and product numbers whose public page
// carries no product.
type webSearch struct {
	DiscoveryCompleted bool
	HasNext            bool
	NextCursor         string
	Products           []researchdomain.ExternalProductObservation
	Items              int
	Rejected           int
}

// elevenStreetSearch is the historical name of the 11st web-search result.
type elevenStreetSearch = webSearch

// searchElevenStreet runs the 11st web path; kept for the existing callers.
func (g *Gateway) searchElevenStreet(ctx context.Context, query, id string, pages ...int) (webSearch, error) {
	page := 1
	if len(pages) > 0 {
		page = pages[0]
	}
	mall, _ := researchdomain.KoreanMall(researchdomain.SourceElevenStreet)
	return g.searchViaWeb(ctx, mall, query, id, page)
}

// searchViaWeb finds one mall's original product pages through a web search
// restricted to that mall, then reads each page's public metadata when the
// registry says the mall serves it. At most two pages are read per call.
func (g *Gateway) searchViaWeb(ctx context.Context, mall researchdomain.KRMall, query, id string, page int, skipDetail ...bool) (webSearch, error) {
	return g.searchViaWebBudget(ctx, mall, query, id, page, researchapp.DefaultDiscoveryCollectionBudget().HTMLDetails, skipDetail...)
}
func (g *Gateway) searchViaWebBudget(ctx context.Context, mall researchdomain.KRMall, query, id string, page, detailLimit int, skipDetail ...bool) (webSearch, error) {
	return g.searchViaWebPage(ctx, mall, query, id, page, detailLimit, "", skipDetail...)
}
func (g *Gateway) searchViaWebPage(ctx context.Context, mall researchdomain.KRMall, query, id string, page, detailLimit int, cursor string, skipDetail ...bool) (webSearch, error) {
	page = max(1, min(100, page))
	products := webSearch{Products: []researchdomain.ExternalProductObservation{}}
	if mall.SiteFilter == "" {
		return products, safeFailure("CATALOG_API_INVALID")
	}
	if id != "NAVER_WEBKR" {
		if err := g.ensureQuota(ctx, id); err != nil {
			return products, err
		}
	}
	type link struct{ URL, Title, Snippet string }
	links := []link{}
	query += " " + mall.SiteFilter
	provider, api, channel := "NAVER API HUB", "Search Webkr", "NAVER_WEB"
	switch id {
	case "NAVER_WEBKR":
		var body struct {
			Items tolerantItems[struct {
				Title       string `json:"title"`
				Link        string `json:"link"`
				Description string `json:"description"`
				Snippet     string `json:"snippet"`
			}] `json:"items"`
			ErrorCode string `json:"errorCode"`
		}
		if _, err := g.get(ctx, id, "SEARCH", "https://naverapihub.apigw.ntruss.com", "/search/v1/webkr", url.Values{"query": {query}, "display": {"5"}, "start": {strconv.Itoa(1 + (page-1)*5)}, "format": {"json"}}, &body); err != nil {
			return products, err
		}
		if body.ErrorCode != "" || body.Items == nil {
			return products, safeFailure("CATALOG_SCHEMA_MISMATCH")
		}
		for _, item := range body.Items {
			links = append(links, link{item.Link, item.Title, cleanTitle(item.Description + " " + item.Snippet)})
		}
	case "OWN_WEB":
		provider, api, channel = "OpenWebNinja", "Real-Time Web Search", "GOOGLE_WEB"
		var body struct {
			Status string `json:"status"`
			Data   struct {
				Results []struct {
					Title   string `json:"title"`
					URL     string `json:"url"`
					Snippet string `json:"snippet"`
				} `json:"organic_results"`
			} `json:"data"`
		}
		if _, err := g.get(ctx, id, "SEARCH", "https://api.openwebninja.com", "/realtime-web-search/search", url.Values{"q": {query}, "gl": {"kr"}, "hl": {"ko"}}, &body); err != nil {
			return products, err
		}
		if body.Status != "OK" {
			return products, safeFailure("CATALOG_SCHEMA_MISMATCH")
		}
		for _, item := range body.Data.Results {
			links = append(links, link{item.URL, item.Title, cleanTitle(item.Snippet)})
		}
	case "SERP_GOOGLE":
		provider, api, channel = "SerpApi", "Google Search", "GOOGLE_WEB"
		var body struct {
			Results []struct {
				Title       string `json:"title"`
				Link        string `json:"link"`
				Description string `json:"description"`
				Snippet     string `json:"snippet"`
			} `json:"organic_results"`
			Error      string `json:"error"`
			Pagination struct {
				Next string `json:"next"`
			} `json:"serpapi_pagination"`
		}
		offset := max(0, (page-1)*10)
		if cursor != "" {
			value, err := strconv.Atoi(cursor)
			if err != nil || value < 0 || value > 10000 {
				return products, safeFailure("CATALOG_PROGRESS_INVALID")
			}
			offset = value
		}
		if _, err := g.get(ctx, id, "SEARCH", "https://serpapi.com", "/search.json", url.Values{"engine": {"google"}, "q": {query}, "gl": {"kr"}, "hl": {"ko"}, "num": {"5"}, "start": {strconv.Itoa(offset)}}, &body); err != nil {
			return products, err
		}
		if body.Error != "" {
			return products, safeFailure("CATALOG_SCHEMA_MISMATCH")
		}
		if next, err := url.Parse(body.Pagination.Next); err == nil && next.Host == "serpapi.com" && next.Scheme == "https" {
			if start, err := strconv.Atoi(next.Query().Get("start")); err == nil && start > offset && start <= 10000 {
				products.HasNext = true
				products.NextCursor = strconv.Itoa(start)
			}
		}
		for _, item := range body.Results {
			links = append(links, link{item.Link, item.Title, cleanTitle(item.Description + " " + item.Snippet)})
		}
	default:
		return products, safeFailure("CATALOG_API_INVALID")
	}
	products.DiscoveryCompleted = true
	products.Items = len(links)
	if id == "NAVER_WEBKR" {
		products.HasNext = len(links) > 0
	}
	seen := map[string]bool{}
	attempts := 0
	var detailErr error
	detail, _ := researchapp.CatalogAPIDefinitionFor(mall.DetailAPI)
	for _, item := range links {
		ref, err := researchdomain.SourceProductFromURL(item.URL)
		if err != nil || ref.Source != mall.Source {
			// Q&A lists, other malls and non-product documents match the site
			// search but never become candidates or spend a detail slot.
			products.Rejected++
			continue
		}
		if seen[ref.IdentityKey()] {
			continue
		}
		seen[ref.IdentityKey()] = true

		canonical, _ := ref.ExternalProductURL()
		o := researchdomain.ExternalProductObservation{SchemaVersion: "vitlane.external-product-observation.v1", ProductRef: ref, ProductURL: canonical, Title: cleanTitle(item.Title), Description: cleanTitle(item.Snippet), Price: observedPrice("", ""), PriceScope: "PRODUCT", Seller: researchdomain.ObservedSeller{Kind: "UNKNOWN"}, Provenance: researchdomain.ProductProvenance{APIProvider: provider, APIProduct: api, DiscoveryChannel: channel, Country: "KR", QueryLanguage: "ko"}, ObservedAt: time.Now().UTC()}
		if attempts < detailLimit && mall.Detail == researchdomain.DetailPublicHTML && mall.DetailAPI != "" && !(len(skipDetail) > 0 && skipDetail[0]) {
			attempts++
			path, values := mall.DetailRequest(ref.ProductID)
			raw, err := g.get(ctx, mall.DetailAPI, "DETAIL", mall.DetailHost, path, values, nil)
			if f, ok := fault.As(err); ok && f.Reason == researchapp.CatalogOutcomeProductNotFound {
				// The public page answered but carries no product: the mall's
				// stub for discontinued or invalid product numbers. The call
				// succeeded and nothing was discovered, so the search hit is
				// not a candidate.
				products.Rejected++
				continue
			}
			if err == nil {
				applyProductMetadata(&o, raw)
				o.Provenance.DetailAPIProvider = detail.APIProvider
				o.Provenance.DetailAPIProduct = detail.APIProduct
			} else {
				detailErr = err
			}
		}
		// An original search hit remains useful with UNKNOWN price when public
		// metadata is disabled or unavailable. It is never called a fresh quote.
		if o.Validate() == nil {
			products.Products = append(products.Products, o)
		}
	}
	return products, detailErr
}

// mallJSONSearch is one page of a mall's public search JSON turned into
// observations. Sold-out items count as rejected, never as candidates.
type mallJSONSearch struct {
	Products []researchdomain.ExternalProductObservation
	Items    int
	Rejected int
	HasNext  bool
}

// searchMallJSON is discovery and detail in one call for malls whose public
// search JSON already carries the original product number, title, price and
// image (Kurly). The mall page itself is never requested.
func (g *Gateway) searchMallJSON(ctx context.Context, mall researchdomain.KRMall, query string, page int) (mallJSONSearch, error) {
	result := mallJSONSearch{Products: []researchdomain.ExternalProductObservation{}}
	if mall.Source != researchdomain.SourceKurly || mall.DetailAPI == "" {
		return result, safeFailure("CATALOG_API_INVALID")
	}
	page = max(1, min(50, page))
	if err := g.ensureQuota(ctx, mall.DetailAPI); err != nil {
		return result, err
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			ListSections []struct {
				Data struct {
					Items tolerantItems[struct {
						No               json.Number `json:"no"`
						Name             string      `json:"name"`
						SalesPrice       json.Number `json:"salesPrice"`
						DiscountedPrice  json.Number `json:"discountedPrice"`
						IsSoldOut        bool        `json:"isSoldOut"`
						ListImageURL     string      `json:"listImageUrl"`
						ShortDescription string      `json:"shortDescription"`
					}] `json:"items"`
				} `json:"data"`
			} `json:"listSections"`
			Meta struct {
				Pagination struct {
					CurrentPage int `json:"currentPage"`
					TotalPages  int `json:"totalPages"`
				} `json:"pagination"`
			} `json:"meta"`
		} `json:"data"`
	}
	if _, err := g.get(ctx, mall.DetailAPI, "SEARCH", mall.DetailHost, "/search/v4/sites/market/normal-search", url.Values{"keyword": {query}, "page": {strconv.Itoa(page)}, "per_page": {"20"}}, &body); err != nil {
		return result, err
	}
	if !body.Success {
		return result, safeFailure("CATALOG_SCHEMA_MISMATCH")
	}
	result.HasNext = body.Data.Meta.Pagination.TotalPages > body.Data.Meta.Pagination.CurrentPage
	detail, _ := researchapp.CatalogAPIDefinitionFor(mall.DetailAPI)
	for _, section := range body.Data.ListSections {
		for _, item := range section.Data.Items {
			result.Items++
			if item.IsSoldOut {
				result.Rejected++
				continue
			}
			ref := researchdomain.SourceProductRef{Source: mall.Source, ProductID: item.No.String(), Marketplace: "KR"}
			if ref.Validate() != nil {
				result.Rejected++
				continue
			}
			canonical, _ := ref.ExternalProductURL()
			price := observedPrice(item.DiscountedPrice.String(), "KRW")
			if price.Kind != "OBSERVED" {
				price = observedPrice(item.SalesPrice.String(), "KRW")
			}
			o := researchdomain.ExternalProductObservation{SchemaVersion: "vitlane.external-product-observation.v1", ProductRef: ref, ProductURL: canonical, Title: cleanTitle(item.Name), Description: cleanTitle(item.ShortDescription), ImageURL: safeImage(item.ListImageURL), Price: price, PriceScope: "PRODUCT", Seller: researchdomain.ObservedSeller{Kind: "UNKNOWN"}, Provenance: researchdomain.ProductProvenance{APIProvider: detail.APIProvider, APIProduct: detail.APIProduct, DiscoveryChannel: "KURLY_SEARCH", Country: "KR", QueryLanguage: "ko"}, ObservedAt: time.Now().UTC()}
			if o.Validate() != nil {
				result.Rejected++
				continue
			}
			result.Products = append(result.Products, o)
		}
	}
	return result, nil
}

// publicProductPage reports whether public HTML carries product metadata.
// Live product pages expose og:title/og:image or JSON-LD Product; the HTTP 200
// stub some malls serve for discontinued or invalid product numbers has neither.
func publicProductPage(raw []byte) bool { return elevenStreetProductPage(raw) }

// elevenStreetProductPage reports whether public HTML carries product
// metadata. Live product pages expose og:title/og:image and JSON-LD Product;
// the HTTP 200 stub for discontinued or invalid product numbers has neither.
func elevenStreetProductPage(raw []byte) bool {
	z := html.NewTokenizer(bytes.NewReader(raw))
	scriptType := ""
	for {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken, html.SelfClosingTagToken:
			token := z.Token()
			if token.Data == "script" {
				scriptType = ""
				for _, a := range token.Attr {
					if a.Key == "type" {
						scriptType = a.Val
					}
				}
				continue
			}
			if token.Data != "meta" {
				continue
			}
			property, content := "", ""
			for _, a := range token.Attr {
				if a.Key == "property" || a.Key == "name" {
					property = strings.ToLower(a.Val)
				}
				if a.Key == "content" {
					content = a.Val
				}
			}
			if (property == "og:title" || property == "og:image") && strings.TrimSpace(content) != "" {
				return true
			}
		case html.TextToken:
			if scriptType == "application/ld+json" && strings.Contains(string(z.Text()), `"Product"`) {
				return true
			}
		case html.EndTagToken:
			if z.Token().Data == "script" {
				scriptType = ""
			}
		}
	}
}

var priceText = regexp.MustCompile(`^(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)(?:\.[0-9]{1,2})?$`)

func observedPrice(raw, currency string) researchdomain.VariantObservedPrice {
	unknown := researchdomain.VariantObservedPrice{Kind: "UNKNOWN", ReasonCode: "PRICE_NOT_REPORTED"}
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "₩") {
		currency = "KRW"
		value = strings.TrimSpace(strings.TrimPrefix(value, "₩"))
	} else if strings.HasPrefix(value, "$") {
		currency = "USD"
		value = strings.TrimSpace(strings.TrimPrefix(value, "$"))
	} else if strings.HasSuffix(value, "원") {
		currency = "KRW"
		value = strings.TrimSpace(strings.TrimSuffix(value, "원"))
	}
	if len(value) > 30 || !priceText.MatchString(value) || (currency != "KRW" && currency != "USD") {
		return unknown
	}
	number, ok := new(big.Rat).SetString(strings.ReplaceAll(value, ",", ""))
	if !ok {
		return unknown
	}
	if currency == "USD" {
		number.Mul(number, big.NewRat(100, 1))
	}
	if !number.IsInt() || !number.Num().IsInt64() || number.Sign() <= 0 || number.Num().Int64() > 9007199254740991 {
		return unknown
	}
	minor := number.Num().Int64()
	return researchdomain.VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &minor, Currency: currency}
}
func safeImage(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return ""
	}
	return raw
}
func cleanTitle(raw string) string {
	z := html.NewTokenizer(strings.NewReader(raw))
	var value strings.Builder
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			break
		}
		if kind == html.TextToken {
			value.Write(z.Text())
		}
	}
	text := strings.TrimSpace(stdhtml.UnescapeString(value.String()))
	runes := []rune(text)
	if len(runes) > 300 {
		text = string(runes[:300])
	}
	return text
}
func applyProductMetadata(o *researchdomain.ExternalProductObservation, raw []byte) {
	z := html.NewTokenizer(bytes.NewReader(raw))
	meta := map[string]string{}
	scriptType := ""
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			break
		}
		switch kind {
		case html.StartTagToken, html.SelfClosingTagToken:
			token := z.Token()
			if token.Data == "meta" {
				key, value := "", ""
				for _, a := range token.Attr {
					if a.Key == "property" || a.Key == "name" {
						key = strings.ToLower(a.Val)
					}
					if a.Key == "content" {
						value = a.Val
					}
				}
				meta[key] = value
			}
			if token.Data == "script" {
				for _, a := range token.Attr {
					if a.Key == "type" {
						scriptType = a.Val
					}
				}
			}
		case html.TextToken:
			if scriptType == "application/ld+json" {
				var object any
				decoder := json.NewDecoder(bytes.NewReader(z.Text()))
				decoder.UseNumber()
				if decoder.Decode(&object) == nil {
					readProductJSON(o, object)
				}
			}
		case html.EndTagToken:
			if z.Token().Data == "script" {
				scriptType = ""
			}
		}
	}
	if description := cleanTitle(meta["og:description"]); description != "" {
		o.Description = description
	}
	if title := cleanTitle(meta["og:title"]); title != "" {
		o.Title = title
	}
	o.ImageURL = safeImage(meta["og:image"])
	if o.Price.Kind != "OBSERVED" {
		// Korean malls price in KRW; several omit the currency meta.
		currency := meta["product:price:currency"]
		if currency == "" {
			currency = "KRW"
		}
		o.Price = observedPrice(meta["product:price:amount"], currency)
	}
}
func readProductJSON(o *researchdomain.ExternalProductObservation, value any) {
	switch node := value.(type) {
	case []any:
		for _, child := range node {
			readProductJSON(o, child)
		}
	case map[string]any:
		if node["@type"] == "Product" {
			// Related products may share the same document. A JSON-LD price
			// needs the requested original identity, not merely type Product.
			matched := false
			if raw, ok := node["url"].(string); ok {
				ref, err := researchdomain.SourceProductFromURL(raw)
				if err != nil || ref != o.ProductRef {
					return
				}
				matched = true
			}
			for _, key := range []string{"sku", "productID"} {
				if id, ok := node[key].(string); ok && id == o.ProductRef.ProductID {
					matched = true
				}
			}
			if !matched {
				return
			}
			if offer, ok := node["offers"].(map[string]any); ok {
				currency, _ := offer["priceCurrency"].(string)
				var price string
				switch n := offer["price"].(type) {
				case string:
					price = n
				case json.Number:
					price = n.String()
				}
				o.Price = observedPrice(price, currency)
			}
		}
		if graph, ok := node["@graph"]; ok {
			readProductJSON(o, graph)
		}
	}
}

// The initial page was received; only item resolution failed. Call accounting
// still records each failed detail request, while the next query may advance.
type partialDiscoveryError struct{ cause error }

func (e *partialDiscoveryError) Error() string { return e.cause.Error() }
func (e *partialDiscoveryError) Unwrap() error { return e.cause }

// An invalid item must not erase a valid neighbor or the page's original size.
type tolerantItems[T any] []T

func (items *tolerantItems[T]) UnmarshalJSON(raw []byte) error {
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return err
	}
	*items = make([]T, len(rows))
	for i, row := range rows {
		var value T
		if json.Unmarshal(row, &value) == nil {
			(*items)[i] = value
		}
	}
	return nil
}
