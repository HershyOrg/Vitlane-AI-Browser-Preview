package postgres

import (
	"encoding/json"
	c "github.com/vitlane/vitlane/server/internal/curation/domain"
	a "github.com/vitlane/vitlane/server/internal/curation/research/app"
	d "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"sync"
	"testing"
	"time"
)

func recoveryProduct(id string) d.DealProduct {
	now := time.Now().UTC()
	return d.DealProduct{SchemaVersion: "vitlane.deal-product.v1", Provider: "TELEGRAM_JIRUM", ExternalID: "jirum/" + id, Identity: "telegram:jirum/" + id, Title: "Headphones", URL: "https://t.me/jirum/" + id, Country: "KR", Currency: "KRW", ObservedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
}
func TestFeedRecoveryAtomicCheckpointAndDurableRetry(t *testing.T) {
	ctx, db, repo, _ := bgFixture(t)
	cp, err := repo.FeedCheckpoint(ctx, "TELEGRAM_JIRUM")
	if err != nil {
		t.Fatal(err)
	}
	p := recoveryProduct("101")
	raw := "https://link.coupang.com/a/recovery"
	b := d.FeedBatch{Checkpoint: cp, Products: []d.DealProduct{p}, Links: map[string]string{p.ExternalID: raw}}
	b.Checkpoint.LastID = 101
	// A DB failure after ingestion must roll back both products and checkpoint.
	if _, err = db.DB.ExecContext(ctx, `ALTER TABLE research_feed_links ADD CONSTRAINT test_reject CHECK(url<>'https://link.coupang.com/a/recovery')`); err != nil {
		t.Fatal(err)
	}
	if err = repo.CommitFeedBatch(ctx, "TELEGRAM_JIRUM", b); err == nil {
		t.Fatal("expected transaction rollback")
	}
	got, _ := repo.FeedCheckpoint(ctx, "TELEGRAM_JIRUM")
	var count int
	db.DB.QueryRowContext(ctx, "SELECT count(*) FROM research_feed_products").Scan(&count)
	if got != cp || count != 0 {
		t.Fatalf("partial commit: %+v products=%d", got, count)
	}
	db.DB.ExecContext(ctx, "ALTER TABLE research_feed_links DROP CONSTRAINT test_reject")
	if err = repo.CommitFeedBatch(ctx, "TELEGRAM_JIRUM", b); err != nil {
		t.Fatal(err)
	}
	if err = repo.CommitFeedBatch(ctx, "TELEGRAM_JIRUM", b); err == nil {
		t.Fatal("stale checkpoint accepted")
	}
	// Claims are exclusive even across repositories.
	var wg sync.WaitGroup
	claims := make(chan []d.FeedLinkJob, 2)
	errors := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobs, e := NewRepository(db).ClaimFeedLinks(ctx, "TELEGRAM_JIRUM")
			claims <- jobs
			errors <- e
		}()
	}
	wg.Wait()
	close(claims)
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	var jobs []d.FeedLinkJob
	for rows := range claims {
		jobs = append(jobs, rows...)
	}
	if len(jobs) != 1 {
		t.Fatalf("claims=%d", len(jobs))
	}
	if err = repo.FinishFeedLink(ctx, jobs[0], d.FeedLinkResult{Reason: "LOCAL_LIMIT"}); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var status string
	db.DB.QueryRowContext(ctx, "SELECT attempts,status FROM research_feed_links WHERE id=$1", jobs[0].ID).Scan(&attempts, &status)
	if attempts != 0 || status != "PENDING" {
		t.Fatalf("local denial consumed attempt %d %s", attempts, status)
	}
	for attempt := 1; attempt <= 4; attempt++ {
		db.DB.ExecContext(ctx, "UPDATE research_feed_links SET next_at=now()-interval '1 second'")
		jobs, err = NewRepository(db).ClaimFeedLinks(ctx, "TELEGRAM_JIRUM")
		if err != nil || len(jobs) != 1 || jobs[0].Attempts != attempt {
			t.Fatalf("restart claim %+v %v", jobs, err)
		}
		if err = repo.FinishFeedLink(ctx, jobs[0], d.FeedLinkResult{Attempted: true, Retryable: true, Reason: "TIMEOUT"}); err != nil {
			t.Fatal(err)
		}
	}
	db.DB.ExecContext(ctx, "UPDATE research_feed_links SET next_at=now()-interval '1 second'")
	jobs, err = repo.ClaimFeedLinks(ctx, "TELEGRAM_JIRUM")
	if err != nil || len(jobs) != 0 {
		t.Fatal("max attempts not enforced", err)
	}
	db.DB.QueryRowContext(ctx, "SELECT attempts,status FROM research_feed_links").Scan(&attempts, &status)
	if attempts != 4 || status != "FAILED" {
		t.Fatalf("%d %s", attempts, status)
	}
}
func TestFeedRecoveryLateLinkDoesNotNotifyTwiceAndFencesOldWorker(t *testing.T) {
	ctx, db, repo, terms := bgFixture(t)
	pid := bgProposal(t, ctx, db, 1, terms)
	if err := repo.AcceptSubscription(ctx, bgUser, bgCuration, pid, c.FollowUpAction{TargetID: bgTarget, Subscription: &terms}); err != nil {
		t.Fatal(err)
	}
	var sub string
	db.DB.QueryRowContext(ctx, "SELECT id FROM research_subscriptions LIMIT 1").Scan(&sub)
	p := recoveryProduct("101")
	ref := d.SourceProductRef{Source: d.SourceCoupang, Marketplace: "KR", ProductID: "12345"}
	canonical := p
	canonical.Identity = ref.IdentityKey()
	canonical.ProductRef = &ref
	canonical.ExternalID = "jirum/100"
	for _, product := range []d.DealProduct{canonical, p} {
		raw, _ := json.Marshal(product)
		if _, err := db.DB.ExecContext(ctx, `INSERT INTO research_findings(user_id,curation_id,subscription_id,target_id,identity,product,reason) VALUES($1,$2,$3,$4,$5,$6,'match')`, bgUser, bgCuration, sub, bgTarget, product.Identity, raw); err != nil {
			t.Fatal(err)
		}
	}
	cp, _ := repo.FeedCheckpoint(ctx, "TELEGRAM_JIRUM")
	cp.LastID = 101
	rawURL := "https://link.coupang.com/a/shared"
	if err := repo.CommitFeedBatch(ctx, "TELEGRAM_JIRUM", d.FeedBatch{Checkpoint: cp, Products: []d.DealProduct{p}, Links: map[string]string{p.ExternalID: rawURL}}); err != nil {
		t.Fatal(err)
	}
	jobs, err := repo.ClaimFeedLinks(ctx, "TELEGRAM_JIRUM")
	if err != nil || len(jobs) != 1 {
		t.Fatal(err, jobs)
	}
	old := jobs[0]
	db.DB.ExecContext(ctx, "UPDATE research_feed_links SET lease_until=now()-interval '1 second'")
	jobs, err = repo.ClaimFeedLinks(ctx, "TELEGRAM_JIRUM")
	if err != nil || len(jobs) != 1 {
		t.Fatal(err, jobs)
	}
	if err = repo.FinishFeedLink(ctx, old, d.FeedLinkResult{Attempted: true, ProductRef: &ref}); err != nil {
		t.Fatal(err)
	}
	var status string
	db.DB.QueryRowContext(ctx, "SELECT status FROM research_feed_links").Scan(&status)
	if status != "RUNNING" {
		t.Fatal("stale worker completed new lease")
	}
	// Clear existing indicator; resolution must not raise it again.
	if _, err = repo.NoticeSync(ctx, bgUser, "98000000-0000-4000-8000-000000000099", bgCuration, true); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishFeedLink(ctx, jobs[0], d.FeedLinkResult{Attempted: true, ProductRef: &ref}); err != nil {
		t.Fatal(err)
	}
	var count int
	db.DB.QueryRowContext(ctx, "SELECT count(*) FROM research_findings WHERE status='NEW'").Scan(&count)
	if count != 1 {
		t.Fatalf("duplicate visible canonical products: %d", count)
	}
	notices, err := repo.NoticeSync(ctx, bgUser, "98000000-0000-4000-8000-000000000099", "", false)
	if err != nil || len(notices) != 0 {
		t.Fatalf("resolution renotified: %v %v", notices, err)
	}
	// Subsequent posts sharing a resolved URL use the stored identity without new work.
	cp, _ = repo.FeedCheckpoint(ctx, "TELEGRAM_JIRUM")
	cp.LastID = 102
	p = recoveryProduct("102")
	if err = repo.CommitFeedBatch(ctx, "TELEGRAM_JIRUM", d.FeedBatch{Checkpoint: cp, Products: []d.DealProduct{p}, Links: map[string]string{p.ExternalID: rawURL}}); err != nil {
		t.Fatal(err)
	}
	var identity string
	db.DB.QueryRowContext(ctx, "SELECT identity FROM research_feed_products WHERE external_id='jirum/102'").Scan(&identity)
	if identity != ref.IdentityKey() {
		t.Fatal("resolved link not reused")
	}
}
func TestTelegramOperationBudgetsAndAmazonForegroundReserve(t *testing.T) {
	ctx, db, repo, _ := bgFixture(t)
	now := time.Now()
	if _, err := repo.UpdateProviderControl(ctx, "TELEGRAM_JIRUM", bgUser, true, 1, now); err != nil {
		t.Fatal(err)
	}
	// Exhaust link allowance only; page scans retain their own budget.
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO research_catalog_api_calls(source,operation,outcome,started_at,completed_at) SELECT 'TELEGRAM_JIRUM','LINK_RESOLVE','SUCCESS',now()-interval '1 hour',now()-interval '1 hour' FROM generate_series(1,384)`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReserveProviderCall(ctx, "TELEGRAM_JIRUM", "LINK_RESOLVE", now); err == nil {
		t.Fatal("link limit ignored")
	}
	call, err := repo.ReserveProviderCall(ctx, "TELEGRAM_JIRUM", "FEED_PAGE", now)
	if err != nil {
		t.Fatal("link exhaustion blocked pages", err)
	}
	if err = repo.CompleteProviderCall(ctx, call, "SUCCESS", 200, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.ExecContext(ctx, `INSERT INTO research_catalog_api_calls(source,operation,outcome,started_at,completed_at) SELECT 'TELEGRAM_JIRUM','FEED_PAGE','SUCCESS',now()-interval '1 hour',now()-interval '1 hour' FROM generate_series(1,479)`); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReserveProviderCall(ctx, "TELEGRAM_JIRUM", "FEED_PAGE", now); err == nil {
		t.Fatal("page limit ignored")
	}
	usage, err := repo.ReadProviderUsage(ctx, "TELEGRAM_JIRUM")
	if err != nil || len(usage.Operations24h) != 2 {
		t.Fatal("missing operation usage", usage, err)
	}
	repo.ConfigureBackgroundAmazon(1, 10)
	if err = repo.SaveAmazonQuota(ctx, a.CatalogAPIQuota{Limit: 100, Remaining: 11, Used: 89, ObservedAt: now, ResetAt: now.Add(time.Hour)}, now); err != nil {
		t.Fatal(err)
	}
	call, err = repo.ReserveAmazonCall(ctx, "FEED", now)
	if err != nil {
		t.Fatal(err)
	}
	repo.CompleteAmazonCall(ctx, call, "SUCCESS", now)
	if _, err = repo.ReserveAmazonCall(ctx, "FEED", now.Add(2*time.Second)); err == nil {
		t.Fatal("background reserve ignored")
	}
	if _, err = repo.ReserveAmazonCall(ctx, "SEARCH", now.Add(2*time.Second)); err != nil {
		t.Fatal("foreground quota blocked", err)
	}
}

func TestRecoveredPostsOnlyReachSubscriptionsActiveAtPublication(t *testing.T) {
	ctx, db, repo, terms := bgFixture(t)
	pid := bgProposal(t, ctx, db, 1, terms)
	if err := repo.AcceptSubscription(ctx, bgUser, bgCuration, pid, c.FollowUpAction{TargetID: bgTarget, Subscription: &terms}); err != nil {
		t.Fatal(err)
	}
	old := recoveryProduct("100")
	old.ObservedAt = time.Now().Add(-time.Hour)
	fresh := recoveryProduct("101")
	if err := repo.IngestDeals(ctx, []d.DealProduct{old, fresh}); err != nil {
		t.Fatal(err)
	}
	snap, err := repo.ClassificationBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows := []a.Classification{}
	for _, item := range snap.Items {
		rows = append(rows, a.Classification{ID: item.ID, Categories: []string{"electronics"}})
	}
	if err = repo.SaveClassifications(ctx, snap, nil, rows); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		if err = repo.RouteDeals(ctx); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := repo.ClaimMatches(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].Product.ExternalID != "jirum/101" {
		t.Fatalf("historical post delivered: %+v %v", jobs, err)
	}
}
func TestTelegramLinkCooldownDoesNotPauseFeedPages(t *testing.T) {
	ctx, _, repo, _ := bgFixture(t)
	now := time.Now()
	if _, err := repo.UpdateProviderControl(ctx, "TELEGRAM_JIRUM", bgUser, true, 1, now); err != nil {
		t.Fatal(err)
	}
	id, err := repo.ReserveProviderCall(ctx, "TELEGRAM_JIRUM", "LINK_RESOLVE", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteProviderCall(ctx, id, "PROVIDER_RATE_LIMITED", 429, 7200, now); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReserveProviderCall(ctx, "TELEGRAM_JIRUM", "LINK_RESOLVE", now); err == nil {
		t.Fatal("Retry-After ignored")
	}
	if _, err = repo.ReserveProviderCall(ctx, "TELEGRAM_JIRUM", "FEED_PAGE", now); err != nil {
		t.Fatal("link host cooldown blocked feed host", err)
	}
}
