package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/shared/testdb"
)

func TestCatalogCandidatePoolCommandRepositoryV2(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	database := openCatalogPoolIntegrationDatabaseV2(t, ctx, databaseURL)
	seedCatalogPoolIntegrationTargetV2(t, ctx, database)
	repository := NewRepository(database)

	const (
		userID     = "98000000-0000-4000-8000-000000000001"
		curationID = "98000000-0000-4000-8000-000000000003"
		targetID   = "98000000-0000-4000-8000-000000000004"
	)
	now := time.Now().UTC()
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO phase8_research_pools(
			user_id,curation_id,plan_target_id,version,expand_ordinal,
			latest_mode,created_at,updated_at
		) VALUES ($1,$2,$3,1,0,'REPLACE',$4,$4)
	`, userID, curationID, targetID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO phase8_research_candidates(
			user_id,curation_id,plan_target_id,candidate_id,
			provider_product_id,source_kind,identity_key,locator_kind,
			product_url,visible,display_order,first_seen_at,last_seen_at
		) VALUES
			($1,$2,$3,'candidate-0','product-0','SHOPIFY_LIVE','identity-0',
			 'PRODUCT_URL','https://shop.example/products/0',true,0,$4,$4),
			($1,$2,$3,'candidate-1','product-1','SHOPIFY_LIVE','identity-1',
			 'PRODUCT_URL','https://shop.example/products/1',false,1,$4,$4)
	`, userID, curationID, targetID, now); err != nil {
		t.Fatal(err)
	}

	emptyReplace := catalogPoolIntegrationCommandV2(
		userID, curationID, targetID, "empty-replace", 1,
		researchapp.CatalogResearchReplaceV2, 'a',
	)
	budgetAmount := "100.00"
	maximum := int64(5000)
	emptyReplace.Budget = &researchapp.ResearchBudgetSnapshot{Budget: curationdomain.BudgetResearchSnapshot{SchemaVersion: curationdomain.BudgetSchema, Version: 3, Enabled: true, Currency: "USD", TargetID: targetID, Quantity: 2, Amount: &budgetAmount}, ProviderCurrency: "USD", MaximumUnitMinor: &maximum}
	preflight, err := repository.PreflightCatalogSearchV2(ctx, emptyReplace)
	if err != nil || preflight.Replay {
		t.Fatalf("empty replace preflight=%#v err=%v", preflight, err)
	}
	if len(preflight.FencingToken) != 64 || !preflight.LeaseExpiresAt.After(time.Now().UTC()) ||
		preflight.LeaseExpiresAt.After(time.Now().UTC().Add(3*time.Minute)) {
		t.Fatalf("empty replace reservation=%#v", preflight)
	}
	savedBudget, budgetErr := repository.ReadSearchBudgetSnapshot(ctx, userID, curationID, targetID, emptyReplace.IdempotencyKey)
	if budgetErr != nil || savedBudget == nil || *savedBudget.MaximumUnitMinor != 5000 || *savedBudget.Budget.Amount != "100.00" || savedBudget.Budget.Quantity != 2 {
		t.Fatalf("durable budget snapshot: %+v %v", savedBudget, budgetErr)
	}
	emptyReplace = catalogPoolCommandWithReservationV2(emptyReplace, preflight)
	completed, err := repository.CompleteCatalogSearchV2(
		ctx, emptyReplace, nil,
		researchapp.LiveCatalogReviewMetricsV2{
			CompletedAt: now.Add(time.Second), ShopifyCallCount: 1,
			LocalCallsRemaining: 7,
		},
	)
	if err != nil || completed.Version != 2 {
		t.Fatalf("empty replace completion=%#v err=%v", completed, err)
	}
	assertCatalogPoolVisibilityV2(t, ctx, database, userID, curationID, targetID, []bool{true, false})

	beforeReplay := catalogPoolMutationSnapshotV2(t, ctx, database, userID, curationID, targetID)
	replay, err := repository.PreflightCatalogSearchV2(ctx, emptyReplace)
	if err != nil || !replay.Replay || replay.Pool.Version != completed.Version {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	if fmt.Sprint(replay.Pool.AdmittedCandidateIDs) != fmt.Sprint([]string{"candidate-0"}) {
		t.Fatalf("lost-response replay projection=%v", replay.Pool.AdmittedCandidateIDs)
	}
	if replay.FencingToken != "" || !replay.LeaseExpiresAt.IsZero() {
		t.Fatalf("completed replay leaked reservation=%#v", replay)
	}
	afterReplay := catalogPoolMutationSnapshotV2(t, ctx, database, userID, curationID, targetID)
	if beforeReplay != afterReplay {
		t.Fatalf("completed replay mutated pool: before=%#v after=%#v", beforeReplay, afterReplay)
	}
	conflicting := emptyReplace
	conflicting.RequestHash = "0x" + strings.Repeat("b", 64)
	_, err = repository.PreflightCatalogSearchV2(ctx, conflicting)
	assertCatalogPoolFaultV2(
		t, err, fault.Conflict,
		researchapp.CatalogCandidatePoolIdempotencyConflictV2,
	)
	stale := catalogPoolIntegrationCommandV2(
		userID, curationID, targetID, "stale", 1,
		researchapp.CatalogResearchAppendV2, 'c',
	)
	_, err = repository.PreflightCatalogSearchV2(ctx, stale)
	assertCatalogPoolFaultV2(
		t, err, fault.Conflict,
		researchapp.CatalogCandidatePoolVersionConflictV2,
	)

	// A crashed provider read is reclaimable after its bounded lease. The new
	// preflight rotates the opaque fencing token, so the old provider response
	// and cleanup are both mutation-0 even though their semantic command is the
	// same.
	reclaimable := catalogPoolIntegrationCommandV2(
		userID, curationID, targetID, "reclaimable", 2,
		researchapp.CatalogResearchReplaceV2, '9',
	)
	firstLease, err := repository.PreflightCatalogSearchV2(ctx, reclaimable)
	if err != nil {
		t.Fatal(err)
	}
	lateCommand := catalogPoolCommandWithReservationV2(reclaimable, firstLease)
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE phase8_research_pool_commands
		SET created_at=CURRENT_TIMESTAMP - interval '5 minutes',
		    updated_at=CURRENT_TIMESTAMP - interval '5 minutes',
		    lease_expires_at=CURRENT_TIMESTAMP - interval '1 second'
		WHERE user_id=$1 AND idempotency_key=$2
	`, userID, reclaimable.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	secondLease, err := repository.PreflightCatalogSearchV2(ctx, reclaimable)
	if err != nil {
		t.Fatalf("expired command was not reclaimable: %v", err)
	}
	if secondLease.FencingToken == firstLease.FencingToken || len(secondLease.FencingToken) != 64 {
		t.Fatalf("reclaim did not rotate fencing token: first=%q second=%q",
			firstLease.FencingToken, secondLease.FencingToken)
	}
	beforeLateResult := catalogPoolMutationSnapshotV2(
		t, ctx, database, userID, curationID, targetID,
	)
	_, err = repository.CompleteCatalogSearchV2(
		ctx, lateCommand, nil,
		researchapp.LiveCatalogReviewMetricsV2{CompletedAt: now.Add(2 * time.Second)},
	)
	assertCatalogPoolFaultV2(
		t, err, fault.Conflict, researchapp.CatalogCandidatePoolReservationLostV2,
	)
	afterLateResult := catalogPoolMutationSnapshotV2(
		t, ctx, database, userID, curationID, targetID,
	)
	if beforeLateResult != afterLateResult {
		t.Fatalf("late fenced completion mutated pool: before=%#v after=%#v",
			beforeLateResult, afterLateResult)
	}
	if err := repository.AbortCatalogSearchV2(ctx, lateCommand); err == nil {
		t.Fatal("late fenced abort unexpectedly removed the reclaimed reservation")
	} else {
		assertCatalogPoolFaultV2(
			t, err, fault.Conflict, researchapp.CatalogCandidatePoolReservationLostV2,
		)
	}
	currentCommand := catalogPoolCommandWithReservationV2(reclaimable, secondLease)
	if err := repository.AbortCatalogSearchV2(ctx, currentCommand); err != nil {
		t.Fatalf("current reclaimed reservation abort: %v", err)
	}

	for index := 2; index < 50; index++ {
		if _, err := database.DB.ExecContext(ctx, `
			INSERT INTO phase8_research_candidates(
				user_id,curation_id,plan_target_id,candidate_id,
				provider_product_id,source_kind,identity_key,locator_kind,
				product_url,visible,display_order,first_seen_at,last_seen_at
			) VALUES ($1,$2,$3,$4,$5,'SHOPIFY_LIVE',$6,'PRODUCT_URL',$7,true,$8,$9,$9)
		`, userID, curationID, targetID, fmt.Sprintf("candidate-%d", index),
			fmt.Sprintf("product-%d", index), fmt.Sprintf("identity-%d", index),
			fmt.Sprintf("https://shop.example/products/%d", index), index, now); err != nil {
			t.Fatalf("seed candidate %d: %v", index, err)
		}
	}
	capacity := catalogPoolIntegrationCommandV2(
		userID, curationID, targetID, "capacity", 2,
		researchapp.CatalogResearchAppendV2, 'd',
	)
	_, err = repository.PreflightCatalogSearchV2(ctx, capacity)
	assertCatalogPoolFaultV2(
		t, err, fault.Conflict,
		researchapp.CatalogCandidatePoolCapacityReachedV2,
	)
	var capacityCommandCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM phase8_research_pool_commands
		WHERE user_id=$1 AND idempotency_key=$2
	`, userID, capacity.IdempotencyKey).Scan(&capacityCommandCount); err != nil {
		t.Fatal(err)
	}
	if capacityCommandCount != 0 {
		t.Fatalf("capacity preflight persisted commands=%d", capacityCommandCount)
	}

	first := catalogPoolIntegrationCommandV2(
		userID, curationID, targetID, "first-running", 2,
		researchapp.CatalogResearchReplaceV2, 'e',
	)
	second := catalogPoolIntegrationCommandV2(
		userID, curationID, targetID, "second-running", 2,
		researchapp.CatalogResearchReplaceV2, 'f',
	)
	firstPreflight, err := repository.PreflightCatalogSearchV2(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	first = catalogPoolCommandWithReservationV2(first, firstPreflight)
	_, err = repository.PreflightCatalogSearchV2(ctx, second)
	assertCatalogPoolFaultV2(
		t, err, fault.Conflict,
		researchapp.CatalogCandidatePoolSearchInProgressV2,
	)

	before := catalogPoolMutationSnapshotV2(t, ctx, database, userID, curationID, targetID)
	if err := repository.AbortCatalogSearchV2(ctx, first); err != nil {
		t.Fatal(err)
	}
	after := catalogPoolMutationSnapshotV2(t, ctx, database, userID, curationID, targetID)
	if before != after {
		t.Fatalf("provider-fault abort mutated pool: before=%#v after=%#v", before, after)
	}
	var running int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM phase8_research_pool_commands
		WHERE user_id=$1 AND idempotency_key=$2
	`, userID, first.IdempotencyKey).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if running != 0 {
		t.Fatalf("aborted reservation count=%d", running)
	}
	secondPreflight, err := repository.PreflightCatalogSearchV2(ctx, second)
	if err != nil {
		t.Fatalf("serialized command did not become available: %v", err)
	}
	second = catalogPoolCommandWithReservationV2(second, secondPreflight)
	if err := repository.AbortCatalogSearchV2(ctx, second); err != nil {
		t.Fatal(err)
	}

	if _, err := database.DB.ExecContext(ctx, `
		DELETE FROM phase8_research_candidates
		WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
		  AND identity_key='identity-49'
	`, userID, curationID, targetID); err != nil {
		t.Fatal(err)
	}
	concurrent := []researchapp.CatalogCandidatePoolCommandV2{
		catalogPoolIntegrationCommandV2(
			userID, curationID, targetID, "concurrent-expand", 2,
			researchapp.CatalogResearchAppendV2, '1',
		),
		catalogPoolIntegrationCommandV2(
			userID, curationID, targetID, "concurrent-research-again", 2,
			researchapp.CatalogResearchReplaceV2, '2',
		),
	}
	start := make(chan struct{})
	errorsByCommand := make([]error, len(concurrent))
	preflightsByCommand := make([]researchapp.CatalogCandidatePoolPreflightV2, len(concurrent))
	var wait sync.WaitGroup
	for index := range concurrent {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			preflightsByCommand[index], errorsByCommand[index] = repository.PreflightCatalogSearchV2(
				ctx, concurrent[index],
			)
		}(index)
	}
	close(start)
	wait.Wait()
	winner := -1
	losers := 0
	for index, preflightErr := range errorsByCommand {
		if preflightErr == nil {
			winner = index
			continue
		}
		failure, ok := fault.As(preflightErr)
		if ok && failure.Reason == researchapp.CatalogCandidatePoolSearchInProgressV2 {
			losers++
			continue
		}
		t.Fatalf("concurrent command %d unexpected err=%v", index, preflightErr)
	}
	if winner < 0 || losers != 1 {
		t.Fatalf("concurrent errors=%v winner=%d losers=%d", errorsByCommand, winner, losers)
	}
	concurrent[winner] = catalogPoolCommandWithReservationV2(
		concurrent[winner], preflightsByCommand[winner],
	)
	if err := repository.AbortCatalogSearchV2(ctx, concurrent[winner]); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO phase8_research_candidates(
			user_id,curation_id,plan_target_id,candidate_id,
			provider_product_id,source_kind,identity_key,locator_kind,
			product_url,visible,display_order,first_seen_at,last_seen_at
		) VALUES ($1,$2,$3,'candidate-49','product-49','SHOPIFY_LIVE',
		          'identity-49','PRODUCT_URL','https://shop.example/products/49',
		          true,49,$4,$4)
	`, userID, curationID, targetID, now); err != nil {
		t.Fatal(err)
	}
	var refreshedCandidateID string
	if err := database.DB.QueryRowContext(ctx, `
		INSERT INTO phase8_research_candidates(
			user_id,curation_id,plan_target_id,candidate_id,
			provider_product_id,source_kind,identity_key,locator_kind,
			product_url,visible,display_order,first_seen_at,last_seen_at
		) VALUES ($1,$2,$3,'different-id-for-existing-product','product-49',
		          'SHOPIFY_LIVE','shopify-product:product-49','PRODUCT_URL',
		          'https://shop.example/products/49-refreshed',true,999,$4,$4)
		ON CONFLICT (user_id,curation_id,plan_target_id,source_kind,provider_product_id)
		DO UPDATE SET identity_key=EXCLUDED.identity_key,
		              product_url=EXCLUDED.product_url,
		              last_seen_at=EXCLUDED.last_seen_at
		RETURNING candidate_id
	`, userID, curationID, targetID, now).Scan(&refreshedCandidateID); err != nil {
		t.Fatalf("refresh existing provider product at 50: %v", err)
	}
	if refreshedCandidateID != "candidate-49" {
		t.Fatalf("50-cap refresh changed Candidate ID=%q", refreshedCandidateID)
	}

	_, err = database.DB.ExecContext(ctx, `
		INSERT INTO phase8_research_candidates(
			user_id,curation_id,plan_target_id,candidate_id,
			provider_product_id,source_kind,identity_key,locator_kind,
			product_url,visible,display_order,first_seen_at,last_seen_at
		) VALUES ($1,$2,$3,'candidate-50','product-50','SHOPIFY_LIVE',
		          'identity-50','PRODUCT_URL','https://shop.example/products/50',
		          true,50,$4,$4)
	`, userID, curationID, targetID, now)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) ||
		postgresError.ConstraintName != "phase8_research_candidates_capacity_guard" {
		t.Fatalf("51st Candidate err=%v constraint=%v", err, postgresError)
	}
}

func TestCatalogCandidatePoolUsesProviderProductIdentityAndModeSpecificOrderV2(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	database := openCatalogPoolIntegrationDatabaseV2(t, ctx, databaseURL)
	seedCatalogPoolIntegrationTargetV2(t, ctx, database)
	repository := NewRepository(database)
	const (
		userID     = "98000000-0000-4000-8000-000000000001"
		curationID = "98000000-0000-4000-8000-000000000003"
		targetID   = "98000000-0000-4000-8000-000000000004"
	)
	now := time.Now().UTC()
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO phase8_research_pools(
			user_id,curation_id,plan_target_id,version,expand_ordinal,
			latest_mode,created_at,updated_at
		) VALUES ($1,$2,$3,1,0,'REPLACE',$4,$4)
	`, userID, curationID, targetID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO phase8_research_candidates(
			user_id,curation_id,plan_target_id,candidate_id,
			provider_product_id,source_kind,identity_key,locator_kind,
			product_url,visible,display_order,first_seen_at,last_seen_at
		) VALUES
			($1,$2,$3,'durable-a','product-a','SHOPIFY_LIVE','legacy-url-a',
			 'PRODUCT_URL','https://old.example/products/a',true,8,$4,$4),
			($1,$2,$3,'durable-b','product-b','SHOPIFY_LIVE','legacy-url-b',
			 'PRODUCT_URL','https://shop.example/products/b',true,2,$4,$4),
			($1,$2,$3,'retained-pinned-first','product-pinned-first','SHOPIFY_LIVE',
			 'shopify-product:product-pinned-first','PRODUCT_URL',
			 'https://shop.example/products/pinned-first',true,0,$4,$4),
			($1,$2,$3,'retained-pinned-second','product-pinned-second','SHOPIFY_LIVE',
			 'shopify-product:product-pinned-second','PRODUCT_URL',
			 'https://shop.example/products/pinned-second',true,5,$4,$4)
	`, userID, curationID, targetID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO phase8_variant_interactions(
			user_id,curation_id,plan_target_id,candidate_id,variant_id,
			pinned,sentiment,updated_at
		) VALUES
			($1,$2,$3,'retained-pinned-first','variant-pinned-first',true,'NONE',$4),
			($1,$2,$3,'retained-pinned-second','variant-pinned-second',true,'NONE',$4)
	`, userID, curationID, targetID, now); err != nil {
		t.Fatal(err)
	}

	replace := catalogPoolIntegrationCommandV2(
		userID, curationID, targetID, "provider-identity-replace", 1,
		researchapp.CatalogResearchReplaceV2, '6',
	)
	preflight, err := repository.PreflightCatalogSearchV2(ctx, replace)
	if err != nil {
		t.Fatal(err)
	}
	replace = catalogPoolCommandWithReservationV2(replace, preflight)
	replaced, err := repository.CompleteCatalogSearchV2(ctx, replace, []researchapp.CatalogCandidateReferenceV2{
		catalogCandidateReferenceForPoolTestV2(
			userID, curationID, targetID, "proposed-b", "product-b",
			"shopify-product:product-b", "https://shop.example/products/b-new", 0, now,
		),
		{
			UserID: userID, CurationID: curationID, PlanTargetID: targetID,
			CandidateID: "proposed-a", ProviderProductID: "product-a",
			SourceKind: "SHOPIFY_LIVE", IdentityKey: "shopify-product:product-a",
			Locator: researchapp.CatalogProductLocator{
				Kind: researchapp.CatalogLocatorMerchantVariant,
				MerchantVariant: &researchapp.CatalogMerchantVariantLocator{
					SellerDomain: "merchant.example", VariantID: "variant-a",
				},
			},
			DisplayOrder: 1, FirstSeenAt: now, LastSeenAt: now,
		},
	}, researchapp.LiveCatalogReviewMetricsV2{CompletedAt: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(replaced.AdmittedCandidateIDs) != fmt.Sprint([]string{"durable-b", "durable-a"}) {
		t.Fatalf("replace durable IDs=%v bindings=%v", replaced.AdmittedCandidateIDs, replaced.CandidateIDBindings)
	}
	assertCatalogCandidateIdentityOrderV2(t, ctx, database, userID, curationID, targetID, []string{
		"product-b:durable-b:0:PRODUCT_URL",
		"product-a:durable-a:1:PRODUCT_URL",
		"product-pinned-first:retained-pinned-first:2:PRODUCT_URL",
		"product-pinned-second:retained-pinned-second:3:PRODUCT_URL",
	})
	// Research Again skips every previously discovered identity, including
	// hidden products. Their locator, assessment and visibility remain unchanged.
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO phase8_research_candidates(
			user_id,curation_id,plan_target_id,candidate_id,
			provider_product_id,source_kind,identity_key,locator_kind,
			product_url,visible,display_order,first_seen_at,last_seen_at
		) VALUES (
			$1,$2,$3,'durable-hidden','product-hidden','SHOPIFY_LIVE',
			'shopify-product:product-hidden','PRODUCT_URL',
			'https://old.example/products/hidden',false,0,$4,$4
		)
	`, userID, curationID, targetID, now); err != nil {
		t.Fatal(err)
	}

	appendCommand := catalogPoolIntegrationCommandV2(
		userID, curationID, targetID, "provider-identity-append", 2,
		researchapp.CatalogResearchAppendV2, '7',
	)
	preflight, err = repository.PreflightCatalogSearchV2(ctx, appendCommand)
	if err != nil {
		t.Fatal(err)
	}
	appendCommand = catalogPoolCommandWithReservationV2(appendCommand, preflight)
	appended, err := repository.CompleteCatalogSearchV2(ctx, appendCommand, []researchapp.CatalogCandidateReferenceV2{
		catalogCandidateReferenceForPoolTestV2(
			userID, curationID, targetID, "another-proposed-a", "product-a",
			"shopify-product:product-a", "https://new.example/products/a", 0, now,
		),
		catalogCandidateReferenceForPoolTestV2(
			userID, curationID, targetID, "proposed-hidden", "product-hidden",
			"shopify-product:product-hidden", "https://new.example/products/hidden", 1, now,
		),
		catalogCandidateReferenceForPoolTestV2(
			userID, curationID, targetID, "durable-c", "product-c",
			"shopify-product:product-c", "https://shop.example/products/c", 2, now,
		),
	}, researchapp.LiveCatalogReviewMetricsV2{CompletedAt: now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(appended.AdmittedCandidateIDs) != fmt.Sprint([]string{
		"durable-c",
	}) {
		t.Fatalf("append durable IDs=%v bindings=%v", appended.AdmittedCandidateIDs, appended.CandidateIDBindings)
	}
	assertCatalogCandidateIdentityOrderV2(t, ctx, database, userID, curationID, targetID, []string{
		"product-b:durable-b:0:PRODUCT_URL",
		"product-hidden:durable-hidden:0:PRODUCT_URL",
		"product-a:durable-a:1:PRODUCT_URL",
		"product-pinned-first:retained-pinned-first:2:PRODUCT_URL",
		"product-pinned-second:retained-pinned-second:3:PRODUCT_URL",
		"product-c:durable-c:4:PRODUCT_URL",
	})
}

func catalogCandidateReferenceForPoolTestV2(
	userID, curationID, targetID, candidateID, providerProductID,
	identityKey, productURL string,
	displayOrder int,
	now time.Time,
) researchapp.CatalogCandidateReferenceV2 {
	return researchapp.CatalogCandidateReferenceV2{
		UserID: userID, CurationID: curationID, PlanTargetID: targetID,
		CandidateID: candidateID, ProviderProductID: providerProductID,
		SourceKind: "SHOPIFY_LIVE", IdentityKey: identityKey,
		Locator: researchapp.CatalogProductLocator{
			Kind: researchapp.CatalogLocatorProductURL,
			ProductURL: &researchapp.CatalogProductURLLocator{
				CanonicalURL: productURL,
			},
		},
		DisplayOrder: displayOrder, FirstSeenAt: now, LastSeenAt: now,
	}
}

func assertCatalogCandidateIdentityOrderV2(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	userID, curationID, targetID string,
	want []string,
) {
	t.Helper()
	rows, err := database.DB.QueryContext(ctx, `
		SELECT provider_product_id, candidate_id, display_order, locator_kind
		FROM phase8_research_candidates
		WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
		ORDER BY display_order, candidate_id
	`, userID, curationID, targetID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := []string{}
	for rows.Next() {
		var providerProductID, candidateID, locatorKind string
		var displayOrder int
		if err := rows.Scan(&providerProductID, &candidateID, &displayOrder, &locatorKind); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s:%s:%d:%s", providerProductID, candidateID, displayOrder, locatorKind))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("candidate identity/order=%v want=%v", got, want)
	}
}

func catalogPoolIntegrationCommandV2(
	userID, curationID, targetID, key string,
	expected int64,
	mode researchapp.CatalogResearchModeV2,
	hashDigit rune,
) researchapp.CatalogCandidatePoolCommandV2 {
	return researchapp.CatalogCandidatePoolCommandV2{
		UserID: userID, CurationID: curationID, TargetID: targetID,
		Mode: mode, ExpectedPoolVersion: expected, IdempotencyKey: key,
		RequestHash: "0x" + strings.Repeat(string(hashDigit), 64),
	}
}

func catalogPoolCommandWithReservationV2(
	command researchapp.CatalogCandidatePoolCommandV2,
	preflight researchapp.CatalogCandidatePoolPreflightV2,
) researchapp.CatalogCandidatePoolCommandV2 {
	command.FencingToken = preflight.FencingToken
	return command
}

func assertCatalogPoolFaultV2(
	t *testing.T,
	err error,
	code fault.Code,
	reason string,
) {
	t.Helper()
	failure, ok := fault.As(err)
	if !ok || failure.Code != code || failure.Reason != reason {
		t.Fatalf("fault=%v want code=%s reason=%s", err, code, reason)
	}
}

func assertCatalogPoolVisibilityV2(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	userID, curationID, targetID string,
	want []bool,
) {
	t.Helper()
	rows, err := database.DB.QueryContext(ctx, `
		SELECT visible FROM phase8_research_candidates
		WHERE user_id=$1 AND curation_id=$2 AND plan_target_id=$3
		ORDER BY display_order
	`, userID, curationID, targetID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := []bool{}
	for rows.Next() {
		var value bool
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		got = append(got, value)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("visibility=%v want=%v", got, want)
	}
}

type catalogPoolSnapshotV2 struct {
	Version         int64
	ExpandOrdinal   int
	CandidateCount  int
	VisibleCount    int
	VisibilityOrder string
}

func catalogPoolMutationSnapshotV2(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	userID, curationID, targetID string,
) catalogPoolSnapshotV2 {
	t.Helper()
	var result catalogPoolSnapshotV2
	if err := database.DB.QueryRowContext(ctx, `
		SELECT pool.version, pool.expand_ordinal,
		       count(candidate.candidate_id),
		       count(candidate.candidate_id) FILTER (WHERE candidate.visible),
		       COALESCE(string_agg(candidate.candidate_id || ':' || candidate.visible::text,
		                           ',' ORDER BY candidate.display_order), '')
		FROM phase8_research_pools pool
		LEFT JOIN phase8_research_candidates candidate
		  ON candidate.user_id=pool.user_id
		 AND candidate.curation_id=pool.curation_id
		 AND candidate.plan_target_id=pool.plan_target_id
		WHERE pool.user_id=$1 AND pool.curation_id=$2 AND pool.plan_target_id=$3
		GROUP BY pool.version, pool.expand_ordinal
	`, userID, curationID, targetID).Scan(
		&result.Version, &result.ExpandOrdinal, &result.CandidateCount,
		&result.VisibleCount, &result.VisibilityOrder,
	); err != nil {
		t.Fatal(err)
	}
	return result
}

func seedCatalogPoolIntegrationTargetV2(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
) {
	t.Helper()
	now := time.Now().UTC()
	statements := []string{
		`
		INSERT INTO users(id,status,created_at,updated_at)
		VALUES ('98000000-0000-4000-8000-000000000001','ACTIVE',$1,$1)
		`, `
		INSERT INTO shopping_plans(
			id,user_id,original_intent,plan_mode,execution_mode,
			budget_amount,budget_currency,country,city,created_at
		) VALUES (
			'98000000-0000-4000-8000-000000000002',
			'98000000-0000-4000-8000-000000000001',
			'test product','SINGLE','EXPERIMENT',100,'USD','US','',$1
		)
		`, `
		INSERT INTO curations(
			id,shopping_plan_id,user_id,phase,version,created_at,updated_at
		) VALUES (
			'98000000-0000-4000-8000-000000000003',
			'98000000-0000-4000-8000-000000000002',
			'98000000-0000-4000-8000-000000000001','CURATING',1,$1,$1
		)
		`, `
		INSERT INTO plan_targets(
			id,curation_id,user_id,plan_id,title,normalized_intent,category,
			allocated_amount,allocated_currency,country,city,url_mode,
			order_index,version,created_at,updated_at
		) VALUES (
			'98000000-0000-4000-8000-000000000004',
			'98000000-0000-4000-8000-000000000003',
			'98000000-0000-4000-8000-000000000001',
			'98000000-0000-4000-8000-000000000002',
			'test product','test product','test',100,'USD','US','','NONE',0,1,$1,$1
		)
		`,
	}
	for _, statement := range statements {
		if _, err := database.DB.ExecContext(ctx, statement, now); err != nil {
			t.Fatal(err)
		}
	}
}

func openCatalogPoolIntegrationDatabaseV2(
	t *testing.T,
	ctx context.Context,
	databaseURL string,
) *sharedpostgres.Database {
	t.Helper()
	if testdb.Enabled() {
		directory, err := filepath.Abs("../../../../../migrations")
		if err != nil {
			t.Fatal(err)
		}
		return testdb.Open(t, ctx, databaseURL, directory)
	}
	admin, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("vitlane_phase8_pool_%d", time.Now().UnixNano())
	if _, err := admin.DB.ExecContext(ctx, `CREATE DATABASE "`+databaseName+`" TEMPLATE template0`); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	isolated, err := sharedpostgres.Open(
		ctx, catalogPoolDatabaseURLV2(t, databaseURL, databaseName),
	)
	if err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = isolated.Close()
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, dropErr := admin.DB.ExecContext(
			cleanupContext, `DROP DATABASE "`+databaseName+`" WITH (FORCE)`,
		)
		if dropErr != nil {
			t.Errorf("drop integration database: %v", dropErr)
		}
		_ = admin.Close()
	})
	directory, err := filepath.Abs("../../../../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err := isolated.Migrate(ctx, directory); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}
	return isolated
}

func catalogPoolDatabaseURLV2(
	t *testing.T,
	databaseURL, databaseName string,
) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	return parsed.String()
}
