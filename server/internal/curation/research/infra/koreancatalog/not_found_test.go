package koreancatalog

import (
	"context"
	"net/http"
	"strings"
	"testing"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

// Captured 2026-09-13 from https://www.11st.co.kr/products/6848013817: 11st
// answers a discontinued product number with HTTP 200 and no product metadata.
const elevenStreetDiscontinuedStub = `<html>
<head>
    <title>11번가</title>
    <script type='text/javascript'>
        alert("죄송합니다. 판매가 중지된 상품이거나 잘못된 상품번호입니다.");
        history.back();
    </script>
</head>
<body>
</body>
</html>`

const elevenStreetLiveProductPage = `<meta property="og:title" content="[11번가] 라미 사파리 만년필"><meta property="og:image" content="https://cdn.011st.com/11dims/resize/600x600/quality/75/11src/product/5337333981/B.webp"><meta property="product:price:amount" content="23,400"><meta property="product:price:currency" content="KRW">`

// NAVER Webkr indexes 11st Q&A lists as separate documents titled by their
// first question. They match site:11st.co.kr/products but are not products.
const elevenStreetQnATitle = "쇼핑백은 종이가방인거죠?쇼핑백 유무에 따라 가격차이가 많이 나서 문의드려...."
const elevenStreetQnALink = "https://11st.co.kr/products/6848013817?method=getProductQnAList&brdInfoClfNo=6848013817&curPage=1&isMart=false&storeNo=&martNo=&pageTypCd=first&sellerNo=74677478&ldispCtgrNo=1001307&isSohoPrd=false&isTour=false&isRenewYn=Y"

func elevenStreetFixtureGateway(t *testing.T, items string, detail map[string]string) (*Gateway, *reviewControl, *[]string) {
	t.Helper()
	control := &reviewControl{}
	requested := []string{}
	g, err := New(Config{Enabled: true, NaverClientID: "id", NaverClientSecret: "secret", Control: control, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "naverapihub.apigw.ntruss.com":
			return response(200, `{"items":[`+items+`]}`, "text/plain;charset=UTF-8"), nil
		case "www.11st.co.kr":
			requested = append(requested, r.URL.Path)
			body, ok := detail[r.URL.Path]
			if !ok {
				t.Fatalf("unexpected detail request %s", r.URL.Path)
			}
			return response(200, body, "text/html;charset=UTF-8"), nil
		}
		t.Fatalf("unexpected host %s", r.URL.Host)
		return nil, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	return g, control, &requested
}

func TestElevenStreetNonProductDocumentsAreRejectedNotFailed(t *testing.T) {
	items := `{"title":"` + elevenStreetQnATitle + `","link":"` + elevenStreetQnALink + `","description":"문의/답변 이제 상품입고는 안되는거죠??"},` +
		`{"title":"단종 상품","link":"https://www.11st.co.kr/products/6848013817"},` +
		`{"title":"라미 사파리","link":"https://www.11st.co.kr/products/5337333981"}`
	g, control, requested := elevenStreetFixtureGateway(t, items, map[string]string{"/products/6848013817": elevenStreetDiscontinuedStub, "/products/5337333981": elevenStreetLiveProductPage})
	result, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{Query: "라미 사파리 만년필", Country: "KR", Seeds: []string{"라미 사파리 만년필"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 1 || result.Observations[0].ProductRef.ProductID != "5337333981" || result.Observations[0].Title != "[11번가] 라미 사파리 만년필" || result.Observations[0].Price.Kind != "OBSERVED" {
		t.Fatalf("observations=%+v", result.Observations)
	}
	for _, o := range result.Observations {
		if strings.Contains(o.Title, "문의") || strings.Contains(o.Description, "문의") {
			t.Fatal("Q&A text reached a candidate")
		}
	}
	if result.RejectedCount != 2 {
		t.Fatalf("rejected=%d", result.RejectedCount)
	}
	// The Q&A document spends no detail slot; the stub and the live page do.
	if len(*requested) != 2 || (*requested)[0] != "/products/6848013817" || (*requested)[1] != "/products/5337333981" {
		t.Fatalf("detail requests=%v", *requested)
	}
	notFound, failures := 0, 0
	for _, code := range control.codes {
		switch code {
		case "SUCCESS":
		case researchapp.CatalogOutcomeProductNotFound:
			notFound++
		default:
			failures++
		}
	}
	if notFound != 1 || failures != 0 || control.status != 200 {
		t.Fatalf("ledger codes=%v status=%d", control.codes, control.status)
	}
	for _, c := range result.Coverage {
		if c.Source == researchdomain.SourceElevenStreet && (c.Status != "SUCCEEDED" || c.CandidateCount != 1 || c.ReasonCode != "") {
			t.Fatalf("coverage=%+v", c)
		}
	}
	// Paging follows provider items, so the next page of this seed is read next.
	if p := result.NextProgress["NAVER_WEBKR"]; p.Page != 2 || p.Query != "라미 사파리 만년필" {
		t.Fatalf("progress=%+v", p)
	}
}

func TestElevenStreetQnAOnlyPageIsEmptyNotFailedAndKeepsPaging(t *testing.T) {
	g, control, requested := elevenStreetFixtureGateway(t, `{"title":"`+elevenStreetQnATitle+`","link":"`+elevenStreetQnALink+`"}`, nil)
	result, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{Query: "쇼핑백 종이가방", Country: "KR", Seeds: []string{"쇼핑백 종이가방"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 0 || result.RejectedCount != 1 || len(*requested) != 0 {
		t.Fatalf("observations=%d rejected=%d detail=%v", len(result.Observations), result.RejectedCount, *requested)
	}
	for _, c := range result.Coverage {
		if c.Source == researchdomain.SourceElevenStreet && (c.Status != "EMPTY" || c.ReasonCode != "") {
			t.Fatalf("coverage=%+v", c)
		}
	}
	if p := result.NextProgress["NAVER_WEBKR"]; p.Page != 2 {
		t.Fatalf("progress=%+v", p)
	}
	for _, code := range control.codes {
		if code != "SUCCESS" {
			t.Fatalf("ledger codes=%v", control.codes)
		}
	}
}

func TestElevenStreetProductPageDetection(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		product   bool
	}{
		{"discontinued stub", elevenStreetDiscontinuedStub, false},
		{"empty", "", false},
		{"og title", `<meta property="og:title" content="[11번가] 라미 사파리 만년필">`, true},
		{"og image only", `<meta property="og:image" content="https://cdn.011st.com/x.webp">`, true},
		{"json-ld product", `<script type="application/ld+json">{"@type":"Product","url":"https://www.11st.co.kr/products/5337333981"}</script>`, true},
		{"blank og title", `<meta property="og:title" content="  "><title>11번가</title>`, false},
	} {
		if got := elevenStreetProductPage([]byte(tc.raw)); got != tc.product {
			t.Errorf("%s: product=%v", tc.name, got)
		}
	}
}
