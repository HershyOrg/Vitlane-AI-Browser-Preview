package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	accountdomain "github.com/vitlane/vitlane/server/internal/account/domain"
	accountpostgres "github.com/vitlane/vitlane/server/internal/account/infra/postgres"
	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

func TestExpiredUnknownPayPalRefundCanBeExactlyAdoptedAndReconciledAgainstPostgres(
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 6, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	repository := NewRepository(database)
	const (
		compensationID = "eeeeeeee-eeee-4eee-8eee-eeeeeeee5001"
		operationID    = "ffffffff-ffff-4fff-8fff-ffffffff5001"
		adoptionID     = "dddddddd-dddd-4ddd-8ddd-dddddddd5001"
		refundID       = "PAYPAL-REFUND-ADOPTED-1"
	)
	execution, _, err := repository.PrepareMOCompensation(
		ctx, paymentapp.MOCompensationRequest{
			AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantAID,
			AllocationID: fundingAllocationA,
			Cause:        domain.MOCompensationDeliveryException,
			IdempotencyKey: "payment.execute_mo_compensation.v1:" +
				fundingOrderID + ":refund-adoption",
		}, compensationID, operationID, now.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	firstSentAt := now.Add(2 * time.Minute)
	deadline := now.Add(3 * time.Minute)
	if err := repository.MarkMOCompensationSent(
		ctx, execution, firstSentAt, deadline,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RecordMOCompensationOutcome(
		ctx, execution, domain.MOCompensationOutcomeUnknown, domain.OperationUnknown,
		"", "NETWORK_TIMEOUT", now.Add(150*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PreparePayPalMORefundAdoption(
		ctx, compensationID, refundID, deadline.Add(-time.Nanosecond),
	); !errors.Is(err, domain.ErrPayPalRefundAdoptionMissing) {
		t.Fatalf("pre-deadline adoption error=%v", err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_external_operations SET provider_resource_id=$2 WHERE id=$1
	`, operationID, refundID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PreparePayPalMORefundAdoption(
		ctx, compensationID, refundID, deadline.Add(time.Nanosecond),
	); !errors.Is(err, domain.ErrPayPalRefundAdoptionMissing) {
		t.Fatalf("known operation resource adoption error=%v", err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_external_operations SET provider_resource_id=NULL WHERE id=$1
	`, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_mo_compensations SET provider_resource_id=$2 WHERE id=$1
	`, compensationID, refundID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PreparePayPalMORefundAdoption(
		ctx, compensationID, refundID, deadline.Add(time.Nanosecond),
	); !errors.Is(err, domain.ErrPayPalRefundAdoptionMissing) {
		t.Fatalf("known compensation resource adoption error=%v", err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_mo_compensations SET provider_resource_id=NULL WHERE id=$1
	`, compensationID); err != nil {
		t.Fatal(err)
	}
	plan, err := repository.PreparePayPalMORefundAdoption(
		ctx, compensationID, refundID, deadline.Add(time.Nanosecond),
	)
	if err != nil || plan.Execution.OperationID != operationID ||
		plan.Execution.OperationIdempotencyKey == "" ||
		plan.Execution.ProviderCaptureID != "CAPTURE-FUNDING-1" ||
		plan.Execution.OperationResourceID != "" ||
		!plan.OperationFirstSentAt.Equal(firstSentAt) ||
		!plan.IdempotencyDeadline.Equal(deadline) {
		t.Fatalf("expired refund adoption plan=%+v err=%v", plan, err)
	}
	adoption := domain.PayPalRefundAdoption{
		ID: adoptionID, CompensationID: compensationID, OperationID: operationID,
		ProviderEnvironment: "SANDBOX", ProviderRefundID: refundID,
		ProviderStatus: "PENDING", OutcomeState: domain.MOCompensationOutcomeUnknown,
		AmountMinor: 10570, Currency: "USD", ParentCaptureID: "CAPTURE-FUNDING-1",
		InvoiceID:                    plan.Execution.OperationIdempotencyKey,
		OperationFirstSentAt:         plan.OperationFirstSentAt,
		OperationIdempotencyDeadline: plan.IdempotencyDeadline,
		OperatorUserID:               fundingOperatorID,
		PublicRationale:              "PayPal activity identified the refund created by the original request.",
		EvidenceSource:               domain.PayPalRefundAdoptionEvidenceDashboard,
		EvidenceHash:                 "0x" + strings.Repeat("a", 64),
		ObservedAt:                   deadline, RequestHash: "0x" + strings.Repeat("b", 64),
		CreatedAt: deadline.Add(time.Second),
	}
	if err := adoption.Validate(adoption.CreatedAt); err != nil {
		t.Fatal(err)
	}
	pending, recorded, replay, err := repository.RecordPayPalMORefundAdoption(
		ctx, plan, adoption, domain.MOCompensationOutcomeUnknown,
		domain.OperationUnknown, "REFUND_NOT_COMPLETED", adoption.CreatedAt,
	)
	if err != nil || replay || pending.State != domain.MOCompensationOutcomeUnknown ||
		pending.ProviderResourceID != refundID || recorded.ID != adoptionID {
		t.Fatalf("record pending refund adoption: compensation=%+v audit=%+v replay=%v err=%v",
			pending, recorded, replay, err)
	}

	replayPlan, err := repository.PreparePayPalMORefundAdoption(
		ctx, compensationID, refundID, deadline.Add(2*time.Second),
	)
	if err != nil || replayPlan.ExistingAdoption == nil ||
		replayPlan.Execution.OperationResourceID != refundID {
		t.Fatalf("load adopted refund replay plan=%+v err=%v", replayPlan, err)
	}
	completedObservation := adoption
	completedObservation.ProviderStatus = "COMPLETED"
	completedObservation.OutcomeState = domain.MOCompensationSucceeded
	completedObservation.CreatedAt = deadline.Add(3 * time.Second)
	completed, originalAudit, replay, err := repository.RecordPayPalMORefundAdoption(
		ctx, replayPlan, completedObservation, domain.MOCompensationSucceeded,
		domain.OperationSucceeded, "", completedObservation.CreatedAt,
	)
	if err != nil || !replay || completed.State != domain.MOCompensationSucceeded ||
		completed.ProviderResourceID != refundID || originalAudit.ProviderStatus != "PENDING" {
		t.Fatalf("reconcile adopted refund: compensation=%+v audit=%+v replay=%v err=%v",
			completed, originalAudit, replay, err)
	}

	var auditCount, operationCount int
	var operationState, operationResource, fundingState string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM payment_paypal_refund_adoptions
		WHERE compensation_id=$1 AND operation_id=$2
	`, compensationID, operationID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*),min(state),min(COALESCE(provider_resource_id,''))
		FROM payment_external_operations
		WHERE owner_kind='MO_COMPENSATION' AND owner_id=$1
	`, compensationID).Scan(&operationCount, &operationState, &operationResource); err != nil {
		t.Fatal(err)
	}
	if err := database.DB.QueryRowContext(ctx, `
		SELECT state FROM payment_mo_funding_positions WHERE id=$1
	`, fundingPositionA).Scan(&fundingState); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 || operationCount != 1 || operationState != "SUCCEEDED" ||
		operationResource != refundID || fundingState != "RELEASED" {
		t.Fatalf("adoption persistence audit=%d operations=%d/%s/%s funding=%s",
			auditCount, operationCount, operationState, operationResource, fundingState)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_paypal_refund_adoptions SET public_rationale='changed' WHERE id=$1
	`, adoptionID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("refund adoption update error=%v", err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		DELETE FROM payment_paypal_refund_adoptions WHERE id=$1
	`, adoptionID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("refund adoption delete error=%v", err)
	}

	// Account development reset must remove both adoption ledgers before their
	// RESTRICT-owned operations and roots. Seed the sibling adoption explicitly
	// so this refund regression also protects the shared deletion order.
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO payment_external_operations(
			id,purpose,owner_kind,owner_id,idempotency_key,request_hash,state,
			provider_resource_id,first_sent_at,idempotency_deadline,resolved_at,
			created_at,updated_at
		) VALUES(
			'ffffffff-ffff-4fff-8fff-ffffffff5090','PAYPAL_REAUTHORIZE',
			'PAYPAL_AUTHORIZATION',$1,'paypal:reauthorize:reset-adoption:1',
			'reset-adoption-request','SUCCEEDED','AUTH-RESET-ADOPTED',
			$2,$3,$4,$2,$4
		)
	`, fundingAuthorizeID, now.Add(4*time.Minute), now.Add(5*time.Minute),
		now.Add(6*time.Minute)); err != nil {
		t.Fatalf("seed reauthorization adoption operation: %v", err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO payment_paypal_reauthorization_adoptions(
			id,merchant_order_id,target_funding_position_id,paypal_authorization_id,
			operation_id,operation_owner_kind,provider_environment,
			previous_provider_authorization_id,provider_authorization_id,
			provider_status,operation_state,amount_minor,currency,paypal_order_id,
			payee_merchant_id,provider_created_at,actor_user_id,evidence_source,
			evidence_hash,internal_note,observed_at,operation_first_sent_at,
			operation_idempotency_deadline,original_authorized_at,
			idempotency_key_hash,request_hash,created_at
		) VALUES(
			'dddddddd-dddd-4ddd-8ddd-dddddddd5090',$1,$2,$3,
			'ffffffff-ffff-4fff-8fff-ffffffff5090','PAYPAL_AUTHORIZATION','SANDBOX',
			'AUTH-FUNDING','AUTH-RESET-ADOPTED','CREATED','SUCCEEDED',2138,'USD',
			'PAYPAL-ORDER-FUNDING','MERCHANT-1',$4,$5,'PAYPAL_DASHBOARD',
			'0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',
			'reset ordering regression',$6,$7,$8,$9,
			'0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee',
			'0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff',$6
		)
	`, fundingMerchantBID, fundingPositionB, fundingAuthorizeID,
		now.Add(270*time.Second), fundingOperatorID, now.Add(6*time.Minute),
		now.Add(4*time.Minute), now.Add(5*time.Minute), now); err != nil {
		t.Fatalf("seed reauthorization adoption reset edge: %v", err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		DELETE FROM payment_paypal_reauthorization_adoptions
		WHERE id='dddddddd-dddd-4ddd-8ddd-dddddddd5090'
	`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("reauthorization adoption delete error=%v", err)
	}
	accountRepository := accountpostgres.NewRepository(database)
	if err := accountRepository.ResetDevelopmentUser(
		ctx, accountdomain.UserID(fundingUserID), now.Add(7*time.Minute),
	); err != nil {
		t.Fatalf("reset account with PayPal adoption ledgers: %v", err)
	}
	var adoptionRows int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM payment_paypal_refund_adoptions) +
			(SELECT count(*) FROM payment_paypal_reauthorization_adoptions)
	`).Scan(&adoptionRows); err != nil {
		t.Fatal(err)
	}
	if adoptionRows != 0 {
		t.Fatalf("development reset left adoption rows=%d", adoptionRows)
	}
}

func TestExpiredUnknownPayPalRefundAdoptsDefinitiveFailureAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	now := time.Date(2026, 8, 27, 7, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	repository := NewRepository(database)
	const (
		compensationID = "eeeeeeee-eeee-4eee-8eee-eeeeeeee5002"
		operationID    = "ffffffff-ffff-4fff-8fff-ffffffff5002"
		adoptionID     = "dddddddd-dddd-4ddd-8ddd-dddddddd5002"
		refundID       = "PAYPAL-REFUND-FAILED-1"
	)
	execution, _, err := repository.PrepareMOCompensation(
		ctx, paymentapp.MOCompensationRequest{
			AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantAID,
			AllocationID: fundingAllocationA,
			Cause:        domain.MOCompensationCustomerRefundPostEffect,
			IdempotencyKey: "payment.execute_mo_compensation.v1:" +
				fundingOrderID + ":refund-adoption-failed",
		}, compensationID, operationID, now.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(2 * time.Minute)
	if err := repository.MarkMOCompensationSent(ctx, execution, now.Add(time.Minute), deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RecordMOCompensationOutcome(
		ctx, execution, domain.MOCompensationOutcomeUnknown, domain.OperationUnknown,
		"", "NETWORK_TIMEOUT", now.Add(90*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	plan, err := repository.PreparePayPalMORefundAdoption(
		ctx, compensationID, refundID, deadline.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	adoption := domain.PayPalRefundAdoption{
		ID: adoptionID, CompensationID: compensationID, OperationID: operationID,
		ProviderEnvironment: "SANDBOX", ProviderRefundID: refundID,
		ProviderStatus: "FAILED", OutcomeState: domain.MOCompensationFailed,
		AmountMinor: 10570, Currency: "USD", ParentCaptureID: "CAPTURE-FUNDING-1",
		InvoiceID:                    plan.Execution.OperationIdempotencyKey,
		OperationFirstSentAt:         plan.OperationFirstSentAt,
		OperationIdempotencyDeadline: plan.IdempotencyDeadline,
		OperatorUserID:               fundingOperatorID, PublicRationale: "PayPal reports a failed refund.",
		EvidenceSource: domain.PayPalRefundAdoptionEvidenceAPI,
		EvidenceHash:   "0x" + strings.Repeat("c", 64), ObservedAt: deadline,
		RequestHash: "0x" + strings.Repeat("d", 64), CreatedAt: deadline.Add(time.Second),
	}
	failed, _, replay, err := repository.RecordPayPalMORefundAdoption(
		ctx, plan, adoption, domain.MOCompensationFailed,
		domain.OperationFailed, "REFUND_FAILED", adoption.CreatedAt,
	)
	if err != nil || replay || failed.State != domain.MOCompensationFailed ||
		failed.ProviderResourceID != refundID {
		t.Fatalf("failed adoption compensation=%+v replay=%v err=%v", failed, replay, err)
	}
	var fundingState, operationState string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT funding.state,operation.state
		FROM payment_mo_funding_positions funding
		JOIN payment_external_operations operation
		  ON operation.owner_kind='MO_COMPENSATION' AND operation.owner_id=$1
		WHERE funding.id=$2
	`, compensationID, fundingPositionA).Scan(&fundingState, &operationState); err != nil {
		t.Fatal(err)
	}
	if fundingState != "FAILED" || operationState != "FAILED" {
		t.Fatalf("definitive failure funding=%s operation=%s", fundingState, operationState)
	}
}
