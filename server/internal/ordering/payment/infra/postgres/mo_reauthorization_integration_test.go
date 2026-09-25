package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

func resetFundingFixtureToTwoAvailable(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	now time.Time,
) {
	t.Helper()
	tx, err := database.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	statements := []struct {
		query string
		args  []any
	}{
		{`SELECT set_config('vitlane.account_reset','on',true)`, nil},
		{`DELETE FROM procurement_recovery_entries WHERE agency_order_id=$1`, []any{fundingOrderID}},
		{`DELETE FROM payment_mo_cash_receipts WHERE funding_position_id=$1`, []any{fundingPositionA}},
		{`UPDATE payment_mo_funding_positions
		  SET state='AVAILABLE',activated_at=NULL,updated_at=$2
		  WHERE id=$1`, []any{fundingPositionA, now}},
		{`UPDATE payment_paypal_authorizations
		  SET state='AUTHORIZED',updated_at=$2 WHERE id=$1`, []any{fundingAuthorizeID, now}},
		{`UPDATE payment_customer_payments
		  SET state='AUTHORIZED',updated_at=$2 WHERE id=$1`, []any{fundingCustomerPay, now}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("reset funding fixture: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestMOReauthorizationUsesAllRemainingCapturablePositions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	authorizedAt := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, authorizedAt)
	resetFundingFixtureToTwoAvailable(t, ctx, database, authorizedAt)
	now := authorizedAt.Add(4 * 24 * time.Hour)
	repository := NewRepository(database)

	plan, err := repository.PrepareMOReauthorization(
		ctx, fundingMerchantBID,
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee4101", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Required || plan.TargetPositionID != fundingPositionB ||
		plan.AuthorizedAmountMinor != 12708 ||
		plan.RemainingCapturableMinor != 12708 {
		t.Fatalf("reauthorization did not bind all remaining positions: %+v", plan)
	}

	if _, _, err := repository.PrepareMOFundingActivation(
		ctx, fundingMerchantBID,
		domain.ExternalOperation{ID: "eeeeeeee-eeee-4eee-8eee-eeeeeeee4102"}, now,
	); !errors.Is(err, domain.ErrFundingOutcomeUnknown) {
		t.Fatalf("capture escaped open reauthorization: %v", err)
	}
	if _, _, err := repository.PrepareMOCompensation(
		ctx, paymentapp.MOCompensationRequest{
			AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantBID,
			AllocationID:   fundingAllocationB,
			Cause:          domain.MOCompensationCustomerCancelPreEffect,
			IdempotencyKey: "compensate:reauthorization:blocked",
		},
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee4103",
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee4104", now,
	); !errors.Is(err, domain.ErrCompensationOutcomeUnknown) {
		t.Fatalf("release escaped open reauthorization: %v", err)
	}

	if err := repository.MarkMOReauthorizationSent(
		ctx, plan, now, now.Add(72*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkMOReauthorizationSent(
		ctx, plan, now, now.Add(72*time.Hour),
	); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second sender claim error=%v want conflict", err)
	}
	refreshedAt := now.Add(time.Minute)
	if err := repository.RecordMOReauthorizationOutcome(
		ctx, plan, domain.OperationSucceeded, "AUTH-FUNDING-REFRESHED",
		12707, "USD", refreshedAt, "", refreshedAt,
	); !errors.Is(err, domain.ErrInstructionMismatch) {
		t.Fatalf("mismatched provider amount error=%v want instruction mismatch", err)
	}
	if err := repository.RecordMOReauthorizationOutcome(
		ctx, plan, domain.OperationSucceeded, "AUTH-FUNDING-REFRESHED",
		12708, "USD", refreshedAt, "", refreshedAt,
	); err != nil {
		t.Fatal(err)
	}
}

func TestMOReauthorizationBlocksSiblingPendingAndUnknownStates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	authorizedAt := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, authorizedAt)
	resetFundingFixtureToTwoAvailable(t, ctx, database, authorizedAt)
	now := authorizedAt.Add(4 * 24 * time.Hour)
	repository := NewRepository(database)

	states := []domain.MOFundingState{
		domain.MOFundingActivationPending,
		domain.MOFundingActivationUnknown,
		domain.MOFundingReleasePending,
		domain.MOFundingReleaseUnknown,
	}
	for index, state := range states {
		if _, err := database.DB.ExecContext(ctx, `
			UPDATE payment_mo_funding_positions SET state=$2,updated_at=$3 WHERE id=$1
		`, fundingPositionA, state, now); err != nil {
			t.Fatal(err)
		}
		_, err := repository.PrepareMOReauthorization(
			ctx, fundingMerchantBID,
			[]string{
				"eeeeeeee-eeee-4eee-8eee-eeeeeeee4201",
				"eeeeeeee-eeee-4eee-8eee-eeeeeeee4202",
				"eeeeeeee-eeee-4eee-8eee-eeeeeeee4203",
				"eeeeeeee-eeee-4eee-8eee-eeeeeeee4204",
			}[index], now,
		)
		if !errors.Is(err, domain.ErrFundingOutcomeUnknown) {
			t.Fatalf("state=%s error=%v want funding outcome unknown", state, err)
		}
		if _, err := database.DB.ExecContext(ctx, `
			UPDATE payment_mo_funding_positions SET state='AVAILABLE',updated_at=$2 WHERE id=$1
		`, fundingPositionA, now); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMOReauthorizationCannotAdoptSuccessAfterFailedCAS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	authorizedAt := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, authorizedAt)
	now := authorizedAt.Add(4 * 24 * time.Hour)
	repository := NewRepository(database)
	plan, err := repository.PrepareMOReauthorization(
		ctx, fundingMerchantBID,
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee4301", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkMOReauthorizationSent(
		ctx, plan, now, now.Add(72*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordMOReauthorizationOutcome(
		ctx, plan, domain.OperationFailed, "", 0, "", time.Time{},
		"REAUTHORIZATION_DECLINED", now,
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordMOReauthorizationOutcome(
		ctx, plan, domain.OperationSucceeded, "AUTH-FUNDING-LATE",
		2138, "USD", now.Add(time.Minute), "", now.Add(time.Minute),
	); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("late success error=%v want conflict", err)
	}
	var operationState, providerAuthorizationID, positionState string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT op.state,auth.paypal_authorization_id,position.state
		FROM payment_external_operations op
		JOIN payment_paypal_authorizations auth ON auth.id=op.owner_id
		JOIN payment_mo_funding_positions position
		  ON position.id=$2
		WHERE op.id=$1
	`, plan.OperationID, plan.TargetPositionID).Scan(
		&operationState, &providerAuthorizationID, &positionState,
	); err != nil {
		t.Fatal(err)
	}
	if operationState != string(domain.OperationFailed) ||
		providerAuthorizationID != "AUTH-FUNDING" ||
		positionState != string(domain.MOFundingFailed) {
		t.Fatalf("terminal reauthorization was not closed: operation=%s provider=%s position=%s",
			operationState, providerAuthorizationID, positionState)
	}
}

func TestMOReauthorizationProjectsNaturalAuthorizationExpiry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	authorizedAt := time.Date(2026, 7, 1, 2, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, authorizedAt)
	resetFundingFixtureToTwoAvailable(t, ctx, database, authorizedAt)
	now := authorizedAt.Add(29 * 24 * time.Hour)
	repository := NewRepository(database)

	plan, err := repository.PrepareMOReauthorization(
		ctx, fundingMerchantBID,
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee4401", now,
	)
	if err != nil || !plan.Expired || plan.Required {
		t.Fatalf("expiry plan=%+v err=%v", plan, err)
	}
	var authorizationState, positionState string
	var terminalAt time.Time
	if err := database.DB.QueryRowContext(ctx, `
		SELECT paypal_authorization.state,paypal_authorization.terminal_at,funding_position.state
		FROM payment_paypal_authorizations paypal_authorization
		JOIN payment_mo_funding_positions funding_position ON funding_position.id=$2
		WHERE paypal_authorization.id=$1
	`, fundingAuthorizeID, fundingPositionB).Scan(
		&authorizationState, &terminalAt, &positionState,
	); err != nil {
		t.Fatal(err)
	}
	if authorizationState != string(domain.AuthorizationExpired) ||
		positionState != string(domain.MOFundingFailed) || !terminalAt.Equal(now) {
		t.Fatalf("expiry projection auth=%s terminal=%s position=%s",
			authorizationState, terminalAt, positionState)
	}

	replay, err := repository.PrepareMOReauthorization(
		ctx, fundingMerchantBID,
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee4402", now.Add(time.Minute),
	)
	if err != nil || !replay.Expired {
		t.Fatalf("expiry replay=%+v err=%v", replay, err)
	}
}

func TestMOReauthorizationRecoversStaleSendWithSameRequestID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openIsolatedFundingDatabase(t, ctx, fundingMigrationDirectory(t))
	authorizedAt := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, authorizedAt)
	now := authorizedAt.Add(4 * 24 * time.Hour)
	repository := NewRepository(database)
	plan, err := repository.PrepareMOReauthorization(
		ctx, fundingMerchantBID,
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee4501", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkMOReauthorizationSent(
		ctx, plan, now, now.Add(72*time.Hour),
	); err != nil {
		t.Fatal(err)
	}

	recovered, err := repository.PrepareMOReauthorization(
		ctx, fundingMerchantBID,
		"eeeeeeee-eeee-4eee-8eee-eeeeeeee4502",
		now.Add(payPalProviderSendLease+time.Second),
	)
	if err != nil || recovered.OperationState != domain.OperationUnknown ||
		recovered.OperationID != plan.OperationID ||
		recovered.OperationIdempotencyKey != plan.OperationIdempotencyKey {
		t.Fatalf("stale send recovery=%+v err=%v", recovered, err)
	}
	if err := repository.MarkMOReauthorizationSent(
		ctx, recovered, now.Add(payPalProviderSendLease+time.Second),
		now.Add(72*time.Hour),
	); err != nil {
		t.Fatalf("same-key retry claim: %v", err)
	}
}
