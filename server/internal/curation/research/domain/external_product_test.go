package domain

import (
	"testing"
	"time"
)

func TestOriginalPlatformIdentityRejectsProviderAndRedirectIDs(t *testing.T) {
	for _, raw := range []string{"https://www.google.com/shopping/product/123", "https://search.shopping.naver.com/catalog/123", "https://biz.adpick.co.kr/api/x/link", "https://www.coupang.com.evil.test/vp/products/123", "https://www.coupang.com@evil.test/vp/products/123", "http://www.coupang.com/vp/products/123", "https://www.11st.co.kr/products/0123", "https://www.coupang.com:443/vp/products/123", "https://11st.co.kr/products/6848013817?method=getProductQnAList&brdInfoClfNo=6848013817&curPage=1&isMart=false"} {
		if _, err := SourceProductFromURL(raw); err == nil {
			t.Errorf("accepted non-original product: %s", raw)
		}
	}
	ref, err := SourceProductFromURL("https://www.coupang.com/vp/products/8825648110?itemId=25717201283&vendorItemId=92706038164")
	if err != nil || ref.IdentityKey() != "coupang:KR:8825648110" {
		t.Fatalf("source=%+v err=%v", ref, err)
	}
	u, _ := ref.ExternalProductURL()
	if u != "https://www.coupang.com/vp/products/8825648110" {
		t.Fatal(u)
	}
	// Only 11st action views are rejected; a tracking query still names the
	// original product page.
	eleven, err := SourceProductFromURL("https://11st.co.kr/products/6848013817?trTypeCd=22")
	if err != nil || eleven.IdentityKey() != "elevenst:KR:6848013817" {
		t.Fatalf("tracking query rejected original 11st product: %+v %v", eleven, err)
	}
}

func TestExternalProductCanHaveUnknownPriceAndSellerWithoutVariant(t *testing.T) {
	ref := SourceProductRef{Source: SourceElevenStreet, Marketplace: "KR", ProductID: "5337333981"}
	u, _ := ref.ExternalProductURL()
	o := ExternalProductObservation{SchemaVersion: "vitlane.external-product-observation.v1", ProductRef: ref, ProductURL: u, Title: "라미 사파리 만년필", Price: VariantObservedPrice{Kind: "UNKNOWN", ReasonCode: "PRICE_NOT_REPORTED"}, PriceScope: "PRODUCT", Seller: ObservedSeller{Kind: "UNKNOWN"}, Provenance: ProductProvenance{APIProvider: "NAVER API HUB", APIProduct: "Search Webkr", DiscoveryChannel: "NAVER_WEB", Country: "KR", QueryLanguage: "ko"}, ObservedAt: time.Now().UTC()}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	o.Price.AmountMinor = &zero
	if o.Validate() == nil {
		t.Fatal("unknown price became zero")
	}
	o.Price.AmountMinor = nil
	o.Seller = ObservedSeller{Kind: "KNOWN", ID: "google-merchant-id"}
	if o.Validate() == nil {
		t.Fatal("provider ID claimed seller identity")
	}
}
