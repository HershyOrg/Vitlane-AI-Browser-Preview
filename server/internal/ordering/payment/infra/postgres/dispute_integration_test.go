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

func TestPayPalDisputeWebhookQueueAndRefundInterlockAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	migrations := fundingMigrationDirectory(t)
	database := openIsolatedFundingDatabase(t, ctx, migrations)
	now := time.Date(2026, 8, 27, 3, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	repository := NewRepository(database)
	due := now.Add(48 * time.Hour)
	observation, err := domain.NewDisputeObservation(
		"SANDBOX", "WH-DISPUTE-PG-1", domain.PayPalDisputeCreated,
		"PP-D-PG-1", "CAPTURE-FUNDING-1", "WAITING_FOR_SELLER_RESPONSE", "",
		"MERCHANDISE_OR_SERVICE_NOT_RECEIVED", "INQUIRY", &due, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	event := domain.WebhookEvent{
		Environment: "SANDBOX", WebhookID: "WH-SANDBOX",
		EventID: observation.EventID, EventType: observation.EventType,
		TransmissionID: "TX-DISPUTE-PG-1", ResourceKind: "dispute",
		ResourceID: observation.DisputeID, ReceivedAt: now,
	}
	caseID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbb001"
	item, applied, err := repository.ProcessDisputeWebhook(
		ctx, event, observation, caseID,
	)
	if err != nil || !applied {
		t.Fatalf("process dispute webhook: applied=%v err=%v", applied, err)
	}
	if item.Environment != "SANDBOX" || item.AgencyOrderID != fundingOrderID ||
		item.CustomerPaymentID != fundingCustomerPay || item.MOCashReceiptID != fundingReceiptID ||
		item.CaptureID != "CAPTURE-FUNDING-1" || item.State != domain.DisputeOpen {
		t.Fatalf("unexpected dispute binding: %+v", item)
	}
	duplicate, applied, err := repository.ProcessDisputeWebhook(
		ctx, event, observation, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbb099",
	)
	if err != nil || applied || duplicate.ID != caseID {
		t.Fatalf("duplicate webhook: item=%+v applied=%v err=%v", duplicate, applied, err)
	}
	if err := database.WithinTransaction(ctx, func(tx context.Context) error {
		return repository.AssertMOCompensationAllowed(tx, fundingReceiptID)
	}); !errors.Is(err, domain.ErrRefundBlockedByDispute) {
		t.Fatalf("open dispute refund interlock error=%v", err)
	}

	action := domain.PayPalDisputeManualAction{
		ID: "cccccccc-cccc-4ccc-8ccc-ccccccccc001", DisputeCaseID: caseID,
		ActionKind:        domain.DisputeActionCaseObserved,
		ExternalReference: "PP-RC-PG-1", PublicRationale: "PayPal resolved the case for the seller.",
		ActorUserID:            fundingOperatorID,
		ObservedProviderStatus: domain.DisputeStatusResolved,
		ObservedOutcome:        domain.DisputeOutcomeSellerFavour,
		EvidenceSource:         domain.DisputeEvidencePayPalResolutionCenter,
		EvidenceHash:           "0x" + strings.Repeat("a", 64), ObservedAt: now.Add(time.Hour),
		IdempotencyKeyHash: "0x" + strings.Repeat("b", 64),
		RequestHash:        "0x" + strings.Repeat("c", 64), CreatedAt: now.Add(time.Hour),
	}
	if _, err := repository.GetPayPalDispute(ctx, "LIVE", caseID); !errors.Is(
		err, domain.ErrDisputeNotFound,
	) {
		t.Fatalf("cross-environment dispute read error=%v", err)
	}
	if _, _, _, err := repository.RecordPayPalDisputeAction(
		ctx, "LIVE", action, item.Version,
	); !errors.Is(err, domain.ErrDisputeNotFound) {
		t.Fatalf("cross-environment dispute action error=%v", err)
	}
	resolved, _, replay, err := repository.RecordPayPalDisputeAction(
		ctx, "SANDBOX", action, item.Version,
	)
	if err != nil || replay || resolved.State != domain.DisputeResolved ||
		resolved.Outcome != domain.DisputeOutcomeSellerFavour {
		t.Fatalf("resolve dispute: item=%+v replay=%v err=%v", resolved, replay, err)
	}
	if err := database.WithinTransaction(ctx, func(tx context.Context) error {
		return repository.AssertMOCompensationAllowed(tx, fundingReceiptID)
	}); err != nil {
		t.Fatalf("seller-favour resolution should release refund lane: %v", err)
	}

	crossEnvironment, err := domain.NewDisputeObservation(
		"LIVE", "WH-DISPUTE-PG-LIVE", domain.PayPalDisputeCreated,
		"PP-D-PG-LIVE", "CAPTURE-FUNDING-1", "OPEN", "", "OTHER", "INQUIRY",
		nil, now.Add(2*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = repository.ProcessDisputeWebhook(ctx, domain.WebhookEvent{
		Environment: "LIVE", WebhookID: "WH-LIVE", EventID: crossEnvironment.EventID,
		EventType: crossEnvironment.EventType, TransmissionID: "TX-DISPUTE-PG-LIVE",
		ResourceKind: "dispute", ResourceID: crossEnvironment.DisputeID,
		ReceivedAt: now.Add(2 * time.Hour),
	}, crossEnvironment, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbb002")
	if !errors.Is(err, domain.ErrInstructionMismatch) {
		t.Fatalf("cross-environment dispute binding error=%v", err)
	}
}

func TestPayPalDisputeWebhookWaitsForRefundSendReceiptLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	migrations := fundingMigrationDirectory(t)
	database := openIsolatedFundingDatabase(t, ctx, migrations)
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	seedMOAccountingOrder(t, ctx, database, now)
	repository := NewRepository(database)
	execution, _, err := repository.PrepareMOCompensation(
		ctx, paymentapp.MOCompensationRequest{
			AgencyOrderID: fundingOrderID, MerchantOrderID: fundingMerchantAID,
			AllocationID:   fundingAllocationA,
			Cause:          domain.MOCompensationDeliveryException,
			IdempotencyKey: "compensate:dispute-race:mo-a",
		}, "eeeeeeee-eeee-4eee-8eee-eeeeeeee4001",
		"ffffffff-ffff-4fff-8fff-ffffffff4001", now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("prepare exact-MO refund: %v", err)
	}

	// Exercise the production effect-lock method, including its nested sender
	// claim, and hold its provider callback open. Signed dispute ingestion must
	// not commit an active case while that exact-MO refund call is in flight.
	lockHeld := make(chan struct{})
	releaseLock := make(chan struct{})
	refundTransactionDone := make(chan error, 1)
	go func() {
		refundTransactionDone <- repository.WithMOCompensationEffectLock(
			ctx, execution, func(effectCtx context.Context) error {
				if err := repository.MarkMOCompensationSent(
					effectCtx, execution, now.Add(2*time.Minute), now.Add(time.Hour),
				); err != nil {
					return err
				}
				close(lockHeld)
				select {
				case <-releaseLock:
					return nil
				case <-effectCtx.Done():
					return effectCtx.Err()
				}
			})
	}()
	select {
	case <-lockHeld:
	case err := <-refundTransactionDone:
		t.Fatalf("refund transaction ended before holding receipt lock: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	observation, err := domain.NewDisputeObservation(
		"SANDBOX", "WH-DISPUTE-RACE-1", domain.PayPalDisputeCreated,
		"PP-D-RACE-1", "CAPTURE-FUNDING-1", "OPEN", "", "OTHER", "INQUIRY",
		nil, now.Add(time.Minute),
	)
	if err != nil {
		close(releaseLock)
		t.Fatal(err)
	}
	type disputeResult struct {
		item    domain.PayPalDisputeCase
		applied bool
		err     error
	}
	disputeDone := make(chan disputeResult, 1)
	go func() {
		item, applied, processErr := repository.ProcessDisputeWebhook(
			ctx,
			domain.WebhookEvent{
				Environment: "SANDBOX", WebhookID: "WH-SANDBOX",
				EventID: observation.EventID, EventType: observation.EventType,
				TransmissionID: "TX-DISPUTE-RACE-1", ResourceKind: "dispute",
				ResourceID: observation.DisputeID, ReceivedAt: now.Add(time.Minute),
			},
			observation,
			"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbb003",
		)
		disputeDone <- disputeResult{item: item, applied: applied, err: processErr}
	}()

	select {
	case result := <-disputeDone:
		close(releaseLock)
		t.Fatalf("dispute escaped receipt lock: item=%+v applied=%v err=%v",
			result.item, result.applied, result.err)
	case <-time.After(150 * time.Millisecond):
		// Expected: ProcessDisputeWebhook is waiting on FOR UPDATE OF receipt.
	case <-ctx.Done():
		close(releaseLock)
		t.Fatal(ctx.Err())
	}
	close(releaseLock)
	if err := <-refundTransactionDone; err != nil {
		t.Fatalf("release refund transaction: %v", err)
	}
	result := <-disputeDone
	if result.err != nil || !result.applied || result.item.State != domain.DisputeOpen {
		t.Fatalf("dispute after receipt release: item=%+v applied=%v err=%v",
			result.item, result.applied, result.err)
	}
	callbackRan := false
	err = repository.WithMOCompensationEffectLock(
		ctx, execution, func(context.Context) error {
			callbackRan = true
			return nil
		},
	)
	if !errors.Is(err, domain.ErrRefundBlockedByDispute) || callbackRan {
		t.Fatalf("open dispute did not block production refund callback: ran=%v err=%v",
			callbackRan, err)
	}
}
