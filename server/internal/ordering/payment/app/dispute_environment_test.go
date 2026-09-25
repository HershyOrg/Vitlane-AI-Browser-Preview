package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
)

func TestPayPalDisputeReadAndActionRequireMatchingEnvironment(t *testing.T) {
	repository := fixtureRepository()
	service := newTestService(repository, &fakeProvider{})
	now := service.clock.Now()
	item := domain.PayPalDisputeCase{
		ID: "dispute-case-1", Environment: "SANDBOX", DisputeID: "PP-D-1",
		AgencyOrderID: "order-1", CustomerPaymentID: "payment-1",
		MOCashReceiptID: "receipt-1", CaptureID: "PP-CAPTURE-1",
		State: domain.DisputeOpen, ProviderStatus: domain.DisputeStatusOpen,
		Outcome: domain.DisputeOutcomeNone, Reason: "OTHER",
		LifecycleStage: domain.DisputeStageInquiry,
		LatestEventID:  "WH-D-1", LatestEventType: domain.PayPalDisputeCreated,
		OpenedAt: now, LastObservedAt: now, Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	repository.disputes["SANDBOX|PP-D-1"] = item

	view, err := service.GetPayPalDisputeView(
		context.Background(), " sandbox ", item.ID,
	)
	if err != nil || view.Case.ID != item.ID {
		t.Fatalf("matching environment view=%+v err=%v", view, err)
	}
	if _, err := service.GetPayPalDisputeView(
		context.Background(), "", item.ID,
	); !errors.Is(err, domain.ErrDisputeInvalid) {
		t.Fatalf("missing environment read error=%v", err)
	}
	if _, err := service.GetPayPalDisputeView(
		context.Background(), "LIVE", item.ID,
	); !errors.Is(err, domain.ErrDisputeNotFound) {
		t.Fatalf("cross-environment read error=%v", err)
	}

	input := RecordPayPalDisputeActionInput{
		ExpectedVersion:        item.Version,
		ActionKind:             domain.DisputeActionCaseObserved,
		ExternalReference:      "PP-RC-1",
		PublicRationale:        "The PayPal case was reviewed in Resolution Center.",
		ObservedProviderStatus: domain.DisputeStatusOpen,
		EvidenceSource:         domain.DisputeEvidencePayPalResolutionCenter,
		EvidenceHash:           "0x" + strings.Repeat("a", 64),
		ObservedAt:             now,
	}
	if _, err := service.RecordPayPalDisputeAction(
		context.Background(), "", item.ID, "operator-1", "idem-1", input,
	); !errors.Is(err, domain.ErrDisputeInvalid) {
		t.Fatalf("missing environment action error=%v", err)
	}
	if _, err := service.RecordPayPalDisputeAction(
		context.Background(), "LIVE", item.ID, "operator-1", "idem-1", input,
	); !errors.Is(err, domain.ErrDisputeNotFound) {
		t.Fatalf("cross-environment action error=%v", err)
	}
	if actions := repository.disputeActions[item.ID]; len(actions) != 0 {
		t.Fatalf("cross-environment action escaped: %+v", actions)
	}
}
