package domain

import "testing"

// Every registry entry must round-trip a real product page URL from the
// 2026-09-15 platform audit into an identity and back into the canonical
// outbound URL, and must reject non-product pages of the same host.
func TestKoreanMallRegistryRoundTripsAuditedProductURLs(t *testing.T) {
	for _, test := range []struct {
		source    Source
		raw       string
		id        string
		canonical string
	}{
		{SourceCoupang, "https://www.coupang.com/vp/products/8202544905?itemId=20106538968&vendorItemId=3000043960", "8202544905", "https://www.coupang.com/vp/products/8202544905"},
		{SourceElevenStreet, "https://www.11st.co.kr/products/5337333981", "5337333981", "https://www.11st.co.kr/products/5337333981"},
		{SourceNaverSmartstore, "https://smartstore.naver.com/sportstoktok/products/7058693395", "sportstoktok/7058693395", "https://smartstore.naver.com/sportstoktok/products/7058693395"},
		{SourceNaverBrandstore, "https://brand.naver.com/samsungstore/products/5261403955?NaPm=x", "samsungstore/5261403955", "https://brand.naver.com/samsungstore/products/5261403955"},
		{SourceMusinsa, "https://www.musinsa.com/products/4297589", "4297589", "https://www.musinsa.com/products/4297589"},
		{SourceMusinsa, "https://www.musinsa.com/app/goods/4297589", "4297589", "https://www.musinsa.com/products/4297589"},
		{SourceTwentyNineCM, "https://product.29cm.co.kr/catalog/397451", "397451", "https://product.29cm.co.kr/catalog/397451"},
		{SourceOliveyoung, "https://www.oliveyoung.co.kr/store/goods/getGoodsDetail.do?goodsNo=A000000189556&dispCatNo=1", "A000000189556", "https://www.oliveyoung.co.kr/store/goods/getGoodsDetail.do?goodsNo=A000000189556"},
		{SourceKurly, "https://www.kurly.com/goods/5063110", "5063110", "https://www.kurly.com/goods/5063110"},
		{SourceGmarket, "https://item.gmarket.co.kr/Item?goodscode=3505000953", "3505000953", "https://item.gmarket.co.kr/Item?goodscode=3505000953"},
		{SourceAuction, "https://itempage3.auction.co.kr/DetailView.aspx?itemno=D762102946", "D762102946", "https://itempage3.auction.co.kr/DetailView.aspx?itemno=D762102946"},
		{SourceSSG, "https://www.ssg.com/item/itemView.ssg?itemId=1000039223876", "1000039223876", "https://www.ssg.com/item/itemView.ssg?itemId=1000039223876"},
		{SourceZigzag, "https://zigzag.kr/catalog/products/108087931", "108087931", "https://zigzag.kr/catalog/products/108087931"},
		{SourceZigzag, "https://store.zigzag.kr/catalog/products/108087931", "108087931", "https://zigzag.kr/catalog/products/108087931"},
		{SourceWconcept, "https://www.wconcept.co.kr/Product/301447859", "301447859", "https://www.wconcept.co.kr/product/301447859"},
		{SourceOhouse, "https://ohou.se/productions/102652/selling", "102652", "https://ohou.se/productions/102652/selling"},
		{SourceLotteon, "https://www.lotteon.com/p/product/LO1489459593", "LO1489459593", "https://www.lotteon.com/p/product/LO1489459593"},
		{SourceDaisomall, "https://www.daisomall.co.kr/pd/pdr/SCR_PDR_0001?pdNo=1058641", "1058641", "https://www.daisomall.co.kr/pd/pdr/SCR_PDR_0001?pdNo=1058641"},
	} {
		ref, err := SourceProductFromURL(test.raw)
		if err != nil || ref.Source != test.source || ref.ProductID != test.id || ref.Marketplace != "KR" {
			t.Fatalf("%s: ref=%+v err=%v", test.raw, ref, err)
		}
		if !ref.Source.KoreanExternal() || ref.IdentityKey() == "" {
			t.Fatalf("%s: not a Korean external identity", test.raw)
		}
		canonical, err := ref.ExternalProductURL()
		if err != nil || canonical != test.canonical {
			t.Fatalf("%s: canonical=%s err=%v", test.raw, canonical, err)
		}
		again, err := SourceProductFromURL(canonical)
		if err != nil || again != ref {
			t.Fatalf("%s: canonical URL does not round-trip: %+v %v", test.raw, again, err)
		}
	}
	for _, raw := range []string{
		"https://www.musinsa.com/search/goods?keyword=pen",
		"https://www.kurly.com/search?sword=milk",
		"https://zigzag.kr/search?keyword=dress",
		"https://www.oliveyoung.co.kr/store/goods/getGoodsDetail.do?goodsNo=189556",
		"https://item.gmarket.co.kr/Item?goodscode=abc",
		"https://www.lotteon.com/p/product/1489459593",
		"https://smartstore.naver.com/sportstoktok",
		"https://search.shopping.naver.com/catalog/123",
		"https://11st.co.kr/products/6848013817?method=getProductQnAList",
		"http://www.kurly.com/goods/5063110",
	} {
		if _, err := SourceProductFromURL(raw); err == nil {
			t.Errorf("accepted non-product or unsafe URL: %s", raw)
		}
	}
	if (Source("SHOPIFY")).KoreanExternal() || (Source("AMAZON")).KoreanExternal() || (Source("UNKNOWN_MALL")).KoreanExternal() {
		t.Fatal("non-registry sources must not be Korean external")
	}
	seen := map[Source]bool{}
	for _, mall := range KoreanMalls() {
		if seen[mall.Source] || mall.LabelEN == "" || mall.LabelKO == "" || len(mall.Hosts) == 0 || mall.ID == nil {
			t.Fatalf("registry entry incomplete or duplicated: %+v", mall.Source)
		}
		seen[mall.Source] = true
	}
}
