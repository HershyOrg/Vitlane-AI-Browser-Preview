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
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	procurementapp "github.com/vitlane/vitlane/server/internal/ordering/procurement/app"
	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

const (
	failedFundingUserID       = "10101010-1010-4010-8010-101010101001"
	failedFundingOperatorID   = "10101010-1010-4010-8010-101010101002"
	recoveryFundingOperatorID = "10101010-1010-4010-8010-101010101003"
	resultFundingOperatorID   = "10101010-1010-4010-8010-101010101004"
	failedFundingShippingID   = "20202020-2020-4020-8020-202020202001"
	failedFundingSessionID    = "30303030-3030-4030-8030-303030303001"
	failedFundingSourceCart   = "30303030-3030-4030-8030-303030303002"
	failedFundingOrderID      = "40404040-4040-4040-8040-404040404001"
	failedFundingPaymentID    = "50505050-5050-4050-8050-505050505001"
	failedFundingAttemptID    = "60606060-6060-4060-8060-606060606001"
	failedFundingAuthID       = "70707070-7070-4070-8070-707070707001"
	failedFundingAllocation   = "80808080-8080-4080-8080-808080808001"
	failedFundingPositionID   = "90909090-9090-4090-8090-909090909001"
	failedFundingProfileHash  = "0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665"
)

var errTerminalSiblingReauthorization = errors.New("terminal sibling reauthorization")

type failedFundingClock struct{ now time.Time }

func (clock failedFundingClock) Now() time.Time { return clock.now }

type failedFundingActivator struct{}

func (failedFundingActivator) ActivateMerchantOrderFunding(
	context.Context, string, string,
) (ownerFundingTestResult, error) {
	return ownerFundingTestResult{
		PositionID: failedFundingPositionID,
		State:      "FAILED",
	}, errTerminalSiblingReauthorization
}

func TestRecoveryLookupAcceptsAgencyOrderAndMerchantOrderID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openFailedFundingDatabase(t, ctx)
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	seedFailedFundingOrder(t, ctx, database, now)
	repository := NewRepository(database)
	if err := repository.PlanFromFunding(ctx, failedFundingOrderID, fundingProof(), now); err != nil {
		t.Fatal(err)
	}
	var merchantOrderID string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT id::text FROM merchant_orders WHERE allocation_id=$1
	`, failedFundingAllocation).Scan(&merchantOrderID); err != nil {
		t.Fatal(err)
	}

	byOrder, err := repository.ListRecoveryEntries(ctx, failedFundingOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if byOrder.AgencyOrderID != failedFundingOrderID || byOrder.MatchedBy != "AGENCY_ORDER" ||
		byOrder.FocusMerchantOrderID != "" || len(byOrder.MerchantOrders) != 1 {
		t.Fatalf("order lookup=%+v", byOrder)
	}
	byMerchantOrder, err := repository.ListRecoveryEntries(ctx, merchantOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if byMerchantOrder.AgencyOrderID != byOrder.AgencyOrderID ||
		byMerchantOrder.MatchedBy != "MERCHANT_ORDER" ||
		byMerchantOrder.FocusMerchantOrderID != merchantOrderID ||
		len(byMerchantOrder.MerchantOrders) != len(byOrder.MerchantOrders) ||
		len(byMerchantOrder.Entries) != len(byOrder.Entries) {
		t.Fatalf("merchant-order lookup=%+v order lookup=%+v", byMerchantOrder, byOrder)
	}
}

type recoveringFundingActivator struct {
	database *sharedpostgres.Database
	calls    int
}

func TestMaterialCustomerRequestPersistsDecisionAndRequestAtomically(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openFailedFundingDatabase(t, ctx)
	now := time.Date(2026, 8, 27, 14, 0, 0, 0, time.UTC)
	seedFailedFundingOrder(t, ctx, database, now)
	repository := NewRepository(database)
	if err := repository.PlanFromFunding(ctx, failedFundingOrderID, fundingProof(), now); err != nil {
		t.Fatal(err)
	}
	var taskID, merchantOrderID string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT task.id::text, merchant_order.id::text
		FROM merchant_order_execution_tasks task
		JOIN merchant_orders merchant_order ON merchant_order.id=task.merchant_order_id
		WHERE merchant_order.allocation_id=$1
	`, failedFundingAllocation).Scan(&taskID, &merchantOrderID); err != nil {
		t.Fatal(err)
	}
	claimedAt := now.Add(time.Minute)
	if _, replay, err := repository.ClaimTask(
		ctx, taskID, failedFundingOperatorID, "material-request-claim",
		claimedAt.Add(time.Hour), claimedAt,
	); err != nil || replay {
		t.Fatalf("claim: replay=%v err=%v", replay, err)
	}
	observedAt := claimedAt.Add(time.Minute)
	request, replay, err := repository.CreateCustomerRequest(
		ctx, taskID, failedFundingOperatorID, "material-request-command",
		"The merchant now requires a different delivery condition.",
		"Internal follow-up note.", domain.EvidenceMerchantPage,
		"material-request-evidence-hash", observedAt,
		domain.RequestConsent, "Proceed with the new delivery condition?",
		domain.ResponseBooleanConsent, nil,
		"A new delivery condition requires your approval before purchase.",
		observedAt.Add(7*24*time.Hour), observedAt,
	)
	if err != nil || replay {
		t.Fatalf("create material request: replay=%v err=%v", replay, err)
	}
	var decision, requestMerchantOrderID string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT decision.decision, request.merchant_order_id::text
		FROM procurement_customer_requests request
		JOIN procurement_decision_records decision
		  ON decision.id=request.source_decision_id
		WHERE request.id=$1
	`, request.ID).Scan(
		&decision, &requestMerchantOrderID,
	); err != nil {
		t.Fatal(err)
	}
	if decision != string(domain.DecisionMaterialCondition) ||
		requestMerchantOrderID != merchantOrderID {
		t.Fatalf("combined owner facts mismatch: decision=%s merchantOrder=%s",
			decision, requestMerchantOrderID)
	}
	if _, replay, err = repository.CreateCustomerRequest(
		ctx, taskID, failedFundingOperatorID, "material-request-command",
		"The merchant now requires a different delivery condition.",
		"Internal follow-up note.", domain.EvidenceMerchantPage,
		"material-request-evidence-hash", observedAt,
		domain.RequestConsent, "Proceed with the new delivery condition?",
		domain.ResponseBooleanConsent, nil,
		"A new delivery condition requires your approval before purchase.",
		observedAt.Add(7*24*time.Hour), observedAt,
	); err != nil || !replay {
		t.Fatalf("combined command replay: replay=%v err=%v", replay, err)
	}
	if _, _, err = repository.CreateCustomerRequest(
		ctx, taskID, failedFundingOperatorID, "material-request-conflict",
		"The merchant now requires another material condition.",
		"This attempt must roll back with the request.", domain.EvidenceMerchantPage,
		"material-request-conflict-hash", observedAt.Add(time.Minute),
		domain.RequestInformation, "Please provide another preference.",
		domain.ResponseText, nil,
		"A second open request is not allowed for this merchant order.",
		observedAt.Add(7*24*time.Hour), observedAt.Add(time.Minute),
	); err != domain.ErrOpenCustomerRequest {
		t.Fatalf("second open request error=%v", err)
	}
	var rolledBackDecisionCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM procurement_decision_records
		WHERE idempotency_key='material-request-conflict:decision'
	`).Scan(&rolledBackDecisionCount); err != nil {
		t.Fatal(err)
	}
	if rolledBackDecisionCount != 0 {
		t.Fatalf("request conflict left %d partial material decisions", rolledBackDecisionCount)
	}
}

func (activator *recoveringFundingActivator) ActivateMerchantOrderFunding(
	ctx context.Context, _ string, _ string,
) (ownerFundingTestResult, error) {
	activator.calls++
	state := "ACTIVATION_UNKNOWN"
	if activator.calls > 1 {
		state = "ACTIVE"
	}
	if _, err := activator.database.DB.ExecContext(ctx, `
		UPDATE payment_mo_funding_positions
		SET state=$2,version=version+1,updated_at=updated_at + interval '1 second'
		WHERE id=$1
	`, failedFundingPositionID, state); err != nil {
		return ownerFundingTestResult{}, err
	}
	return ownerFundingTestResult{
		PositionID: failedFundingPositionID,
		State:      state,
	}, nil
}

// A PayPal reauthorization failure fans every still-AVAILABLE sibling funding
// position to FAILED. Procurement remains the owner of the corresponding
// MerchantOrder/task projection: when that sibling is opened later, it must
// adopt the terminal funding fact and close the exact MO instead of rejecting
// the begin before an effect lock exists.
func TestFailedSiblingFundingIsAdoptedAndTerminatesProcurement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openFailedFundingDatabase(t, ctx)
	now := time.Date(2026, 8, 27, 15, 0, 0, 0, time.UTC)
	seedFailedFundingOrder(t, ctx, database, now)
	repository := NewRepository(database)

	if err := repository.PlanFromFunding(ctx, failedFundingOrderID, fundingProof(), now); err != nil {
		t.Fatalf("plan sibling MO from authorized funding: %v", err)
	}
	var totalCurrency, taxCurrency, continueURLSafeRef string
	var deliveryGroupCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT checkout_snapshot->'authoritativeTotal'->>'currency',
		       checkout_snapshot->'taxTotal'->>'currency',
		       checkout_snapshot->>'continueUrlSafeRef',
		       jsonb_array_length(checkout_snapshot->'deliveryGroups')
		FROM merchant_orders WHERE allocation_id=$1
	`, failedFundingAllocation).Scan(
		&totalCurrency, &taxCurrency, &continueURLSafeRef, &deliveryGroupCount,
	); err != nil {
		t.Fatal(err)
	}
	if totalCurrency != "USD" || taxCurrency != "USD" ||
		continueURLSafeRef != "checkout-safe-ref" || deliveryGroupCount != 1 {
		t.Fatalf("checkout snapshot was narrowed during funding plan: total=%s tax=%s continue=%s deliveryGroups=%d",
			totalCurrency, taxCurrency, continueURLSafeRef, deliveryGroupCount)
	}
	var merchantOrderID, taskID, unitID string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT merchant_order.id::text,task.id::text,unit.id::text
		FROM merchant_orders merchant_order
		JOIN merchant_order_execution_tasks task
		  ON task.merchant_order_id=merchant_order.id
		JOIN merchant_order_units unit
		  ON unit.merchant_order_id=merchant_order.id
		WHERE merchant_order.allocation_id=$1
	`, failedFundingAllocation).Scan(&merchantOrderID, &taskID, &unitID); err != nil {
		t.Fatal(err)
	}
	failedAt := now.Add(time.Minute)
	execFailedFunding(t, ctx, database, `
		UPDATE payment_mo_funding_positions
		SET state='FAILED',version=version+1,updated_at=$2
		WHERE id=$1
	`, failedFundingPositionID, failedAt)
	execFailedFunding(t, ctx, database, `
		INSERT INTO logistics_expected_units(
			id,merchant_order_unit_id,merchant_order_id,agency_order_id,line_id,
			unit_index,fulfillment,registered_at,version,updated_at
		) VALUES(md5($1||':expected')::uuid,$1::uuid,$2::uuid,$3::uuid,'line-1',1,
			'AWAITING_EFFECT',$4,1,$4)
	`, unitID, merchantOrderID, failedFundingOrderID, failedAt)

	claimedAt := now.Add(2 * time.Minute)
	if _, replay, err := repository.ClaimTask(
		ctx, taskID, failedFundingOperatorID, "failed-funding-claim",
		claimedAt.Add(time.Hour), claimedAt,
	); err != nil || replay {
		t.Fatalf("claim sibling task: replay=%v err=%v", replay, err)
	}
	if _, replay, err := repository.AuthorizeShippingReveal(
		ctx, taskID, failedFundingOperatorID, "PLACE_MERCHANT_ORDER",
		"Review the exact shipping address before the manual merchant purchase.",
		"failed-funding-reveal-correlation", "failed-funding-reveal", claimedAt,
	); err != nil || replay {
		t.Fatalf("authorize sibling shipping reveal: replay=%v err=%v", replay, err)
	}
	if _, replay, err := repository.RecordManualDecision(
		ctx, taskID, failedFundingOperatorID, "failed-funding-decision",
		domain.DecisionWithinAuthorization,
		"The observed merchant conditions remain within the approved scope.",
		"Sibling funding was already made terminal by PayPal reauthorization.",
		"The exact product, option, quantity, and amount were observed.",
		domain.EvidenceMerchantPage, "failed-funding-evidence-hash", claimedAt,
		claimedAt,
	); err != nil || replay {
		t.Fatalf("record sibling decision: replay=%v err=%v", replay, err)
	}
	service := newOwnerPurchasePGDriver(t, database, procurementapp.NewService(
		repository, nil, nil, failedFundingClock{now: claimedAt.Add(time.Minute)}, false,
	))
	service.EnableMerchantOrderFunding(failedFundingActivator{})
	item, replay, err := service.BeginMerchantEffect(
		ctx, taskID, failedFundingOperatorID, "failed-funding-effect",
	)
	if !errors.Is(err, errTerminalSiblingReauthorization) || replay {
		t.Fatalf("begin failed sibling effect: replay=%v err=%v", replay, err)
	}
	if item.MerchantOrder.State != domain.OrderFailed ||
		item.Task.State != domain.TaskFailed || item.Funding.State != "FAILED" {
		t.Fatalf("failed sibling was not projected terminally: %+v", item)
	}

	var lockState, fundingState, paymentState, failureCode string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT effect.state,effect.funding_state,merchant_payment.state,
		       merchant_order.failure_code
		FROM procurement_effect_locks effect
		JOIN merchant_orders merchant_order
		  ON merchant_order.id=effect.merchant_order_id
		JOIN merchant_payments merchant_payment
		  ON merchant_payment.merchant_order_id=merchant_order.id
		WHERE merchant_order.id=$1
	`, merchantOrderID).Scan(
		&lockState, &fundingState, &paymentState, &failureCode,
	); err != nil {
		t.Fatal(err)
	}
	if lockState != "FAILED" || fundingState != "FAILED" ||
		paymentState != "FAILED" || failureCode != "FUNDING_ACTIVATION_FAILED" {
		t.Fatalf("terminal projection lock=%s funding=%s payment=%s failure=%s",
			lockState, fundingState, paymentState, failureCode)
	}

	replayedItem, replay, err := service.BeginMerchantEffect(
		ctx, taskID, failedFundingOperatorID, "failed-funding-effect",
	)
	if !errors.Is(err, errTerminalSiblingReauthorization) || !replay ||
		replayedItem.MerchantOrder.State != domain.OrderFailed ||
		replayedItem.Task.State != domain.TaskFailed {
		t.Fatalf("terminal funding replay changed result: item=%+v replay=%v err=%v",
			replayedItem, replay, err)
	}
}

// A timeout after the technical effect lock is committed is recoverable even
// when the browser retries with a new command key. The one lock and the one MO
// funding position are adopted; only ACTIVE funding opens seller permission.
func TestUnknownFundingEffectLockRecoversWithFreshClientKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openFailedFundingDatabase(t, ctx)
	now := time.Date(2026, 8, 27, 16, 0, 0, 0, time.UTC)
	seedFailedFundingOrder(t, ctx, database, now)
	execFailedFunding(t, ctx, database, `
		INSERT INTO users(id,status,created_at,updated_at) VALUES
			($1,'ACTIVE',$3,$3),($2,'ACTIVE',$3,$3)
	`, recoveryFundingOperatorID, resultFundingOperatorID, now)
	repository := NewRepository(database)
	if err := repository.PlanFromFunding(ctx, failedFundingOrderID, fundingProof(), now); err != nil {
		t.Fatalf("plan MO: %v", err)
	}
	var merchantOrderID, taskID, unitID string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT merchant_order.id::text,task.id::text,unit.id::text
		FROM merchant_orders merchant_order
		JOIN merchant_order_execution_tasks task
		  ON task.merchant_order_id=merchant_order.id
		JOIN merchant_order_units unit
		  ON unit.merchant_order_id=merchant_order.id
		WHERE merchant_order.allocation_id=$1
	`, failedFundingAllocation).Scan(&merchantOrderID, &taskID, &unitID); err != nil {
		t.Fatal(err)
	}
	execFailedFunding(t, ctx, database, `
		INSERT INTO logistics_expected_units(
			id,merchant_order_unit_id,merchant_order_id,agency_order_id,line_id,
			unit_index,fulfillment,registered_at,version,updated_at
		) VALUES(md5($1||':expected')::uuid,$1::uuid,$2::uuid,$3::uuid,'line-1',1,
			'AWAITING_EFFECT',$4,1,$4)
	`, unitID, merchantOrderID, failedFundingOrderID, now)
	claimedAt := now.Add(time.Minute)
	if _, replay, err := repository.ClaimTask(
		ctx, taskID, failedFundingOperatorID, "unknown-funding-claim",
		claimedAt.Add(2*time.Minute), claimedAt,
	); err != nil || replay {
		t.Fatalf("claim: replay=%v err=%v", replay, err)
	}
	if _, replay, err := repository.AuthorizeShippingReveal(
		ctx, taskID, failedFundingOperatorID, "PLACE_MERCHANT_ORDER",
		"Review the exact shipping address before the manual merchant purchase.",
		"unknown-funding-reveal-correlation", "unknown-funding-reveal", claimedAt,
	); err != nil || replay {
		t.Fatalf("reveal: replay=%v err=%v", replay, err)
	}
	if _, replay, err := repository.RecordManualDecision(
		ctx, taskID, failedFundingOperatorID, "unknown-funding-decision",
		domain.DecisionWithinAuthorization,
		"The observed merchant conditions remain within the approved scope.",
		"Funding recovery test.",
		"The exact product, option, quantity, and amount were observed.",
		domain.EvidenceMerchantPage, "unknown-funding-evidence-hash", claimedAt,
		claimedAt,
	); err != nil || replay {
		t.Fatalf("decision: replay=%v err=%v", replay, err)
	}
	activator := &recoveringFundingActivator{database: database}
	service := newOwnerPurchasePGDriver(t, database, procurementapp.NewService(
		repository, nil, nil, failedFundingClock{now: claimedAt.Add(time.Minute)}, false,
	))
	service.EnableMerchantOrderFunding(activator)

	unknown, replay, err := service.BeginMerchantEffect(
		ctx, taskID, failedFundingOperatorID, "unknown-funding-effect:first",
	)
	if err != nil || replay || unknown.Funding.State != "ACTIVATION_UNKNOWN" ||
		unknown.MerchantOrder.State != domain.OrderPlanned {
		t.Fatalf("first begin: item=%+v replay=%v err=%v", unknown, replay, err)
	}
	if _, _, err := repository.RecordManualDecision(
		ctx, taskID, failedFundingOperatorID, "unknown-funding-new-decision",
		domain.DecisionUnableToPurchase,
		"The merchant order cannot be completed while funding is unresolved.",
		"This decision must not overtake the provider reconciliation.",
		"The provider result has not reached a terminal state.",
		domain.EvidenceOperatorObservation, "unknown-funding-new-decision-evidence",
		claimedAt.Add(time.Minute), claimedAt.Add(time.Minute),
	); !errors.Is(err, domain.ErrFundingNotReady) {
		t.Fatalf("decision crossed unresolved funding lock: %v", err)
	}
	if _, _, err := repository.CreateCustomerRequest(
		ctx, taskID, failedFundingOperatorID, "unknown-funding-new-request",
		"The provider result has not reached a terminal state.",
		"This request must not overtake provider reconciliation.",
		domain.EvidenceOperatorObservation,
		"unknown-funding-new-request-evidence", claimedAt.Add(time.Minute),
		domain.RequestInformation, "Please provide another merchant preference.",
		domain.ResponseText, nil, "Funding is already being reconciled.",
		claimedAt.Add(7*24*time.Hour), claimedAt.Add(time.Minute),
	); !errors.Is(err, domain.ErrFundingNotReady) {
		t.Fatalf("customer request crossed unresolved funding lock: %v", err)
	}
	if _, _, err := repository.RecordFailure(
		ctx, taskID, failedFundingOperatorID, "unknown-funding-failure",
		"MERCHANT_UNAVAILABLE", claimedAt.Add(time.Minute),
	); !errors.Is(err, domain.ErrFundingNotReady) {
		t.Fatalf("failure crossed unresolved funding lock: %v", err)
	}

	// Cancellation can no longer call the Owner directly: Workflow must reserve it.
	if _, err := repository.CancelMerchantOrder(ctx, failedFundingOrderID, merchantOrderID, failedFundingUserID, "DELAY_RULE", procurementapp.CancellationAuthority{}, now.Add(31*24*time.Hour)); err == nil {
		t.Fatal("unscoped cancellation accepted")
	}
	takeoverAt := claimedAt.Add(3 * time.Minute)
	if _, replay, err := repository.ClaimTask(
		ctx, taskID, recoveryFundingOperatorID, "unknown-funding-takeover",
		takeoverAt.Add(time.Hour), takeoverAt,
	); err != nil || replay {
		t.Fatalf("take over expired task: replay=%v err=%v", replay, err)
	}
	service = newOwnerPurchasePGDriver(t, database, procurementapp.NewService(
		repository, nil, nil, failedFundingClock{now: takeoverAt.Add(time.Minute)}, false,
	))
	service.EnableMerchantOrderFunding(activator)
	recovered, replay, err := service.BeginMerchantEffect(
		ctx, taskID, recoveryFundingOperatorID, "unknown-funding-effect:retry",
	)
	if err != nil || !replay || recovered.Funding.State != "ACTIVE" ||
		recovered.MerchantOrder.State != domain.OrderPlacementPending ||
		recovered.Task.State != domain.TaskInProgress {
		t.Fatalf("recovered begin: item=%+v replay=%v err=%v", recovered, replay, err)
	}
	var lockCount int
	var lockState, fundingState, storedKey, lockOperatorID string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*),min(state),min(funding_state),min(idempotency_key),
		       min(operator_user_id::text)
		FROM procurement_effect_locks WHERE merchant_order_id=$1
	`, merchantOrderID).Scan(
		&lockCount, &lockState, &fundingState, &storedKey, &lockOperatorID,
	); err != nil {
		t.Fatal(err)
	}
	if activator.calls != 2 || lockCount != 1 || lockState != "STARTED" ||
		fundingState != "FUNDED" || storedKey != "unknown-funding-effect:first" ||
		lockOperatorID != recoveryFundingOperatorID {
		t.Fatalf("recovery calls=%d locks=%d state=%s funding=%s key=%s operator=%s",
			activator.calls, lockCount, lockState, fundingState, storedKey, lockOperatorID)
	}

	// STARTED carries captured funding and possibly an in-flight human purchase.
	// The explicit post-expiry Claim transfers that responsibility atomically,
	// and the new assignee can reveal PII again and record the final evidence.
	resultTakeoverAt := takeoverAt.Add(2 * time.Hour)
	if _, replay, err := repository.ClaimTask(
		ctx, taskID, resultFundingOperatorID, "started-effect-takeover",
		resultTakeoverAt.Add(time.Hour), resultTakeoverAt,
	); err != nil || replay {
		t.Fatalf("take over started effect: replay=%v err=%v", replay, err)
	}
	var transferState, transferred string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT details->>'effectLockState',details->>'effectLockTransferred'
		FROM agency_order_execution_audits
		WHERE idempotency_key='started-effect-takeover'
	`).Scan(&transferState, &transferred); err != nil {
		t.Fatal(err)
	}
	if transferState != "STARTED" || transferred != "true" {
		t.Fatalf("started effect takeover audit state=%s transferred=%s",
			transferState, transferred)
	}
	if _, replay, err := repository.AuthorizeShippingReveal(
		ctx, taskID, resultFundingOperatorID, "PLACE_MERCHANT_ORDER",
		"Review the inherited order shipping address before recording its result.",
		"started-effect-result-correlation", "started-effect-result-reveal",
		resultTakeoverAt.Add(time.Minute),
	); err != nil || replay {
		t.Fatalf("new assignee reveal: replay=%v err=%v", replay, err)
	}
	automaticEvidence := domain.PlacementEvidence{
		AmountMode:        domain.PlacementAmountChanged,
		Kind:              domain.PlacementEvidenceSandboxTest,
		ExternalOrderRef:  "TEST-SHOP-ORDER-INHERITED",
		ReceiptSafeRef:    "test-receipt-inherited",
		ActualAmountMinor: 2000,
		Currency:          "USD",
		EvidenceSource:    domain.EvidenceReceipt,
	}
	recordedAt := resultTakeoverAt.Add(2 * time.Minute)
	placed, replay, err := repository.RecordPlaced(
		ctx, taskID, resultFundingOperatorID, "started-effect-result",
		automaticEvidence, false, recordedAt,
	)
	if err != nil || replay || placed.MerchantOrder.State != domain.OrderPlaced ||
		placed.Task.State != domain.TaskSucceeded ||
		placed.MerchantOrder.PlacementEvidence == nil ||
		!placed.MerchantOrder.PlacementEvidence.ObservedAt.Equal(recordedAt) ||
		len(placed.MerchantOrder.PlacementEvidence.EvidenceHash) != 71 ||
		!strings.HasPrefix(placed.MerchantOrder.PlacementEvidence.EvidenceHash, "sha256:") ||
		placed.MerchantOrder.PlacementEvidence.RecordedByUserID != resultFundingOperatorID {
		t.Fatalf("new assignee result: item=%+v replay=%v err=%v", placed, replay, err)
	}
	if _, replay, err = repository.RecordPlaced(
		ctx, taskID, resultFundingOperatorID, "started-effect-result",
		automaticEvidence, false, recordedAt.Add(time.Minute),
	); err != nil || !replay {
		t.Fatalf("automatic observed-at replay: replay=%v err=%v", replay, err)
	}
}

// A customer stop is normally impossible after Prepare because request ingress
// and resolution share the effect lock. If a stale/imported stop fact is still
// observed after PayPal Capture becomes ACTIVE, Resolve must never open seller
// permission: it fails the exact MO and emits the compensation trigger instead.
func TestActiveFundingWithCustomerStopRoutesRefundWithoutSellerEffect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	database := openFailedFundingDatabase(t, ctx)
	now := time.Date(2026, 8, 27, 18, 0, 0, 0, time.UTC)
	seedFailedFundingOrder(t, ctx, database, now)
	repository := NewRepository(database)
	if err := repository.PlanFromFunding(ctx, failedFundingOrderID, fundingProof(), now); err != nil {
		t.Fatalf("plan MO: %v", err)
	}
	var merchantOrderID, taskID, unitID string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT merchant_order.id::text,task.id::text,unit.id::text
		FROM merchant_orders merchant_order
		JOIN merchant_order_execution_tasks task
		  ON task.merchant_order_id=merchant_order.id
		JOIN merchant_order_units unit
		  ON unit.merchant_order_id=merchant_order.id
		WHERE merchant_order.allocation_id=$1
	`, failedFundingAllocation).Scan(&merchantOrderID, &taskID, &unitID); err != nil {
		t.Fatal(err)
	}
	execFailedFunding(t, ctx, database, `
		INSERT INTO logistics_expected_units(
			id,merchant_order_unit_id,merchant_order_id,agency_order_id,line_id,
			unit_index,fulfillment,registered_at,version,updated_at
		) VALUES(md5($1||':expected')::uuid,$1::uuid,$2::uuid,$3::uuid,'line-1',1,
			'AWAITING_EFFECT',$4,1,$4)
	`, unitID, merchantOrderID, failedFundingOrderID, now)
	claimedAt := now.Add(time.Minute)
	if _, replay, err := repository.ClaimTask(
		ctx, taskID, failedFundingOperatorID, "customer-stop-claim",
		claimedAt.Add(time.Hour), claimedAt,
	); err != nil || replay {
		t.Fatalf("claim: replay=%v err=%v", replay, err)
	}
	if _, replay, err := repository.AuthorizeShippingReveal(
		ctx, taskID, failedFundingOperatorID, "PLACE_MERCHANT_ORDER",
		"Review the exact shipping address before the manual merchant purchase.",
		"customer-stop-reveal-correlation", "customer-stop-reveal", claimedAt,
	); err != nil || replay {
		t.Fatalf("reveal: replay=%v err=%v", replay, err)
	}
	decision, replay, err := repository.RecordManualDecision(
		ctx, taskID, failedFundingOperatorID, "customer-stop-decision",
		domain.DecisionWithinAuthorization,
		"The observed merchant conditions remain within the approved scope.",
		"Customer stop reconciliation test.",
		"The exact product, option, quantity, and amount were observed.",
		domain.EvidenceMerchantPage, "customer-stop-decision-evidence-hash", claimedAt,
		claimedAt,
	)
	if err != nil || replay {
		t.Fatalf("decision: replay=%v err=%v", replay, err)
	}
	authority := ownerTestAuthority("AVAILABLE")
	if err := database.DB.QueryRowContext(ctx, `SELECT authorization_hash FROM agency_order_procurement_authorizations WHERE agency_order_id=$1`, failedFundingOrderID).Scan(&authority.AuthorizationHash); err != nil {
		t.Fatal(err)
	}
	prepareCtx := ownerContractScope(t, ctx, database, merchantOrderID, failedFundingOperatorID, "customer-stop-effect", procmsg.EffectReservePurchase)
	if _, replay, err := repository.PrepareMerchantEffectFunding(
		prepareCtx, taskID, failedFundingOperatorID, "customer-stop-effect", false, authority,
		claimedAt.Add(time.Minute),
	); err != nil || replay {
		t.Fatalf("prepare effect: replay=%v err=%v", replay, err)
	}
	execFailedFunding(t, ctx, database, `
		UPDATE payment_mo_funding_positions
		SET state='ACTIVE',version=version+1,updated_at=$2
		WHERE id=$1
	`, failedFundingPositionID, claimedAt.Add(2*time.Minute))
	// Bypass the normal ingress fence to exercise Resolve's final fail-closed
	// adoption of an already-durable stop fact.
	execFailedFunding(t, ctx, database, `
		INSERT INTO procurement_customer_requests(
			id,merchant_order_id,agency_order_id,user_id,kind,prompt,response_type,
			response_options,public_context,state,response,requested_by_user_id,
			requested_at,due_at,resolved_at,resolved_by_user_id,resolution_reason,
			source_decision_id,idempotency_key,resolution_idempotency_key,
			resolution_request_hash,version,created_at,updated_at
		) VALUES(
			md5($1||':stale-stop')::uuid,$1::uuid,$2::uuid,$3::uuid,'CONSENT',
			'Proceed with the changed merchant condition?','BOOLEAN_CONSENT','[]',
			'A material merchant condition requires customer approval.','DECLINED',
			NULL,$4::uuid,$5::timestamptz,$5::timestamptz + interval '7 days',
			$6::timestamptz,$3::uuid,
			'CUSTOMER_DECLINED',$7::uuid,'customer-stop-request',
			'customer-stop-resolution','customer-stop-resolution-hash',2,
			$5::timestamptz,$6::timestamptz
		)
	`, merchantOrderID, failedFundingOrderID, failedFundingUserID,
		failedFundingOperatorID, claimedAt, claimedAt.Add(2*time.Minute), decision.ID)

	scope, _ := procmsg.ExecutionFrom(prepareCtx)
	scope.Action = procmsg.EffectGrantMerchantPurchase
	authority.FundingState = "ACTIVE"
	item, replay, err := repository.ResolveMerchantEffectFunding(
		procmsg.WithExecutionScope(ctx, scope), taskID, failedFundingOperatorID, "customer-stop-effect",
		failedFundingPositionID, "ACTIVE", authority, claimedAt.Add(3*time.Minute),
	)
	if err != nil || replay || item.MerchantOrder.State != domain.OrderFailed ||
		item.Task.State != domain.TaskFailed {
		t.Fatalf("customer stop adoption: item=%+v replay=%v err=%v", item, replay, err)
	}
	var lockState, lockFundingState, failureCode, compensationCause string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT effect.state,effect.funding_state,merchant_order.failure_code,
		       event.payload->>'compensationCause'
		FROM procurement_effect_locks effect
		JOIN merchant_orders merchant_order ON merchant_order.id=effect.merchant_order_id
		JOIN order_process_events event
		  ON event.agency_order_id=merchant_order.agency_order_id
		 AND event.type='procurement.merchant_order.state_changed.v1'
		 AND event.payload->>'merchantOrderId'=merchant_order.id::text
		 AND event.payload->>'state'='FAILED'
		WHERE merchant_order.id=$1
		ORDER BY event.seq DESC LIMIT 1
	`, merchantOrderID).Scan(
		&lockState, &lockFundingState, &failureCode, &compensationCause,
	); err != nil {
		t.Fatal(err)
	}
	if lockState != "FAILED" || lockFundingState != "FUNDED" ||
		failureCode != "CUSTOMER_PURCHASE_STOP_AFTER_FUNDING" ||
		compensationCause != "PROCUREMENT_FAILURE" {
		t.Fatalf("stop projection lock=%s funding=%s failure=%s cause=%s",
			lockState, lockFundingState, failureCode, compensationCause)
	}
	var sellerStarts int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM agency_order_execution_audits
		WHERE merchant_order_id=$1 AND action='MERCHANT_EFFECT_STARTED'
	`, merchantOrderID).Scan(&sellerStarts); err != nil {
		t.Fatal(err)
	}
	if sellerStarts != 0 {
		t.Fatalf("seller permission opened after customer stop: %d", sellerStarts)
	}
}

func seedFailedFundingOrder(
	t *testing.T,
	ctx context.Context,
	database *sharedpostgres.Database,
	now time.Time,
) {
	t.Helper()
	execFailedFunding(t, ctx, database, `
		INSERT INTO users(id,status,created_at,updated_at) VALUES
			($1,'ACTIVE',$3,$3),($2,'ACTIVE',$3,$3)
	`, failedFundingUserID, failedFundingOperatorID, now)
	execFailedFunding(t, ctx, database, `
		INSERT INTO shipping_snapshots(
			id,user_id,profile_version,country,masked_summary,encrypted_payload,
			payload_nonce,key_version,snapshot_hmac,created_at,source_kind
		) VALUES($1,$2,1,'US','F*** O**, US',decode('00','hex'),decode('00','hex'),
			1,'failed-funding-hmac',$3,'ORDER_SHEET_INPUT')
	`, failedFundingShippingID, failedFundingUserID, now)
	execFailedFunding(t, ctx, database, `
		INSERT INTO agency_order_sheet_sessions(
			id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
			state,version,creation_key_hash,creation_request_hash,snapshot,
			created_at,expires_at,updated_at
		) VALUES($1,$2,$3,1,'failed-funding-cart','CONSUMED',1,
			'failed-funding-sheet-key','failed-funding-sheet-request','{}',$4,
			$4::timestamptz + interval '20 minutes',$4)
	`, failedFundingSessionID, failedFundingUserID, failedFundingSourceCart, now)
	execFailedFunding(t, ctx, database, `
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
	`, failedFundingOrderID, failedFundingUserID, failedFundingSessionID,
		failedFundingSourceCart, failedFundingShippingID, failedFundingProfileHash, now)
	execFailedFunding(t, ctx, database, `
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
	`, failedFundingOrderID, failedFundingUserID, failedFundingSessionID,
		"0x"+strings.Repeat("ab", 32), failedFundingProfileHash, now)
	execFailedFunding(t, ctx, database, `
		INSERT INTO agency_order_processes(
			agency_order_id,state,version,created_at,updated_at
		) VALUES($1,'PROCUREMENT_IN_PROGRESS',1,$2,$2)
	`, failedFundingOrderID, now)
	execFailedFunding(t, ctx, database, `
		INSERT INTO agency_order_mo_allocations(
			id,agency_order_id,checkout_ordinal,merchant_id,shop_domain,
			pass_through_minor,fee_variable_minor,fee_fixed_minor,fee_total_minor,
			customer_gross_minor,currency,fee_policy_version,allocation_hash,
			execution_profile_hash,created_at
		) VALUES($1,$2,1,'merchant-sibling','sibling.example',2000,108,30,138,
			2138,'USD','PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1',
			'0x'||repeat('cd',32),$3,$4)
	`, failedFundingAllocation, failedFundingOrderID, failedFundingProfileHash, now)
	execFailedFunding(t, ctx, database, `
		INSERT INTO payment_customer_payments(
			id,agency_order_id,user_id,rail,provider_environment,asset,economic_effect,
			amount_minor,currency,state,merchant_execution_mode,execution_profile_hash,
			version,created_at,updated_at
		) VALUES($1,$2,$3,'PAYPAL','SANDBOX','USD','NO_REAL_VALUE',2138,'USD',
			'AUTHORIZED','SIMULATED_NO_EFFECT',$4,1,$5,$5)
	`, failedFundingPaymentID, failedFundingOrderID, failedFundingUserID,
		failedFundingProfileHash, now)
	execFailedFunding(t, ctx, database, `
		INSERT INTO payment_paypal_attempts(
			id,customer_payment_id,sequence,state,paypal_order_id,return_nonce,
			version,created_at,updated_at
		) VALUES($1,$2,1,'AUTHORIZE_COMPLETED','PAYPAL-ORDER-FAILED-SIBLING',
			'failed-funding-return-nonce',1,$3,$3)
	`, failedFundingAttemptID, failedFundingPaymentID, now)
	execFailedFunding(t, ctx, database, `
		INSERT INTO payment_paypal_authorizations(
			id,customer_payment_id,agency_order_id,paypal_attempt_id,rail,
			provider_environment,paypal_order_id,payee_merchant_id,paypal_authorization_id,
			amount_minor,currency,execution_profile_hash,state,version,
			authorized_at,honor_refreshed_at,created_at,updated_at
		) VALUES($1,$2,$3,$4,'PAYPAL','SANDBOX','PAYPAL-ORDER-FAILED-SIBLING',
			'MERCHANT-1','PAYPAL-AUTH-FAILED-SIBLING',2138,'USD',$5,'AUTHORIZED',1,$6,$6,$6,$6)
	`, failedFundingAuthID, failedFundingPaymentID, failedFundingOrderID,
		failedFundingAttemptID, failedFundingProfileHash, now)
	execFailedFunding(t, ctx, database, `
		INSERT INTO payment_mo_funding_positions(
			id,allocation_id,agency_order_id,customer_payment_id,paypal_authorization_id,
			rail,source,provider_environment,amount_minor,currency,execution_profile_hash,
			state,version,available_at,created_at,updated_at
		) VALUES($1,$2,$3,$4,$5,'PAYPAL','PAYPAL_AUTHORIZATION','SANDBOX',2138,
			'USD',$6,'AVAILABLE',1,$7,$7,$7)
	`, failedFundingPositionID, failedFundingAllocation, failedFundingOrderID,
		failedFundingPaymentID, failedFundingAuthID, failedFundingProfileHash, now)
}

func execFailedFunding(
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

func openFailedFundingDatabase(
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
	databaseName := "vitlane_failed_funding_" + hex.EncodeToString(suffix)
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
			context.Background(), 15*time.Second,
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
		filepath.Dir(currentFile), "../../../../../migrations",
	))
	if err := database.Migrate(ctx, migrations); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}
	return database
}

func fundingProof() procmsg.PlanFromFundingPayload {
	return procmsg.PlanFromFundingPayload{CustomerPaymentID: failedFundingPaymentID, Rail: "PAYPAL", AmountMinor: 2138, Positions: []procmsg.FundingPositionSnapshot{{PositionID: failedFundingPositionID, AllocationID: failedFundingAllocation, AmountMinor: 2138, State: "AVAILABLE"}}}
}
