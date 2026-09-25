package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func TestCatalogVariantPreferenceIsAtomicInPostgres(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	admin, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("vitlane_phase8_preference_%d", time.Now().UnixNano())
	if _, err := admin.DB.ExecContext(ctx, "CREATE DATABASE "+databaseName+" TEMPLATE template0"); err != nil {
		_ = admin.Close()
		t.Fatalf("create isolated preference database: %v", err)
	}

	var isolated *sharedpostgres.Database
	t.Cleanup(func() {
		if isolated != nil {
			_ = isolated.Close()
		}
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := admin.DB.ExecContext(
			cleanupContext, "DROP DATABASE "+databaseName+" WITH (FORCE)",
		); err != nil {
			t.Errorf("drop isolated preference database: %v", err)
		}
		_ = admin.Close()
	})

	isolatedURL, err := catalogPreferenceDatabaseURL(databaseURL, databaseName)
	if err != nil {
		t.Fatal(err)
	}
	isolated, err = sharedpostgres.Open(ctx, isolatedURL)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := filepath.Abs("../../../../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err := isolated.Migrate(ctx, migrations); err != nil {
		t.Fatalf("migrate isolated preference database: %v", err)
	}
	seedCatalogPreferenceFixture(t, ctx, isolated)

	repository := NewRepository(isolated)
	now := time.Date(2026, 8, 13, 3, 4, 5, 0, time.UTC)
	interaction := researchapp.CatalogVariantInteractionV2{
		TargetID: catalogPreferenceTargetID, CandidateID: catalogPreferenceCandidateID,
		VariantID: catalogPreferenceVariantID, Pinned: true,
		Sentiment: "LIKE", UpdatedAt: now,
	}
	liked := researchdomain.LikedVariantV2{
		UserID: catalogPreferenceUserID, CurationID: catalogPreferenceCurationID,
		CandidateID: catalogPreferenceCandidateID, VariantID: catalogPreferenceVariantID,
		ProductTitle: "Commuter Pack", VariantTitle: "Black / Small",
		ProductURL: "https://shop.example/products/commuter-pack?variant=1",
		Merchant:   "Shop Example", PriceMinor: 8200, Currency: "USD",
		TargetTitle: "Commuter backpack", UpdatedAt: now,
	}
	if err := repository.SaveCatalogVariantPreferenceV2(
		ctx, catalogPreferenceUserID, catalogPreferenceCurationID, interaction, &liked,
	); err != nil {
		t.Fatal(err)
	}
	assertCatalogPreferenceState(t, ctx, isolated, "LIKE", true)

	interaction.Sentiment = "NONE"
	interaction.UpdatedAt = now.Add(time.Second)
	if err := repository.SaveCatalogVariantPreferenceV2(
		ctx, catalogPreferenceUserID, catalogPreferenceCurationID, interaction, nil,
	); err != nil {
		t.Fatal(err)
	}
	assertCatalogPreferenceState(t, ctx, isolated, "NONE", false)

	interaction.Sentiment = "LIKE"
	interaction.UpdatedAt = now.Add(2 * time.Second)
	if err := repository.SaveCatalogVariantPreferenceV2(
		ctx, catalogPreferenceUserID, catalogPreferenceCurationID, interaction, nil,
	); !errors.Is(err, researchdomain.ErrLikedVariantInvalid) {
		t.Fatalf("LIKE without liked snapshot error=%v", err)
	}
	assertCatalogPreferenceState(t, ctx, isolated, "NONE", false)

	// The interaction statement executes first. This invalid currency fails the
	// second statement's DB constraint and must roll the interaction back.
	invalid := liked
	invalid.Currency = "US"
	invalid.UpdatedAt = interaction.UpdatedAt
	if err := repository.SaveCatalogVariantPreferenceV2(
		ctx, catalogPreferenceUserID, catalogPreferenceCurationID, interaction, &invalid,
	); err == nil {
		t.Fatal("invalid liked snapshot unexpectedly committed")
	}
	assertCatalogPreferenceState(t, ctx, isolated, "NONE", false)
}

const (
	catalogPreferenceUserID      = "00000000-0000-4000-8000-000000000101"
	catalogPreferencePlanID      = "00000000-0000-4000-8000-000000000102"
	catalogPreferenceCurationID  = "00000000-0000-4000-8000-000000000103"
	catalogPreferenceTargetID    = "00000000-0000-4000-8000-000000000104"
	catalogPreferenceCandidateID = "phase8-preference-candidate"
	catalogPreferenceVariantID   = "gid://shopify/ProductVariant/1"
)

func seedCatalogPreferenceFixture(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,status,created_at,updated_at)
		  VALUES ($1,'ACTIVE',now(),now())`, []any{catalogPreferenceUserID}},
		{`INSERT INTO shopping_plans(
			id,user_id,original_intent,plan_mode,execution_mode,
			budget_amount,budget_currency,country,city,
			agent_mode,model_key,created_at
		  ) VALUES ($1,$2,'Find a commuter pack','SINGLE','EXPERIMENT',
			200,'USD','US','Seattle','MANAGED','fixture',now())`,
			[]any{catalogPreferencePlanID, catalogPreferenceUserID}},
		{`INSERT INTO curations(
			id,shopping_plan_id,user_id,phase,version,created_at,updated_at
		  ) VALUES ($1,$2,$3,'CURATING',1,now(),now())`,
			[]any{catalogPreferenceCurationID, catalogPreferencePlanID, catalogPreferenceUserID}},
		{`INSERT INTO plan_targets(
			id,plan_id,curation_id,user_id,title,normalized_intent,category,
			allocated_amount,allocated_currency,country,city,url_mode,order_index,
			confirmed_at,target_hash,target_hash_schema,version,created_at,updated_at
		  ) VALUES ($1,$2,$3,$4,'Commuter backpack','light commuter pack','bags',
			200,'USD','US','Seattle','NONE',0,now(),'target-hash',
			'vitlane.plan-target.v1',1,now(),now())`,
			[]any{catalogPreferenceTargetID, catalogPreferencePlanID,
				catalogPreferenceCurationID, catalogPreferenceUserID}},
		{`INSERT INTO phase8_research_pools(
			user_id,curation_id,plan_target_id,version,expand_ordinal,
			created_at,updated_at
		  ) VALUES ($1,$2,$3,1,0,now(),now())`,
			[]any{catalogPreferenceUserID, catalogPreferenceCurationID, catalogPreferenceTargetID}},
		{`INSERT INTO phase8_research_candidates(
			user_id,curation_id,plan_target_id,candidate_id,
			provider_product_id,source_kind,identity_key,locator_kind,product_url,
			visible,display_order,first_seen_at,last_seen_at
		  ) VALUES ($1,$2,$3,$4,'shopify-product-1','SHOPIFY_LIVE',
			'url:https://shop.example/products/commuter-pack','PRODUCT_URL',
			'https://shop.example/products/commuter-pack',true,0,now(),now())`,
			[]any{catalogPreferenceUserID, catalogPreferenceCurationID,
				catalogPreferenceTargetID, catalogPreferenceCandidateID}},
	}
	for _, statement := range statements {
		if _, err := database.DB.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed atomic preference fixture: %v", err)
		}
	}
}

func assertCatalogPreferenceState(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	wantSentiment string,
	wantLiked bool,
) {
	t.Helper()
	var sentiment string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT sentiment
		FROM phase8_variant_interactions
		WHERE user_id=$1 AND curation_id=$2 AND candidate_id=$3 AND variant_id=$4
	`, catalogPreferenceUserID, catalogPreferenceCurationID,
		catalogPreferenceCandidateID, catalogPreferenceVariantID).Scan(&sentiment); err != nil {
		t.Fatal(err)
	}
	var likedCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*)
		FROM phase8_liked_variants
		WHERE user_id=$1 AND curation_id=$2 AND candidate_id=$3 AND variant_id=$4
	`, catalogPreferenceUserID, catalogPreferenceCurationID,
		catalogPreferenceCandidateID, catalogPreferenceVariantID).Scan(&likedCount); err != nil {
		t.Fatal(err)
	}
	if sentiment != wantSentiment || (likedCount == 1) != wantLiked {
		t.Fatalf("sentiment=%s likedCount=%d want sentiment=%s liked=%t",
			sentiment, likedCount, wantSentiment, wantLiked)
	}
}

func catalogPreferenceDatabaseURL(base, databaseName string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", fmt.Errorf("TEST_DATABASE_URL must be a PostgreSQL URL")
	}
	if strings.ContainsAny(databaseName, `"' /\\`) {
		return "", fmt.Errorf("unsafe isolated database name")
	}
	parsed.Path = "/" + databaseName
	return parsed.String(), nil
}
