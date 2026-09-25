package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/shared/testdb"
)

const (
	fundingUserID      = "11111111-1111-4111-8111-11111111f001"
	fundingOperatorID  = "11111111-1111-4111-8111-11111111f002"
	fundingShippingID  = "22222222-2222-4222-8222-22222222f001"
	fundingSessionID   = "33333333-3333-4333-8333-33333333f001"
	fundingOrderID     = "44444444-4444-4444-8444-44444444f001"
	fundingCustomerPay = "55555555-5555-4555-8555-55555555f001"
	fundingReceiptID   = "66666666-6666-4666-8666-66666666f001"
	fundingManifestID  = "77777777-7777-4777-8777-77777777f001"
	fundingMerchantAID = "88888888-8888-4888-8888-88888888f001"
	fundingMerchantBID = "88888888-8888-4888-8888-88888888f002"
	fundingAllocationA = "99999999-9999-4999-8999-99999999f001"
	fundingAllocationB = "99999999-9999-4999-8999-99999999f002"
	fundingPositionA   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaf001"
	fundingPositionB   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaf002"
	fundingAttemptID   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbb001"
	fundingAuthorizeID = "cccccccc-cccc-4ccc-8ccc-ccccccccc001"
	fundingRecoveryID  = "dddddddd-dddd-4ddd-8ddd-ddddddddd001"
	fundingSandboxHash = "0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665"
)

func TestOrderAccountingProjectionAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)

	repository := NewRepository(database)
	input, found, err := repository.GetOrderAccountingInput(ctx, fundingOrderID)
	if err != nil || !found {
		t.Fatalf("get order accounting input: found=%v err=%v", found, err)
	}
	if len(input.MerchantOrders) != 2 ||
		input.MerchantOrders[0].AllocationID != fundingAllocationA ||
		input.MerchantOrders[1].AllocationID != fundingAllocationB {
		t.Fatalf("unexpected MO accounting inputs: %+v", input.MerchantOrders)
	}
	service := paymentapp.NewAccountingService(repository, sharedapp.SystemClock{})
	summary, err := service.GetAccounting(ctx, "SANDBOX")
	if err != nil {
		t.Fatal(err)
	}
	if summary.OrderCount != 1 || summary.ActualCustomerGrossInMinor != 10_570 ||
		summary.ActualProcessorFeeMinor != 500 || summary.ActualNetCashInMinor != 10_070 ||
		summary.ActualMerchantSpendMinor != 10_000 ||
		summary.ActualMerchantRecoveredMinor != 100 || summary.RealizedBalanceMinor != 170 ||
		summary.ForecastNetCashInMinor != 2_013 ||
		summary.ForecastProcessorFeeMinor != 125 ||
		summary.ForecastMerchantSpendMinor != 2_000 ||
		summary.ForecastBalanceMinor != 183 {
		t.Fatalf("unexpected event accounting summary: %+v", summary)
	}
	if len(summary.Orders[0].Events) != 3 {
		t.Fatalf("actual ledger event count=%d events=%+v",
			len(summary.Orders[0].Events), summary.Orders[0].Events)
	}
	live, err := service.GetAccounting(ctx, "LIVE")
	if err != nil || live.OrderCount != 0 || live.ActualNetCashInMinor != 0 {
		t.Fatalf("Sandbox accounting leaked into Live: summary=%+v err=%v", live, err)
	}
}

func TestMOReauthorizationRefreshesProviderIdentityBeforeCapturePlan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	authorizedAt := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, authorizedAt)
	now := authorizedAt.Add(4 * 24 * time.Hour)
	repository := NewRepository(database)

	plan, err := repository.PrepareMOReauthorization(
		ctx, fundingMerchantBID,
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee0001", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Required || plan.PreviousProviderAuthorizationID != "AUTH-FUNDING" ||
		plan.AuthorizedAmountMinor != 12708 ||
		plan.RemainingCapturableMinor != 2138 ||
		plan.OperationState != domain.OperationPrepared {
		t.Fatalf("unexpected reauthorization plan: %+v", plan)
	}
	if err := repository.MarkMOReauthorizationSent(
		ctx, plan, now, now.Add(72*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	refreshedAt := now.Add(time.Minute)
	if err := repository.RecordMOReauthorizationOutcome(
		ctx, plan, domain.OperationSucceeded, "AUTH-FUNDING-REFRESHED",
		2138, "USD", refreshedAt, "", refreshedAt,
	); err != nil {
		t.Fatal(err)
	}
	var providerID string
	var honorAt time.Time
	var count int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT paypal_authorization_id,honor_refreshed_at,reauthorization_count
		FROM payment_paypal_authorizations WHERE id=$1
	`, fundingAuthorizeID).Scan(&providerID, &honorAt, &count); err != nil {
		t.Fatal(err)
	}
	if providerID != "AUTH-FUNDING-REFRESHED" || !honorAt.Equal(refreshedAt) || count != 1 {
		t.Fatalf("reauthorization not adopted: id=%s honor=%s count=%d",
			providerID, honorAt, count)
	}
	var evidenceCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM payment_paypal_reauthorizations
		WHERE paypal_authorization_id=$1 AND provider_authorization_id=$2
	`, fundingAuthorizeID, providerID).Scan(&evidenceCount); err != nil {
		t.Fatal(err)
	}
	if evidenceCount != 1 {
		t.Fatalf("reauthorization evidence count=%d", evidenceCount)
	}
	activation, _, err := repository.PrepareMOFundingActivation(
		ctx, fundingMerchantBID,
		domain.ExternalOperation{ID: "eeeeeeee-eeee-4eee-8eee-eeeeeeee0002"},
		refreshedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if activation.ProviderAuthorizationID != providerID || !activation.FinalCapture {
		t.Fatalf("capture plan did not use refreshed authorization: %+v", activation)
	}
}

// 완료된 MO 보상은 SUPPORT executor가 참조 id로 읽는다(ADR-0070 §4.5 — marker
// 큐 대신 커맨드 원장이 발신 멱등성을 진다).
func TestCompletedMOCompensationIsReadableByReferenceID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	const compensationID = "eeeeeeee-eeee-4eee-8eee-eeeeeeee0003"
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO payment_mo_compensations(
			id,allocation_id,funding_position_id,agency_order_id,
			customer_payment_id,rail,provider_environment,action,cause,state,
			amount_minor,currency,execution_profile_hash,provider_resource_id,
			idempotency_key,version,approved_at,completed_at,created_at,updated_at
		) VALUES($1,$2,$3,$4,$5,'PAYPAL','SANDBOX','REFUND',
			'CUSTOMER_REFUND_POST_EFFECT','SUCCEEDED',10570,'USD',$6,
			'REFUND-FUNDING-A','support-compensation-key',2,$7,$7,$7,$7)
	`, compensationID, fundingAllocationA, fundingPositionA, fundingOrderID,
		fundingCustomerPay, fundingSandboxHash, now); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(database)
	item, err := repository.GetMOCompensationNotification(ctx, compensationID)
	if err != nil || item.Compensation.ID != compensationID ||
		item.Compensation.State != domain.MOCompensationSucceeded ||
		item.MerchantOrderID != fundingMerchantAID {
		t.Fatalf("compensation notification=%+v err=%v", item, err)
	}
	if _, err := repository.GetMOCompensationNotification(
		ctx, "eeeeeeee-eeee-4eee-8eee-eeeeeeee0009",
	); !errors.Is(err, domain.ErrCompensationNotAvailable) {
		t.Fatalf("missing compensation err=%v", err)
	}
}

func TestPrepareMOCompensationUsesExactAllocationForVoidAndRefund(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	repository := NewRepository(database)

	preEffect, replay, err := repository.PrepareMOCompensation(
		ctx,
		paymentapp.MOCompensationRequest{
			AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantBID,
			AllocationID:   fundingAllocationB,
			Cause:          domain.MOCompensationCustomerCancelPreEffect,
			IdempotencyKey: "compensate:mo-b:cancel",
		},
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee1001",
		"ffffffff-ffff-4fff-8fff-ffffffff1001",
		now.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if replay || preEffect.Compensation.Action != domain.MOCompensationVoid ||
		preEffect.Compensation.AmountMinor != 2138 || !preEffect.ProviderVoidRequired ||
		preEffect.OperationState != domain.OperationPrepared {
		t.Fatalf("unexpected pre-effect compensation: replay=%v execution=%+v",
			replay, preEffect)
	}

	postEffect, replay, err := repository.PrepareMOCompensation(
		ctx,
		paymentapp.MOCompensationRequest{
			AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantAID,
			AllocationID:   fundingAllocationA,
			Cause:          domain.MOCompensationDeliveryException,
			IdempotencyKey: "compensate:mo-a:delivery",
		},
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee1002",
		"ffffffff-ffff-4fff-8fff-ffffffff1002",
		now.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if replay || postEffect.Compensation.Action != domain.MOCompensationRefund ||
		postEffect.Compensation.AmountMinor != 10570 ||
		postEffect.ProviderCaptureID != "CAPTURE-FUNDING-1" ||
		postEffect.OperationState != domain.OperationPrepared {
		t.Fatalf("unexpected post-effect compensation: replay=%v execution=%+v",
			replay, postEffect)
	}
	if err := repository.MarkMOCompensationSent(
		ctx, postEffect, now.Add(3*time.Minute), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	completed, err := repository.RecordMOCompensationOutcome(
		ctx, postEffect, domain.MOCompensationSucceeded,
		domain.OperationSucceeded, "PP-REFUND-EXACT-MO-A", "",
		now.Add(4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != domain.MOCompensationSucceeded ||
		completed.AmountMinor != 10570 ||
		completed.ProviderResourceID != "PP-REFUND-EXACT-MO-A" {
		t.Fatalf("unexpected completed exact-MO refund: %+v", completed)
	}
}

func TestFailedPayPalMORefundRearmsFreshProviderAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	repository := NewRepository(database)
	request := paymentapp.MOCompensationRequest{
		AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantAID,
		AllocationID:   fundingAllocationA,
		Cause:          domain.MOCompensationDeliveryException,
		IdempotencyKey: "compensate:mo-a:terminal-refund-retry",
	}

	first, replay, err := repository.PrepareMOCompensation(
		ctx, request,
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee1201",
		"ffffffff-ffff-4fff-8fff-ffffffff1201", now.Add(time.Minute),
	)
	if err != nil || replay {
		t.Fatalf("prepare first refund: replay=%v err=%v", replay, err)
	}
	if err := repository.MarkMOCompensationSent(
		ctx, first, now.Add(2*time.Minute), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	failed, err := repository.RecordMOCompensationOutcome(
		ctx, first, domain.MOCompensationFailed, domain.OperationFailed,
		"PP-REFUND-FAILED-1", "REFUND_FAILED", now.Add(3*time.Minute),
	)
	if err != nil || failed.State != domain.MOCompensationFailed {
		t.Fatalf("record terminal refund failure: compensation=%+v err=%v", failed, err)
	}

	second, replay, err := repository.PrepareMOCompensation(
		ctx, request,
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee1202",
		"ffffffff-ffff-4fff-8fff-ffffffff1202", now.Add(4*time.Minute),
	)
	if err != nil || !replay || second.Compensation.ID != first.Compensation.ID ||
		second.Compensation.State != domain.MOCompensationApproved ||
		second.OperationID == first.OperationID ||
		second.OperationIdempotencyKey == first.OperationIdempotencyKey ||
		second.OperationResourceID != "" {
		t.Fatalf("refund was not rearmed with a fresh operation: %+v replay=%v err=%v",
			second, replay, err)
	}
	if err := repository.MarkMOCompensationSent(
		ctx, second, now.Add(5*time.Minute), now.Add(2*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	succeeded, err := repository.RecordMOCompensationOutcome(
		ctx, second, domain.MOCompensationSucceeded, domain.OperationSucceeded,
		"PP-REFUND-SUCCEEDED-2", "", now.Add(6*time.Minute),
	)
	if err != nil || succeeded.State != domain.MOCompensationSucceeded ||
		succeeded.ProviderResourceID != "PP-REFUND-SUCCEEDED-2" {
		t.Fatalf("fresh refund attempt did not converge: %+v err=%v", succeeded, err)
	}
	var failedCount, succeededCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE state='FAILED'),
		       count(*) FILTER (WHERE state='SUCCEEDED')
		FROM payment_external_operations
		WHERE owner_kind='MO_COMPENSATION' AND owner_id=$1
		  AND purpose='PAYPAL_MO_REFUND'
	`, first.Compensation.ID).Scan(&failedCount, &succeededCount); err != nil {
		t.Fatal(err)
	}
	if failedCount != 1 || succeededCount != 1 {
		t.Fatalf("refund attempt audit failed=%d succeeded=%d", failedCount, succeededCount)
	}
}

func TestProviderIdempotencyDeadlinesBlockLateMoneyResend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	repository := NewRepository(database)

	refund, _, err := repository.PrepareMOCompensation(
		ctx, paymentapp.MOCompensationRequest{
			AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantAID,
			AllocationID:   fundingAllocationA,
			Cause:          domain.MOCompensationDeliveryException,
			IdempotencyKey: "compensate:mo-a:deadline",
		},
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee1301",
		"ffffffff-ffff-4fff-8fff-ffffffff1301", now.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(3 * time.Minute)
	if err := repository.MarkMOCompensationSent(
		ctx, refund, now.Add(2*time.Minute), deadline,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RecordMOCompensationOutcome(
		ctx, refund, domain.MOCompensationOutcomeUnknown, domain.OperationUnknown,
		"", "NETWORK_TIMEOUT", now.Add(150*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkMOCompensationSent(
		ctx, refund, deadline.Add(time.Second), deadline.Add(time.Hour),
	); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("late refund resend error=%v want conflict", err)
	}

	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO payment_external_operations(
			id,purpose,owner_kind,owner_id,idempotency_key,request_hash,state,
			first_sent_at,idempotency_deadline,created_at,updated_at
		) VALUES(
			'ffffffff-ffff-4fff-8fff-ffffffff1302','PAYPAL_ORDER_CREATE',
			'PAYPAL_ATTEMPT',$1,'paypal:create:deadline','deadline-hash','UNKNOWN',
			$2,$3,$2,$2
		)
	`, fundingAttemptID, now.Add(time.Minute), deadline); err != nil {
		t.Fatal(err)
	}
	lateOperation := domain.ExternalOperation{
		ID: "ignored-local-retry-id", Purpose: domain.OperationPayPalOrderCreate,
		OwnerKind: "PAYPAL_ATTEMPT", OwnerID: fundingAttemptID,
		IdempotencyKey: "paypal:create:deadline", RequestHash: "deadline-hash",
		State: domain.OperationUnknown,
	}
	if err := repository.MarkOperationSent(
		ctx, lateOperation, fundingAttemptID, domain.AttemptOrderCreateSubmitted,
		fundingCustomerPay, domain.PaymentProcessing,
		deadline.Add(time.Second), deadline.Add(time.Hour),
	); !errors.Is(err, domain.ErrIdempotencyExpired) {
		t.Fatalf("late create/authorize resend error=%v want idempotency expired", err)
	}
}

func TestRefundSendAnchorSurvivesReceiptTransactionRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 4, 20, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	repository := NewRepository(database)

	execution, _, err := repository.PrepareMOCompensation(
		ctx, paymentapp.MOCompensationRequest{
			AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantAID,
			AllocationID:   fundingAllocationA,
			Cause:          domain.MOCompensationDeliveryException,
			IdempotencyKey: "compensate:mo-a:durable-send-anchor",
		},
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee1351",
		"ffffffff-ffff-4fff-8fff-ffffffff1351", now.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	firstSentAt := now.Add(2 * time.Minute)
	deadline := now.Add(time.Hour)
	providerTimeout := errors.New("provider timeout")
	err = repository.WithMOCompensationEffectLock(
		ctx, execution, func(context.Context) error {
			// Production deliberately uses the caller context here. This creates a
			// separate transaction while the receipt lock remains held.
			if markErr := repository.MarkMOCompensationSent(
				ctx, execution, firstSentAt, deadline,
			); markErr != nil {
				return markErr
			}
			return providerTimeout
		},
	)
	if !errors.Is(err, providerTimeout) {
		t.Fatalf("effect transaction error=%v want provider timeout", err)
	}
	var state string
	var storedFirstSentAt, storedDeadline time.Time
	if err := database.DB.QueryRowContext(ctx, `
		SELECT state,first_sent_at,idempotency_deadline
		FROM payment_external_operations WHERE id=$1
	`, execution.OperationID).Scan(&state, &storedFirstSentAt, &storedDeadline); err != nil {
		t.Fatal(err)
	}
	if state != string(domain.OperationSent) ||
		!storedFirstSentAt.Equal(firstSentAt) || !storedDeadline.Equal(deadline) {
		t.Fatalf("send anchor rolled back: state=%s first=%v deadline=%v",
			state, storedFirstSentAt, storedDeadline)
	}
	// Once the in-flight lease is stale, the same immutable provider key can be
	// reclaimed within its original deadline; the deadline is never extended.
	if err := repository.MarkMOCompensationSent(
		ctx, execution, firstSentAt.Add(3*time.Minute), deadline.Add(time.Hour),
	); err != nil {
		t.Fatalf("same-key stale recovery: %v", err)
	}
	if err := database.DB.QueryRowContext(ctx, `
		SELECT first_sent_at,idempotency_deadline
		FROM payment_external_operations WHERE id=$1
	`, execution.OperationID).Scan(&storedFirstSentAt, &storedDeadline); err != nil {
		t.Fatal(err)
	}
	if !storedFirstSentAt.Equal(firstSentAt) || !storedDeadline.Equal(deadline) {
		t.Fatalf("stale recovery changed retention anchor: first=%v deadline=%v",
			storedFirstSentAt, storedDeadline)
	}
}

func TestStaleSubmittedAuthorizationReclaimsSameProviderRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 4, 40, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now.Add(-time.Hour))
	const (
		opID = "ffffffff-ffff-4fff-8fff-fffffffff101"
		key  = "paypal:authorize:bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbb001:1"
		hash = "authorization-recovery-request-hash"
	)
	firstSentAt := now.Add(-3 * time.Minute)
	deadline := now.Add(72 * time.Hour)
	resetTx, err := database.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resetTx.Rollback() }()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`SELECT set_config('vitlane.account_reset','on',true)`, nil},
		{`DELETE FROM payment_mo_cash_receipts WHERE customer_payment_id=$1`, []any{fundingCustomerPay}},
		{`DELETE FROM payment_mo_funding_positions WHERE customer_payment_id=$1`, []any{fundingCustomerPay}},
		{`DELETE FROM payment_paypal_authorizations WHERE customer_payment_id=$1`, []any{fundingCustomerPay}},
		{`UPDATE payment_paypal_attempts
		  SET state='AUTHORIZE_SUBMITTED',paypal_order_id='PAYPAL-ORDER-RECOVERY',updated_at=$2
		  WHERE id=$1`, []any{fundingAttemptID, firstSentAt}},
		{`UPDATE payment_customer_payments
		  SET state='PROCESSING',updated_at=$2 WHERE id=$1`, []any{fundingCustomerPay, firstSentAt}},
	} {
		if _, err := resetTx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := resetTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO payment_external_operations(
			id,purpose,owner_kind,owner_id,idempotency_key,request_hash,state,
			first_sent_at,idempotency_deadline,created_at,updated_at
		) VALUES($1,'PAYPAL_AUTHORIZE','PAYPAL_ATTEMPT',$2,$3,$4,'SENT',$5,$6,$5,$5)
	`, opID, fundingAttemptID, key, hash, firstSentAt, deadline); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(database)
	operation, created, err := repository.PrepareAuthorizationOperation(
		ctx, fundingAttemptID, domain.ExternalOperation{
			ID:      "ffffffff-ffff-4fff-8fff-fffffffff102",
			Purpose: domain.OperationPayPalAuthorize, OwnerKind: "PAYPAL_ATTEMPT",
			OwnerID: fundingAttemptID, IdempotencyKey: key, RequestHash: hash,
		}, now,
	)
	if err != nil || created || operation.ID != opID ||
		operation.State != domain.OperationUnknown {
		t.Fatalf("stale sender not recovered: operation=%+v created=%v err=%v",
			operation, created, err)
	}
	if err := repository.MarkOperationSent(
		ctx, operation, fundingAttemptID, domain.AttemptAuthorizeSubmitted,
		fundingCustomerPay, domain.PaymentProcessing, now, deadline.Add(time.Hour),
	); err != nil {
		t.Fatalf("same-key authorization reclaim: %v", err)
	}
	var state string
	var storedFirstSentAt, storedDeadline time.Time
	if err := database.DB.QueryRowContext(ctx, `
		SELECT state,first_sent_at,idempotency_deadline
		FROM payment_external_operations WHERE id=$1
	`, opID).Scan(&state, &storedFirstSentAt, &storedDeadline); err != nil {
		t.Fatal(err)
	}
	if state != string(domain.OperationSent) ||
		!storedFirstSentAt.Equal(firstSentAt) || !storedDeadline.Equal(deadline) {
		t.Fatalf("authorization recovery changed provider identity window: state=%s first=%v deadline=%v",
			state, storedFirstSentAt, storedDeadline)
	}
}

func TestCapturedMORefundRemainsAvailableAfterResidualAuthorizationCloses(t *testing.T) {
	for _, test := range []struct {
		name   string
		reason string
		state  domain.PayPalAuthorizationState
	}{
		{name: "voided", state: domain.AuthorizationVoided},
		{name: "expired", reason: "AUTHORIZATION_EXPIRED", state: domain.AuthorizationExpired},
		{name: "denied", reason: "AUTHORIZATION_DENIED", state: domain.AuthorizationDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
			now := time.Date(2026, 8, 27, 4, 30, 0, 0, time.UTC)
			seedMOAccountingOrder(t, ctx, database, now)
			repository := NewRepository(database)

			// MO-B owns the final residual hold. Closing it moves the shared
			// authorization to VOIDED/EXPIRED, but must not invalidate MO-A's
			// already captured, immutable refund target.
			release, _, err := repository.PrepareMOCompensation(
				ctx, paymentapp.MOCompensationRequest{
					AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantBID,
					AllocationID:   fundingAllocationB,
					Cause:          domain.MOCompensationCustomerCancelPreEffect,
					IdempotencyKey: "compensate:close-residual:" + test.name,
				}, "eeeeeeee-eeee-4eee-8eee-eeeeeeee1501",
				"ffffffff-ffff-4fff-8fff-ffffffff1501", now.Add(time.Minute),
			)
			if err != nil || !release.ProviderVoidRequired {
				t.Fatalf("prepare residual release: execution=%+v err=%v", release, err)
			}
			if err := repository.MarkMOCompensationSent(
				ctx, release, now.Add(2*time.Minute), now.Add(time.Hour),
			); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.RecordMOCompensationOutcome(
				ctx, release, domain.MOCompensationSucceeded,
				domain.OperationSucceeded, "AUTH-FUNDING", test.reason,
				now.Add(3*time.Minute),
			); err != nil {
				t.Fatal(err)
			}
			var authorizationState domain.PayPalAuthorizationState
			if err := database.DB.QueryRowContext(ctx, `
				SELECT state FROM payment_paypal_authorizations WHERE id=$1
			`, fundingAuthorizeID).Scan(&authorizationState); err != nil {
				t.Fatal(err)
			}
			if authorizationState != test.state {
				t.Fatalf("authorization state=%s want=%s", authorizationState, test.state)
			}

			refund, replay, err := repository.PrepareMOCompensation(
				ctx, paymentapp.MOCompensationRequest{
					AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantAID,
					AllocationID:   fundingAllocationA,
					Cause:          domain.MOCompensationDeliveryException,
					IdempotencyKey: "compensate:captured-after-close:" + test.name,
				}, "eeeeeeee-eeee-4eee-8eee-eeeeeeee1502",
				"ffffffff-ffff-4fff-8fff-ffffffff1502", now.Add(4*time.Minute),
			)
			if err != nil || replay ||
				refund.Compensation.Action != domain.MOCompensationRefund ||
				refund.ProviderCaptureID != "CAPTURE-FUNDING-1" ||
				refund.Compensation.AmountMinor != 10570 {
				t.Fatalf("captured MO refund was lost after auth close: %+v replay=%v err=%v",
					refund, replay, err)
			}
		})
	}
}

func TestStaleRefundWaiterCannotSendAfterRefundIdentityCheckpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 4, 50, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	repository := NewRepository(database)
	request := paymentapp.MOCompensationRequest{
		AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantAID,
		AllocationID:   fundingAllocationA,
		Cause:          domain.MOCompensationDeliveryException,
		IdempotencyKey: "compensate:refund:single-sender",
	}
	first, replay, err := repository.PrepareMOCompensation(
		ctx, request, "eeeeeeee-eeee-4eee-8eee-eeeeeeee1601",
		"ffffffff-ffff-4fff-8fff-ffffffff1601", now.Add(time.Minute),
	)
	if err != nil || replay {
		t.Fatalf("prepare first refund: replay=%v err=%v", replay, err)
	}
	stale, replay, err := repository.PrepareMOCompensation(
		ctx, request, "eeeeeeee-eeee-4eee-8eee-eeeeeeee1602",
		"ffffffff-ffff-4fff-8fff-ffffffff1602", now.Add(2*time.Minute),
	)
	if err != nil || !replay || stale.OperationState != domain.OperationPrepared {
		t.Fatalf("prepare stale waiter: execution=%+v replay=%v err=%v", stale, replay, err)
	}
	if err := repository.WithMOCompensationEffectLock(ctx, first, func(tx context.Context) error {
		if err := repository.MarkMOCompensationSent(
			tx, first, now.Add(3*time.Minute), now.Add(time.Hour),
		); err != nil {
			return err
		}
		_, err := repository.RecordMOCompensationOutcome(
			tx, first, domain.MOCompensationOutcomeUnknown,
			domain.OperationUnknown, "REFUND-CHECKPOINTED",
			"REFUND_AWAITING_GET", now.Add(4*time.Minute),
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sendClaims := 0
	err = repository.WithMOCompensationEffectLock(ctx, stale, func(tx context.Context) error {
		if err := repository.MarkMOCompensationSent(
			tx, stale, now.Add(5*time.Minute), now.Add(time.Hour),
		); err != nil {
			return err
		}
		sendClaims++
		return nil
	})
	if !errors.Is(err, domain.ErrConflict) || sendClaims != 0 {
		t.Fatalf("stale refund waiter escaped sender CAS: claims=%d err=%v", sendClaims, err)
	}
}

func TestMOFundingActivationAndCancellationAreMutuallyExclusive(t *testing.T) {
	t.Run("capture claim blocks cancellation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
		now := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
		seedMOAccountingOrder(t, ctx, database, now)
		repository := NewRepository(database)

		activation, _, err := repository.PrepareMOFundingActivation(
			ctx, fundingMerchantBID,
			domain.ExternalOperation{ID: "ffffffff-ffff-4fff-8fff-ffffffff2001"},
			now.Add(time.Minute),
		)
		if err != nil {
			t.Fatalf("prepare capture: %v", err)
		}
		if err := repository.MarkMOFundingActivationSent(
			ctx, activation.PositionID, activation.OperationID,
			now.Add(90*time.Second), now.Add(time.Hour),
		); err != nil {
			t.Fatalf("claim capture sender: %v", err)
		}
		if err := repository.MarkMOFundingActivationSent(
			ctx, activation.PositionID, activation.OperationID,
			now.Add(91*time.Second), now.Add(time.Hour),
		); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("second capture sender was not rejected: %v", err)
		}
		_, _, err = repository.PrepareMOCompensation(
			ctx, paymentapp.MOCompensationRequest{
				AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantBID,
				AllocationID:   fundingAllocationB,
				Cause:          domain.MOCompensationCustomerCancelPreEffect,
				IdempotencyKey: "compensate:race:capture-first",
			}, "eeeeeeee-eeee-4eee-8eee-eeeeeeee2001",
			"ffffffff-ffff-4fff-8fff-ffffffff2002", now.Add(2*time.Minute),
		)
		if !errors.Is(err, domain.ErrCompensationOutcomeUnknown) {
			t.Fatalf("capture claim did not block cancellation: %v", err)
		}
	})

	t.Run("cancellation claim blocks capture", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
		now := time.Date(2026, 8, 27, 6, 0, 0, 0, time.UTC)
		seedMOAccountingOrder(t, ctx, database, now)
		repository := NewRepository(database)

		_, _, err := repository.PrepareMOCompensation(
			ctx, paymentapp.MOCompensationRequest{
				AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantBID,
				AllocationID:   fundingAllocationB,
				Cause:          domain.MOCompensationCustomerCancelPreEffect,
				IdempotencyKey: "compensate:race:cancel-first",
			}, "eeeeeeee-eeee-4eee-8eee-eeeeeeee2002",
			"ffffffff-ffff-4fff-8fff-ffffffff2003", now.Add(time.Minute),
		)
		if err != nil {
			t.Fatalf("prepare cancellation: %v", err)
		}
		_, _, err = repository.PrepareMOFundingActivation(
			ctx, fundingMerchantBID,
			domain.ExternalOperation{ID: "ffffffff-ffff-4fff-8fff-ffffffff2004"},
			now.Add(2*time.Minute),
		)
		if !errors.Is(err, domain.ErrFundingNotAvailable) {
			t.Fatalf("cancellation claim did not block capture: %v", err)
		}
	})
}

func TestFinalCaptureExcludesPermanentlyFailedSibling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 6, 30, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_mo_funding_positions SET state='FAILED',updated_at=$1
		WHERE allocation_id=$2
	`, now, fundingAllocationA); err != nil {
		t.Fatal(err)
	}
	activation, _, err := NewRepository(database).PrepareMOFundingActivation(
		ctx, fundingMerchantBID,
		domain.ExternalOperation{ID: "ffffffff-ffff-4fff-8fff-ffffffff2501"},
		now.Add(time.Minute),
	)
	if err != nil || !activation.FinalCapture {
		t.Fatalf("failed sibling kept residual authorization open: activation=%+v err=%v",
			activation, err)
	}
}

func TestFailedPositionCanCloseLocallyAfterAuthorizationTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 6, 45, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_mo_funding_positions SET state='FAILED',updated_at=$1
		WHERE allocation_id=$2
	`, now, fundingAllocationB); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_paypal_authorizations
		SET state='CAPTURED',terminal_at=$1,updated_at=$1
		WHERE id=$2
	`, now, fundingAuthorizeID); err != nil {
		t.Fatal(err)
	}
	execution, _, err := NewRepository(database).PrepareMOCompensation(
		ctx, paymentapp.MOCompensationRequest{
			AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantBID,
			AllocationID:   fundingAllocationB,
			Cause:          domain.MOCompensationProcurementFailure,
			IdempotencyKey: "compensate:failed:terminal-auth",
		}, "eeeeeeee-eeee-4eee-8eee-eeeeeeee2501",
		"ffffffff-ffff-4fff-8fff-ffffffff2502", now.Add(time.Minute),
	)
	if err != nil || execution.ProviderVoidRequired || execution.OperationID != "" ||
		execution.Compensation.Action != domain.MOCompensationVoid {
		t.Fatalf("terminal authorization did not allow local failed-MO release: %+v err=%v",
			execution, err)
	}
}

func TestConcurrentPreEffectReleasesLeaveOneResidualVoidOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 7, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	resetFundingFixtureToTwoAvailable(t, ctx, database, now)
	repository := NewRepository(database)
	type releaseCase struct {
		merchantOrderID string
		allocationID    string
		compensationID  string
		operationID     string
		key             string
	}
	cases := []releaseCase{
		{fundingMerchantAID, fundingAllocationA,
			"eeeeeeee-eeee-4eee-8eee-eeeeeeee3001",
			"ffffffff-ffff-4fff-8fff-ffffffff3001", "compensate:concurrent:mo-a"},
		{fundingMerchantBID, fundingAllocationB,
			"eeeeeeee-eeee-4eee-8eee-eeeeeeee3002",
			"ffffffff-ffff-4fff-8fff-ffffffff3002", "compensate:concurrent:mo-b"},
	}
	type releaseResult struct {
		index     int
		execution paymentapp.MOCompensationExecution
		err       error
	}
	start := make(chan struct{})
	results := make(chan releaseResult, len(cases))
	var wg sync.WaitGroup
	for index, item := range cases {
		wg.Add(1)
		go func(index int, item releaseCase) {
			defer wg.Done()
			<-start
			execution, _, err := repository.PrepareMOCompensation(
				ctx, paymentapp.MOCompensationRequest{
					AgencyOrderID: fundingOrderID, MerchantOrderID: item.merchantOrderID,
					AllocationID:   item.allocationID,
					Cause:          domain.MOCompensationCustomerCancelPreEffect,
					IdempotencyKey: item.key,
				}, item.compensationID, item.operationID, now.Add(time.Minute),
			)
			results <- releaseResult{index: index, execution: execution, err: err}
		}(index, item)
	}
	close(start)
	wg.Wait()
	close(results)

	successIndex, blockedIndex := -1, -1
	var succeeded paymentapp.MOCompensationExecution
	for item := range results {
		switch {
		case item.err == nil:
			successIndex, succeeded = item.index, item.execution
		case errors.Is(item.err, domain.ErrCompensationOutcomeUnknown):
			blockedIndex = item.index
		default:
			t.Fatalf("unexpected concurrent release error: %v", item.err)
		}
	}
	if successIndex < 0 || blockedIndex < 0 || succeeded.ProviderVoidRequired {
		t.Fatalf("release owners success=%d blocked=%d execution=%+v",
			successIndex, blockedIndex, succeeded)
	}
	if _, err := repository.RecordMOCompensationOutcome(
		ctx, succeeded, domain.MOCompensationSucceeded,
		domain.OperationSucceeded, "", "", now.Add(2*time.Minute),
	); err != nil {
		t.Fatalf("complete first logical release: %v", err)
	}
	blocked := cases[blockedIndex]
	last, _, err := repository.PrepareMOCompensation(
		ctx, paymentapp.MOCompensationRequest{
			AgencyOrderID: fundingOrderID, MerchantOrderID: blocked.merchantOrderID,
			AllocationID:   blocked.allocationID,
			Cause:          domain.MOCompensationCustomerCancelPreEffect,
			IdempotencyKey: blocked.key,
		}, blocked.compensationID, blocked.operationID, now.Add(3*time.Minute),
	)
	if err != nil || !last.ProviderVoidRequired ||
		last.OperationState != domain.OperationPrepared {
		t.Fatalf("last release did not own residual void: execution=%+v err=%v", last, err)
	}
}

// seedMOAccountingOrder creates one reconciled/captured/purchased MO and one
// uncaptured MO sharing a full-order PayPal authorization.
func seedMOAccountingOrder(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	now time.Time,
) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed MO accounting fixture: %v", err)
		}
	}
	exec(`INSERT INTO users(id,status,created_at,updated_at) VALUES
		($1,'ACTIVE',$3,$3),($2,'ACTIVE',$3,$3)`, fundingUserID, fundingOperatorID, now)
	exec(`INSERT INTO shipping_snapshots(
		id,user_id,profile_version,country,masked_summary,encrypted_payload,
		payload_nonce,key_version,snapshot_hmac,created_at,source_kind
	) VALUES($1,$2,1,'US','F*** O**, US',decode('00','hex'),decode('00','hex'),
		1,'hmac',$3,'ORDER_SHEET_INPUT')`, fundingShippingID, fundingUserID, now)
	exec(`INSERT INTO agency_order_sheet_sessions(
		id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
		state,version,creation_key_hash,creation_request_hash,snapshot,
		created_at,expires_at,updated_at
	) VALUES($1,$2,gen_random_uuid(),1,'funding-cart','CONSUMED',1,
		'funding-key','funding-request','{}',$3,$3::timestamptz + interval '20 minutes',$3)`,
		fundingSessionID, fundingUserID, now)
	exec(`INSERT INTO agency_orders(
		id,user_id,order_sheet_session_id,source_cart_id,source_cart_version,
		source_cart_snapshot_hash,shipping_snapshot_id,snapshot_hash,idempotency_key_hash,
		status,customer_payable_minor,currency,snapshot,payment_rail,
		provider_environment,asset,economic_effect,merchant_execution_mode,
		execution_profile_hash,issued_at,expires_at
	) VALUES($1,$2,$3,gen_random_uuid(),1,'funding-cart',$4,'funding-order-hash',
		'funding-idempotency','ISSUED',12708,'USD','{}','PAYPAL','SANDBOX','USD',
		'NO_REAL_VALUE','SIMULATED_NO_EFFECT',$5,$6,$6::timestamptz + interval '20 minutes')`,
		fundingOrderID, fundingUserID, fundingSessionID, fundingShippingID,
		fundingSandboxHash, now)
	exec(`INSERT INTO agency_order_mo_allocations(
		id,agency_order_id,checkout_ordinal,merchant_id,shop_domain,
		pass_through_minor,fee_variable_minor,fee_fixed_minor,fee_total_minor,
		customer_gross_minor,currency,fee_policy_version,allocation_hash,
		execution_profile_hash,created_at
	) VALUES
		($1,$3,1,'merchant-a','shop-a.example',10000,540,30,570,10570,'USD',
			 'PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1','0x'||repeat('11',32),$4,$5),
		($2,$3,2,'merchant-b','shop-b.example',2000,108,30,138,2138,'USD',
			 'PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1','0x'||repeat('22',32),$4,$5)`,
		fundingAllocationA, fundingAllocationB, fundingOrderID, fundingSandboxHash, now)
	exec(`INSERT INTO payment_customer_payments(
		id,agency_order_id,user_id,rail,provider_environment,asset,economic_effect,
		amount_minor,currency,state,merchant_execution_mode,execution_profile_hash,
		version,created_at,updated_at
	) VALUES($1,$2,$3,'PAYPAL','SANDBOX','USD','NO_REAL_VALUE',12708,'USD',
		'PARTIALLY_CAPTURED','SIMULATED_NO_EFFECT',$4,1,$5,$5)`,
		fundingCustomerPay, fundingOrderID, fundingUserID, fundingSandboxHash, now)
	exec(`INSERT INTO payment_paypal_attempts(
		id,customer_payment_id,sequence,state,paypal_order_id,return_nonce,
		version,created_at,updated_at
	) VALUES($1,$2,1,'AUTHORIZE_COMPLETED','PAYPAL-ORDER-FUNDING',
		'funding-return-nonce',1,$3,$3)`, fundingAttemptID, fundingCustomerPay, now)
	exec(`INSERT INTO payment_paypal_authorizations(
		id,customer_payment_id,agency_order_id,paypal_attempt_id,rail,
		provider_environment,paypal_order_id,payee_merchant_id,paypal_authorization_id,
		amount_minor,currency,execution_profile_hash,state,version,
		authorized_at,honor_refreshed_at,created_at,updated_at
	) VALUES($1,$2,$3,$4,'PAYPAL','SANDBOX','PAYPAL-ORDER-FUNDING','MERCHANT-1','AUTH-FUNDING',
		12708,'USD',$5,'PARTIALLY_CAPTURED',1,$6,$6,$6,$6)`,
		fundingAuthorizeID, fundingCustomerPay, fundingOrderID, fundingAttemptID,
		fundingSandboxHash, now)
	exec(`INSERT INTO payment_mo_funding_positions(
		id,allocation_id,agency_order_id,customer_payment_id,paypal_authorization_id,
		rail,source,provider_environment,amount_minor,currency,execution_profile_hash,
		state,version,available_at,activated_at,created_at,updated_at
	) VALUES
		($1,$3,$5,$6,$7,'PAYPAL','PAYPAL_AUTHORIZATION','SANDBOX',10570,'USD',$8,
		 'ACTIVE',1,$9,$9,$9,$9),
		($2,$4,$5,$6,$7,'PAYPAL','PAYPAL_AUTHORIZATION','SANDBOX',2138,'USD',$8,
		 'AVAILABLE',1,$9,NULL,$9,$9)`,
		fundingPositionA, fundingPositionB, fundingAllocationA, fundingAllocationB,
		fundingOrderID, fundingCustomerPay, fundingAuthorizeID, fundingSandboxHash, now)
	exec(`INSERT INTO payment_mo_cash_receipts(
		id,funding_position_id,allocation_id,agency_order_id,customer_payment_id,
		paypal_authorization_id,provider_environment,kind,provider_capture_id,
		gross_minor,economics_reconciled,processor_fee_minor,net_receivable_minor,
		currency,execution_profile_hash,occurred_at,created_at
	) VALUES($1,$2,$3,$4,$5,$6,'SANDBOX','PAYPAL_CAPTURE','CAPTURE-FUNDING-1',
		10570,TRUE,500,10070,'USD',$7,$8,$8)`,
		fundingReceiptID, fundingPositionA, fundingAllocationA, fundingOrderID,
		fundingCustomerPay, fundingAuthorizeID, fundingSandboxHash, now.Add(time.Minute))
	exec(`INSERT INTO procurement_manifests(
		id,agency_order_id,funds_receipt_id,customer_payment_id,snapshot_hash,
		execution_profile_hash,created_at
	) VALUES($1,$2,NULL,$3,'funding-manifest-hash',$4,$5)`,
		fundingManifestID, fundingOrderID, fundingCustomerPay, fundingSandboxHash, now)
	exec(`INSERT INTO merchant_orders(
		id,agency_order_id,manifest_id,merchant_id,shop_domain,checkout_ordinal,
		checkout_snapshot,execution_mode,state,result_hash,version,created_at,updated_at,
		allocation_id,external_order_ref,placement_evidence_kind,
		placement_receipt_safe_ref,placement_actual_amount_minor,
		placement_evidence_source,placement_evidence_hash,placement_observed_at,
		placement_recorded_by_user_id,placement_recorded_at
	) VALUES
		($1,$3,$4,'merchant-a','shop-a.example',1,'{}','SIMULATED_NO_EFFECT',
		 'PLACED','sandbox:merchant-a',1,$5,$5,$6,'TEST-ORDER-A',
		 'SANDBOX_TEST_EVIDENCE','receipt:test:a',10000,'RECEIPT',
		 'sandbox-evidence-hash-a',$5,$8,$5),
		($2,$3,$4,'merchant-b','shop-b.example',2,'{}','SIMULATED_NO_EFFECT',
		 'PLANNED',NULL,1,$5,$5,$7,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL)`,
		fundingMerchantAID, fundingMerchantBID, fundingOrderID, fundingManifestID,
		now, fundingAllocationA, fundingAllocationB, fundingOperatorID)
	exec(`UPDATE payment_mo_funding_positions fp SET merchant_order_id=mo.id FROM merchant_orders mo WHERE mo.allocation_id=fp.allocation_id`)
	exec(`INSERT INTO merchant_payments(
		id,merchant_order_id,agency_order_id,amount_minor,currency,state,version,
		created_at,updated_at
	) VALUES
		(md5($1||':payment')::uuid,$1::uuid,$3,10000,'USD','SUCCEEDED',1,$4,$4),
		(md5($2||':payment')::uuid,$2::uuid,$3,2000,'USD','PLANNED',1,$4,$4)`,
		fundingMerchantAID, fundingMerchantBID, fundingOrderID, now)
	exec(`INSERT INTO procurement_recovery_entries(
		id,merchant_order_id,agency_order_id,cause,expected_amount_minor,
		received_amount_minor,state,evidence_ref,note,version,created_at,updated_at
	) VALUES($1,$2,$3,'COST_ADJUSTMENT',100,100,'RECEIVED','recovery-evidence',
		'Price adjustment',1,$4,$4)`,
		fundingRecoveryID, fundingMerchantAID, fundingOrderID, now.Add(2*time.Minute))
}

func openIsolatedFundingDatabase(
	t *testing.T,
	ctx context.Context,
	migrationDirectory string,
) *sharedpostgres.Database {
	t.Helper()
	if testdb.Enabled() && migrationDirectory == fundingMigrationDirectory(t) {
		base := os.Getenv("TEST_DATABASE_URL")
		if base == "" {
			t.Skip("TEST_DATABASE_URL is not set")
		}
		return testdb.Open(t, ctx, base, migrationDirectory)
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
	databaseName := "vitlane_order_funding_" + hex.EncodeToString(suffix)
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
	if err := isolated.Migrate(ctx, migrationDirectory); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}
	return isolated
}

func fundingMigrationDirectory(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve funding integration test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../../../migrations"))
}

func fundingMigrationDirectoryThrough(
	t *testing.T,
	sourceDirectory string,
	lastUpMigration string,
) string {
	t.Helper()
	targetDirectory := t.TempDir()
	entries, err := os.ReadDir(sourceDirectory)
	if err != nil {
		t.Fatalf("read migration directory: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") || name > lastUpMigration {
			continue
		}
		body, err := os.ReadFile(filepath.Join(sourceDirectory, name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(targetDirectory, name), body, 0o600); err != nil {
			t.Fatalf("copy migration %s: %v", name, err)
		}
	}
	return targetDirectory
}
