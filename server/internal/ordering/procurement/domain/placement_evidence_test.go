package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validPlacementEvidence(kind PlacementEvidenceKind) PlacementEvidence {
	return FinalizePlacementEvidence(
		"merchant-order-8842", "operator-1",
		time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC),
		PlacementEvidence{
			Kind: kind, ExternalOrderRef: "SHOP-ORDER-8842",
			ReceiptSafeRef: "receipt-safe-ref-8842", ActualAmountMinor: 5_223,
			Currency: "USD", EvidenceSource: EvidenceReceipt,
			ClaimsExternalLive: kind == PlacementEvidenceLiveEffect,
		},
	)
}

func TestFinalizePlacementEvidenceOwnsHashTimeAndActor(t *testing.T) {
	input := PlacementEvidence{
		Kind: PlacementEvidenceSandboxTest, ExternalOrderRef: "  TEST-ORDER-1  ",
		ReceiptSafeRef: " receipt:test:1 ", ActualAmountMinor: 3030,
		Currency: "usd", EvidenceSource: EvidenceReceipt,
		EvidenceHash: "caller-hash", ObservedAt: time.Unix(1, 0),
		ClaimsExternalLive: false,
	}
	observedAt := time.Date(2026, 8, 28, 9, 30, 0, 123456789, time.FixedZone("KST", 9*60*60))
	recorded := FinalizePlacementEvidence("merchant-order-1", "operator-1", observedAt, input)
	if recorded.ExternalOrderRef != "TEST-ORDER-1" || recorded.ReceiptSafeRef != "receipt:test:1" ||
		recorded.Currency != "USD" || recorded.RecordedByUserID != "operator-1" ||
		!recorded.ObservedAt.Equal(observedAt.UTC().Truncate(time.Microsecond)) ||
		recorded.RecordedAt != recorded.ObservedAt || len(recorded.EvidenceHash) != 71 ||
		!strings.HasPrefix(recorded.EvidenceHash, "sha256:") {
		t.Fatalf("server evidence was not finalized: %+v", recorded)
	}
	replay := FinalizePlacementEvidence("merchant-order-1", "operator-1", observedAt, input)
	if replay.EvidenceHash != recorded.EvidenceHash {
		t.Fatalf("same canonical record produced a different hash: %s != %s", replay.EvidenceHash, recorded.EvidenceHash)
	}
	changed := FinalizePlacementEvidence("merchant-order-2", "operator-1", observedAt, input)
	if changed.EvidenceHash == recorded.EvidenceHash {
		t.Fatal("merchant-order identity was not bound into the server hash")
	}
}

func TestResolvePlacementAmountDefaultsToAuthorizedAndRequiresExplicitChangedAmount(t *testing.T) {
	unchanged, err := ResolvePlacementAmount(5_223, PlacementEvidence{AmountMode: PlacementAmountUnchanged})
	if err != nil || unchanged.ActualAmountMinor != 5_223 {
		t.Fatalf("unchanged=%+v err=%v", unchanged, err)
	}
	if _, err := ResolvePlacementAmount(5_223, PlacementEvidence{
		AmountMode: PlacementAmountUnchanged, ActualAmountMinor: 5_000,
	}); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("unchanged with client amount err=%v", err)
	}
	changed, err := ResolvePlacementAmount(5_223, PlacementEvidence{
		AmountMode: PlacementAmountChanged, ActualAmountMinor: 5_000,
	})
	if err != nil || changed.ActualAmountMinor != 5_000 {
		t.Fatalf("changed=%+v err=%v", changed, err)
	}
	if _, err := ResolvePlacementAmount(5_223, PlacementEvidence{AmountMode: PlacementAmountChanged}); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("missing changed amount err=%v", err)
	}
}

func TestPlacementEvidenceSeparatesSandboxTestFromLiveEffect(t *testing.T) {
	testEvidence := validPlacementEvidence(PlacementEvidenceSandboxTest)
	if err := ValidatePlacementEvidence(
		ModeSimulatedNoEffect, 5_223, testEvidence,
	); err != nil {
		t.Fatalf("sandbox TEST evidence: %v", err)
	}
	if err := ValidatePlacementEvidence(
		ModeLiveMerchantEffect, 5_223, testEvidence,
	); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("sandbox evidence claimed LIVE: %v", err)
	}

	liveEvidence := validPlacementEvidence(PlacementEvidenceLiveEffect)
	if err := ValidatePlacementEvidence(
		ModeLiveMerchantEffect, 5_223, liveEvidence,
	); err != nil {
		t.Fatalf("LIVE evidence: %v", err)
	}
	if err := ValidatePlacementEvidence(
		ModeSimulatedNoEffect, 5_223, liveEvidence,
	); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("LIVE evidence entered into Sandbox: %v", err)
	}
}

func TestPlacementEvidenceAcceptsActualAmountUpToApprovedMaximumAndSafeReferences(t *testing.T) {
	evidence := validPlacementEvidence(PlacementEvidenceSandboxTest)
	lowerAmount := evidence
	lowerAmount.ActualAmountMinor--
	if err := ValidatePlacementEvidence(
		ModeSimulatedNoEffect, 5_223, lowerAmount,
	); err != nil {
		t.Fatalf("amount below approved maximum: %v", err)
	}
	checks := []struct {
		name   string
		mutate func(*PlacementEvidence)
	}{
		{name: "amount above approved maximum", mutate: func(value *PlacementEvidence) {
			value.ActualAmountMinor++
		}},
		{name: "zero amount", mutate: func(value *PlacementEvidence) {
			value.ActualAmountMinor = 0
		}},
		{name: "missing external order", mutate: func(value *PlacementEvidence) {
			value.ExternalOrderRef = ""
		}},
		{name: "missing receipt", mutate: func(value *PlacementEvidence) {
			value.ReceiptSafeRef = ""
		}},
		{name: "receipt payload URL", mutate: func(value *PlacementEvidence) {
			value.ReceiptSafeRef = "https://merchant.example/private/receipt"
		}},
		{name: "multiline external payload", mutate: func(value *PlacementEvidence) {
			value.ExternalOrderRef = "ORDER-1\nsecret=payload"
		}},
		{name: "missing hash", mutate: func(value *PlacementEvidence) {
			value.EvidenceHash = "short"
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			candidate := evidence
			check.mutate(&candidate)
			if err := ValidatePlacementEvidence(
				ModeSimulatedNoEffect, 5_223, candidate,
			); !errors.Is(err, ErrEvidenceInvalid) {
				t.Fatalf("invalid evidence accepted: %+v err=%v", candidate, err)
			}
		})
	}
}
