package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDisputeObservationKeepsUnknownOutcomeFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 27, 3, 0, 0, 0, time.UTC)
	observation, err := NewDisputeObservation(
		"sandbox", "WH-EVT-1", PayPalDisputeResolved, "PP-D-1", "CAPTURE-1",
		"", "A_NEW_PROVIDER_OUTCOME", "MERCHANDISE_OR_SERVICE_NOT_RECEIVED",
		"INQUIRY", nil, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State() != DisputeResolved ||
		observation.ProviderStatus != DisputeStatusResolved ||
		observation.Outcome != DisputeOutcomeNone {
		t.Fatalf("unexpected observation: %+v", observation)
	}
	item := PayPalDisputeCase{State: observation.State(), Outcome: observation.Outcome}
	if !item.BlocksDirectRefund() {
		t.Fatal("resolved dispute with unknown outcome must block a separate refund")
	}
}

func TestDisputeDirectRefundOnlyReleasesForNoBuyerFundMovement(t *testing.T) {
	for _, outcome := range []DisputeOutcome{
		DisputeOutcomeSellerFavour, DisputeOutcomeCanceledByBuyer, DisputeOutcomeDenied,
	} {
		if (PayPalDisputeCase{State: DisputeResolved, Outcome: outcome}).BlocksDirectRefund() {
			t.Fatalf("outcome %s should release direct refund lane", outcome)
		}
	}
	for _, outcome := range []DisputeOutcome{
		DisputeOutcomeNone, DisputeOutcomeBuyerFavour,
		DisputeOutcomeWithPayout, DisputeOutcomeAccepted,
	} {
		if !(PayPalDisputeCase{State: DisputeResolved, Outcome: outcome}).BlocksDirectRefund() {
			t.Fatalf("outcome %s could duplicate buyer funds", outcome)
		}
	}
}

func TestManualDisputeActionRequiresPublicAndHashedEvidence(t *testing.T) {
	now := time.Date(2026, 8, 27, 3, 0, 0, 0, time.UTC)
	valid := PayPalDisputeManualAction{
		ID: "action-1", DisputeCaseID: "case-1",
		ActionKind:        DisputeActionEvidenceSubmitted,
		ExternalReference: "PP-RC-ACK-1", PublicRationale: "Tracking evidence was submitted.",
		ActorUserID: "operator-1", EvidenceSource: DisputeEvidencePayPalResolutionCenter,
		EvidenceHash: "0x" + strings.Repeat("a", 64), ObservedAt: now,
		IdempotencyKeyHash: "0x" + strings.Repeat("b", 64),
		RequestHash:        "0x" + strings.Repeat("c", 64), CreatedAt: now,
	}
	if err := valid.Validate(now); err != nil {
		t.Fatalf("valid action: %v", err)
	}
	invalid := valid
	invalid.PublicRationale = " "
	if err := invalid.Validate(now); !errors.Is(err, ErrDisputeInvalid) {
		t.Fatalf("missing public rationale error=%v", err)
	}
	invalid = valid
	invalid.ExternalReference = "https://paypal.test/case?token=secret"
	if err := invalid.Validate(now); !errors.Is(err, ErrDisputeInvalid) {
		t.Fatalf("unsafe external reference error=%v", err)
	}
}
