// Package apifyactor buys one search result from a third-party Apify Actor for
// the Korean malls that answer neither Vitlane nor web search.
//
// Vitlane implements no circumvention of its own here: it calls the Apify API,
// reads what the Actor returns, and records what the run cost. The owner
// allowed this path on 2026-09-16 (PLAN §8 Q12) with a monthly budget cap.
package apifyactor

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

// actorSpec is the whole per-mall contract: which Actor to run, what to send
// it, and how to read one of its dataset items. Adding a mall is adding an
// entry, and an Actor Vitlane does not know is never run.
type actorSpec struct {
	actor string
	input func(query string, limit int) map[string]any
	item  func(raw map[string]any) actorItem
	// startMicros and itemMicros are the Actor's published event prices as
	// measured on 2026-09-16. The provider settles per-item charges a little
	// after a run ends, so a run read at completion reports only its start
	// event. The ledger therefore records at least what these prices imply,
	// and the monthly cap counts a run the moment it is bought.
	startMicros int64
	itemMicros  int64
}

// actorItem is the small, provider-neutral shape the mapper produces. Missing
// values stay empty so the observation says "unknown" instead of inventing.
type actorItem struct {
	url       string
	title     string
	price     string
	imageURL  string
	soldOut   bool
	sponsored bool
}

var koreanProxy = map[string]any{
	"useApifyProxy":     true,
	"apifyProxyGroups":  []string{"RESIDENTIAL"},
	"apifyProxyCountry": "KR",
}

// Measured 2026-09-16: Musinsa and 29CM cost $0.004 per item, Gmarket $0.001,
// all under 13 seconds.
var actorSpecs = map[researchdomain.Source]actorSpec{
	researchdomain.SourceMusinsa: {
		actor:       "kdatafactory~musinsa-scraper",
		startMicros: 50, itemMicros: 4000,
		input: func(query string, limit int) map[string]any {
			return map[string]any{"mode": "search", "query": query, "maxItems": limit}
		},
		item: kdataFactoryItem,
	},
	researchdomain.SourceTwentyNineCM: {
		actor:       "kdatafactory~29cm-scraper",
		startMicros: 50, itemMicros: 4000,
		input: func(query string, limit int) map[string]any {
			return map[string]any{"mode": "search", "query": query, "maxItems": limit, "proxyConfiguration": koreanProxy}
		},
		item: kdataFactoryItem,
	},
	researchdomain.SourceGmarket: {
		actor:       "getascraper~gmarket-scraper",
		startMicros: 50, itemMicros: 990,
		input: func(query string, limit int) map[string]any {
			return map[string]any{"query": query, "maxItems": limit, "proxyConfiguration": koreanProxy}
		},
		item: func(raw map[string]any) actorItem {
			return actorItem{
				url:       text(raw["product_url"]),
				title:     text(raw["title"]),
				price:     number(raw["price_krw"]),
				imageURL:  text(raw["image_url"]),
				sponsored: flag(raw["is_sponsored"]),
			}
		},
	},
}

func kdataFactoryItem(raw map[string]any) actorItem {
	price := number(raw["sale_price_krw"])
	if price == "" {
		price = number(raw["price_krw"])
	}
	return actorItem{
		url:      text(raw["url"]),
		title:    text(raw["name"]),
		price:    price,
		imageURL: text(raw["image_url"]),
		soldOut:  flag(raw["is_sold_out"]),
	}
}

func text(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func number(value any) string {
	switch typed := value.(type) {
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case string:
		return strings.TrimSpace(typed)
	}
	return ""
}

func flag(value any) bool {
	got, _ := value.(bool)
	return got
}

// usdMicros converts the provider's reported dollars into the integer micros
// the ledger stores, rounding up so a cap is never crossed by rounding.
func usdMicros(value any) int64 {
	raw := number(value)
	if raw == "" {
		return 0
	}
	amount, err := strconv.ParseFloat(raw, 64)
	if err != nil || amount <= 0 {
		return 0
	}
	micros := int64(amount*1_000_000) + 1
	return micros
}

func datasetPath(datasetID string, limit int) string {
	return "/v2/datasets/" + url.PathEscape(datasetID) + "/items?clean=true&limit=" + strconv.Itoa(limit)
}

func terminal(status string) bool {
	switch status {
	case "SUCCEEDED", "FAILED", "ABORTED", "TIMED-OUT", "TIMING-OUT", "ABORTING":
		return true
	}
	return false
}

const (
	pollInterval   = 3 * time.Second
	runWallClock   = 100 * time.Second
	runTimeoutSecs = 120
	runMemoryMB    = 1024
)

// runCost is what the ledger records: the provider's own number when it has
// settled, and the published event prices otherwise. Under-recording would let
// the monthly cap pass runs the account is actually paying for.
func (s actorSpec) runCost(reported any, items int) int64 {
	if items < 0 {
		items = 0
	}
	floor := s.startMicros + int64(items)*s.itemMicros
	if got := usdMicros(reported); got > floor {
		return got
	}
	return floor
}
