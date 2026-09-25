package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	paymentdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	settlementapp "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/app"
	settlementdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/giwa/domain"
	paymentpostgres "github.com/vitlane/vitlane/server/internal/ordering/payment/infra/postgres"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

const (
	giwaTestUserID         = "11111111-1111-1111-1111-11111111f001"
	giwaTestShippingID     = "12121212-1212-1212-1212-12121212f001"
	giwaTestSessionID      = "22222222-2222-2222-2222-22222222f001"
	giwaTestOrderID        = "33333333-3333-3333-3333-33333333f001"
	giwaTestSettlementID   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaf001"
	giwaTestCustomerPayID  = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbf001"
	giwaTestFundsReceiptID = "cccccccc-cccc-cccc-cccc-ccccccccf001"
	giwaTestManifestID     = "dddddddd-dddd-dddd-dddd-ddddddddf001"
	giwaTestProfileHash    = "0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732"
	giwaTestOrderHash      = "0xabababababababababababababababababababababababababababababababab"
	giwaTestSettlementAddr = "0x1111111111111111111111111111111111111111"
)

type giwaCompensationFixture struct {
	database         *sharedpostgres.Database
	now              time.Time
	allocationIDs    []string
	fundingIDs       []string
	merchantOrderIDs []string
	compensationIDs  []string
	passMinor        []int64
	feeMinor         []int64
}

// One approved TVIT_REFUND must become one deterministic contract command for
// the entire immutable MO gross. A late canonical event may recover an unknown
// observation, and only succeeded compensations close the aggregate.
func TestEnsureCommandsPlansAndFinalizesWholeMOCompensations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	fixture := seedGIWACompensationGraph(t, ctx, 2)
	repository := NewRepository(fixture.database)

	for attempt := 0; attempt < 2; attempt++ {
		if err := repository.EnsureCommands(ctx,
			"0x1111111111111111111111111111111111111111",
			"0x2222222222222222222222222222222222222222",
			fixture.now,
		); err != nil {
			t.Fatalf("ensure commands attempt %d: %v", attempt+1, err)
		}
	}

	for index, compensationID := range fixture.compensationIDs {
		assertMOCommand(t, ctx, fixture, index)
		assertCompensationProjection(t, ctx, fixture.database.DB, compensationID,
			"EXECUTION_PENDING", "RELEASE_PENDING")
	}

	unknownTx := "0x" + strings.Repeat("1", 64)
	markMOCommandBroadcast(t, ctx, fixture.database.DB,
		fixture.compensationIDs[0], 1, unknownTx, fixture.now.Add(time.Minute))
	if err := repository.MarkTransactionObservationUnknown(ctx,
		settlementapp.ReconcileItem{
			Payment: settlementdomain.Payment{
				ID: giwaTestSettlementID, AgencyOrderID: giwaTestOrderID, ChainID: 10,
			},
			TxHash: unknownTx, Purpose: settlementapp.PurposeRefundPartial,
		},
		settlementapp.ReasonTransactionObservationExpired,
		fixture.now.Add(2*time.Minute),
	); err != nil {
		t.Fatalf("mark compensation observation unknown: %v", err)
	}
	assertCompensationProjection(t, ctx, fixture.database.DB,
		fixture.compensationIDs[0], "OUTCOME_UNKNOWN", "RELEASE_UNKNOWN")
	assertLatestCompensationEvent(t, ctx, fixture.database.DB,
		fixture.compensationIDs[0], "OUTCOME_UNKNOWN", 2, 0, false)

	projectMORefundFinality(t, ctx, repository, unknownTx, 101,
		fixture.now.Add(3*time.Minute))
	assertCompensationProjection(t, ctx, fixture.database.DB,
		fixture.compensationIDs[0], "SUCCEEDED", "RELEASED")
	assertLatestCompensationEvent(t, ctx, fixture.database.DB,
		fixture.compensationIDs[0], "SUCCEEDED", 1, 1, false)

	secondTx := "0x" + strings.Repeat("2", 64)
	markMOCommandBroadcast(t, ctx, fixture.database.DB,
		fixture.compensationIDs[1], 2, secondTx, fixture.now.Add(4*time.Minute))
	projectMORefundFinality(t, ctx, repository, secondTx, 102,
		fixture.now.Add(5*time.Minute))
	assertCompensationProjection(t, ctx, fixture.database.DB,
		fixture.compensationIDs[1], "SUCCEEDED", "RELEASED")
	assertLatestCompensationEvent(t, ctx, fixture.database.DB,
		fixture.compensationIDs[1], "SUCCEEDED", 0, 2, true)

	var settlementState string
	if err := fixture.database.DB.QueryRowContext(ctx,
		`SELECT state FROM settlement_payments WHERE id=$1`, giwaTestSettlementID,
	).Scan(&settlementState); err != nil {
		t.Fatal(err)
	}
	if settlementState != "FINALIZED" {
		t.Fatalf("MO compensation changed order settlement state to %s", settlementState)
	}
}

func TestRevertedMOCompensationCanBeExplicitlyRearmedAndFinalized(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	fixture := seedGIWACompensationGraph(t, ctx, 1)
	repository := NewRepository(fixture.database)
	if err := repository.EnsureCommands(ctx,
		"0x1111111111111111111111111111111111111111",
		"0x2222222222222222222222222222222222222222",
		fixture.now,
	); err != nil {
		t.Fatal(err)
	}

	txHash := "0x" + strings.Repeat("3", 64)
	markMOCommandBroadcast(t, ctx, fixture.database.DB,
		fixture.compensationIDs[0], 3, txHash, fixture.now.Add(time.Minute))
	if err := repository.MarkTransactionFailed(ctx,
		settlementapp.ReconcileItem{
			Payment: settlementdomain.Payment{
				ID: giwaTestSettlementID, AgencyOrderID: giwaTestOrderID, ChainID: 10,
			},
			TxHash: txHash, Purpose: settlementapp.PurposeRefundPartial,
		},
		settlementapp.ReasonTransactionReverted,
		fixture.now.Add(2*time.Minute),
	); err != nil {
		t.Fatalf("mark reverted MO compensation: %v", err)
	}

	assertCompensationProjection(t, ctx, fixture.database.DB,
		fixture.compensationIDs[0], "FAILED", "FAILED")
	assertLatestCompensationEvent(t, ctx, fixture.database.DB,
		fixture.compensationIDs[0], "FAILED", 1, 0, false)
	var completed bool
	if err := fixture.database.DB.QueryRowContext(ctx, `
		SELECT completed_at IS NOT NULL
		FROM payment_mo_compensations WHERE id=$1
	`, fixture.compensationIDs[0]).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("definitive failed compensation must have a completion timestamp")
	}

	rearmed, replay, err := paymentpostgres.NewRepository(fixture.database).
		PrepareMOCompensation(ctx, paymentapp.MOCompensationRequest{
			AgencyOrderID:   giwaTestOrderID,
			MerchantOrderID: fixture.merchantOrderIDs[0],
			AllocationID:    fixture.allocationIDs[0],
			Cause:           paymentdomain.MOCompensationCustomerCancelPreEffect,
			IdempotencyKey:  "giwa-mo-compensation:" + fixture.compensationIDs[0],
		}, "88888888-8888-4888-8888-88888888f001",
			"99999999-9999-4999-8999-99999999f001", fixture.now.Add(3*time.Minute))
	if err != nil || !replay ||
		rearmed.Compensation.State != paymentdomain.MOCompensationApproved ||
		rearmed.FundingState != paymentdomain.MOFundingReleasePending {
		t.Fatalf("rearm failed compensation: execution=%+v replay=%v err=%v",
			rearmed, replay, err)
	}
	if err := repository.EnsureCommands(ctx,
		"0x1111111111111111111111111111111111111111",
		"0x2222222222222222222222222222222222222222",
		fixture.now.Add(4*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	var attemptCount, plannedCount, oldIdentityCount int
	if err := fixture.database.DB.QueryRowContext(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE state='PLANNED'),
		       count(*) FILTER (WHERE lower(tx_hash)=lower($2))
		FROM settlement_command_outbox
		WHERE mo_compensation_id=$1
	`, fixture.compensationIDs[0], txHash).Scan(
		&attemptCount, &plannedCount, &oldIdentityCount,
	); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 2 || plannedCount != 1 || oldIdentityCount != 1 {
		t.Fatalf("retry attempts=%d planned=%d oldIdentity=%d",
			attemptCount, plannedCount, oldIdentityCount)
	}
	assertCompensationProjection(t, ctx, fixture.database.DB,
		fixture.compensationIDs[0], "EXECUTION_PENDING", "RELEASE_PENDING")

	retryTx := "0x" + strings.Repeat("4", 64)
	markMOCommandBroadcast(t, ctx, fixture.database.DB,
		fixture.compensationIDs[0], 4, retryTx, fixture.now.Add(5*time.Minute))
	projectMORefundFinality(t, ctx, repository, retryTx, 103,
		fixture.now.Add(6*time.Minute))
	assertCompensationProjection(t, ctx, fixture.database.DB,
		fixture.compensationIDs[0], "SUCCEEDED", "RELEASED")
	var providerResourceID string
	if err := fixture.database.DB.QueryRowContext(ctx, `
		SELECT provider_resource_id FROM payment_mo_compensations WHERE id=$1
	`, fixture.compensationIDs[0]).Scan(&providerResourceID); err != nil {
		t.Fatal(err)
	}
	if providerResourceID != retryTx {
		t.Fatalf("final compensation resource=%s want retry tx=%s", providerResourceID, retryTx)
	}
}

func seedGIWACompensationGraph(
	t *testing.T,
	ctx context.Context,
	merchantOrderCount int,
) giwaCompensationFixture {
	t.Helper()
	database := openIsolatedGIWA(t, ctx)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed %q: %v", query[:min(52, len(query))], err)
		}
	}

	passMinor := make([]int64, merchantOrderCount)
	feeMinor := make([]int64, merchantOrderCount)
	var totalMinor int64
	for index := range merchantOrderCount {
		passMinor[index] = int64(1_400 + (index+1)*50)
		feeMinor[index] = 15
		totalMinor += passMinor[index] + feeMinor[index]
	}

	exec(`INSERT INTO users(id,status,created_at,updated_at)
		VALUES($1,'ACTIVE',$2,$2)`, giwaTestUserID, now)
	exec(`INSERT INTO shipping_snapshots(
		id,user_id,profile_version,country,masked_summary,encrypted_payload,
		payload_nonce,key_version,snapshot_hmac,created_at,source_kind
	) VALUES($1,$2,1,'US','T*** B**, US',decode('00','hex'),decode('00','hex'),
		1,'hmac',$3,'ORDER_SHEET_INPUT')`, giwaTestShippingID, giwaTestUserID, now)
	exec(`INSERT INTO agency_order_sheet_sessions(
		id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
		state,version,creation_key_hash,creation_request_hash,snapshot,
		created_at,expires_at,updated_at
	) VALUES($1,$2,gen_random_uuid(),1,'cart-hash','CONSUMED',1,'key','request','{}',
		$3::timestamptz,$3::timestamptz + interval '20 minutes',$3::timestamptz)`,
		giwaTestSessionID, giwaTestUserID, now)
	exec(`INSERT INTO agency_orders(
		id,user_id,order_sheet_session_id,source_cart_id,source_cart_version,
		source_cart_snapshot_hash,shipping_snapshot_id,snapshot_hash,
		idempotency_key_hash,status,customer_payable_minor,currency,snapshot,
		payment_rail,provider_environment,asset,economic_effect,merchant_execution_mode,
		execution_profile_hash,issued_at,expires_at
	) VALUES($1,$2,$3,gen_random_uuid(),1,'cart-hash',$4,'order-hash','idem',
		'ISSUED',$5,'USD','{}','GIWA','TESTNET','TVITUSD','NO_REAL_VALUE',
		'SIMULATED_NO_EFFECT',$6,$7::timestamptz,$7::timestamptz + interval '20 minutes')`,
		giwaTestOrderID, giwaTestUserID, giwaTestSessionID, giwaTestShippingID,
		totalMinor, giwaTestProfileHash, now)
	exec(`INSERT INTO settlement_payments(
		id,order_hash,chain_id,settlement_address,payer,amount_base_units,state,
		pay_tx_hash,agency_order_id,created_at,updated_at
	) VALUES($1,$2,10,$3,'0x'||repeat('22',20),$4,'FINALIZED',
		'0x'||repeat('cd',32),$5,$6,$6)`, giwaTestSettlementID, giwaTestOrderHash,
		giwaTestSettlementAddr, totalMinor*10_000, giwaTestOrderID, now)
	exec(`INSERT INTO payment_customer_payments(
		id,agency_order_id,user_id,rail,provider_environment,asset,economic_effect,
		merchant_execution_mode,execution_profile_hash,amount_minor,currency,state,
		version,created_at,updated_at
	) VALUES($1,$2,$3,'GIWA','TESTNET','TVITUSD','NO_REAL_VALUE',
		'SIMULATED_NO_EFFECT',$4,$5,'USD','CAPTURED',1,$6,$6)`,
		giwaTestCustomerPayID, giwaTestOrderID, giwaTestUserID,
		giwaTestProfileHash, totalMinor, now)
	exec(`INSERT INTO payment_funds_receipts(
		id,customer_payment_id,agency_order_id,kind,provider_environment,
		execution_profile_hash,order_hash,pay_tx_hash,amount_minor,currency,
		accepted,occurred_at,created_at
	) VALUES($1,$2,$3,'GIWA_FINALIZED_PAY','TESTNET',$4,$5,
		'0x'||repeat('cd',32),$6,'USD',TRUE,$7,$7)`,
		giwaTestFundsReceiptID, giwaTestCustomerPayID, giwaTestOrderID,
		giwaTestProfileHash, giwaTestOrderHash, totalMinor, now)
	exec(`INSERT INTO procurement_manifests(
		id,agency_order_id,funds_receipt_id,snapshot_hash,execution_profile_hash,created_at
	) VALUES($1,$2,$3,'manifest-hash',$4,$5)`,
		giwaTestManifestID, giwaTestOrderID, giwaTestFundsReceiptID,
		giwaTestProfileHash, now)

	fixture := giwaCompensationFixture{
		database: database, now: now, passMinor: passMinor, feeMinor: feeMinor,
	}
	for index := range merchantOrderCount {
		ordinal := index + 1
		allocationID := fmt.Sprintf("44444444-4444-4444-4444-44444444f%03d", ordinal)
		fundingID := fmt.Sprintf("55555555-5555-5555-5555-55555555f%03d", ordinal)
		merchantOrderID := fmt.Sprintf("66666666-6666-6666-6666-66666666f%03d", ordinal)
		compensationID := fmt.Sprintf("77777777-7777-7777-7777-77777777f%03d", ordinal)
		grossMinor := passMinor[index] + feeMinor[index]
		allocationHash := fmt.Sprintf("0x%064x", ordinal)

		exec(`INSERT INTO agency_order_mo_allocations(
			id,agency_order_id,checkout_ordinal,merchant_id,shop_domain,
			pass_through_minor,fee_variable_minor,fee_fixed_minor,fee_total_minor,
			customer_gross_minor,currency,fee_policy_version,allocation_hash,
			execution_profile_hash,created_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,0,$7,$8,'USD','TVIT_1_PERCENT_V1',$9,$10,$11)`,
			allocationID, giwaTestOrderID, ordinal, fmt.Sprintf("merchant-%d", ordinal),
			fmt.Sprintf("shop-%d.example", ordinal), passMinor[index], feeMinor[index],
			grossMinor, allocationHash, giwaTestProfileHash, now)
		fundingState := "ACTIVE"
		if index == 0 {
			fundingState = "AVAILABLE"
		}
		exec(`INSERT INTO payment_mo_funding_positions(
			id,allocation_id,agency_order_id,customer_payment_id,paypal_authorization_id,
			rail,source,provider_environment,amount_minor,currency,execution_profile_hash,
			state,available_at,activated_at,created_at,updated_at
		) VALUES($1,$2,$3,$4,NULL,'GIWA','GIWA_PREPAID','TESTNET',$5,'USD',$6,
			$7,$8::timestamptz,
			CASE WHEN $7='ACTIVE' THEN $8::timestamptz ELSE NULL END,
			$8::timestamptz,$8::timestamptz)`,
			fundingID, allocationID, giwaTestOrderID, giwaTestCustomerPayID,
			grossMinor, giwaTestProfileHash, fundingState, now)
		exec(`INSERT INTO merchant_orders(
			id,agency_order_id,manifest_id,merchant_id,shop_domain,checkout_ordinal,
			checkout_snapshot,execution_mode,state,result_hash,version,created_at,updated_at,
			allocation_id
		) VALUES($1,$2,$3,$4,$5,$6,'{}','SIMULATED_NO_EFFECT','CANCELLED',$7,1,$8,$8,$9)`,
			merchantOrderID, giwaTestOrderID, giwaTestManifestID,
			fmt.Sprintf("merchant-%d", ordinal), fmt.Sprintf("shop-%d.example", ordinal),
			ordinal, fmt.Sprintf("result-%d", ordinal), now, allocationID)
		exec(`UPDATE payment_mo_funding_positions SET merchant_order_id=$2 WHERE id=$1`, fundingID, merchantOrderID)
		exec(`INSERT INTO payment_mo_compensations(
			id,allocation_id,funding_position_id,agency_order_id,customer_payment_id,
			rail,provider_environment,action,cause,state,amount_minor,currency,
			execution_profile_hash,idempotency_key,version,approved_at,created_at,updated_at
		) VALUES($1,$2,$3,$4,$5,'GIWA','TESTNET','TVIT_REFUND',
			'CUSTOMER_CANCEL_PRE_EFFECT','APPROVED',$6,'USD',$7,$8,1,$9,$9,$9)`,
			compensationID, allocationID, fundingID, giwaTestOrderID,
			giwaTestCustomerPayID, grossMinor, giwaTestProfileHash,
			"giwa-mo-compensation:"+compensationID, now)

		fixture.allocationIDs = append(fixture.allocationIDs, allocationID)
		fixture.fundingIDs = append(fixture.fundingIDs, fundingID)
		fixture.merchantOrderIDs = append(fixture.merchantOrderIDs, merchantOrderID)
		fixture.compensationIDs = append(fixture.compensationIDs, compensationID)
	}
	return fixture
}

func assertMOCommand(t *testing.T, ctx context.Context, fixture giwaCompensationFixture, index int) {
	t.Helper()
	var purpose, state, refundKey string
	var passThrough, fee int64
	var count int
	if err := fixture.database.DB.QueryRowContext(ctx, `
		SELECT min(purpose), min(state), min(pass_through_part), min(fee_part),
		       min(refund_key), count(*)
		FROM settlement_command_outbox WHERE mo_compensation_id=$1
	`, fixture.compensationIDs[index]).Scan(
		&purpose, &state, &passThrough, &fee, &refundKey, &count,
	); err != nil {
		t.Fatal(err)
	}
	if count != 1 || purpose != "REFUND_PARTIAL" || state != "PLANNED" ||
		passThrough != fixture.passMinor[index]*10_000 ||
		fee != fixture.feeMinor[index]*10_000 ||
		len(refundKey) != 66 || !strings.HasPrefix(refundKey, "0x") {
		t.Fatalf("command[%d]=(count=%d purpose=%s state=%s pass=%d fee=%d key=%s)",
			index, count, purpose, state, passThrough, fee, refundKey)
	}
}

func markMOCommandBroadcast(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	compensationID string,
	nonce int64,
	txHash string,
	now time.Time,
) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		UPDATE settlement_command_outbox
		SET state='BROADCAST', signer_nonce=$2, tx_hash=lower($3),
		    raw_transaction=decode('01','hex'), signed_at=$4,
		    broadcast_at=$4, updated_at=$4
		WHERE mo_compensation_id=$1 AND state='PLANNED'
	`, compensationID, nonce, txHash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO chain_transactions(
			chain_id,tx_hash,settlement_payment_id,purpose,state,
			next_observation_at,submitted_at,updated_at
		) VALUES(10,lower($1),$2,'REFUND_PARTIAL','SUBMITTED',$3,$3,$3)
	`, txHash, giwaTestSettlementID, now); err != nil {
		t.Fatal(err)
	}
}

func projectMORefundFinality(
	t *testing.T,
	ctx context.Context,
	repository *Repository,
	txHash string,
	block uint64,
	now time.Time,
) {
	t.Helper()
	if err := repository.ProjectFinalizedEvents(ctx, 10, giwaTestSettlementAddr,
		[]settlementapp.FinalizedEvent{{
			TxHash: txHash, LogIndex: 0, BlockNumber: block,
			BlockHash: "0x" + strings.Repeat("a", 64),
			EventName: "PaymentRefunded", OrderHash: giwaTestOrderHash,
			FromAddress: "0x2222222222222222222222222222222222222222",
		}}, block, now,
	); err != nil {
		t.Fatalf("project MO refund finality: %v", err)
	}
}

func assertCompensationProjection(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	compensationID, wantCompensationState, wantFundingState string,
) {
	t.Helper()
	var compensationState, fundingState string
	if err := db.QueryRowContext(ctx, `
		SELECT compensation.state, funding.state
		FROM payment_mo_compensations compensation
		JOIN payment_mo_funding_positions funding
		  ON funding.id=compensation.funding_position_id
		WHERE compensation.id=$1
	`, compensationID).Scan(&compensationState, &fundingState); err != nil {
		t.Fatal(err)
	}
	if compensationState != wantCompensationState || fundingState != wantFundingState {
		t.Fatalf("compensation=%s funding=%s, want %s/%s",
			compensationState, fundingState, wantCompensationState, wantFundingState)
	}
}

// assertLatestCompensationEvent checks the latest whole-MO compensation event
// of one compensation. Since ADR-0070 the event carries only the owner row
// (state/action/cause); the order aggregate is derived by the reducer, so the
// count arguments are retained for call-site readability but not asserted.
func assertLatestCompensationEvent(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	compensationID, wantState string,
	_, _ int,
	_ bool,
) {
	t.Helper()
	var state string
	if err := db.QueryRowContext(ctx, `
		SELECT payload->>'state'
		FROM order_process_events
		WHERE type='payment.mo_compensation.state_changed.v1'
		  AND payload->>'compensationId'=$1
		ORDER BY seq DESC LIMIT 1
	`, compensationID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != wantState {
		t.Fatalf("event state=%s, want %s", state, wantState)
	}
}

func giwaMigrationDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../../../migrations"))
}

func openIsolatedGIWA(t *testing.T, ctx context.Context) *sharedpostgres.Database {
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
	databaseName := "vitlane_giwa_mo_" + hex.EncodeToString(suffix)
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
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		query = rest[q:]
	}
	isolated, err := sharedpostgres.Open(ctx, databaseURL[:slash+1]+databaseName+query)
	if err != nil {
		t.Fatalf("open isolated database: %v", err)
	}
	t.Cleanup(func() {
		_ = isolated.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := admin.DB.ExecContext(cleanupCtx,
			`DROP DATABASE "`+databaseName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
		_ = admin.Close()
	})
	if err := isolated.Migrate(ctx, giwaMigrationDirectory(t)); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}
	return isolated
}

// PLACED MO가 전부 whole-MO 보상으로 환불된 결제(배송 예외·환불 심사 → REFUNDED_ALL)
// 는 escrow가 이미 비었으므로 COMPLETE를 계획하지 않는다 — contract는 REFUNDED라
// COMPLETE가 PaymentNotEscrowed로 영구 revert하며 settlement worker를 degraded로
// 묶는다. 환불되지 않은 PLACED MO가 하나라도 남으면 잔여 방출을 계획한다.
func TestEnsureCommandsSkipsCompletionWhenEveryPlacedMOWasRefunded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	fixture := seedGIWACompensationGraph(t, ctx, 2)
	repository := NewRepository(fixture.database)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := fixture.database.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query[:40], err)
		}
	}
	exec(`UPDATE merchant_orders SET state='PLACED' WHERE agency_order_id=$1`, giwaTestOrderID)
	exec(`UPDATE payment_mo_compensations
	      SET state='SUCCEEDED', completed_at=$2, updated_at=$2
	      WHERE agency_order_id=$1`, giwaTestOrderID, fixture.now)
	completeCount := func() int {
		var count int
		if err := fixture.database.DB.QueryRowContext(ctx, `
			SELECT count(*) FROM settlement_command_outbox
			WHERE settlement_payment_id=$1 AND purpose='COMPLETE'
		`, giwaTestSettlementID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	ensure := func() {
		t.Helper()
		if err := repository.EnsureCommands(ctx,
			"0x1111111111111111111111111111111111111111",
			"0x2222222222222222222222222222222222222222",
			fixture.now,
		); err != nil {
			t.Fatal(err)
		}
	}
	ensure()
	if got := completeCount(); got != 0 {
		t.Fatalf("fully refunded payment planned COMPLETE commands: %d", got)
	}
	// 한 MO의 보상이 없으면(정상 수령) 잔여 pass-through 방출은 계획된다.
	exec(`DELETE FROM payment_mo_compensations WHERE id=$1`, fixture.compensationIDs[1])
	exec(`DELETE FROM settlement_command_outbox WHERE mo_compensation_id=$1`, fixture.compensationIDs[1])
	ensure()
	if got := completeCount(); got != 1 {
		t.Fatalf("partially refunded payment must plan one COMPLETE, got %d", got)
	}
}
