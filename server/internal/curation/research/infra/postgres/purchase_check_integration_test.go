package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

const (
	purchaseCheckTestUser     = "98000000-0000-4000-8000-000000000001"
	purchaseCheckTestCuration = "98000000-0000-4000-8000-000000000003"
	purchaseCheckTestTarget   = "98000000-0000-4000-8000-000000000004"
)

func purchaseCheckTestObservation(t *testing.T) (researchdomain.SourceProductRef, []byte) {
	t.Helper()
	ref := researchdomain.SourceProductRef{Source: researchdomain.SourceCoupang, Marketplace: "KR", ProductID: "8825648110"}
	amount := int64(64000)
	observation := researchdomain.ExternalProductObservation{
		SchemaVersion: "vitlane.external-product-observation.v1", ProductRef: ref,
		ProductURL: "https://www.coupang.com/vp/products/8825648110", Title: "라미 사파리 만년필",
		Price: researchdomain.VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &amount, Currency: "KRW"}, PriceScope: "PRODUCT",
		Seller:     researchdomain.ObservedSeller{Kind: "UNKNOWN"},
		Provenance: researchdomain.ProductProvenance{APIProvider: "OpenWebNinja", APIProduct: "Real-Time Product Search v2", DiscoveryChannel: "GOOGLE_SHOPPING", Country: "KR", QueryLanguage: "ko"},
		ObservedAt: time.Now().UTC(),
	}
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	return ref, raw
}

func seedPurchaseCheckCandidates(t *testing.T, ctx context.Context, db *sharedpostgres.Database, observation []byte) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO phase8_research_pools(user_id,curation_id,plan_target_id,version,expand_ordinal,created_at,updated_at) VALUES($1,$2,$3,1,0,now(),now())`,
		`INSERT INTO phase8_research_candidates(user_id,curation_id,plan_target_id,candidate_id,provider_product_id,source_kind,identity_key,locator_kind,product_url,visible,display_order,first_seen_at,last_seen_at) VALUES($1,$2,$3,'candidate-amazon','amazon:US:B012345678','AMAZON','amazon:US:B012345678','PRODUCT_URL','https://www.amazon.com/dp/B012345678',true,0,now(),now())`,
	} {
		if _, err := db.DB.ExecContext(ctx, statement, purchaseCheckTestUser, purchaseCheckTestCuration, purchaseCheckTestTarget); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO phase8_research_candidates(user_id,curation_id,plan_target_id,candidate_id,provider_product_id,source_kind,identity_key,locator_kind,product_url,visible,display_order,first_seen_at,last_seen_at,external_observation) VALUES($1,$2,$3,'candidate-coupang','coupang:KR:8825648110','COUPANG','coupang:KR:8825648110','PRODUCT_URL','https://www.coupang.com/vp/products/8825648110',true,1,now(),now(),$4)`, purchaseCheckTestUser, purchaseCheckTestCuration, purchaseCheckTestTarget, observation); err != nil {
		t.Fatal(err)
	}
}

func TestPurchaseCheckAccountListPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, url)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	ref, observation := purchaseCheckTestObservation(t)
	seedPurchaseCheckCandidates(t, ctx, db, observation)
	repo := NewRepository(db)
	user, curation := purchaseCheckTestUser, purchaseCheckTestCuration
	base := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	amazon := researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B012345678"}
	amazonIn := researchapp.MarkExternalPurchaseInput{UserID: user, CurationID: curation, CandidateID: "candidate-amazon", VariantRef: amazon, Checked: true, IdempotencyKey: "amazon-1",
		Snapshot: &researchdomain.PurchaseCheckSnapshot{ProductTitle: "Wireless Headset", VariantTitle: "Black", Merchant: "Amazon", PriceMinor: 12999, Currency: "USD"}}
	if _, err := repo.MarkExternalPurchase(ctx, amazonIn, base); err != nil {
		t.Fatal(err)
	}
	koreanIn := researchapp.MarkExternalPurchaseInput{UserID: user, CurationID: curation, CandidateID: "candidate-coupang", ProductRef: &ref, Checked: true, IdempotencyKey: "korean-1"}
	feedback, err := repo.MarkExternalPurchase(ctx, koreanIn, base.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(feedback); strings.Contains(string(raw), "snapshot") || strings.Contains(string(raw), "productTitle") {
		t.Fatalf("per-curation feedback contract leaked the list snapshot: %s", raw)
	}
	list, err := repo.ListPurchaseChecks(ctx, user, 50)
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	korean, amazonRow := list[0], list[1]
	if korean.ProductRef == nil || korean.ProductRef.ProductID != "8825648110" || korean.VariantRef != nil || korean.ProductURL != "https://www.coupang.com/vp/products/8825648110" ||
		korean.TargetID != purchaseCheckTestTarget || korean.TargetTitle != "test product" || korean.CandidateID != "candidate-coupang" || !korean.Checked || korean.Version != 1 || korean.Evidence != "SELF_REPORTED" ||
		korean.Snapshot == nil || korean.Snapshot.ProductTitle != "라미 사파리 만년필" || korean.Snapshot.PriceMinor != 64000 || korean.Snapshot.Currency != "KRW" || korean.Snapshot.PriceUnknown || korean.Snapshot.Merchant != "" || korean.SnapshotAt == nil || !korean.SnapshotAt.Equal(base.Add(time.Second)) {
		t.Fatalf("korean row derived from the saved observation: %#v snapshot=%#v", korean, korean.Snapshot)
	}
	if amazonRow.VariantRef == nil || amazonRow.VariantRef.ASIN != "B012345678" || amazonRow.ProductRef != nil || amazonRow.ProductURL != "https://www.amazon.com/dp/B012345678" || amazonRow.TargetTitle != "test product" ||
		amazonRow.Snapshot == nil || amazonRow.Snapshot.ProductTitle != "Wireless Headset" || amazonRow.Snapshot.VariantTitle != "Black" || amazonRow.Snapshot.Merchant != "Amazon" || amazonRow.Snapshot.PriceMinor != 12999 || amazonRow.Snapshot.Currency != "USD" {
		t.Fatalf("amazon row kept the client snapshot: %#v snapshot=%#v", amazonRow, amazonRow.Snapshot)
	}
	// Records written before snapshot capture list with a null snapshot and a derived product page.
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO research_external_purchase_records(user_id,curation_id,source,marketplace,asin,candidate_id,checked,version,recorded_at) VALUES($1,$2,'AMAZON','US','B000000001','candidate-amazon',true,1,$3)`, user, curation, base.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	list, err = repo.ListPurchaseChecks(ctx, user, 50)
	if err != nil || len(list) != 3 || list[0].Snapshot != nil || list[0].SnapshotAt != nil || list[0].ProductURL != "https://www.amazon.com/dp/B000000001" {
		t.Fatalf("legacy row: %#v err=%v", list, err)
	}
	if limited, err := repo.ListPurchaseChecks(ctx, user, 1); err != nil || len(limited) != 1 || limited[0].VariantRef == nil || limited[0].VariantRef.ASIN != "B000000001" {
		t.Fatalf("limit/order: %#v err=%v", limited, err)
	}
	if other, err := repo.ListPurchaseChecks(ctx, "98000000-0000-4000-8000-000000000099", 50); err != nil || len(other) != 0 {
		t.Fatalf("cross-user list: %#v err=%v", other, err)
	}
	// Undo leaves the list; a later re-check without a snapshot keeps the original one.
	undo := amazonIn
	undo.Checked, undo.ExpectedVersion, undo.IdempotencyKey, undo.Snapshot = false, 1, "amazon-2", nil
	if _, err := repo.MarkExternalPurchase(ctx, undo, base.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListPurchaseChecks(ctx, user, 50)
	for _, row := range list {
		if row.VariantRef != nil && row.VariantRef.ASIN == "B012345678" {
			t.Fatal("undone check still listed")
		}
	}
	redo := undo
	redo.Checked, redo.ExpectedVersion, redo.IdempotencyKey = true, 2, "amazon-3"
	if _, err := repo.MarkExternalPurchase(ctx, redo, base.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListPurchaseChecks(ctx, user, 50)
	if len(list) != 3 || list[0].VariantRef == nil || list[0].VariantRef.ASIN != "B012345678" || list[0].Version != 3 || list[0].Snapshot == nil || list[0].Snapshot.ProductTitle != "Wireless Headset" || list[0].SnapshotAt == nil || !list[0].SnapshotAt.Equal(base) {
		t.Fatalf("re-check without snapshot must keep the original: %#v", list[0])
	}
	// A new snapshot on re-check replaces the whole set.
	undo2 := redo
	undo2.Checked, undo2.ExpectedVersion, undo2.IdempotencyKey = false, 3, "amazon-4"
	if _, err := repo.MarkExternalPurchase(ctx, undo2, base.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	redo2 := undo2
	redo2.Checked, redo2.ExpectedVersion, redo2.IdempotencyKey = true, 4, "amazon-5"
	redo2.Snapshot = &researchdomain.PurchaseCheckSnapshot{ProductTitle: "Wireless Headset (2nd gen)", PriceUnknown: true}
	if _, err := repo.MarkExternalPurchase(ctx, redo2, base.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListPurchaseChecks(ctx, user, 50)
	if list[0].Snapshot == nil || list[0].Snapshot.ProductTitle != "Wireless Headset (2nd gen)" || !list[0].Snapshot.PriceUnknown || list[0].Snapshot.PriceMinor != 0 || list[0].Snapshot.Currency != "" || list[0].Snapshot.VariantTitle != "" || list[0].SnapshotAt == nil || !list[0].SnapshotAt.Equal(base.Add(6*time.Second)) {
		t.Fatalf("new snapshot must replace the whole set: %#v", list[0].Snapshot)
	}
	// The DB rejects a half-filled snapshot regardless of the writer.
	if _, err := db.DB.ExecContext(ctx, `UPDATE research_external_purchase_records SET currency=NULL, price_unknown=false, price_minor=1 WHERE user_id=$1 AND asin='B012345678'`, user); err == nil || !strings.Contains(err.Error(), "external_purchase_snapshot_check") {
		t.Fatalf("inconsistent snapshot accepted: %v", err)
	}
}

// The backfill only touches Korean rows: their observation is server-owned.
// Amazon rows written before snapshot capture stay without one.
func TestPurchaseCheckSnapshotBackfillPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	migrations, err := filepath.Abs("../../../../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	before := t.TempDir()
	entries, err := os.ReadDir(migrations)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") || name >= "000116_" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(migrations, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(before, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	admin, err := sharedpostgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("vitlane_purchase_backfill_%d", time.Now().UnixNano())
	if _, err := admin.DB.ExecContext(ctx, `CREATE DATABASE "`+name+`" TEMPLATE template0`); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	db, err := sharedpostgres.Open(ctx, catalogPoolDatabaseURLV2(t, url, name))
	if err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.DB.ExecContext(cleanup, `DROP DATABASE "`+name+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop backfill database: %v", err)
		}
		_ = admin.Close()
	})
	if err := db.Migrate(ctx, before); err != nil {
		t.Fatalf("migrate through 000115: %v", err)
	}
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	_, observation := purchaseCheckTestObservation(t)
	seedPurchaseCheckCandidates(t, ctx, db, observation)
	recorded := time.Date(2026, 9, 13, 9, 30, 0, 0, time.UTC)
	for _, statement := range []string{
		`INSERT INTO research_external_purchase_records(user_id,curation_id,source,marketplace,asin,candidate_id,checked,version,recorded_at) VALUES($1,$2,'AMAZON','US','B012345678','candidate-amazon',true,1,$3)`,
		`INSERT INTO research_external_purchase_records(user_id,curation_id,source,marketplace,product_id,candidate_id,checked,version,recorded_at) VALUES($1,$2,'COUPANG','KR','8825648110','candidate-coupang',true,1,$3)`,
	} {
		if _, err := db.DB.ExecContext(ctx, statement, purchaseCheckTestUser, purchaseCheckTestCuration, recorded); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("migrate 000116: %v", err)
	}
	list, err := NewRepository(db).ListPurchaseChecks(ctx, purchaseCheckTestUser, 50)
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	var korean, amazon *researchdomain.PurchaseCheck
	for index := range list {
		if list[index].ProductRef != nil {
			korean = &list[index]
		} else {
			amazon = &list[index]
		}
	}
	if korean == nil || korean.Snapshot == nil || korean.Snapshot.ProductTitle != "라미 사파리 만년필" || korean.Snapshot.PriceMinor != 64000 || korean.Snapshot.Currency != "KRW" || korean.Snapshot.PriceUnknown || korean.Snapshot.Merchant != "" || korean.SnapshotAt == nil || !korean.SnapshotAt.Equal(recorded) {
		t.Fatalf("korean backfill: %#v", korean)
	}
	if amazon == nil || amazon.Snapshot != nil || amazon.SnapshotAt != nil || amazon.ProductURL != "https://www.amazon.com/dp/B012345678" {
		t.Fatalf("amazon legacy row must stay without snapshot: %#v", amazon)
	}
}

// A card read must not queue behind a writer or another card of the same
// curation: the read takes no lock and one statement keeps version and records
// consistent.
func TestPurchaseFeedbackReadDoesNotWaitForTheCurationLockPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, url)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	ref, observation := purchaseCheckTestObservation(t)
	seedPurchaseCheckCandidates(t, ctx, db, observation)
	repo := NewRepository(db)
	user, curation := purchaseCheckTestUser, purchaseCheckTestCuration
	now := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)

	empty, err := repo.ReadPurchaseFeedback(ctx, user, curation)
	if err != nil || empty.Version != 0 || len(empty.Records) != 0 {
		t.Fatalf("empty curation: %#v %v", empty, err)
	}
	if _, err := repo.MarkExternalPurchase(ctx, researchapp.MarkExternalPurchaseInput{UserID: user, CurationID: curation, CandidateID: "candidate-coupang", ProductRef: &ref, Checked: true, IdempotencyKey: "lock-free-1"}, now); err != nil {
		t.Fatal(err)
	}

	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- db.WithinTransaction(ctx, func(tx context.Context) error {
			if err := repo.lockPurchaseFeedback(tx, user, curation); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	start := time.Now()
	feedback, err := repo.ReadPurchaseFeedback(ctx, user, curation)
	elapsed := time.Since(start)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("read waited for the writer lock: %s", elapsed)
	}
	if feedback.Version != 1 || len(feedback.Records) != 1 || !feedback.Records[0].Checked || feedback.Records[0].Version != 1 {
		t.Fatalf("version and records drifted apart: %#v", feedback)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Round creation still reads under the lock and sees the same snapshot.
	planned, err := repo.PurchaseFeedbackForPlan(ctx, user, "98000000-0000-4000-8000-000000000002")
	if err != nil || planned.Version != feedback.Version || len(planned.Records) != len(feedback.Records) {
		t.Fatalf("round snapshot: %#v %v", planned, err)
	}
}

// Amazon keeps the same kind of saved observation Korean products keep, so a
// check with no client snapshot still lands with a title, and a card can render
// from the database when the provider is unavailable (ADR-0077).
func TestAmazonObservationFillsTheSnapshotPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, url)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	_, observation := purchaseCheckTestObservation(t)
	seedPurchaseCheckCandidates(t, ctx, db, observation)
	repo := NewRepository(db)
	user, curation := purchaseCheckTestUser, purchaseCheckTestCuration
	now := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	amount := int64(12999)
	saved := researchdomain.AmazonObservation{
		SchemaVersion: researchdomain.AmazonObservationSchemaVersion,
		VariantRef:    researchdomain.SourceVariantRef{Source: researchdomain.SourceAmazon, Marketplace: "US", ASIN: "B012345678"},
		ProductURL:    "https://www.amazon.com/dp/B012345678", Title: "Wireless Headset",
		Price:      researchdomain.VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &amount, Currency: "USD"},
		ObservedAt: now,
	}
	if err := repo.SaveAmazonObservation(ctx, user, curation, "candidate-amazon", saved); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAmazonObservation(ctx, user, curation, "candidate-coupang", saved); err != nil {
		t.Fatal(err)
	}
	var koreanObservation []byte
	if err := db.DB.QueryRowContext(ctx, `SELECT amazon_observation FROM phase8_research_candidates WHERE user_id=$1 AND curation_id=$2 AND candidate_id='candidate-coupang'`, user, curation).Scan(&koreanObservation); err != nil {
		t.Fatal(err)
	}
	if len(koreanObservation) != 0 {
		t.Fatal("a Korean candidate must not carry an Amazon observation")
	}
	in := researchapp.MarkExternalPurchaseInput{UserID: user, CurationID: curation, CandidateID: "candidate-amazon", VariantRef: saved.VariantRef, Checked: true, IdempotencyKey: "amazon-derived"}
	if _, err := repo.MarkExternalPurchase(ctx, in, now); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListPurchaseChecks(ctx, user, 50)
	if err != nil || len(list) != 1 || list[0].Snapshot == nil {
		t.Fatalf("list: %#v %v", list, err)
	}
	if list[0].Snapshot.ProductTitle != "Wireless Headset" || list[0].Snapshot.PriceMinor != 12999 || list[0].Snapshot.Currency != "USD" || list[0].Snapshot.Merchant != "Amazon" {
		t.Fatalf("server derived snapshot: %#v", list[0].Snapshot)
	}
	// The stored record reaches the workspace read, so a card renders without a provider call.
	state, err := repo.LoadCatalogWorkspaceStateV2(ctx, user, curation)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range state.Candidates {
		if candidate.CandidateID != "candidate-amazon" {
			continue
		}
		if candidate.AmazonObservation == nil || candidate.AmazonObservation.Title != "Wireless Headset" {
			t.Fatalf("workspace state lost the saved observation: %#v", candidate.AmazonObservation)
		}
	}
	// The DB rejects content this boundary does not store.
	if _, err := db.DB.ExecContext(ctx, `UPDATE phase8_research_candidates SET amazon_observation=amazon_observation||'{"imageUrl":"https://example.com/x.jpg"}'::jsonb WHERE user_id=$1 AND candidate_id='candidate-amazon'`, user); err == nil {
		t.Fatal("image content accepted into the Amazon observation")
	}
}
