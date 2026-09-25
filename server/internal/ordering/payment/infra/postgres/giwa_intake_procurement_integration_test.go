package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"testing"
	"time"

	agencyorderpostgres "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/infra/postgres"
	procurementpostgres "github.com/vitlane/vitlane/server/internal/ordering/procurement/infra/postgres"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

const (
	giwaIntakeUserID      = "11111111-1111-4111-8111-11111111a001"
	giwaIntakeShippingID  = "22222222-2222-4222-8222-22222222a001"
	giwaIntakeSessionID   = "33333333-3333-4333-8333-33333333a001"
	giwaIntakeOrderID     = "44444444-4444-4444-8444-44444444a001"
	giwaIntakePaymentInst = "55555555-5555-4555-8555-55555555a001"
	giwaIntakeSettlement  = "66666666-6666-4666-8666-66666666a001"
	giwaIntakeAllocationA = "77777777-7777-4777-8777-77777777a001"
	giwaIntakeAllocationB = "77777777-7777-4777-8777-77777777a002"
	giwaIntakeProfileHash = "0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732"
)

func TestGIWAFinalizedIntakePlansProcurementAndProjectsExactMORefund(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	seedGIWAFinalizedOrderForPlanning(t, ctx, database, now)

	paymentRepository := NewRepository(database)
	count, err := paymentRepository.IntakeGIWAFinalized(ctx, now.Add(time.Minute))
	if err != nil || count != 1 {
		t.Fatalf("GIWA intake count=%d err=%v", count, err)
	}
	var customerPaymentID, paymentState string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT id::text,state FROM payment_customer_payments
		WHERE agency_order_id=$1 AND rail='GIWA'
	`, giwaIntakeOrderID).Scan(&customerPaymentID, &paymentState); err != nil {
		t.Fatal(err)
	}
	if paymentState != "CAPTURED" {
		t.Fatalf("GIWA intake payment state=%s want CAPTURED", paymentState)
	}

	var fundingRaw []byte
	if err := database.DB.QueryRowContext(ctx, `SELECT payload FROM order_process_events WHERE agency_order_id=$1 AND type=$2 ORDER BY id DESC LIMIT 1`, giwaIntakeOrderID, procmsg.EventCustomerFundingReady).Scan(&fundingRaw); err != nil {
		t.Fatal(err)
	}
	var observed procmsg.CustomerFundingReadyPayload
	if err := json.Unmarshal(fundingRaw, &observed); err != nil {
		t.Fatal(err)
	}
	proof := procmsg.PlanFromFundingPayload{CustomerPaymentID: customerPaymentID, Rail: "GIWA", AmountMinor: observed.AmountMinor, FundsReceiptID: observed.FundsReceiptID, Positions: observed.Positions}
	if err := procurementpostgres.NewRepository(database).PlanFromFunding(
		ctx, giwaIntakeOrderID, proof, now.Add(2*time.Minute),
	); err != nil {
		t.Fatalf("plan Procurement from CAPTURED GIWA payment: %v", err)
	}
	var manifestCount, merchantOrderCount, positionCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM procurement_manifests WHERE agency_order_id=$1),
		  (SELECT count(*) FROM merchant_orders WHERE agency_order_id=$1),
		  (SELECT count(*) FROM payment_mo_funding_positions
		   WHERE agency_order_id=$1 AND rail='GIWA' AND state='AVAILABLE')
	`, giwaIntakeOrderID).Scan(
		&manifestCount, &merchantOrderCount, &positionCount,
	); err != nil {
		t.Fatal(err)
	}
	if manifestCount != 1 || merchantOrderCount != 2 || positionCount != 2 {
		t.Fatalf("GIWA graph manifest=%d MOs=%d positions=%d",
			manifestCount, merchantOrderCount, positionCount)
	}

	var positionA string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT id::text FROM payment_mo_funding_positions WHERE allocation_id=$1
	`, giwaIntakeAllocationA).Scan(&positionA); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO payment_mo_compensations(
			id,allocation_id,funding_position_id,agency_order_id,customer_payment_id,
			rail,provider_environment,action,cause,state,amount_minor,currency,
			execution_profile_hash,provider_resource_id,idempotency_key,version,
			approved_at,completed_at,created_at,updated_at
		) VALUES(
			'88888888-8888-4888-8888-88888888a001',$1,$2,$3,$4,
			'GIWA','TESTNET','TVIT_REFUND','DELIVERY_EXCEPTION','SUCCEEDED',1010,'USD',
			$5,'0x'||repeat('ef',32),'giwa:exact-mo-a-refund',2,$6,$6,$6,$6
		)
	`, giwaIntakeAllocationA, positionA, giwaIntakeOrderID, customerPaymentID,
		giwaIntakeProfileHash, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	agencyRepository := agencyorderpostgres.NewRepository(database)
	projection, err := agencyRepository.GetProjection(ctx, giwaIntakeUserID, giwaIntakeOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Payment == nil || projection.Payment.Rail != "GIWA" ||
		projection.Payment.RefundedTotalCent != 1010 {
		t.Fatalf("CAPTURED GIWA refund projection=%+v", projection.Payment)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_customer_payments SET state='CLOSED',updated_at=$2 WHERE id=$1
	`, customerPaymentID, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	projection, err = agencyRepository.GetProjection(ctx, giwaIntakeUserID, giwaIntakeOrderID)
	if err != nil || projection.Payment == nil || projection.Payment.RefundedTotalCent != 1010 {
		t.Fatalf("CLOSED GIWA refund projection=%+v err=%v", projection.Payment, err)
	}
	intakeCount, err := paymentRepository.IntakeGIWAFinalized(ctx, now.Add(5*time.Minute))
	if err != nil || intakeCount != 0 {
		t.Fatalf("CLOSED GIWA payment was re-intaken: count=%d err=%v", intakeCount, err)
	}
	var paymentCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM payment_customer_payments WHERE agency_order_id=$1
	`, giwaIntakeOrderID).Scan(&paymentCount); err != nil {
		t.Fatal(err)
	}
	if paymentCount != 1 {
		t.Fatalf("GIWA intake duplicated customer payment: %d", paymentCount)
	}
}

func seedGIWAFinalizedOrderForPlanning(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	now time.Time,
) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed GIWA planning fixture: %v", err)
		}
	}
	snapshot := fmt.Sprintf(`{
		"id":%q,"status":"ISSUED",
		"lines":[
			{"lineId":"line-a","quantity":1},
			{"lineId":"line-b","quantity":2}
		],
		"merchantCheckouts":[
			{"merchantId":"merchant-a","shopDomain":"shop-a.example",
			 "lineRefs":["line-a"],"authoritativeTotal":{"amountMinor":1000,"currency":"USD"}},
			{"merchantId":"merchant-b","shopDomain":"shop-b.example",
			 "lineRefs":["line-b"],"authoritativeTotal":{"amountMinor":2000,"currency":"USD"}}
		],
		"issuedAt":%q,"expiresAt":%q
	}`, giwaIntakeOrderID, now.Format(time.RFC3339Nano),
		now.Add(20*time.Minute).Format(time.RFC3339Nano))
	instruction := fmt.Sprintf(`{
		"id":%q,"agencyOrderId":%q,"state":"PENDING",
		"createdAt":%q,"expiresAt":%q
	}`, giwaIntakePaymentInst, giwaIntakeOrderID,
		now.Format(time.RFC3339Nano), now.Add(20*time.Minute).Format(time.RFC3339Nano))

	exec(`INSERT INTO users(id,status,created_at,updated_at)
		VALUES($1,'ACTIVE',$2,$2)`, giwaIntakeUserID, now)
	exec(`INSERT INTO shipping_snapshots(
		id,user_id,profile_version,country,masked_summary,encrypted_payload,
		payload_nonce,key_version,snapshot_hmac,created_at,source_kind
	) VALUES($1,$2,1,'US','G*** U**, US',decode('00','hex'),decode('00','hex'),
		1,'hmac',$3,'ORDER_SHEET_INPUT')`, giwaIntakeShippingID, giwaIntakeUserID, now)
	exec(`INSERT INTO agency_order_sheet_sessions(
		id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
		state,version,creation_key_hash,creation_request_hash,snapshot,
		created_at,expires_at,updated_at
	) VALUES($1,$2,gen_random_uuid(),1,'giwa-cart','CONSUMED',1,
		'giwa-key','giwa-request','{}',$3,$3::timestamptz + interval '20 minutes',$3)`,
		giwaIntakeSessionID, giwaIntakeUserID, now)
	exec(`INSERT INTO agency_orders(
		id,user_id,order_sheet_session_id,source_cart_id,source_cart_version,
		source_cart_snapshot_hash,shipping_snapshot_id,snapshot_hash,idempotency_key_hash,
		status,customer_payable_minor,currency,snapshot,payment_rail,
		provider_environment,asset,economic_effect,merchant_execution_mode,
		execution_profile_hash,issued_at,expires_at
	) VALUES($1,$2,$3,gen_random_uuid(),1,'giwa-cart',$4,'giwa-order-hash',
		'giwa-idempotency','ISSUED',3030,'USD',$5::jsonb,'GIWA','TESTNET','TVITUSD',
		'NO_REAL_VALUE','SIMULATED_NO_EFFECT',$6,$7,$7::timestamptz + interval '20 minutes')`,
		giwaIntakeOrderID, giwaIntakeUserID, giwaIntakeSessionID,
		giwaIntakeShippingID, snapshot, giwaIntakeProfileHash, now)
	exec(`INSERT INTO agency_order_payment_instructions(
		id,agency_order_id,user_id,agency_order_snapshot_hash,amount_minor,currency,
		rail,asset,provider_environment,economic_effect,merchant_execution_mode,
		execution_profile_hash,state,payload,expires_at,created_at
	) VALUES($1,$2,$3,'giwa-order-hash',3030,'USD','GIWA','TVITUSD','TESTNET',
		'NO_REAL_VALUE','SIMULATED_NO_EFFECT',$4,'PENDING',$5::jsonb,
		$6::timestamptz + interval '20 minutes',$6)`, giwaIntakePaymentInst,
		giwaIntakeOrderID, giwaIntakeUserID, giwaIntakeProfileHash, instruction, now)
	exec(`INSERT INTO agency_order_mo_allocations(
		id,agency_order_id,checkout_ordinal,merchant_id,shop_domain,
		pass_through_minor,fee_variable_minor,fee_fixed_minor,fee_total_minor,
		customer_gross_minor,currency,fee_policy_version,allocation_hash,
		execution_profile_hash,created_at
	) VALUES
		($1,$3,1,'merchant-a','shop-a.example',1000,10,0,10,1010,'USD',
		 'TVITUSD_ORDER_PASS_THROUGH_100BPS_MO_ALLOC_V1','0x'||repeat('a1',32),$4,$5),
		($2,$3,2,'merchant-b','shop-b.example',2000,20,0,20,2020,'USD',
		 'TVITUSD_ORDER_PASS_THROUGH_100BPS_MO_ALLOC_V1','0x'||repeat('b2',32),$4,$5)`,
		giwaIntakeAllocationA, giwaIntakeAllocationB, giwaIntakeOrderID,
		giwaIntakeProfileHash, now)
	exec(`INSERT INTO settlement_payments(
		id,order_hash,chain_id,settlement_address,payer,amount_base_units,state,
		pay_tx_hash,agency_order_id,created_at,updated_at
	) VALUES($1,'0x'||repeat('ab',32),10,'0x'||repeat('11',20),
		'0x'||repeat('22',20),30300000,'FINALIZED','0x'||repeat('cd',32),$2,$3,$3)`,
		giwaIntakeSettlement, giwaIntakeOrderID, now)
}
