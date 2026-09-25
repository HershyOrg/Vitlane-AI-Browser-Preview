package testfixture

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/shared/testdb"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	UserID                    = "10101010-1010-4010-8010-101010101001"
	OperatorID                = "10101010-1010-4010-8010-101010101002"
	recoveryFundingOperatorID = "10101010-1010-4010-8010-101010101003"
	resultFundingOperatorID   = "10101010-1010-4010-8010-101010101004"
	ShippingID                = "20202020-2020-4020-8020-202020202001"
	SessionID                 = "30303030-3030-4030-8030-303030303001"
	SourceCart                = "30303030-3030-4030-8030-303030303002"
	OrderID                   = "40404040-4040-4040-8040-404040404001"
	PaymentID                 = "50505050-5050-4050-8050-505050505001"
	AttemptID                 = "60606060-6060-4060-8060-606060606001"
	AuthID                    = "70707070-7070-4070-8070-707070707001"
	Allocation                = "80808080-8080-4080-8080-808080808001"
	PositionID                = "90909090-9090-4090-8090-909090909001"
	ProfileHash               = "0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665"
)

func SeedOrder(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	now time.Time,
) {
	t.Helper()
	Exec(t, ctx, database, `
		INSERT INTO users(id,status,created_at,updated_at) VALUES
			($1,'ACTIVE',$3,$3),($2,'ACTIVE',$3,$3)
	`, UserID, OperatorID, now)
	Exec(t, ctx, database, `
		INSERT INTO shipping_snapshots(
			id,user_id,profile_version,country,masked_summary,encrypted_payload,
			payload_nonce,key_version,snapshot_hmac,created_at,source_kind
		) VALUES($1,$2,1,'US','F*** O**, US',decode('00','hex'),decode('00','hex'),
			1,'failed-funding-hmac',$3,'ORDER_SHEET_INPUT')
	`, ShippingID, UserID, now)
	Exec(t, ctx, database, `
		INSERT INTO agency_order_sheet_sessions(
			id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
			state,version,creation_key_hash,creation_request_hash,snapshot,
			created_at,expires_at,updated_at
		) VALUES($1,$2,$3,1,'failed-funding-cart','CONSUMED',1,
			'failed-funding-sheet-key','failed-funding-sheet-request','{}',$4,
			$4::timestamptz + interval '20 minutes',$4)
	`, SessionID, UserID, SourceCart, now)
	Exec(t, ctx, database, `
		INSERT INTO agency_orders(
			id,user_id,order_sheet_session_id,source_cart_id,source_cart_version,
			source_cart_snapshot_hash,shipping_snapshot_id,snapshot_hash,
			idempotency_key_hash,status,customer_payable_minor,currency,snapshot,
			payment_rail,provider_environment,asset,economic_effect,
			merchant_execution_mode,execution_profile_hash,issued_at,expires_at
		) VALUES($1,$2,$3,$4,1,'failed-funding-cart',$5,'failed-funding-order-hash',
			'failed-funding-order-key','ISSUED',2138,'USD',jsonb_build_object(
				'lines',jsonb_build_array(jsonb_build_object(
					'lineId','line-1','quantity',1)),
				'merchantCheckouts',jsonb_build_array(jsonb_build_object(
					'merchantId','merchant-sibling','shopDomain','sibling.example',
					'lineRefs',jsonb_build_array('line-1'),
					'taxTotal',jsonb_build_object('amountMinor',100,'currency','USD'),
					'continueUrlSafeRef','checkout-safe-ref',
					'deliveryGroups',jsonb_build_array(jsonb_build_object(
						'id','group-1','lineRefs',jsonb_build_array('line-1'),
						'selectedOptionRef','standard','options',jsonb_build_array(
							jsonb_build_object('id','standard','title','Standard',
								'amountMinor',500,'currency','USD')))),
					'authoritativeTotal',jsonb_build_object(
						'amountMinor',2000,'currency','USD'))),
				'paymentSelection',jsonb_build_object(
					'rail','PAYPAL','providerEnvironment','SANDBOX','asset','USD',
					'economicEffect','NO_REAL_VALUE',
					'merchantExecution','SIMULATED_NO_EFFECT')
			),'PAYPAL','SANDBOX','USD','NO_REAL_VALUE','SIMULATED_NO_EFFECT',$6,
			$7,$7::timestamptz + interval '20 minutes')
	`, OrderID, UserID, SessionID,
		SourceCart, ShippingID, ProfileHash, now)
	Exec(t, ctx, database, `
		INSERT INTO agency_order_procurement_authorizations(
			agency_order_id,user_id,order_sheet_session_id,authorization_kind,
			authorization_hash,execution_profile_hash,source_cart_snapshot_hash,
			displayed_snapshot_hash,order_snapshot_hash,locale,copy_version,
			accepted_at,payload,created_at
		) VALUES($1::uuid,$2::uuid,$3::uuid,'MANUAL_OPERATOR_PURCHASE',$4,$5,
			'failed-funding-cart','failed-funding-display','failed-funding-order-hash',
			'en-US','procurement-authorization.v1',$6,jsonb_build_object(
				'kind','MANUAL_OPERATOR_PURCHASE',
				'authorizationHash',$4::text,
				'executionProfileHash',$5::text,
				'sourceCartSnapshotHash','failed-funding-cart',
				'displayedSnapshotHash','failed-funding-display',
				'orderSheetSessionId',($3::uuid)::text,
				'acceptedAt',to_jsonb($6::timestamptz),
				'shops',jsonb_build_array(jsonb_build_object(
					'shopDomain','sibling.example')),
				'approvedPassThrough',jsonb_build_object(
					'amountMinor',2000,'currency','USD'),
				'approvedAgencyFee',jsonb_build_object(
					'amountMinor',138,'currency','USD'),
				'approvedCustomerPayable',jsonb_build_object(
					'amountMinor',2138,'currency','USD'),
				'customerApproval',jsonb_build_object(
					'agencyConsent',true,'privacyConsent',true,'locale','en-US',
					'copyVersion','procurement-authorization.v1')
			),$6)
	`, OrderID, UserID, SessionID,
		"0x"+strings.Repeat("ab", 32), ProfileHash, now)

	Exec(t, ctx, database, `
		INSERT INTO agency_order_mo_allocations(
			id,agency_order_id,checkout_ordinal,merchant_id,shop_domain,
			pass_through_minor,fee_variable_minor,fee_fixed_minor,fee_total_minor,
			customer_gross_minor,currency,fee_policy_version,allocation_hash,
			execution_profile_hash,created_at
		) VALUES($1,$2,1,'merchant-sibling','sibling.example',2000,108,30,138,
			2138,'USD','PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1',
			'0x'||repeat('cd',32),$3,$4)
	`, Allocation, OrderID, ProfileHash, now)
	Exec(t, ctx, database, `
		INSERT INTO payment_customer_payments(
			id,agency_order_id,user_id,rail,provider_environment,asset,economic_effect,
			amount_minor,currency,state,merchant_execution_mode,execution_profile_hash,
			version,created_at,updated_at
		) VALUES($1,$2,$3,'PAYPAL','SANDBOX','USD','NO_REAL_VALUE',2138,'USD',
			'AUTHORIZED','SIMULATED_NO_EFFECT',$4,1,$5,$5)
	`, PaymentID, OrderID, UserID,
		ProfileHash, now)
	Exec(t, ctx, database, `
		INSERT INTO payment_paypal_attempts(
			id,customer_payment_id,sequence,state,paypal_order_id,return_nonce,
			version,created_at,updated_at
		) VALUES($1,$2,1,'AUTHORIZE_COMPLETED','PAYPAL-ORDER-FAILED-SIBLING',
			'failed-funding-return-nonce',1,$3,$3)
	`, AttemptID, PaymentID, now)
	Exec(t, ctx, database, `
		INSERT INTO payment_paypal_authorizations(
			id,customer_payment_id,agency_order_id,paypal_attempt_id,rail,
			provider_environment,paypal_order_id,payee_merchant_id,paypal_authorization_id,
			amount_minor,currency,execution_profile_hash,state,version,
			authorized_at,honor_refreshed_at,created_at,updated_at
		) VALUES($1,$2,$3,$4,'PAYPAL','SANDBOX','PAYPAL-ORDER-FAILED-SIBLING',
			'MERCHANT-1','PAYPAL-AUTH-FAILED-SIBLING',2138,'USD',$5,'AUTHORIZED',1,$6,$6,$6,$6)
	`, AuthID, PaymentID, OrderID,
		AttemptID, ProfileHash, now)
	Exec(t, ctx, database, `
		INSERT INTO payment_mo_funding_positions(
			id,allocation_id,agency_order_id,customer_payment_id,paypal_authorization_id,
			rail,source,provider_environment,amount_minor,currency,execution_profile_hash,
			state,version,available_at,created_at,updated_at
		) VALUES($1,$2,$3,$4,$5,'PAYPAL','PAYPAL_AUTHORIZATION','SANDBOX',2138,
			'USD',$6,'AVAILABLE',1,$7,$7,$7)
	`, PositionID, Allocation, OrderID,
		PaymentID, AuthID, ProfileHash, now)
}

func Exec(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	query string,
	args ...any,
) {
	t.Helper()
	if _, err := database.DB.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("seed failed funding fixture: %v\nquery: %s", err, query)
	}
}

func Open(
	t *testing.T,
	ctx context.Context,
	beforeReducer ...bool,
) *sharedpostgres.Database {
	t.Helper()
	if testdb.Enabled() && (len(beforeReducer) == 0 || !beforeReducer[0]) {
		base := os.Getenv("TEST_DATABASE_URL")
		if base == "" {
			t.Skip("TEST_DATABASE_URL is not set")
		}
		_, file, _, _ := runtime.Caller(0)
		return testdb.Open(t, ctx, base, filepath.Join(filepath.Dir(file), "../../../migrations"))
	}
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
	databaseName := "vitlane_reducer_" + hex.EncodeToString(suffix)
	if _, err := admin.DB.ExecContext(
		ctx, `CREATE DATABASE "`+databaseName+`" TEMPLATE template0`,
	); err != nil {
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
	database, err := sharedpostgres.Open(
		ctx, databaseURL[:slash+1]+databaseName+query,
	)
	if err != nil {
		t.Fatalf("open isolated database: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.WithoutCancel(ctx), 15*time.Second,
		)
		defer cleanupCancel()
		if _, err := admin.DB.ExecContext(
			cleanupCtx, `DROP DATABASE "`+databaseName+`" WITH (FORCE)`,
		); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
		_ = admin.Close()
	})
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test migration path")
	}
	migrations := filepath.Clean(filepath.Join(
		filepath.Dir(currentFile), "../../../migrations",
	))
	if len(beforeReducer) > 0 && beforeReducer[0] {
		subset := t.TempDir()
		files, err := filepath.Glob(filepath.Join(migrations, "*.up.sql"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if filepath.Base(file) >= "000099" {
				continue
			}
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(subset, filepath.Base(file)), body, 0600); err != nil {
				t.Fatal(err)
			}
		}
		migrations = subset
	}
	if err := database.Migrate(ctx, migrations); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}
	return database
}
