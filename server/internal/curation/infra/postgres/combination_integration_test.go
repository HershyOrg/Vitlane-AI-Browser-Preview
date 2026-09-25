package postgres

import (
	"context"
	c "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	pp "github.com/vitlane/vitlane/server/internal/curation/planning/infra/postgres"
	sa "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	sp "github.com/vitlane/vitlane/server/internal/curation/research/session/infra/postgres"
	shared "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	dbp "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

type combinationTestSource struct {
	responseTestSource
	state string
}

func (s *combinationTestSource) CombinationState(context.Context, string, string) (string, error) {
	return s.state, nil
}
func TestCombinationCartAtomicReplayAndStaleness(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	database, err := dbp.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.Migrate(ctx, "../../../../migrations"); err != nil {
		t.Fatal(err)
	}
	conn, err := database.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended('vitlane.integration_tests',0))"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtextextended('vitlane.integration_tests',0))")
	if _, err = database.DB.ExecContext(ctx, "TRUNCATE users CASCADE"); err != nil {
		t.Fatal(err)
	}
	seedCurationThreadFixtureRows(t, ctx, database)
	const user = "50000000-0000-4000-8000-000000000001"
	const cur = "52000000-0000-4000-8000-000000000002"
	const target = "53000000-0000-4000-8000-000000000001"
	const planID = "51000000-0000-4000-8000-000000000002"
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := database.DB.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO phase8_research_pools(user_id,curation_id,plan_target_id,version,latest_mode,created_at,updated_at) VALUES($1,$2,$3,1,'APPEND',now(),now())", user, cur, target)
	exec("INSERT INTO phase8_research_candidates(user_id,curation_id,plan_target_id,candidate_id,provider_product_id,source_kind,identity_key,locator_kind,variant_id,seller_domain,display_order,first_seen_at,last_seen_at) VALUES($1,$2,$3,'candidate','shopify:test','SHOPIFY_LIVE','test','MERCHANT_VARIANT','variant','shop.example.com',0,now(),now())", user, cur, target)
	ids := shared.UUIDGenerator{}
	clock := shared.SystemClock{}
	repo := NewRepository(database, pp.NewRepository(database))
	core := c.NewService(repo, sa.NewService(sp.NewRepository(database), clock, ids), database, clock, ids, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service := c.NewThreadService(core, repo, nil, nil)
	cart, _ := c.NewCatalogCartServiceV2(repo, clock)
	service.SetCombinationCart(cart)
	source := &combinationTestSource{state: "initial"}
	service.SetResponder(nil, source)
	exec("INSERT INTO curation_budgets(curation_id,enabled,currency,version,research_version,allocations,initial_materialized) VALUES($1,false,'USD',1,1,'[]',true)", cur)
	budget, err := core.Budget(ctx, user, cur)
	if err != nil {
		t.Fatal(err)
	}
	criteria, err := core.TargetCriteria(ctx, user, cur, target)
	if err != nil {
		t.Fatal(err)
	}
	cv := int64(0)
	if criteria != nil {
		cv = criteria.Version
	}
	saved := d.CombinationResponse{ID: "pick", SourceState: "initial", BudgetVersion: budget.Version, CartVersion: 0, CriteriaVersions: map[string]int64{target: cv}, Compatibility: "UNVERIFIED", Items: []d.CombinationItem{{Ref: "c1", TargetID: target, CandidateID: "candidate", CheckoutEligible: true}}}
	response, err := d.NewActionResponse("COMMENT", "[[c1]] 조합이에요.", "ko-KR", "test", []d.ResponseReference{{Ref: "c1", CandidateID: "candidate", TargetID: target}}, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	response.SchemaVersion = "vitlane.thread-response.v2"
	response.Combination = &saved
	thread := d.CurationThread{ID: ids.NewID(), UserID: user, CurationID: cur, PlanID: planID, Status: "SUCCEEDED", Origin: "SYSTEM", Revision: 1, CreatedAt: clock.Now(), UpdatedAt: clock.Now(), Actions: []d.CurationAction{{ID: d.CurationActionID(ids.NewID()), Type: d.CurationActionResponse, Status: "SUCCEEDED", Response: &response}}}
	if err = repo.InsertThread(ctx, thread); err != nil {
		t.Fatal(err)
	}
	status, err := service.CombinationStatus(ctx, user, cur, thread.ID)
	if err != nil || !status.Current {
		t.Fatal(status, err)
	}
	item := c.CatalogCartItemV2{TargetID: target, Item: d.CartItemV2{CandidateID: "candidate", ProductTitleSnapshot: "Chair", VariantID: "variant", VariantTitleSnapshot: "Black", SelectedOptions: []string{"Color: Black"}, PreviewPriceMinor: 3000, PreviewCurrency: "USD", Quantity: 2, ObservedAt: clock.Now()}}
	command := c.CombinationCartCommand{CommandID: ids.NewID(), Mode: "ADD", Items: []c.CatalogCartItemV2{item}}
	reason := func(err error, want string) {
		t.Helper()
		f, ok := fault.As(err)
		if !ok || f.Reason != want {
			t.Fatalf("wanted %s got %v", want, err)
		}
	}
	_, err = service.ApplyCombinationCart(ctx, user, cur, thread.ID, command)
	reason(err, "COMBINATION_VARIANT_NOT_CONFIRMED")
	exec("INSERT INTO phase8_candidate_configurations(user_id,curation_id,plan_target_id,candidate_id,variant_id,selected_options,observed_at,updated_at) VALUES($1,$2,$3,'candidate','different','[]',now(),now())", user, cur, target)
	_, err = service.ApplyCombinationCart(ctx, user, cur, thread.ID, command)
	reason(err, "COMBINATION_VARIANT_CHANGED")
	exec("UPDATE phase8_candidate_configurations SET variant_id='variant' WHERE user_id=$1 AND curation_id=$2 AND candidate_id='candidate'", user, cur)

	exec("UPDATE phase8_research_candidates SET source_kind='AMAZON',provider_product_id='amazon:US:B000000001',identity_key='amazon:US:B000000001',locator_kind='PRODUCT_URL',product_url='https://www.amazon.com/dp/B000000001',variant_id=NULL,seller_domain=NULL WHERE user_id=$1 AND curation_id=$2 AND candidate_id='candidate'", user, cur)
	_, err = service.ApplyCombinationCart(ctx, user, cur, thread.ID, command)
	reason(err, "EXTERNAL_PRODUCT_CART_FORBIDDEN")
	exec("UPDATE phase8_research_candidates SET source_kind='SHOPIFY_LIVE',provider_product_id='shopify:test',identity_key='test',locator_kind='MERCHANT_VARIANT',product_url=NULL,variant_id='variant',seller_domain='shop.example.com' WHERE user_id=$1 AND curation_id=$2 AND candidate_id='candidate'", user, cur)

	source.state = "changed"
	_, err = service.ApplyCombinationCart(ctx, user, cur, thread.ID, command)
	reason(err, "COMBINATION_CHANGED")
	source.state = "initial"
	exec("UPDATE curation_budgets SET version=version+1 WHERE curation_id=$1", cur)
	_, err = service.ApplyCombinationCart(ctx, user, cur, thread.ID, command)
	reason(err, "COMBINATION_CHANGED")
	exec("UPDATE curation_budgets SET version=version-1 WHERE curation_id=$1", cur)
	if _, err = service.ApplyCombinationCart(ctx, user, "52000000-0000-4000-8000-000000000001", thread.ID, command); err == nil {
		t.Fatal("cross curation accepted")
	}
	result, err := service.ApplyCombinationCart(ctx, user, cur, thread.ID, command)
	if err != nil || result.Version != 1 || len(result.Items) != 1 || result.Items[0].Item.Quantity != 2 {
		t.Fatal(result, err)
	}
	replay, err := service.ApplyCombinationCart(ctx, user, cur, thread.ID, command)
	if err != nil || replay.Version != 1 || replay.Items[0].Item.Quantity != 2 {
		t.Fatal(replay, err)
	}
	command.Items[0].Item.Quantity = 3
	_, err = service.ApplyCombinationCart(ctx, user, cur, thread.ID, command)
	reason(err, "IDEMPOTENCY_KEY_REUSED")
	command.CommandID = ids.NewID()
	command.ExpectedVersion = 1
	_, err = service.ApplyCombinationCart(ctx, user, cur, thread.ID, command)
	reason(err, "PHASE8_CART_VERSION_CONFLICT")
	if _, err = service.CombinationStatus(ctx, "50000000-0000-4000-8000-000000000099", cur, thread.ID); err == nil {
		t.Fatal("cross user accepted")
	}
	current, err := cart.Get(ctx, user, cur)
	if err != nil || current.Version != 1 || current.Items[0].Item.Quantity != 2 {
		t.Fatal(current, err)
	}
	// A later representative need not belong to the initial AI combination,
	// and choosing its variant must not force another model response.
	exec("INSERT INTO phase8_research_candidates(user_id,curation_id,plan_target_id,candidate_id,provider_product_id,source_kind,identity_key,locator_kind,variant_id,seller_domain,display_order,first_seen_at,last_seen_at) VALUES($1,$2,$3,'replacement','shopify:replacement','SHOPIFY_LIVE','replacement','MERCHANT_VARIANT','new-variant','shop.example.com',1,now(),now())", user, cur, target)
	exec("INSERT INTO phase8_candidate_configurations(user_id,curation_id,plan_target_id,candidate_id,variant_id,selected_options,observed_at,updated_at) VALUES($1,$2,$3,'replacement','new-variant','[]',now(),now())", user, cur, target)
	replacement := item
	replacement.Item.CandidateID = "replacement"
	replacement.Item.VariantID = "new-variant"
	cmd := c.CombinationCartCommand{CommandID: ids.NewID(), Mode: "ADD", ExpectedVersion: current.Version, Items: []c.CatalogCartItemV2{replacement}}
	added, err := service.ApplyRepresentativeCart(ctx, user, cur, cmd)
	if err != nil || len(added.Items) != 2 || added.Version != 2 {
		t.Fatal(added, err)
	}
	replay, err = service.ApplyRepresentativeCart(ctx, user, cur, cmd)
	if err != nil || replay.Version != 2 || len(replay.Items) != 2 {
		t.Fatal(replay, err)
	}
	cmd.CommandID = ids.NewID()
	_, err = service.ApplyRepresentativeCart(ctx, user, cur, cmd)
	reason(err, "PHASE8_CART_VERSION_CONFLICT")
	cmd.ExpectedVersion = 2
	cmd.Items[0].Item.VariantID = "not-selected"
	_, err = service.ApplyRepresentativeCart(ctx, user, cur, cmd)
	reason(err, "COMBINATION_VARIANT_CHANGED")
	if _, err = service.ApplyRepresentativeCart(ctx, "50000000-0000-4000-8000-000000000099", cur, cmd); err == nil {
		t.Fatal("cross-user representative Cart accepted")
	}

}
