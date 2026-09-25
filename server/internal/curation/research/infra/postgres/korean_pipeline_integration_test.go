package postgres

import (
	"context"
	"encoding/json"
	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationpostgres "github.com/vitlane/vitlane/server/internal/curation/infra/postgres"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/curation/research/infra/catalogstub"
	"github.com/vitlane/vitlane/server/internal/curation/research/infra/koreancatalog"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type koreanTransport func(*http.Request) (*http.Response, error)

func (f koreanTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestKoreanPipelineSettingsSearchPersistPurchaseAndCartBoundary(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, dsn)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	const user = "98000000-0000-4000-8000-000000000001"
	const curation = "98000000-0000-4000-8000-000000000003"
	const target = "98000000-0000-4000-8000-000000000004"
	prefs := &accountapp.PreferencesService{Repository: accountpostgres.NewRepository(db)}
	en, usd := "en-US", "USD"
	if _, err := prefs.Patch(ctx, user, accountdomain.PreferencesPatch{UILocale: &en, PreferredCurrency: &usd}, nil); err != nil {
		t.Fatal(err)
	}
	cs := curationapp.NewService(curationpostgres.NewRepository(db, nil), nil, db, amazonPipelineClock{}, sharedapp.UUIDGenerator{}, slog.Default())
	cs.EnableResearchSelectionRecorder(prefs)
	old, err := cs.ResearchSettings(ctx, user, curation)
	if err != nil || old.Country != "US" || old.Version != 0 {
		t.Fatalf("legacy seed: %+v %v", old, err)
	}
	changed, err := cs.ChangeResearchSettings(ctx, user, curation, "KR", 0)
	if err != nil || changed.Country != "KR" || changed.Version != 1 {
		t.Fatalf("next country: %+v %v", changed, err)
	}
	if _, err = cs.ChangeResearchSettings(ctx, user, curation, "US", 0); err == nil {
		t.Fatal("stale setting accepted")
	}
	if _, err = cs.ResearchSettings(ctx, "98000000-0000-4000-8000-000000000099", curation); err == nil {
		t.Fatal("cross-user settings disclosed")
	}
	selected, _ := prefs.Read(ctx, user)
	if selected.UILocale != en || selected.PreferredCurrency != usd || selected.ResearchCountry != "KR" || selected.Version != 2 {
		t.Fatalf("country overwrote other last select: %+v", selected)
	}
	var phase, country, currency, amount string
	var version int
	if err = db.DB.QueryRowContext(ctx, `SELECT c.phase,c.version,p.country,p.budget_currency,p.budget_amount::text FROM curations c JOIN shopping_plans p ON p.id=c.shopping_plan_id WHERE c.id=$1`, curation).Scan(&phase, &version, &country, &currency, &amount); err != nil {
		t.Fatal(err)
	}
	if phase != "CURATING" || version != 1 || country != "US" || currency != "USD" || !strings.HasPrefix(amount, "100") {
		t.Fatal("settings changed existing domain/budget")
	}
	var jobs, rounds int
	if err = db.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM intelligence_jobs),(SELECT count(*) FROM research_rounds)`).Scan(&jobs, &rounds); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 || rounds != 0 {
		t.Fatal("setting started research")
	}
	calls := 0
	client := &http.Client{Transport: koreanTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body := ""
		ctype := "application/json"
		switch r.URL.Path {
		case "/usage":
			body = `{"status":"OK","data":{"api_id":"realtime_product_search","plan":{"is_free":true},"quotas":[{"name":"Requests","limit":100,"used":0,"remaining":100,"reset_at":"` + time.Now().Add(time.Hour).Format(time.RFC3339) + `"}]}}`
		case "/realtime-product-search/v2/search":
			if r.URL.Query().Get("country") != "kr" || r.URL.Query().Get("language") != "ko" || r.URL.Query().Get("q") != "라미 사파리 만년필" {
				t.Fatal("provider country/language lost")
			}
			body = `{"status":"OK","data":{"products":[{"product_id":"google-lookup-only","product_title":"라미 사파리"}]}}`
		case "/realtime-product-search/v2/product-details":
			// A registered mall beyond Coupang/11st (Kurly) is admitted from the
			// same Google Shopping offer list without visiting the mall.
			body = `{"status":"OK","data":{"product_title":"라미 사파리","offers":[{"offer_page_url":"https://www.coupang.com/vp/products/8825648110?itemId=25717201283&vendorItemId=92706038164","price":"₩23,400"},{"offer_page_url":"https://www.google.com/shopping/product/123","price":"₩1"},{"offer_page_url":"https://www.kurly.com/goods/5063110","price":"₩21,900"}]}}`
		case "/search/v1/webkr":
			// A Q&A list document, a discontinued product and a live product: only the last becomes a candidate.
			body = `{"items":[{"title":"쇼핑백은 종이가방인거죠?쇼핑백 유무에 따라 가격차이가 많이 나서 문의드려....","link":"https://11st.co.kr/products/6848013817?method=getProductQnAList&brdInfoClfNo=6848013817&curPage=1"},{"title":"단종 상품","link":"https://www.11st.co.kr/products/6848013817"},{"title":"라미 사파리","link":"https://www.11st.co.kr/products/5337333981"}]}`
			ctype = "text/plain"
		case "/products/6848013817":
			// 11st's HTTP 200 stub for a discontinued product number.
			body = `<html><head><title>11번가</title><script type='text/javascript'>alert("죄송합니다. 판매가 중지된 상품이거나 잘못된 상품번호입니다.");history.back();</script></head><body></body></html>`
			ctype = "text/html"
		case "/products/5337333981":
			body = `<meta property="og:title" content="라미 사파리">`
			ctype = "text/html"
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {ctype}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	repo := NewRepository(db)
	gateway, _ := koreancatalog.New(koreancatalog.Config{Enabled: true, OWNKey: "test", NaverClientID: "test", NaverClientSecret: "test", Client: client, Control: repo})
	shop, _ := catalogstub.New("test")
	service, _ := researchapp.NewLiveCatalogReviewServiceV2(shop, amazonPipelineClock{}, researchapp.LiveCatalogReviewConfigV2{MaximumCallsPerWindow: 20, Window: time.Minute, MaximumConcurrent: 2})
	_ = service.EnableWorkspaceRepositoryV2(repo)
	service.EnableKoreanCatalog(gateway, nil)
	input := researchapp.CatalogWorkspaceSearchInputV2{UserID: user, CurationID: curation, TargetID: target, Mode: researchapp.CatalogResearchReplaceV2, IdempotencyKey: "korean-search", Search: researchapp.LiveCatalogReviewSearchInputV2{Query: "라미 사파리 만년필", Limit: 8}}
	searched, err := service.SearchWorkspaceV2(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(searched.Search.Products) != 3 || calls != 6 {
		t.Fatalf("products=%d calls=%d coverage=%+v", len(searched.Search.Products), calls, searched.Metrics.SourceCoverage)
	}
	kurlyCovered := false
	for _, c := range searched.Metrics.SourceCoverage {
		if c.Source == researchdomain.SourceKurly && c.Status == "SUCCEEDED" && c.CandidateCount == 1 {
			kurlyCovered = true
		}
	}
	if !kurlyCovered {
		t.Fatalf("registered mall missing from coverage: %+v", searched.Metrics.SourceCoverage)
	}
	if searched.ProviderRejectedCount != 2 {
		t.Fatalf("Q&A document and discontinued stub not counted as rejected: %d", searched.ProviderRejectedCount)
	}
	htmlUsage, err := repo.ReadProviderUsage(ctx, "ELEVENST_HTML")
	if err != nil || htmlUsage.NotFound24h != 1 || len(htmlUsage.Failures24h) != 0 || htmlUsage.Requests24h != 2 {
		t.Fatalf("not-found stub misreported in usage: %+v err=%v", htmlUsage, err)
	}
	for _, c := range searched.Metrics.SourceCoverage {
		if c.Source == researchdomain.SourceElevenStreet && (c.Status != "SUCCEEDED" || c.CandidateCount != 1) {
			t.Fatalf("11st coverage treated not-found as failure: %+v", c)
		}
	}
	candidate := ""
	ref := researchdomain.SourceProductRef{}
	kurly := ""
	kurlyRef := researchdomain.SourceProductRef{}
	for _, p := range searched.Search.Products {
		if p.PreviewVariant != nil || p.VariantObservation != nil || p.ExternalObservation == nil {
			t.Fatal("product invented variant")
		}
		if p.Source() == researchdomain.SourceKurly {
			kurly = p.ProviderProductID
			kurlyRef = p.ProductRef()
			if kurlyRef.ProductID != "5063110" || p.ExternalObservation.ProductURL != "https://www.kurly.com/goods/5063110" || p.ExternalObservation.Price.Kind != "OBSERVED" {
				t.Fatalf("Kurly identity: %+v", p.ExternalObservation)
			}
		}
		if p.Source() == researchdomain.SourceCoupang {
			candidate = p.ProviderProductID
			ref = p.ProductRef()
			if ref.ProductID != "8825648110" || p.ExternalObservation.Seller.Kind != "UNKNOWN" {
				t.Fatal("provider ID/seller identity conflated")
			}
		}
	}
	if candidate == "" || kurly == "" {
		t.Fatal("Coupang or Kurly candidate missing")
	}
	replaySearch, err := service.SearchWorkspaceV2(ctx, input)
	if err != nil || !replaySearch.Replay || len(replaySearch.Search.Products) != 3 || replaySearch.Search.Products[0].ExternalObservation == nil || calls != 6 {
		t.Fatalf("replay lost observations or recharged API: %+v %v calls=%d", replaySearch, err, calls)
	}
	if err = service.SaveWorkspaceConfigurationV2(ctx, researchapp.CatalogSaveConfigurationInputV2{UserID: user, CurationID: curation, CandidateID: candidate, VariantID: "fabricated", ObservedAt: time.Now()}); err == nil {
		t.Fatal("product-only candidate accepted fabricated variant configuration")
	}
	if _, err = cs.ChangeResearchSettings(ctx, user, curation, "US", 1); err != nil {
		t.Fatal(err)
	}
	expansion, err := repo.CatalogExpansionSearchProfileV2(ctx, user, curation, target)
	if err != nil || expansion.Market.Country != "KR" || expansion.NormalizedIntent != "라미 사파리 만년필" {
		t.Fatalf("next country reinterpreted existing expansion: %+v %v", expansion, err)
	}
	selected, _ = prefs.Read(ctx, user)
	// Reload uses saved observations, preserving UNKNOWN price and original IDs.
	reloaded, err := service.LoadWorkspaceV2(ctx, user, curation, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Pools) != 1 || len(reloaded.Pools[0].Products) != 3 || calls != 6 {
		t.Fatalf("DB-only reload: %+v %v", reloaded, err)
	}
	for _, p := range reloaded.Pools[0].Products {
		if p.Source() == researchdomain.SourceElevenStreet && p.ExternalObservation.Price.Kind != "UNKNOWN" {
			t.Fatal("unknown became zero")
		}
	}
	_, err = service.BrowseWorkspaceVariantsV2(ctx, user, curation, candidate, "")
	if err == nil {
		t.Fatal("product-only observation entered variant path")
	}
	in := researchapp.MarkExternalPurchaseInput{UserID: user, CurationID: curation, CandidateID: candidate, ProductRef: &ref, Checked: true, IdempotencyKey: "korean-check"}
	first, err := service.MarkExternalPurchase(ctx, in)
	if err != nil || first.Version != 1 || len(first.Records) != 1 || first.Records[0].ProductRef == nil {
		t.Fatalf("purchase check=%+v err=%v", first, err)
	}
	replay, err := service.MarkExternalPurchase(ctx, in)
	if err != nil || replay.Version != first.Version {
		t.Fatal("purchase replay changed version")
	}
	raw, _ := json.Marshal(replay)
	if strings.Contains(string(raw), "variantRef") || strings.Contains(string(raw), "asin") {
		t.Fatal("product record invented ASIN/variant")
	}
	in.Checked = false
	if _, err = service.MarkExternalPurchase(ctx, in); err == nil {
		t.Fatal("idempotency collision accepted")
	}
	in.IdempotencyKey = "undo"
	in.ExpectedVersion = 1
	undone, err := service.MarkExternalPurchase(ctx, in)
	if err != nil || undone.Version != 2 || undone.Records[0].Checked {
		t.Fatal("undo failed", err)
	}
	in.UserID = "98000000-0000-4000-8000-000000000099"
	if _, err = service.MarkExternalPurchase(ctx, in); err == nil {
		t.Fatal("cross-user purchase accepted")
	}
	// A registered mall beyond the original two passes the same purchase-check
	// and reaction constraints (migration 118).
	kurlyCheck, err := service.MarkExternalPurchase(ctx, researchapp.MarkExternalPurchaseInput{UserID: user, CurationID: curation, CandidateID: kurly, ProductRef: &kurlyRef, Checked: true, IdempotencyKey: "kurly-check"})
	if err != nil || len(kurlyCheck.Records) != 2 {
		t.Fatalf("Kurly purchase check=%+v err=%v", kurlyCheck, err)
	}
	liked, err := service.SaveProductReaction(ctx, researchapp.SaveProductReactionInput{UserID: user, CurationID: curation, CandidateID: kurly, ProductRef: kurlyRef, Pinned: true, Sentiment: "LIKE", ExpectedVersion: 0})
	if err != nil || liked.Version != 1 || liked.LikedSnapshot == nil || liked.ProductRef != kurlyRef {
		t.Fatalf("Kurly reaction=%+v err=%v", liked, err)
	}
	_, err = db.DB.ExecContext(ctx, `INSERT INTO phase8_cart_items(user_id,curation_id,cart_item_id,plan_target_id,candidate_id,product_title_snapshot,variant_id,variant_title_snapshot,preview_price_minor,preview_currency,quantity,observed_at,added_at) VALUES($1,$2,'bad',$3,$4,'Pen','fake','fake',1,'USD',1,now(),now())`, user, curation, target, candidate)
	if err == nil || !strings.Contains(err.Error(), "EXTERNAL_PRODUCT_CART_FORBIDDEN") {
		t.Fatal("external product bypassed cart guard", err)
	}
	if _, err = db.DB.ExecContext(ctx, `UPDATE phase8_research_candidates SET external_observation='{}' WHERE user_id=$1 AND candidate_id=$2`, user, candidate); err == nil {
		t.Fatal("DB accepted missing original product identity")
	}
	after, _ := prefs.Read(ctx, user)
	if after != selected {
		t.Fatal("research or purchase completion overwrote last select")
	}
	if calls != 6 {
		t.Fatal("purchase/reload recharged provider")
	}
	if _, err = db.DB.ExecContext(ctx, `UPDATE plan_targets SET normalized_intent='라미 사파리 만년필' WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	next := input
	next.ExpectedPoolVersion = searched.Pool.Version
	next.IdempotencyKey = "us-after-korean"
	next.Search.Query = "fountain pen"
	usSearch, err := service.SearchWorkspaceV2(ctx, next)
	if err != nil || usSearch.Metrics.ProviderCountry != "US" || usSearch.Metrics.ProviderQuery != "fountain pen" {
		t.Fatalf("US provider did not use existing normalized query: %+v %v", usSearch.Metrics, err)
	}
	// A separate KR/KRW plan is inserted at creation: saved plans are immutable.
	for _, statement := range []string{
		`INSERT INTO shopping_plans(id,user_id,original_intent,plan_mode,execution_mode,budget_amount,budget_currency,country,city,created_at) SELECT '98000000-0000-4000-8000-000000000012',user_id,original_intent,plan_mode,execution_mode,100000,'KRW','KR',city,created_at FROM shopping_plans WHERE id='98000000-0000-4000-8000-000000000002'`,
		`INSERT INTO curations(id,shopping_plan_id,user_id,phase,version,created_at,updated_at) SELECT '98000000-0000-4000-8000-000000000013','98000000-0000-4000-8000-000000000012',user_id,phase,version,created_at,updated_at FROM curations WHERE id='98000000-0000-4000-8000-000000000003'`,
		`INSERT INTO plan_targets(id,curation_id,user_id,plan_id,title,normalized_intent,category,allocated_amount,allocated_currency,country,city,url_mode,order_index,version,created_at,updated_at) SELECT '98000000-0000-4000-8000-000000000014','98000000-0000-4000-8000-000000000013',user_id,'98000000-0000-4000-8000-000000000012',title,normalized_intent,category,100000,'KRW','KR',city,url_mode,order_index,version,created_at,updated_at FROM plan_targets WHERE id='98000000-0000-4000-8000-000000000004'`,
	} {
		if _, err = db.DB.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	cart, err := curationpostgres.NewRepository(db, nil).GetCatalogCartV2(ctx, user, "98000000-0000-4000-8000-000000000013")
	if err != nil || cart.Country != "US" || cart.Currency != "USD" {
		t.Fatal("research budget relabeled Shopify cart", err)
	}
	lookup, err := repo.CatalogTargetMarketContextV2(ctx, user, "98000000-0000-4000-8000-000000000013", "98000000-0000-4000-8000-000000000014")
	if err != nil || lookup.Country != "US" || lookup.Currency != "USD" {
		t.Fatal("research budget relabeled saved Shopify lookup", err)
	}
}

func TestKoreanProviderControlsAndDailyFXAcrossRepositories(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, dsn)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	repo := NewRepository(db)
	other := NewRepository(db)
	now := time.Now().UTC()
	user := "98000000-0000-4000-8000-000000000001"
	disabled, err := repo.UpdateProviderControl(ctx, "OWN_PRODUCT", user, false, 1, now)
	if err != nil || disabled.Enabled {
		t.Fatal(err)
	}
	if _, err = other.ReserveProviderCall(ctx, "OWN_PRODUCT", "SEARCH", now); err == nil {
		t.Fatal("off bypassed")
	}
	if _, err = repo.UpdateProviderControl(ctx, "OWN_PRODUCT", user, true, 1, now); fault.CodeOf(err) != fault.Conflict {
		t.Fatal("stale control accepted", err)
	}
	if _, err = repo.UpdateProviderControl(ctx, "OWN_PRODUCT", user, true, 2, now); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReserveProviderCall(ctx, "OWN_PRODUCT", "SEARCH", now); err == nil {
		t.Fatal("unconfirmed quota allowed")
	}
	if err = repo.SaveProviderQuota(ctx, "OWN_PRODUCT", researchapp.CatalogAPIQuota{Limit: 3, Remaining: 3, ResetAt: now.Add(time.Hour), ObservedAt: now, IsFree: true}, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	id, err := repo.ReserveProviderCall(ctx, "OWN_PRODUCT", "SEARCH", now)
	if err != nil {
		t.Fatal(err)
	}
	// OWN Product admits a second in-flight call (another Target researching
	// at the same time); the third has to wait for a slot.
	second, err := other.ReserveProviderCall(ctx, "OWN_PRODUCT", "DETAIL", now)
	if err != nil {
		t.Fatal("second parallel call must be admitted", err)
	}
	if _, err = other.ReserveProviderCall(ctx, "OWN_PRODUCT", "DETAIL", now); err == nil {
		t.Fatal("parallel inflight not guarded")
	}
	if err = repo.CompleteProviderCall(ctx, second, "SUCCESS", 200, 0, now); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteProviderCall(ctx, id, "CATALOG_UPSTREAM_RATE_LIMITED", 429, 120, now); err != nil {
		t.Fatal(err)
	}
	if _, err = other.ReserveProviderCall(ctx, "OWN_PRODUCT", "DETAIL", now.Add(time.Minute)); err == nil {
		t.Fatal("Retry-After bypassed")
	}
	id, err = other.ReserveProviderCall(ctx, "OWN_PRODUCT", "DETAIL", now.Add(121*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	_ = repo.CompleteProviderCall(ctx, id, "SUCCESS", 200, 0, now.Add(122*time.Second))
	if _, err = repo.ReserveProviderCall(ctx, "OWN_PRODUCT", "DETAIL", now.Add(123*time.Second)); err == nil {
		t.Fatal("quota exceeded")
	}
	usage, err := repo.ReadProviderUsage(ctx, "OWN_PRODUCT")
	if err != nil || usage.EstimatedRemaining == nil || *usage.EstimatedRemaining != 0 || len(usage.Failures24h) < 3 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	// API products have independent controls/quotas. Amazon is unaffected.
	if _, err = other.ReserveProviderCall(ctx, "NAVER_WEBKR", "SEARCH", now); err != nil {
		t.Fatal("other API blocked", err)
	}
	amazon, err := repo.ReadAmazonControl(ctx)
	if err != nil || !amazon.Enabled || amazon.Version != 1 {
		t.Fatal("Amazon control mutated")
	}
	var wg sync.WaitGroup
	claims := make(chan bool, 2)
	for _, r := range []*Repository{repo, other} {
		wg.Add(1)
		go func(r *Repository) {
			defer wg.Done()
			ok, e := r.ClaimExchangeRateRefresh(ctx, now)
			if e != nil {
				t.Error(e)
			}
			claims <- ok
		}(r)
	}
	wg.Wait()
	close(claims)
	count := 0
	for ok := range claims {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatal("daily FX claim duplicated", count)
	}
	rate := researchdomain.DailyExchangeRate{Base: "USD", Quote: "KRW", Rate: "1340.18", AsOf: now.Format("2006-01-02"), ObservedAt: now, Source: "https://frankfurter.dev/"}
	if err = repo.SaveExchangeRate(ctx, rate); err != nil {
		t.Fatal(err)
	}
	if ok, err := other.ClaimExchangeRateRefresh(ctx, now.Add(time.Minute)); err != nil || ok {
		t.Fatal("same-day FX fetched again", err)
	}
	view, err := (&researchapp.ExchangeRateService{Repository: other}).View(ctx)
	if err != nil || view.Status != "CURRENT" || view.Rate.Rate != "1340.18000000" && view.Rate.Rate != "1340.18" {
		t.Fatalf("rate=%+v err=%v", view, err)
	}
}
