package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	c "github.com/vitlane/vitlane/server/internal/curation/domain"
	i "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	id "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	ip "github.com/vitlane/vitlane/server/internal/curation/intelligence/infra/postgres"
	a "github.com/vitlane/vitlane/server/internal/curation/research/app"
	d "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	pg "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const bgUser = "98000000-0000-4000-8000-000000000001"
const bgCuration = "98000000-0000-4000-8000-000000000003"
const bgTarget = "98000000-0000-4000-8000-000000000004"

func bgFixture(t *testing.T) (context.Context, *pg.Database, *Repository, c.SubscriptionTerms) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, dsn)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	criteria := c.TargetCriteriaSetV1{SchemaVersion: c.CriteriaSchema, Version: 1, Subject: c.ResearchSubject{Label: "헤드폰", ProductType: "headphones"}, Axes: []c.ResearchAxis{{AxisID: "portable", Label: "휴대성", Definition: "휴대하기 편리함", Importance: 5, Origin: "REQUEST"}}, Exclusions: []string{}}
	raw, _ := json.Marshal(criteria)
	_, e := db.DB.ExecContext(ctx, `INSERT INTO curation_target_criteria(target_id,user_id,curation_id,version,criteria) VALUES($1,$2,$3,1,$4)`, bgTarget, bgUser, bgCuration, raw)
	if e != nil {
		t.Fatal(e)
	}
	terms := c.SubscriptionTerms{SchemaVersion: "vitlane.research-subscription-terms.v1", Criteria: criteria, Country: "KR", Currency: "KRW", Keywords: []string{"헤드폰"}, ExpiresAt: time.Now().Add(24 * time.Hour)}
	return ctx, db, NewRepository(db), terms
}
func bgProposal(t *testing.T, ctx context.Context, db *pg.Database, n int, terms c.SubscriptionTerms) string {
	t.Helper()
	id := fmt.Sprintf("99000000-0000-4000-8000-%012d", n)
	action := c.FollowUpAction{Kind: "SUBSCRIBE_DEALS", TargetID: bgTarget, Subscription: &terms}
	raw, _ := json.Marshal(action)
	_, e := db.DB.ExecContext(ctx, `INSERT INTO curation_conversation_requests(id,user_id,curation_id,mode,request_hash,status,response_ready) VALUES($1,$2,$3,'FOLLOW_UP_ACCEPT','test','COMPLETE',true)`, id, bgUser, bgCuration)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.DB.ExecContext(ctx, `INSERT INTO curation_follow_ups(id,user_id,curation_id,response_id,kind,status,content,payload,fingerprint) VALUES($1,$2,$3,$1,'PROPOSAL','ACCEPTED','{}',$4,'test')`, id, bgUser, bgCuration, raw)
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestBackgroundSubscriptionLimitImmutableAndOwnership(t *testing.T) {
	ctx, db, repo, terms := bgFixture(t)
	ids := []string{}
	for n := 1; n <= 5; n++ {
		v := terms
		v.Keywords = []string{fmt.Sprint("headphones", n)}
		ids = append(ids, bgProposal(t, ctx, db, n, v))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for n, pid := range ids {
		wg.Add(1)
		go func(n int, pid string) {
			defer wg.Done()
			v := terms
			v.Keywords = []string{fmt.Sprint("headphones", n+1)}
			errs <- repo.AcceptSubscription(ctx, bgUser, bgCuration, pid, c.FollowUpAction{TargetID: bgTarget, Subscription: &v})
		}(n, pid)
	}
	wg.Wait()
	close(errs)
	wins := 0
	for e := range errs {
		if e == nil {
			wins++
		}
	}
	if wins != 3 {
		t.Fatalf("concurrent acceptance winners=%d want 3", wins)
	}
	view, e := repo.BackgroundView(ctx, bgUser, bgCuration)
	if e != nil || len(view.Subscriptions) != 3 {
		t.Fatalf("%+v %v", view, e)
	}
	s := view.Subscriptions[0]
	if _, e = db.DB.ExecContext(ctx, `UPDATE research_subscriptions SET terms='{}' WHERE id=$1`, s.ID); e == nil {
		t.Fatal("immutable conditions edited")
	}
	if e = repo.CancelSubscription(ctx, "00000000-0000-4000-8000-000000000099", bgCuration, s.ID); e == nil {
		t.Fatal("foreign user cancelled")
	}
	if e = repo.CancelSubscription(ctx, bgUser, bgCuration, s.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.DB.ExecContext(ctx, `UPDATE research_subscriptions SET status='ACTIVE' WHERE id=$1`, s.ID); e == nil {
		t.Fatal("terminal subscription resumed")
	}
	if _, e = repo.BackgroundView(ctx, "00000000-0000-4000-8000-000000000099", bgCuration); e == nil {
		t.Fatal("foreign user read")
	}
}

type bgAssessmentProvider struct{ calls int }

func (*bgAssessmentProvider) Kind() id.ProviderKind                   { return id.ProviderManaged }
func (*bgAssessmentProvider) Available(context.Context, string) error { return nil }
func (p *bgAssessmentProvider) Complete(_ context.Context, _ i.CompletionRequest) (i.CompletionResult, error) {
	p.calls++
	return i.CompletionResult{Content: `{"scores":[{"axisId":"portable","scorePercent":55,"basis":"UNKNOWN","explanation":"무게는 확인되지 않았습니다.","factIds":[]}]}`}, nil
}
func TestBackgroundDeliveryNoticeAndBoundedImport(t *testing.T) {
	ctx, db, repo, terms := bgFixture(t)
	pid := bgProposal(t, ctx, db, 1, terms)
	if e := repo.AcceptSubscription(ctx, bgUser, bgCuration, pid, c.FollowUpAction{TargetID: bgTarget, Subscription: &terms}); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	price := int64(9900)
	ref := d.SourceProductRef{Source: d.SourceElevenStreet, Marketplace: "KR", ProductID: "12345"}
	product := d.DealProduct{SchemaVersion: "vitlane.deal-product.v1", Provider: "TEST", ExternalID: "1", Identity: ref.IdentityKey(), ProductRef: &ref, Title: "무선 헤드폰", URL: "https://www.11st.co.kr/products/12345", Country: "KR", Currency: "KRW", PriceMinor: &price, ObservedAt: now, ExpiresAt: now.Add(time.Hour)}
	if e := repo.IngestDeals(ctx, []d.DealProduct{product, product}); e != nil {
		t.Fatal(e)
	}
	classify := func() {
		t.Helper()
		snap, e := repo.ClassificationBatch(ctx)
		if e != nil {
			t.Fatal(e)
		}
		items := []a.Classification{}
		for _, it := range snap.Items {
			items = append(items, a.Classification{ID: it.ID, Categories: []string{"electronics"}})
		}
		if e = repo.SaveClassifications(ctx, snap, nil, items); e != nil {
			t.Fatal(e)
		}
	}
	deliver := func() {
		t.Helper()
		classify()
		if e := repo.RouteDeals(ctx); e != nil {
			t.Fatal(e)
		}
		jobs, e := repo.ClaimMatches(ctx)
		if e != nil || len(jobs) != 1 {
			t.Fatalf("jobs=%+v %v", jobs, e)
		}
		if e = repo.FinishMatches(ctx, jobs, []a.MatchDecision{{ID: jobs[0].ID, Match: true, Reason: "헤드폰 조건 일치"}}); e != nil {
			t.Fatal(e)
		}
		if e = repo.PublishFindings(ctx); e != nil {
			t.Fatal(e)
		}
	}
	deliver()
	view, e := repo.BackgroundView(ctx, bgUser, bgCuration)
	if e != nil || len(view.Findings) != 1 {
		t.Fatalf("%+v %v", view, e)
	}
	const client = "a1000000-0000-4000-8000-000000000001"
	notices, e := repo.NoticeSync(ctx, bgUser, client, "", false)
	if e != nil || len(notices) != 1 {
		t.Fatalf("new notice=%v %v", notices, e)
	}
	notices, e = repo.NoticeSync(ctx, bgUser, client, bgCuration, true)
	if e != nil || len(notices) != 0 {
		t.Fatalf("entry notice=%v %v", notices, e)
	}
	provider := &bgAssessmentProvider{}
	service := a.BackgroundService{Repository: repo, Provider: provider, Model: "test", Enabled: true}
	candidate, e := service.ImportFinding(ctx, bgUser, bgCuration, view.Findings[0].ID)
	if e != nil || candidate == "" {
		t.Fatalf("import=%s %v", candidate, e)
	}
	replay, e := service.ImportFinding(ctx, bgUser, bgCuration, view.Findings[0].ID)
	if e != nil || candidate != replay || provider.calls != 1 {
		t.Fatalf("replay=%s calls=%d err=%v", replay, provider.calls, e)
	}
	notices, e = repo.NoticeSync(ctx, bgUser, client, "", false)
	if e != nil || len(notices) != 0 {
		t.Fatalf("promotion created a notice: %v %v", notices, e)
	}
	// A later thumbnail refresh preserves the delivered snapshot and added state.
	imageRefresh := product
	imageRefresh.ImageURL = "https://cdn4.telesco.pe/file/headphones.jpg"
	changedPrice := int64(100)
	imageRefresh.PriceMinor = &changedPrice
	if e = repo.IngestDeals(ctx, []d.DealProduct{imageRefresh}); e != nil {
		t.Fatal(e)
	}
	refreshed, e := repo.BackgroundView(ctx, bgUser, bgCuration)
	if e != nil || len(refreshed.Findings) != 1 {
		t.Fatalf("image refresh: %+v %v", refreshed, e)
	}
	f := refreshed.Findings[0]
	if f.Product.ImageURL != imageRefresh.ImageURL || *f.Product.PriceMinor != price || f.Status != "ADDED" || f.CandidateID != candidate || !f.CreatedAt.Equal(view.Findings[0].CreatedAt) {
		t.Fatalf("image refresh changed saved finding: %+v", f)
	}
	var feed d.DealProduct
	var feedRaw []byte
	if e = db.DB.QueryRowContext(ctx, `SELECT product FROM research_feed_products WHERE provider=$1 AND external_id=$2`, product.Provider, product.ExternalID).Scan(&feedRaw); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(feedRaw, &feed); e != nil || feed.ImageURL != imageRefresh.ImageURL || *feed.PriceMinor != price {
		t.Fatalf("feed image refresh: %+v %v", feed, e)
	}
	if jobs, e := repo.ClaimMatches(ctx); e != nil || len(jobs) != 0 {
		t.Fatalf("image re-matched: %v %v", jobs, e)
	}
	if notices, e = repo.NoticeSync(ctx, bgUser, client, "", false); e != nil || len(notices) != 0 {
		t.Fatalf("image created a dot: %v %v", notices, e)
	}
	// Deliver a different item while this curation is visible: no dot after leaving.
	if _, e = repo.NoticeSync(ctx, bgUser, client, bgCuration, true); e != nil {
		t.Fatal(e)
	}
	product.ExternalID = "2"
	product.Identity = "elevenst:KR:12346"
	product.ProductRef = &d.SourceProductRef{Source: d.SourceElevenStreet, Marketplace: "KR", ProductID: "12346"}
	product.URL = "https://www.11st.co.kr/products/12346"
	if e = repo.IngestDeals(ctx, []d.DealProduct{product}); e != nil {
		t.Fatal(e)
	}
	deliver()
	if notices, e = repo.NoticeSync(ctx, bgUser, client, "", false); e != nil || len(notices) != 0 {
		t.Fatalf("visible delivery created a dot: %v %v", notices, e)
	}
	view, e = repo.BackgroundView(ctx, bgUser, bgCuration)
	if e != nil || len(view.Findings) != 2 || view.Subscriptions[0].Status != "ACTIVE" {
		t.Fatalf("first result ended subscription: %+v %v", view, e)
	}
	// Same feed replay does not add another finding.
	if e = repo.IngestDeals(ctx, []d.DealProduct{product}); e != nil {
		t.Fatal(e)
	}
	if e = repo.PublishFindings(ctx); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.DB.QueryRowContext(ctx, `SELECT count(*) FROM research_findings`).Scan(&count); e != nil || count != 2 {
		t.Fatalf("duplicate findings=%d %v", count, e)
	}
}

func TestBackgroundCancellationAndTaxonomyCutoverFenceDelivery(t *testing.T) {
	ctx, db, repo, terms := bgFixture(t)
	pid := bgProposal(t, ctx, db, 1, terms)
	if e := repo.AcceptSubscription(ctx, bgUser, bgCuration, pid, c.FollowUpAction{TargetID: bgTarget, Subscription: &terms}); e != nil {
		t.Fatal(e)
	}
	price := int64(9000)
	ref := d.SourceProductRef{Source: d.SourceElevenStreet, Marketplace: "KR", ProductID: "654321"}
	p := d.DealProduct{SchemaVersion: "vitlane.deal-product.v1", Provider: "TEST", ExternalID: "1", Identity: ref.IdentityKey(), ProductRef: &ref, Title: "Headphones", URL: "https://www.11st.co.kr/products/654321", Country: "KR", Currency: "KRW", PriceMinor: &price, ObservedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if e := repo.IngestDeals(ctx, []d.DealProduct{p}); e != nil {
		t.Fatal(e)
	}
	snap, e := repo.ClassificationBatch(ctx)
	if e != nil {
		t.Fatal(e)
	}
	items := []a.Classification{}
	for _, v := range snap.Items {
		items = append(items, a.Classification{ID: v.ID, Categories: []string{"headphones"}})
	}
	if e = repo.SaveClassifications(ctx, snap, []d.ResearchCategory{{ID: "headphones", Label: "Headphones", Definition: "Headphones and earphones"}}, items); e != nil {
		t.Fatal(e)
	}
	if e = repo.RouteDeals(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, e := repo.ClaimMatches(ctx)
	if e != nil || len(jobs) != 0 {
		t.Fatalf("routing before subscription reclassification: %v %v", jobs, e)
	}
	next, e := repo.ClassificationBatch(ctx)
	if e != nil || next.Version != snap.Version+1 {
		t.Fatalf("version: %v %v", next, e)
	}
	items = nil
	for _, v := range next.Items {
		items = append(items, a.Classification{ID: v.ID, Categories: []string{"headphones"}})
	}
	if e = repo.SaveClassifications(ctx, next, nil, items); e != nil {
		t.Fatal(e)
	}
	if e = repo.RouteDeals(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, e = repo.ClaimMatches(ctx)
	if e != nil || len(jobs) != 1 {
		t.Fatalf("%v %v", jobs, e)
	}
	view, e := repo.BackgroundView(ctx, bgUser, bgCuration)
	if e != nil {
		t.Fatal(e)
	}
	if e = repo.CancelSubscription(ctx, bgUser, bgCuration, view.Subscriptions[0].ID); e != nil {
		t.Fatal(e)
	}
	if e = repo.FinishMatches(ctx, jobs, []a.MatchDecision{{ID: jobs[0].ID, Match: true, Reason: "Fits"}}); e != nil {
		t.Fatal(e)
	}
	if e = repo.PublishFindings(ctx); e != nil {
		t.Fatal(e)
	}
	view, e = repo.BackgroundView(ctx, bgUser, bgCuration)
	if e != nil || len(view.Findings) != 0 {
		t.Fatalf("late cancelled delivery %v %v", view, e)
	}
}

func TestBackgroundLifecycleStopsExpiredRemovedAndArchived(t *testing.T) {
	for _, state := range []string{"EXPIRED", "TARGET_REMOVED", "ARCHIVED"} {
		t.Run(state, func(t *testing.T) {
			ctx, db, repo, terms := bgFixture(t)
			if state == "EXPIRED" {
				terms.ExpiresAt = time.Now().Add(-time.Minute)
			}
			pid := bgProposal(t, ctx, db, 1, terms)
			if e := repo.AcceptSubscription(ctx, bgUser, bgCuration, pid, c.FollowUpAction{TargetID: bgTarget, Subscription: &terms}); e != nil {
				t.Fatal(e)
			}
			var e error
			if state == "TARGET_REMOVED" {
				_, e = db.DB.ExecContext(ctx, `UPDATE plan_targets SET removed_at=now(),removed_by_user_id=$2 WHERE id=$1`, bgTarget, bgUser)
			}
			if state == "ARCHIVED" {
				_, e = db.DB.ExecContext(ctx, `UPDATE curations SET archived_at=now(),archived_by_user_id=$2 WHERE id=$1`, bgCuration, bgUser)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = repo.ExpireSubscriptions(ctx); e != nil {
				t.Fatal(e)
			}
			view, e := repo.BackgroundView(ctx, bgUser, bgCuration)
			if e != nil || len(view.Subscriptions) != 1 || view.Subscriptions[0].Status != state {
				t.Fatalf("%+v %v", view, e)
			}
		})
	}
}
func TestFindingEvaluationReservesOneForegroundSlot(t *testing.T) {
	ctx, db, repo, _ := bgFixture(t)
	command := a.CatalogCandidatePoolCommandV2{UserID: bgUser, CurationID: bgCuration, TargetID: bgTarget, Mode: a.CatalogResearchAppendV2, IdempotencyKey: "finding:test:1", RequestHash: "0x" + strings.Repeat("a", 64)}
	first, e := repo.PreflightFindingImport(ctx, command)
	if e != nil {
		t.Fatal(e)
	}
	command2 := command
	command2.IdempotencyKey = "finding:test:2"
	if _, e = repo.PreflightFindingImport(ctx, command2); e == nil {
		t.Fatal("second foreground admitted")
	}
	counter := ip.NewRepository(db, nil)
	counter.EnableThreads()
	if n, e := counter.CountActiveActions(ctx, bgUser); e != nil || n != 1 {
		t.Fatalf("foreground count=%d %v", n, e)
	}
	if busy, e := counter.CurationHasActiveJobs(ctx, bgUser, bgCuration); e != nil || !busy {
		t.Fatalf("foreground busy=%v %v", busy, e)
	}
	command.FencingToken = first.FencingToken
	if e = repo.AbortCatalogSearchV2(ctx, command); e != nil {
		t.Fatal(e)
	}
	if n, e := counter.CountActiveActions(ctx, bgUser); e != nil || n != 0 {
		t.Fatalf("released foreground count=%d %v", n, e)
	}
}
