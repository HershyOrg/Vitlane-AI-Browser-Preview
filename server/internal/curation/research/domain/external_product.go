package domain

import (
	"net/url"
	"regexp"
	"strings"
	"time"
)

var originalProductID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

// KoreanExternal reports whether the Source is a registered Korean mall: a
// platform the customer buys from directly, outside Shopify checkout.
func (s Source) KoreanExternal() bool {
	_, ok := KoreanMall(s)
	return ok
}

func (r SourceProductRef) ExternalProductURL() (string, error) {
	if r.Validate() != nil {
		return "", ErrSourceReference
	}
	mall, ok := KoreanMall(r.Source)
	if !ok {
		return "", ErrSourceReference
	}
	return mall.canonical(r.ProductID), nil
}

// SourceProductFromURL accepts original product pages of registered Korean
// malls, never search results, affiliate bridges or comparison identifiers.
// Tracking query parameters are tolerated; action views are not.
func SourceProductFromURL(raw string) (SourceProductRef, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return SourceProductRef{}, ErrSourceReference
	}
	mall, ok := koreanMallForHost(u.Hostname())
	if !ok {
		return SourceProductRef{}, ErrSourceReference
	}
	ref := SourceProductRef{Source: mall.Source, ProductID: mall.productID(u), Marketplace: "KR"}
	if ref.ProductID == "" || ref.Validate() != nil {
		return SourceProductRef{}, ErrSourceReference
	}
	return ref, nil
}

type ProductProvenance struct {
	DetailAPIProvider string `json:"detailApiProvider,omitempty"`
	DetailAPIProduct  string `json:"detailApiProduct,omitempty"`
	APIProvider       string `json:"apiProvider"`
	APIProduct        string `json:"apiProduct"`
	DiscoveryChannel  string `json:"discoveryChannel"`
	Country           string `json:"country"`
	QueryLanguage     string `json:"queryLanguage"`
	ProviderLookupID  string `json:"providerLookupId,omitempty"`
}

// ExternalProductObservation extends the existing CatalogProductObservation.
// A product-level price is not an exact Variant, checkout quote, or stock promise.
type ExternalProductObservation struct {
	Description       string               `json:"description,omitempty"`
	SchemaVersion     string               `json:"schemaVersion"`
	ProductRef        SourceProductRef     `json:"productRef"`
	ProductURL        string               `json:"productUrl"`
	Title             string               `json:"title"`
	ImageURL          string               `json:"imageUrl,omitempty"`
	Price             VariantObservedPrice `json:"price"`
	PriceScope        string               `json:"priceScope"`
	Seller            ObservedSeller       `json:"seller"`
	Provenance        ProductProvenance    `json:"provenance"`
	OriginalItemID    string               `json:"originalItemId,omitempty"`
	OriginalListingID string               `json:"originalListingId,omitempty"`
	ObservedAt        time.Time            `json:"observedAt"`
}

func (o ExternalProductObservation) Validate() error {
	ref, err := SourceProductFromURL(o.ProductURL)
	if err != nil || ref != o.ProductRef || o.SchemaVersion != "vitlane.external-product-observation.v1" || strings.TrimSpace(o.Title) == "" || len(o.Title) > 2000 || len(o.Description) > 4000 || o.PriceScope != "PRODUCT" || o.ObservedAt.IsZero() {
		return ErrSourceReference
	}
	if o.Provenance.APIProvider == "" || o.Provenance.APIProduct == "" || o.Provenance.DiscoveryChannel == "" || o.Provenance.Country != "KR" || o.Provenance.QueryLanguage != "ko" {
		return ErrSourceReference
	}
	if o.Seller.Kind != "UNKNOWN" && o.Seller.Kind != "KNOWN" {
		return ErrSourceReference
	}
	if o.Seller.Kind == "KNOWN" && (o.Seller.ID == "" || o.Seller.Name == "") {
		return ErrSourceReference
	}
	if o.Seller.Kind == "UNKNOWN" && (o.Seller.ID != "" || o.Seller.Name != "") {
		return ErrSourceReference
	}
	if o.Price.Kind == "OBSERVED" {
		if o.Price.AmountMinor == nil || *o.Price.AmountMinor < 0 || *o.Price.AmountMinor > 9007199254740991 || (o.Price.Currency != "KRW" && o.Price.Currency != "USD") {
			return ErrSourceReference
		}
	} else if o.Price.Kind != "UNKNOWN" || o.Price.AmountMinor != nil || o.Price.ReasonCode == "" {
		return ErrSourceReference
	}
	for _, id := range []string{o.OriginalItemID, o.OriginalListingID} {
		if id != "" && (o.ProductRef.Source != SourceCoupang || !originalProductID.MatchString(id)) {
			return ErrSourceReference
		}
	}
	if o.ImageURL != "" {
		u, err := url.Parse(o.ImageURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return ErrSourceReference
		}
	}
	return nil
}
