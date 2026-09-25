package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func TestPayPalResourceAdoptionProjectionExecutesAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	database := openIsolatedOperatorDatabase(t, ctx)
	repository := NewRepository(database)

	items, err := repository.ListPayPalResourceAdoptions(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("list PayPal resource adoption candidates: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("fresh database returned %d adoption candidates", len(items))
	}
	count, err := repository.CountPayPalResourceAdoptions(ctx, time.Now())
	if err != nil {
		t.Fatalf("count PayPal resource adoption candidates: %v", err)
	}
	if count != 0 {
		t.Fatalf("fresh database adoption count=%d", count)
	}
	liveOrderCount, err := repository.CountLivePayPalOrders(ctx)
	if err != nil {
		t.Fatalf("count LIVE PayPal orders: %v", err)
	}
	if liveOrderCount != 0 {
		t.Fatalf("fresh database LIVE PayPal order count=%d", liveOrderCount)
	}
}

func TestCountLivePayPalOrdersRequiresCustomerAuthorization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	database := openIsolatedOperatorDatabase(t, ctx)
	repository := NewRepository(database)
	now := time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)
	seedLivePayPalOrderCountFixture(t, ctx, database, now)

	count, err := repository.CountLivePayPalOrders(ctx)
	if err != nil {
		t.Fatalf("count profile-only LIVE PayPal orders: %v", err)
	}
	if count != 0 {
		t.Fatalf("LIVE profile-only order count=%d, want 0", count)
	}

	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_paypal_attempts
		SET state='PAYER_ACTION_REQUIRED',paypal_order_id='LIVE-PAYPAL-ORDER-1',
		    version=version+1,updated_at=$2
		WHERE id=$1
	`, "66666666-6666-4666-8666-666666661001", now.Add(time.Minute)); err != nil {
		t.Fatalf("store LIVE PayPal provider order: %v", err)
	}
	count, err = repository.CountLivePayPalOrders(ctx)
	if err != nil || count != 0 {
		t.Fatalf("payer-action LIVE PayPal order count=%d err=%v, want 0", count, err)
	}

	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO payment_paypal_authorizations(
			id,customer_payment_id,agency_order_id,paypal_attempt_id,rail,
			provider_environment,paypal_order_id,payee_merchant_id,paypal_authorization_id,
			amount_minor,currency,execution_profile_hash,state,version,
			authorized_at,honor_refreshed_at,created_at,updated_at
		) VALUES(
			'77777777-7777-4777-8777-777777771001',$1,$2,$3,'PAYPAL','LIVE',
			'LIVE-PAYPAL-ORDER-1','MERCHANT-LIVE-1','LIVE-PAYPAL-AUTHORIZATION-1',
			1000,'USD',$4,'AUTHORIZED',1,$5,$5,$5,$5
		)
	`, "55555555-5555-4555-8555-555555551001",
		"44444444-4444-4444-8444-444444441001",
		"66666666-6666-4666-8666-666666661001",
		"0xba51a8eb9a32c1c6ede94a0ad7b8eb75b81ab1a1028dd7c536219895c37f096a",
		now.Add(2*time.Minute)); err != nil {
		t.Fatalf("store LIVE PayPal customer authorization: %v", err)
	}
	count, err = repository.CountLivePayPalOrders(ctx)
	if err != nil || count != 1 {
		t.Fatalf("authorized LIVE PayPal order count=%d err=%v, want 1", count, err)
	}

	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_paypal_authorizations
		SET state='VOIDED',terminal_at=$2,version=version+1,updated_at=$2
		WHERE id=$1
	`, "77777777-7777-4777-8777-777777771001", now.Add(3*time.Minute)); err != nil {
		t.Fatalf("move LIVE PayPal authorization to terminal state: %v", err)
	}
	count, err = repository.CountLivePayPalOrders(ctx)
	if err != nil || count != 1 {
		t.Fatalf("terminal LIVE PayPal authorization count=%d err=%v, want 1", count, err)
	}
}

func seedLivePayPalOrderCountFixture(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	now time.Time,
) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed LIVE PayPal count fixture: %v", err)
		}
	}
	const (
		userID      = "11111111-1111-4111-8111-111111111001"
		shippingID  = "22222222-2222-4222-8222-222222221001"
		liveSession = "33333333-3333-4333-8333-333333331001"
		liveOrder   = "44444444-4444-4444-8444-444444441001"
		livePayment = "55555555-5555-4555-8555-555555551001"
		liveAttempt = "66666666-6666-4666-8666-666666661001"
		liveHash    = "0xba51a8eb9a32c1c6ede94a0ad7b8eb75b81ab1a1028dd7c536219895c37f096a"
	)
	exec(`INSERT INTO users(id,status,created_at,updated_at)
		VALUES($1,'ACTIVE',$2,$2)`, userID, now)
	exec(`INSERT INTO shipping_snapshots(
		id,user_id,profile_version,country,masked_summary,encrypted_payload,
		payload_nonce,key_version,snapshot_hmac,created_at,source_kind
	) VALUES($1,$2,1,'KR','S***, KR',decode('00','hex'),decode('00','hex'),
		1,'live-count-hmac',$3,'ORDER_SHEET_INPUT')`, shippingID, userID, now)
	exec(`INSERT INTO agency_order_sheet_sessions(
		id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
		state,version,creation_key_hash,creation_request_hash,snapshot,
		created_at,expires_at,updated_at
	) VALUES($1,$2,gen_random_uuid(),1,'live-count-cart','CONSUMED',1,
		'live-count-key','live-count-request','{}',$3,
		$3::timestamptz + interval '20 minutes',$3)`, liveSession, userID, now)
	exec(`INSERT INTO agency_orders(
		id,user_id,order_sheet_session_id,source_cart_id,source_cart_version,
		source_cart_snapshot_hash,shipping_snapshot_id,snapshot_hash,idempotency_key_hash,
		status,customer_payable_minor,currency,snapshot,payment_rail,
		provider_environment,asset,economic_effect,merchant_execution_mode,
		execution_profile_hash,issued_at,expires_at
	) VALUES(
		$1,$2,$3,gen_random_uuid(),1,'live-count-cart',$4,'live-count-order-hash',
		 'live-count-idempotency','ISSUED',1000,'USD','{}','PAYPAL','LIVE','USD',
		 'REAL_MONEY','LIVE_MERCHANT_EFFECT',$5,$6,$6::timestamptz + interval '20 minutes'
	)`, liveOrder, userID, liveSession, shippingID, liveHash, now)
	exec(`INSERT INTO payment_customer_payments(
		id,agency_order_id,user_id,rail,provider_environment,asset,economic_effect,
		amount_minor,currency,state,merchant_execution_mode,execution_profile_hash,
		version,created_at,updated_at
	) VALUES($1,$2,$3,'PAYPAL','LIVE','USD','REAL_MONEY',1000,'USD','CREATED',
		'LIVE_MERCHANT_EFFECT',$4,1,$5,$5)`,
		livePayment, liveOrder, userID, liveHash, now)
	exec(`INSERT INTO payment_paypal_attempts(
		id,customer_payment_id,sequence,state,paypal_order_id,return_nonce,
		version,created_at,updated_at
	) VALUES($1,$2,1,'ORDER_PREPARED',NULL,'live-count-return',1,$3,$3)`,
		liveAttempt, livePayment, now)
}

func openIsolatedOperatorDatabase(
	t *testing.T,
	ctx context.Context,
) *sharedpostgres.Database {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	admin, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	databaseName := "vitlane_operator_adoption_" + hex.EncodeToString(suffix)
	if _, err := admin.DB.ExecContext(ctx,
		`CREATE DATABASE "`+databaseName+`" TEMPLATE template0`); err != nil {
		_ = admin.Close()
		t.Fatalf("create isolated database: %v", err)
	}
	slash := strings.LastIndexByte(databaseURL, '/')
	if slash < 0 {
		t.Fatal("unexpected database URL shape")
	}
	rest := databaseURL[slash+1:]
	query := ""
	if index := strings.IndexByte(rest, '?'); index >= 0 {
		query = rest[index:]
	}
	database, err := sharedpostgres.Open(ctx, databaseURL[:slash+1]+databaseName+query)
	if err != nil {
		t.Fatalf("open isolated database: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := admin.DB.ExecContext(cleanupCtx,
			`DROP DATABASE "`+databaseName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
		_ = admin.Close()
	})
	if err := database.Migrate(ctx, operatorMigrationDirectory(t)); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}
	return database
}

func operatorMigrationDirectory(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve operator integration test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../../../migrations"))
}
