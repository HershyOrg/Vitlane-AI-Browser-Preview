package domain

import (
	"net/url"
	"regexp"
	"strings"
)

// KRMall describes one Korean shopping platform a customer may be sent to.
// The registry replaces per-source switches: it owns which hosts count as the
// platform, how the original product id is read from a product page URL, how
// the outbound URL is rebuilt from that id, and the id grammar. Adding a mall
// is adding an entry; no other identity code changes.
//
// URL shapes come from a reviewed 2026-09-15 platform sample. Whether a
// mall's pages may be fetched is a separate policy question owned by the
// catalog adapters; the registry only names products.
type KRMall struct {
	Source  Source
	LabelEN string
	LabelKO string
	// Hosts are the accepted hostnames without a leading "www.".
	Hosts []string
	// ID is the grammar of the original product id.
	ID *regexp.Regexp
	// Verticals says which product verticals this mall is worth asking for.
	// Nil means every vertical (a general marketplace).
	Verticals []Vertical
	// SiteFilter is the web-search restriction that finds this mall's
	// original product pages ("site:zigzag.kr/catalog/products").
	SiteFilter string
	// BrowserSearch says the reviewed ephemeral browser adapter knows this
	// mall's public search page and canonical product-link shape. It is a
	// discovery capability only: Source still names the merchant, while the
	// browser is recorded in observation provenance.
	BrowserSearch bool
	// Detail says how an observation gets its title, price and image beyond
	// the Google Shopping offer: the mall's public product page, its public
	// search JSON, or nothing (the offer is all we use).
	Detail DetailStrategy
	// DetailAPI is the operator-controlled API product id that owns the
	// mall's public-page or JSON calls (On/Off, local caps, ledger).
	DetailAPI string
	// DetailHost is the https origin the detail calls go to.
	DetailHost string
	// productID reads the id from a parsed original product page URL and
	// returns "" for anything else (search, Q&A, category, affiliate pages).
	productID func(u *url.URL) string
	canonical func(id string) string
}

// Vertical is the product family a research Round is about. The query step
// names it; the registry uses it to decide which specialised malls to ask.
type Vertical string

const (
	VerticalGeneral     Vertical = "GENERAL"
	VerticalFashion     Vertical = "FASHION"
	VerticalBeauty      Vertical = "BEAUTY"
	VerticalFood        Vertical = "FOOD"
	VerticalLiving      Vertical = "LIVING"
	VerticalElectronics Vertical = "ELECTRONICS"
)

// KnownVertical reports whether v is a vertical the registry understands.
func KnownVertical(v string) bool {
	switch Vertical(v) {
	case VerticalGeneral, VerticalFashion, VerticalBeauty, VerticalFood, VerticalLiving, VerticalElectronics:
		return true
	}
	return false
}

type DetailStrategy string

const (
	// DetailOWNOffer: the Google Shopping offer (title, price, URL) is the
	// whole observation; the mall itself is never called.
	DetailOWNOffer DetailStrategy = "OWN_OFFER"
	// DetailPublicHTML: the mall's public product page supplies og/JSON-LD
	// metadata for URLs found through web search.
	DetailPublicHTML DetailStrategy = "PUBLIC_HTML"
	// DetailPublicJSON: the mall's public search JSON is both discovery and
	// detail.
	DetailPublicJSON DetailStrategy = "PUBLIC_JSON"
	// DetailActor: the mall answers neither us nor web search, so a paid
	// third-party Actor run is both discovery and detail. Vitlane never works
	// around the block itself; it buys the result of one (owner decision
	// 2026-09-16, PLAN Q12).
	DetailActor DetailStrategy = "ACTOR"
)

// Serves reports whether the mall is worth asking for a vertical. An unknown
// or empty vertical only reaches general malls.
func (m KRMall) Serves(vertical Vertical) bool {
	if len(m.Verticals) == 0 {
		return true
	}
	for _, v := range m.Verticals {
		if v == vertical {
			return true
		}
	}
	return false
}

// DetailRequest splits the canonical product URL into the path and query the
// detail call needs, so callers never hand-build mall URLs.
func (m KRMall) DetailRequest(id string) (path string, query url.Values) {
	u, err := url.Parse(m.canonical(id))
	if err != nil {
		return "", nil
	}
	return u.Path, u.Query()
}

var (
	numericID     = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	naverStoreID  = regexp.MustCompile(`^[a-z0-9_-]{1,80}/[1-9][0-9]{0,19}$`)
	oliveyoungID  = regexp.MustCompile(`^A[0-9]{12}$`)
	gmarketID     = regexp.MustCompile(`^[0-9]{6,12}$`)
	auctionID     = regexp.MustCompile(`^[A-Z][0-9]{6,12}$`)
	ssgID         = regexp.MustCompile(`^[0-9]{6,20}$`)
	lotteonID     = regexp.MustCompile(`^L[OM][0-9]{6,14}$`)
	daisomallID   = regexp.MustCompile(`^[0-9]{5,12}$`)
	coupangPath   = regexp.MustCompile(`^/vp/products/([1-9][0-9]{0,19})/?$`)
	elevenstPath  = regexp.MustCompile(`^/products/([1-9][0-9]{0,19})/?$`)
	naverPath     = regexp.MustCompile(`^/([a-z0-9_-]{1,80})/products/([1-9][0-9]{0,19})/?$`)
	musinsaPath   = regexp.MustCompile(`^/(?:products|app/goods)/([1-9][0-9]{0,19})/?$`)
	cm29Path      = regexp.MustCompile(`^/(?:catalog|product|products)/([1-9][0-9]{0,19})/?$`)
	kurlyPath     = regexp.MustCompile(`^/goods/([1-9][0-9]{0,19})/?$`)
	zigzagPath    = regexp.MustCompile(`^/catalog/products/([1-9][0-9]{0,19})/?$`)
	wconceptPath  = regexp.MustCompile(`(?i)^/product/([1-9][0-9]{0,19})/?$`)
	ohousePath    = regexp.MustCompile(`^/productions/([1-9][0-9]{0,19})(?:/selling)?/?$`)
	lotteonPath   = regexp.MustCompile(`^/p/product/(L[OM][0-9]{6,14})/?$`)
	gmarketPath   = regexp.MustCompile(`(?i)^/item/?$`)
	auctionPath   = regexp.MustCompile(`(?i)^/detailview\.aspx$`)
	ssgPath       = regexp.MustCompile(`^/item/itemView\.ssg$`)
	daisomallPath = regexp.MustCompile(`^/pd/pdr/SCR_PDR_0001$`)
	oliveyongPath = regexp.MustCompile(`^/store/goods/getGoodsDetail\.do$`)
)

func pathID(pattern *regexp.Regexp) func(*url.URL) string {
	return func(u *url.URL) string {
		if m := pattern.FindStringSubmatch(u.Path); m != nil {
			return m[1]
		}
		return ""
	}
}

func queryID(pattern *regexp.Regexp, key string, grammar *regexp.Regexp) func(*url.URL) string {
	return func(u *url.URL) string {
		if !pattern.MatchString(u.Path) {
			return ""
		}
		value := ""
		for name, values := range u.Query() {
			if strings.EqualFold(name, key) && len(values) > 0 {
				value = values[0]
			}
		}
		if !grammar.MatchString(value) {
			return ""
		}
		return value
	}
}

func prefixURL(prefix string) func(string) string {
	return func(id string) string { return prefix + id }
}

var koreanMalls = []KRMall{
	{Source: SourceCoupang, LabelEN: "Coupang", LabelKO: "쿠팡", Hosts: []string{"coupang.com"}, ID: numericID,
		productID: pathID(coupangPath), canonical: prefixURL("https://www.coupang.com/vp/products/")},
	{Source: SourceElevenStreet, LabelEN: "11st", LabelKO: "11번가", Hosts: []string{"11st.co.kr"}, ID: numericID,
		SiteFilter: "site:11st.co.kr/products", BrowserSearch: true, Detail: DetailPublicHTML, DetailAPI: "ELEVENST_HTML", DetailHost: "https://www.11st.co.kr",
		productID: func(u *url.URL) string {
			// 11st serves Q&A lists and other action views under the same
			// /products/{id} path with a `method=` query. Search engines
			// index them as separate documents titled by their first post,
			// so they are not original product pages and never seed a candidate.
			if u.Query().Has("method") {
				return ""
			}
			return pathID(elevenstPath)(u)
		}, canonical: prefixURL("https://www.11st.co.kr/products/")},
	{Source: SourceNaverSmartstore, LabelEN: "Naver Smart Store", LabelKO: "네이버 스마트스토어", Hosts: []string{"smartstore.naver.com"}, ID: naverStoreID,
		productID: naverStoreProductID, canonical: naverStoreURL("https://smartstore.naver.com/")},
	{Source: SourceNaverBrandstore, LabelEN: "Naver Brand Store", LabelKO: "네이버 브랜드스토어", Hosts: []string{"brand.naver.com"}, ID: naverStoreID,
		productID: naverStoreProductID, canonical: naverStoreURL("https://brand.naver.com/")},
	{Source: SourceMusinsa, LabelEN: "Musinsa", LabelKO: "무신사", Hosts: []string{"musinsa.com"}, ID: numericID, Verticals: []Vertical{VerticalFashion},
		BrowserSearch: true, Detail: DetailActor, DetailAPI: "APIFY_MUSINSA",
		productID: pathID(musinsaPath), canonical: prefixURL("https://www.musinsa.com/products/")},
	{Source: SourceTwentyNineCM, LabelEN: "29CM", LabelKO: "29CM", Hosts: []string{"29cm.co.kr", "product.29cm.co.kr"}, ID: numericID, Verticals: []Vertical{VerticalFashion, VerticalLiving},
		Detail: DetailActor, DetailAPI: "APIFY_29CM",
		productID: pathID(cm29Path), canonical: prefixURL("https://product.29cm.co.kr/catalog/")},
	{Source: SourceOliveyoung, LabelEN: "Olive Young", LabelKO: "올리브영", Hosts: []string{"oliveyoung.co.kr"}, ID: oliveyoungID, Verticals: []Vertical{VerticalBeauty},
		productID: queryID(oliveyongPath, "goodsNo", oliveyoungID), canonical: prefixURL("https://www.oliveyoung.co.kr/store/goods/getGoodsDetail.do?goodsNo=")},
	{Source: SourceKurly, LabelEN: "Kurly", LabelKO: "컬리", Hosts: []string{"kurly.com"}, ID: numericID, Verticals: []Vertical{VerticalFood},
		BrowserSearch: true, Detail: DetailPublicJSON, DetailAPI: "KURLY_JSON", DetailHost: "https://api.kurly.com",
		productID: pathID(kurlyPath), canonical: prefixURL("https://www.kurly.com/goods/")},
	{Source: SourceGmarket, LabelEN: "Gmarket", LabelKO: "G마켓", Hosts: []string{"gmarket.co.kr", "item.gmarket.co.kr", "mg.gmarket.co.kr"}, ID: gmarketID,
		Detail: DetailActor, DetailAPI: "APIFY_GMARKET",
		productID: queryID(gmarketPath, "goodscode", gmarketID), canonical: prefixURL("https://item.gmarket.co.kr/Item?goodscode=")},
	{Source: SourceAuction, LabelEN: "Auction", LabelKO: "옥션", Hosts: []string{"auction.co.kr", "itempage3.auction.co.kr"}, ID: auctionID,
		productID: queryID(auctionPath, "itemno", auctionID), canonical: prefixURL("https://itempage3.auction.co.kr/DetailView.aspx?itemno=")},
	{Source: SourceSSG, LabelEN: "SSG", LabelKO: "SSG", Hosts: []string{"ssg.com"}, ID: ssgID,
		productID: queryID(ssgPath, "itemId", ssgID), canonical: prefixURL("https://www.ssg.com/item/itemView.ssg?itemId=")},
	{Source: SourceZigzag, LabelEN: "Zigzag", LabelKO: "지그재그", Hosts: []string{"zigzag.kr", "store.zigzag.kr"}, ID: numericID, Verticals: []Vertical{VerticalFashion},
		SiteFilter: "site:zigzag.kr/catalog/products", Detail: DetailPublicHTML, DetailAPI: "ZIGZAG_HTML", DetailHost: "https://zigzag.kr",
		productID: pathID(zigzagPath), canonical: prefixURL("https://zigzag.kr/catalog/products/")},
	{Source: SourceWconcept, LabelEN: "W Concept", LabelKO: "W컨셉", Hosts: []string{"wconcept.co.kr"}, ID: numericID, Verticals: []Vertical{VerticalFashion},
		productID: pathID(wconceptPath), canonical: prefixURL("https://www.wconcept.co.kr/product/")},
	{Source: SourceOhouse, LabelEN: "Ohouse", LabelKO: "오늘의집", Hosts: []string{"ohou.se"}, ID: numericID, Verticals: []Vertical{VerticalLiving},
		productID: pathID(ohousePath), canonical: func(id string) string { return "https://ohou.se/productions/" + id + "/selling" }},
	{Source: SourceLotteon, LabelEN: "Lotte ON", LabelKO: "롯데온", Hosts: []string{"lotteon.com"}, ID: lotteonID, Verticals: []Vertical{VerticalElectronics, VerticalLiving, VerticalBeauty},
		SiteFilter: "site:lotteon.com/p/product", Detail: DetailPublicHTML, DetailAPI: "LOTTEON_HTML", DetailHost: "https://www.lotteon.com",
		productID: pathID(lotteonPath), canonical: prefixURL("https://www.lotteon.com/p/product/")},
	{Source: SourceDaisomall, LabelEN: "Daiso Mall", LabelKO: "다이소몰", Hosts: []string{"daisomall.co.kr"}, ID: daisomallID, Verticals: []Vertical{VerticalLiving},
		SiteFilter: "site:daisomall.co.kr/pd/pdr", Detail: DetailPublicHTML, DetailAPI: "DAISOMALL_HTML", DetailHost: "https://www.daisomall.co.kr",
		productID: queryID(daisomallPath, "pdNo", daisomallID), canonical: prefixURL("https://www.daisomall.co.kr/pd/pdr/SCR_PDR_0001?pdNo=")},
}

func naverStoreProductID(u *url.URL) string {
	if m := naverPath.FindStringSubmatch(strings.ToLower(u.Path)); m != nil {
		return m[1] + "/" + m[2]
	}
	return ""
}

func naverStoreURL(base string) func(string) string {
	return func(id string) string {
		slug, number, _ := strings.Cut(id, "/")
		return base + slug + "/products/" + number
	}
}

// KoreanMalls returns the registry in display order.
func KoreanMalls() []KRMall { return append([]KRMall(nil), koreanMalls...) }

// KoreanMallsToAsk returns the malls a Round should search directly for a
// vertical (through web search plus the mall's public page, or through the
// mall's public JSON), in registry order. Malls whose only path is the Google
// Shopping offer are not listed: they are reached through OWN Product.
func KoreanMallsToAsk(vertical Vertical) []KRMall {
	out := []KRMall{}
	for _, mall := range koreanMalls {
		if mall.Detail == DetailOWNOffer || mall.Detail == "" || !mall.Serves(vertical) {
			continue
		}
		out = append(out, mall)
	}
	return out
}

// KoreanMall finds the registry entry for a Source.
func KoreanMall(source Source) (KRMall, bool) {
	for _, mall := range koreanMalls {
		if mall.Source == source {
			return mall, true
		}
	}
	return KRMall{}, false
}

func koreanMallForHost(host string) (KRMall, bool) {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	for _, mall := range koreanMalls {
		for _, candidate := range mall.Hosts {
			if host == candidate {
				return mall, true
			}
		}
	}
	return KRMall{}, false
}
