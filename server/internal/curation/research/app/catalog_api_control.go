package app

import (
	"context"
	"time"
)

type CatalogAPIDefinition struct {
	ID                string `json:"id"`
	APIProvider       string `json:"apiProvider"`
	APIProduct        string `json:"apiProduct"`
	QuotaScope        string `json:"quotaScope"`
	RequestsPerMinute int    `json:"localRequestsPerMinute"`
	DailyLimit        int    `json:"localDailyLimit"`
	// MaxConcurrent bounds in-flight calls per API product across every Round
	// on every replica. Provider APIs tolerate a second parallel call; public
	// HTML hosts get one at a time as a politeness rule, not a provider limit.
	MaxConcurrent int  `json:"localMaxConcurrent"`
	NeedsQuota    bool `json:"-"`
	// Keyless APIs are public pages or public JSON of a mall: no credential,
	// no provider quota, only the local caps and the operator switch.
	Keyless bool `json:"-"`
}

// KoreanCatalogAPIs lists every API product the Korean research path may
// call. Local caps are Vitlane safeguards, not provider limits. NAVER Webkr
// fans out per mall (one call per asked mall per Round), so its daily cap
// covers several calls per Round. Public-page rows follow each mall's
// crawl-delay where one is published (Daiso Mall: 30 seconds).
var KoreanCatalogAPIs = []CatalogAPIDefinition{
	{ID: "BROWSER_MERCHANT", APIProvider: "Vitlane Browser Fork", APIProduct: "Ephemeral public merchant search", QuotaScope: "browser_merchant", RequestsPerMinute: 6, DailyLimit: 120, MaxConcurrent: 1},
	{ID: "OWN_PRODUCT", APIProvider: "OpenWebNinja", APIProduct: "Real-Time Product Search v2", QuotaScope: "realtime_product_search", RequestsPerMinute: 20, DailyLimit: 200, MaxConcurrent: 2, NeedsQuota: true},
	{ID: "OWN_WEB", APIProvider: "OpenWebNinja", APIProduct: "Real-Time Web Search", QuotaScope: "realtime_web_search", RequestsPerMinute: 20, DailyLimit: 20, MaxConcurrent: 2, NeedsQuota: true},
	{ID: "NAVER_WEBKR", APIProvider: "NAVER API HUB", APIProduct: "Search Webkr", QuotaScope: "naver_search_shared", RequestsPerMinute: 30, DailyLimit: 400, MaxConcurrent: 2},
	{ID: "SERP_GOOGLE", APIProvider: "SerpApi", APIProduct: "Google Search", QuotaScope: "serpapi_account", RequestsPerMinute: 10, DailyLimit: 30, MaxConcurrent: 1, NeedsQuota: true},
	{ID: "ELEVENST_HTML", APIProvider: "11st", APIProduct: "Public product page", QuotaScope: "elevenst_public_html", RequestsPerMinute: 5, DailyLimit: 50, MaxConcurrent: 1, Keyless: true},
	{ID: "KURLY_JSON", APIProvider: "Kurly", APIProduct: "Public search JSON", QuotaScope: "kurly_public_json", RequestsPerMinute: 10, DailyLimit: 100, MaxConcurrent: 1, Keyless: true},
	{ID: "ZIGZAG_HTML", APIProvider: "Zigzag", APIProduct: "Public product page", QuotaScope: "zigzag_public_html", RequestsPerMinute: 5, DailyLimit: 50, MaxConcurrent: 1, Keyless: true},
	{ID: "LOTTEON_HTML", APIProvider: "Lotte ON", APIProduct: "Public product page", QuotaScope: "lotteon_public_html", RequestsPerMinute: 5, DailyLimit: 50, MaxConcurrent: 1, Keyless: true},
	{ID: "DAISOMALL_HTML", APIProvider: "Daiso Mall", APIProduct: "Public product page", QuotaScope: "daisomall_public_html", RequestsPerMinute: 2, DailyLimit: 50, MaxConcurrent: 1, Keyless: true},
	// Paid third-party Actor runs. One Round starts at most one run per mall,
	// and the monthly USD cap in the run ledger stops them well before these
	// daily counts matter.
	{ID: "APIFY_MUSINSA", APIProvider: "Apify", APIProduct: "kdatafactory/musinsa-scraper", QuotaScope: "apify_account", RequestsPerMinute: 2, DailyLimit: 20, MaxConcurrent: 1},
	{ID: "APIFY_29CM", APIProvider: "Apify", APIProduct: "kdatafactory/29cm-scraper", QuotaScope: "apify_account", RequestsPerMinute: 2, DailyLimit: 20, MaxConcurrent: 1},
	{ID: "APIFY_GMARKET", APIProvider: "Apify", APIProduct: "getascraper/gmarket-scraper", QuotaScope: "apify_account", RequestsPerMinute: 2, DailyLimit: 20, MaxConcurrent: 1},
}

// CatalogAdmissionTransient reports whether a provider-call reason is a
// momentary condition (another Round holds the slot, the provider throttled,
// the network or the deadline gave out) rather than a configuration, quota or
// contract problem. Only transient reasons justify reopening a Round.
func CatalogAdmissionTransient(reason string) bool {
	switch reason {
	case "CATALOG_API_RATE_LIMITED", "CATALOG_UPSTREAM_RATE_LIMITED", "CATALOG_QUOTA_UNCONFIRMED",
		"CATALOG_TIMEOUT", "CATALOG_NETWORK_FAILED", "CATALOG_UPSTREAM_FAILED", "CATALOG_RESULT_UNKNOWN",
		"CATALOG_REQUEST_CANCELLED", "CATALOG_CALL_FINALIZATION_FAILED":
		return true
	}
	return false
}

// ShopifyCatalogAPIID is the ledger id of the Shopify catalog (UCP search and
// lookup): every research search, every page's price-and-name lookup and every
// order preparation lookup is one call of this API product.
const ShopifyCatalogAPIID = "SHOPIFY_UCP"

// ShopifyCatalogAPI puts Shopify in the same operator ledger as every other
// research API: calls, failures by reason, and the On/Off switch. Its local
// caps are deliberately far above real traffic. The binding rate guard for
// Shopify remains the in-process window (CURATION_CATALOG_RESEARCH_RATE and
// its concurrency), which already answers the screen with a retry time; the
// ledger must record Shopify, not throttle the price lookups every page makes.
var ShopifyCatalogAPI = CatalogAPIDefinition{
	ID: ShopifyCatalogAPIID, APIProvider: "Shopify", APIProduct: "Global Catalog search and lookup (UCP)",
	QuotaScope: "shopify_ucp", RequestsPerMinute: 600, DailyLimit: 200000, MaxConcurrent: 64,
}

// OperatorCatalogAPIs is every API product the operator's usage page lists.
func OperatorCatalogAPIs() []CatalogAPIDefinition {
	return append([]CatalogAPIDefinition{ShopifyCatalogAPI, {ID: "TELEGRAM_JIRUM", APIProvider: "Telegram", APIProduct: "Public channel feed (jirum)", QuotaScope: "telegram_jirum", RequestsPerMinute: 9, DailyLimit: 864, MaxConcurrent: 1, Keyless: true}}, KoreanCatalogAPIs...)
}

func CatalogAPIDefinitionFor(id string) (CatalogAPIDefinition, bool) {
	for _, d := range OperatorCatalogAPIs() {
		if d.ID == id {
			return d, true
		}
	}
	return CatalogAPIDefinition{}, false
}

// Only quota values confirmed by the API are projected. A missing plan tier
// must not become the legacy Amazon quota type's false (paid) zero value.
type CatalogProviderQuota struct {
	Limit      int64     `json:"limit"`
	Used       int64     `json:"used"`
	Remaining  int64     `json:"remaining"`
	ResetAt    time.Time `json:"resetAt"`
	ObservedAt time.Time `json:"observedAt"`
}

// CatalogOutcomeProductNotFound is a success-class call outcome: the provider
// answered normally and the document carried no product, such as 11st's HTTP
// 200 stub for discontinued or invalid product numbers. Usage reports count it
// apart from failures and it never triggers retry or backoff.
const CatalogOutcomeProductNotFound = "CATALOG_PRODUCT_NOT_FOUND"

type CatalogOperationUsage struct {
	Operation   string `json:"operation"`
	Requests24h int64  `json:"requests24h"`
	Failures24h int64  `json:"failures24h"`
	DailyLimit  int    `json:"localDailyLimit,omitempty"`
}

func TelegramOperationLimits(operation string) (int, int) {
	switch operation {
	case "FEED_PAGE":
		return 5, 480
	case "LINK_RESOLVE":
		return 4, 384
	}
	return 0, 0
}

type CatalogProviderUsage struct {
	Operations24h []CatalogOperationUsage `json:"operations24h,omitempty"`
	SchemaVersion string                  `json:"schemaVersion"`
	CatalogAPIDefinition
	Configured         bool                     `json:"configured"`
	Control            AmazonSourceControl      `json:"control"`
	Quota              *CatalogProviderQuota    `json:"quota,omitempty"`
	EstimatedRemaining *int64                   `json:"estimatedRemaining,omitempty"`
	Requests24h        int64                    `json:"requests24h"`
	NotFound24h        int64                    `json:"notFound24h"`
	Resources          *ProviderResourceState   `json:"resources,omitempty"`
	Failures24h        []CatalogAPIFailureCount `json:"failures24h"`
}

type CatalogProviderControlRepository interface {
	ReadProviderUsage(context.Context, string) (CatalogProviderUsage, error)
	UpdateProviderControl(context.Context, string, string, bool, int64, time.Time) (AmazonSourceControl, error)
	ReserveProviderCall(context.Context, string, string, time.Time) (string, error)
	CompleteProviderCall(context.Context, string, string, int, int, time.Time) error
	SaveProviderQuota(context.Context, string, CatalogAPIQuota, time.Time) error
}
