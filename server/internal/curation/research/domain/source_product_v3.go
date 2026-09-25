package domain

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Source identifies the customer's purchasing platform, never the data vendor.
type Source string

const (
	SourceShopify         Source = "SHOPIFY"
	SourceAmazon          Source = "AMAZON"
	SourceCoupang         Source = "COUPANG"
	SourceElevenStreet    Source = "ELEVENST"
	SourceNaverSmartstore Source = "NAVER_SMARTSTORE"
	SourceNaverBrandstore Source = "NAVER_BRANDSTORE"
	SourceMusinsa         Source = "MUSINSA"
	SourceTwentyNineCM    Source = "TWENTYNINECM"
	SourceOliveyoung      Source = "OLIVEYOUNG"
	SourceKurly           Source = "KURLY"
	SourceGmarket         Source = "GMARKET"
	SourceAuction         Source = "AUCTION"
	SourceSSG             Source = "SSG"
	SourceZigzag          Source = "ZIGZAG"
	SourceWconcept        Source = "WCONCEPT"
	SourceOhouse          Source = "OHOUSE"
	SourceLotteon         Source = "LOTTEON"
	SourceDaisomall       Source = "DAISOMALL"
)

var ErrSourceReference = errors.New("SOURCE_REFERENCE_INVALID")
var amazonASIN = regexp.MustCompile(`^[A-Z0-9]{10}$`)

type SourceProductRef struct {
	Source      Source `json:"source"`
	ProductID   string `json:"productId,omitempty"`
	Marketplace string `json:"marketplace,omitempty"`
	AnchorASIN  string `json:"anchorAsin,omitempty"`
}

func (r SourceProductRef) Validate() error {
	switch r.Source {
	case SourceShopify:
		if r.ProductID != "" && r.ProductID == strings.TrimSpace(r.ProductID) && r.Marketplace == "" && r.AnchorASIN == "" {
			return nil
		}
	case SourceAmazon:
		if r.ProductID == "" && r.Marketplace == "US" && amazonASIN.MatchString(r.AnchorASIN) {
			return nil
		}
	default:
		if mall, ok := KoreanMall(r.Source); ok && r.Marketplace == "KR" && mall.ID.MatchString(r.ProductID) && r.AnchorASIN == "" {
			return nil
		}
	}
	return ErrSourceReference
}
func (r SourceProductRef) IdentityKey() string {
	if r.Validate() != nil {
		return ""
	}
	if r.Source == SourceAmazon {
		return "amazon:" + r.Marketplace + ":" + r.AnchorASIN
	}
	if r.Source.KoreanExternal() {
		return strings.ToLower(string(r.Source)) + ":KR:" + r.ProductID
	}
	return "shopify-product:" + r.ProductID
}

type SourceVariantRef struct {
	Source      Source `json:"source"`
	MerchantID  string `json:"merchantId,omitempty"`
	ProductID   string `json:"productId,omitempty"`
	VariantID   string `json:"variantId,omitempty"`
	Marketplace string `json:"marketplace,omitempty"`
	ASIN        string `json:"asin,omitempty"`
}

func (r SourceVariantRef) Validate() error {
	if r.Source == SourceAmazon && r.Marketplace == "US" && amazonASIN.MatchString(r.ASIN) && r.MerchantID == "" && r.ProductID == "" && r.VariantID == "" {
		return nil
	}
	if r.Source == SourceShopify && r.MerchantID != "" && r.ProductID != "" && r.VariantID != "" && r.Marketplace == "" && r.ASIN == "" {
		return nil
	}
	return ErrSourceReference
}
func (r SourceVariantRef) ExternalURL() (string, error) {
	if r.Validate() != nil || r.Source != SourceAmazon {
		return "", ErrSourceReference
	}
	return "https://www.amazon.com/dp/" + r.ASIN, nil
}
func (r SourceVariantRef) ValidateExternalURL(raw string) error {
	expected, err := r.ExternalURL()
	if err != nil {
		return err
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "www.amazon.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.TrimRight(parsed.Path, "/") != "/dp/"+r.ASIN || expected == "" {
		return ErrSourceReference
	}
	return nil
}

type VariantObservedPrice struct {
	Kind        string `json:"kind"`
	AmountMinor *int64 `json:"amountMinor,omitempty"`
	Currency    string `json:"currency,omitempty"`
	ReasonCode  string `json:"reasonCode,omitempty"`
}
type ObservedSeller struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}
type VariantObservation struct {
	ObservationID       string               `json:"observationId"`
	VariantRef          SourceVariantRef     `json:"variantRef"`
	Price               VariantObservedPrice `json:"price"`
	Availability        string               `json:"availability"`
	DeliveryEligibility string               `json:"deliveryEligibility"`
	Seller              ObservedSeller       `json:"seller"`
	PurchaseRoute       string               `json:"purchaseRoute"`
	ProductURL          string               `json:"productUrl"`
	ObservedAt          time.Time            `json:"observedAt"`
	RefreshAfter        time.Time            `json:"refreshAfter"`
}

func (v VariantObservation) Validate() error {
	if v.VariantRef.Validate() != nil || v.ObservationID == "" || v.ObservedAt.IsZero() || !v.RefreshAfter.After(v.ObservedAt) {
		return ErrSourceReference
	}
	if v.VariantRef.Source == SourceAmazon && (v.PurchaseRoute != "EXTERNAL" || v.VariantRef.ValidateExternalURL(v.ProductURL) != nil) {
		return ErrSourceReference
	}
	if v.Price.Kind == "OBSERVED" {
		if v.Price.AmountMinor == nil || *v.Price.AmountMinor < 0 || *v.Price.AmountMinor > 9007199254740991 || v.Price.Currency != "USD" {
			return ErrSourceReference
		}
	} else if v.Price.Kind != "UNKNOWN" || v.Price.AmountMinor != nil || v.Price.ReasonCode == "" {
		return ErrSourceReference
	}
	switch v.Availability {
	case "AVAILABLE", "UNAVAILABLE", "UNKNOWN":
	default:
		return ErrSourceReference
	}
	switch v.DeliveryEligibility {
	case "CONFIRMED", "UNCONFIRMED", "UNSUPPORTED":
	default:
		return ErrSourceReference
	}
	if v.Seller.Kind != "UNKNOWN" && (v.Seller.Kind != "KNOWN" || v.Seller.ID == "" || v.Seller.Name == "") {
		return ErrSourceReference
	}
	return nil
}
