package postgres

import (
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

func TestPlacementEvidenceReplayMatchesOnlyOperatorAuthoredFields(t *testing.T) {
	recorded := domain.PlacementEvidence{
		Kind:             domain.PlacementEvidenceSandboxTest,
		ExternalOrderRef: "TEST-ORDER-1", ReceiptSafeRef: "receipt:test:1",
		ActualAmountMinor: 3030, Currency: "USD",
		EvidenceSource: domain.EvidenceReceipt,
		EvidenceHash:   "sha256:test-placement-record-1",
		ObservedAt:     time.Date(2026, 8, 27, 9, 30, 0, 0, time.UTC),
	}
	if !placementEvidenceReplayMatches(recorded, recorded) {
		t.Fatal("identical placement evidence did not replay")
	}

	operatorFields := []struct {
		name   string
		mutate func(*domain.PlacementEvidence)
	}{
		{name: "kind", mutate: func(value *domain.PlacementEvidence) {
			value.Kind = domain.PlacementEvidenceLiveEffect
		}},
		{name: "order ref", mutate: func(value *domain.PlacementEvidence) {
			value.ExternalOrderRef = "TEST-ORDER-2"
		}},
		{name: "receipt ref", mutate: func(value *domain.PlacementEvidence) {
			value.ReceiptSafeRef = "receipt:test:2"
		}},
		{name: "amount", mutate: func(value *domain.PlacementEvidence) {
			value.ActualAmountMinor++
		}},
		{name: "currency", mutate: func(value *domain.PlacementEvidence) {
			value.Currency = "EUR"
		}},
		{name: "source", mutate: func(value *domain.PlacementEvidence) {
			value.EvidenceSource = domain.EvidenceMerchantPage
		}},
		{name: "live claim", mutate: func(value *domain.PlacementEvidence) {
			value.ClaimsExternalLive = true
		}},
	}
	for _, check := range operatorFields {
		t.Run(check.name, func(t *testing.T) {
			candidate := recorded
			check.mutate(&candidate)
			if placementEvidenceReplayMatches(recorded, candidate) {
				t.Fatalf("mismatched evidence replayed: %+v", candidate)
			}
		})
	}

	serverOwned := recorded
	serverOwned.EvidenceHash = "ignored-caller-hash"
	serverOwned.ObservedAt = recorded.ObservedAt.Add(time.Hour)
	if !placementEvidenceReplayMatches(recorded, serverOwned) {
		t.Fatal("server-owned hash or time incorrectly became a replay input")
	}
}
