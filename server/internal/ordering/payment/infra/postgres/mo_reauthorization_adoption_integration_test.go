package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

func TestMOReauthorizationAdoptionRepairsExpiredResourceLessOperationExactly(
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	originalAuthorizedAt := time.Date(2026, 8, 1, 2, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, originalAuthorizedAt)
	repository := NewRepository(database)
	firstSentAt := originalAuthorizedAt.Add(4 * 24 * time.Hour)
	deadline := firstSentAt.Add(72 * time.Hour)

	providerPlan, err := repository.PrepareMOReauthorization(
		ctx, fundingMerchantBID,
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee5101", firstSentAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkMOReauthorizationSent(
		ctx, providerPlan, firstSentAt, deadline,
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordMOReauthorizationOutcome(
		ctx, providerPlan, domain.OperationUnknown, "", 0, "", time.Time{},
		"PROVIDER_RESPONSE_LOST", firstSentAt.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}

	request := paymentapp.PayPalReauthorizationAdoption{
		ID:                      "eeeeeeee-eeee-4eee-8eee-eeeeeeee5102",
		MerchantOrderID:         fundingMerchantBID,
		ProviderAuthorizationID: "AUTH-FUNDING-MANUALLY-FOUND",
		ActorUserID:             fundingOperatorID,
		EvidenceSource:          paymentapp.PayPalReauthorizationEvidenceDashboard,
		EvidenceHash:            "0x" + strings.Repeat("51", 32),
		InternalNote:            "PayPal dashboard authorization details",
		ObservedAt:              deadline.Add(time.Minute),
		IdempotencyKeyHash:      "0x" + strings.Repeat("52", 32),
		RequestHash:             "0x" + strings.Repeat("53", 32),
		CreatedAt:               deadline.Add(-time.Nanosecond),
	}
	if _, _, err := repository.PrepareMOReauthorizationAdoption(
		ctx, fundingMerchantBID, request,
	); !errors.Is(err, domain.ErrFundingOutcomeUnknown) {
		t.Fatalf("adoption before original deadline error=%v want outcome unknown", err)
	}

	request.CreatedAt = deadline.Add(time.Minute)
	plan, existing, err := repository.PrepareMOReauthorizationAdoption(
		ctx, fundingMerchantBID, request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if existing != nil || plan.OperationID != providerPlan.OperationID ||
		plan.OperationState != domain.OperationUnknown ||
		plan.PreviousProviderAuthorizationID != "AUTH-FUNDING" ||
		plan.RemainingCapturableMinor != 2138 ||
		!plan.FirstSentAt.Equal(firstSentAt) ||
		!plan.IdempotencyDeadline.Equal(deadline) {
		t.Fatalf("wrong manual adoption plan: existing=%+v plan=%+v", existing, plan)
	}

	providerCreatedAt := firstSentAt.Add(time.Minute)
	request.TargetFundingPositionID = plan.TargetPositionID
	request.PayPalAuthorizationID = plan.AuthorizationID
	request.OperationID = plan.OperationID
	request.ProviderEnvironment = plan.ProviderEnvironment
	request.PreviousProviderAuthorizationID = plan.PreviousProviderAuthorizationID
	request.ProviderStatus = "CREATED"
	request.OperationState = domain.OperationSucceeded
	request.AmountMinor = plan.RemainingCapturableMinor
	request.Currency = plan.Currency
	request.PayPalOrderID = plan.PayPalOrderID
	request.PayeeMerchantID = plan.PayeeMerchantID
	request.ProviderCreatedAt = providerCreatedAt
	request.OperationFirstSentAt = plan.FirstSentAt
	request.OperationIdempotencyDeadline = plan.IdempotencyDeadline
	request.OriginalAuthorizedAt = plan.OriginalAuthorizedAt
	mismatched := request
	mismatched.AmountMinor++
	if _, _, err := repository.RecordMOReauthorizationAdoption(
		ctx, plan, mismatched, deadline.Add(2*time.Minute),
	); !errors.Is(err, domain.ErrInstructionMismatch) {
		t.Fatalf("mismatched adoption error=%v want instruction mismatch", err)
	}
	var unchangedProviderID, unchangedOperationState string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT paypal_authorization.paypal_authorization_id,
		       external_operation.state
		FROM payment_paypal_authorizations paypal_authorization
		JOIN payment_external_operations external_operation
		  ON external_operation.id=$2
		WHERE paypal_authorization.id=$1
	`, fundingAuthorizeID, providerPlan.OperationID).Scan(
		&unchangedProviderID, &unchangedOperationState,
	); err != nil {
		t.Fatal(err)
	}
	if unchangedProviderID != "AUTH-FUNDING" ||
		unchangedOperationState != string(domain.OperationUnknown) {
		t.Fatalf("mismatched adoption mutated state: provider=%s operation=%s",
			unchangedProviderID, unchangedOperationState)
	}
	recorded, replay, err := repository.RecordMOReauthorizationAdoption(
		ctx, plan, request, deadline.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if replay || recorded.ID != request.ID ||
		recorded.ProviderAuthorizationID != request.ProviderAuthorizationID {
		t.Fatalf("wrong recorded adoption: replay=%v adoption=%+v", replay, recorded)
	}

	var currentProviderID, operationState, operationResourceID, positionState string
	var honorRefreshedAt time.Time
	var count, historyCount, adoptionCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT paypal_authorization.paypal_authorization_id,
		       paypal_authorization.honor_refreshed_at,
		       paypal_authorization.reauthorization_count,
		       external_operation.state,external_operation.provider_resource_id,
		       funding_position.state
		FROM payment_paypal_authorizations paypal_authorization
		JOIN payment_external_operations external_operation
		  ON external_operation.id=$2
		JOIN payment_mo_funding_positions funding_position
		  ON funding_position.id=$3
		WHERE paypal_authorization.id=$1
	`, fundingAuthorizeID, providerPlan.OperationID, fundingPositionB).Scan(
		&currentProviderID, &honorRefreshedAt, &count,
		&operationState, &operationResourceID, &positionState,
	); err != nil {
		t.Fatal(err)
	}
	if currentProviderID != request.ProviderAuthorizationID ||
		!honorRefreshedAt.Equal(providerCreatedAt) || count != 1 ||
		operationState != string(domain.OperationSucceeded) ||
		operationResourceID != request.ProviderAuthorizationID ||
		positionState != string(domain.MOFundingAvailable) {
		t.Fatalf("manual adoption did not atomically advance exact state: provider=%s honor=%s count=%d operation=%s resource=%s position=%s",
			currentProviderID, honorRefreshedAt, count, operationState,
			operationResourceID, positionState)
	}
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM payment_paypal_reauthorizations
		WHERE operation_id=$1 AND provider_authorization_id=$2
	`, providerPlan.OperationID, request.ProviderAuthorizationID).Scan(
		&historyCount,
	); err != nil {
		t.Fatal(err)
	}
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM payment_paypal_reauthorization_adoptions
		WHERE id=$1 AND operation_id=$2 AND provider_authorization_id=$3
		  AND amount_minor=2138 AND currency='USD'
		  AND paypal_order_id='PAYPAL-ORDER-FUNDING'
		  AND payee_merchant_id='MERCHANT-1'
		  AND operation_first_sent_at=$4
		  AND operation_idempotency_deadline=$5
		  AND original_authorized_at=$6
	`, request.ID, providerPlan.OperationID, request.ProviderAuthorizationID,
		firstSentAt, deadline, originalAuthorizedAt).Scan(&adoptionCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 1 || adoptionCount != 1 {
		t.Fatalf("history=%d immutable adoption=%d", historyCount, adoptionCount)
	}

	_, existing, err = repository.PrepareMOReauthorizationAdoption(
		ctx, fundingMerchantBID, request,
	)
	if err != nil || existing == nil || existing.ID != request.ID {
		t.Fatalf("same-key replay existing=%+v error=%v", existing, err)
	}
	changed := request
	changed.RequestHash = "0x" + strings.Repeat("54", 32)
	if _, _, err := repository.PrepareMOReauthorizationAdoption(
		ctx, fundingMerchantBID, changed,
	); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("changed same-key request error=%v want conflict", err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE payment_paypal_reauthorization_adoptions
		SET internal_note='changed' WHERE id=$1
	`, request.ID); err == nil {
		t.Fatal("immutable reauthorization adoption allowed UPDATE")
	}
	if _, err := database.DB.ExecContext(ctx, `
		DELETE FROM payment_paypal_reauthorization_adoptions WHERE id=$1
	`, request.ID); err == nil {
		t.Fatal("immutable reauthorization adoption allowed DELETE")
	}
}
