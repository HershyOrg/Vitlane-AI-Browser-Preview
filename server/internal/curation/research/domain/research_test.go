package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCandidatePoolResultClosesRoundExactlyOnce(t *testing.T) {
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	round, err := NewRound(
		"round-1", "session-1", "user-1", 1,
		json.RawMessage(`{"roundId":"round-1"}`), now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if round.Status != RoundStatusRequested {
		t.Fatalf("new round status=%s", round.Status)
	}
	if err := round.CompleteFromCandidatePool(true, now); err != nil {
		t.Fatal(err)
	}
	if round.Status != RoundStatusResultsReady {
		t.Fatalf("completed round=%#v", round)
	}
	if err := round.CompleteFromCandidatePool(true, now); !errors.Is(err, ErrRoundClosed) {
		t.Fatalf("second result should close: %v", err)
	}
}

func TestNoResultsAndCancelAreTerminal(t *testing.T) {
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	noResults, _ := NewRound(
		"round-1", "session-1", "user-1", 1,
		json.RawMessage(`{"roundId":"round-1"}`), now,
	)
	if err := noResults.CompleteFromCandidatePool(false, now); err != nil {
		t.Fatal(err)
	}
	if noResults.Status != RoundStatusNoResults {
		t.Fatalf("NO_RESULTS status=%s", noResults.Status)
	}

	cancelled, _ := NewRound(
		"round-2", "session-1", "user-1", 2,
		json.RawMessage(`{"roundId":"round-2"}`), now,
	)
	if err := cancelled.Cancel(now); err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != RoundStatusCancelled {
		t.Fatalf("cancel status=%s", cancelled.Status)
	}
}

func TestFailedRoundIsTerminalAndCarriesSafeReason(t *testing.T) {
	now := time.Date(2026, 8, 14, 3, 0, 0, 0, time.UTC)
	round, err := NewRound(
		"round-failed", "session-1", "user-1", 1,
		json.RawMessage(`{"roundId":"round-failed"}`), now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := round.Fail("CANDIDATE_RANKING_EMPTY", false, now); err != nil {
		t.Fatal(err)
	}
	if round.Status != RoundStatusFailed ||
		round.FailureReasonCode != "CANDIDATE_RANKING_EMPTY" ||
		round.FailureRetryable == nil || *round.FailureRetryable ||
		round.CompletedAt == nil {
		t.Fatalf("failed round=%#v", round)
	}
	if err := round.CompleteFromCandidatePool(true, now); !errors.Is(err, ErrRoundClosed) {
		t.Fatalf("late result should be fenced: %v", err)
	}
}

func TestCandidatePoolCompletionCannotOverwriteCancelledRound(t *testing.T) {
	now := time.Date(2026, 8, 14, 1, 2, 3, 0, time.UTC)
	round, err := NewRound(
		"round-cancelled", "session-1", "user-1", 1,
		json.RawMessage(`{"roundId":"round-cancelled"}`), now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := round.Cancel(now); err != nil {
		t.Fatal(err)
	}

	if err := round.CompleteFromCandidatePool(true, now.Add(time.Second)); !errors.Is(err, ErrRoundClosed) {
		t.Fatalf("late CandidatePool result should be fenced: %v", err)
	}
	if round.Status != RoundStatusCancelled || round.CompletedAt == nil ||
		!round.CompletedAt.Equal(now) {
		t.Fatalf("cancelled round mutated by late result: %#v", round)
	}
}

func TestCanonicalHTTPSURLRejectsCredentialsAndDerivesMerchant(t *testing.T) {
	value, domain, err := CanonicalHTTPSURL("https://SHOP.Example.com/item/1#details")
	if err != nil {
		t.Fatal(err)
	}
	if value != "https://shop.example.com/item/1" || domain != "shop.example.com" {
		t.Fatalf("canonical URL=%q domain=%q", value, domain)
	}
	if _, _, err := CanonicalHTTPSURL("https://user:pass@example.com/item"); !errors.Is(err, ErrCandidateInvalid) {
		t.Fatalf("credential URL should fail: %v", err)
	}
	for _, invalid := range []string{
		"https://localhost/item",
		"https://127.0.0.1/item",
		"https://shop.example.com:8443/item",
	} {
		if _, _, err := CanonicalHTTPSURL(invalid); !errors.Is(err, ErrCandidateInvalid) {
			t.Fatalf("non-public product URL should fail for %q: %v", invalid, err)
		}
	}
}

func TestCanonicalCandidateImageURL(t *testing.T) {
	t.Run("canonicalizes an allowed public hostname", func(t *testing.T) {
		value, err := CanonicalCandidateImageURL(
			"  https://CDN.Example.com:443/products/chair.jpg?size=large#preview  ",
		)
		if err != nil {
			t.Fatal(err)
		}
		if value != "https://cdn.example.com/products/chair.jpg?size=large" {
			t.Fatalf("canonical image URL=%q", value)
		}
	})

	for name, value := range map[string]string{
		"http":                "http://cdn.example.com/chair.jpg",
		"credentials":         "https://user:pass@cdn.example.com/chair.jpg",
		"too long":            "https://cdn.example.com/" + strings.Repeat("a", MaxCandidateImageURLLength),
		"localhost":           "https://localhost/chair.jpg",
		"localhost subdomain": "https://images.localhost/chair.jpg",
		"dot local":           "https://merchant.local/chair.jpg",
		"IPv4 literal":        "https://127.0.0.1/chair.jpg",
		"IPv6 literal":        "https://[::1]/chair.jpg",
		"custom port":         "https://cdn.example.com:8443/chair.jpg",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CanonicalCandidateImageURL(value); !errors.Is(err, ErrCandidateInvalid) {
				t.Fatalf("expected image URL rejection for %q, got %v", value, err)
			}
		})
	}
}

func TestFeedbackSnapshotAndCancelledReresearchRestore(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	round, err := NewRound(
		"round-1", "session-1", "user-1", 1,
		json.RawMessage(`{"roundId":"round-1"}`), now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := round.CompleteFromCandidatePool(true, now); err != nil {
		t.Fatal(err)
	}
	previousStatus, err := round.Supersede(now)
	if err != nil {
		t.Fatal(err)
	}
	feedback, err := NewFeedback(
		"feedback-1", "session-1", round.ID, "round-2", "user-1",
		"더 가벼운 제품", "11111111-1111-4111-8111-111111111111",
		"request-hash", previousStatus,
		InteractionSnapshot{Variants: []CatalogVariantInteractionSnapshot{{
			CandidateID: "candidate-1", VariantID: "variant-1", Pinned: true,
			Sentiment: CatalogVariantSentimentLike,
		}}},
		now,
	)
	if err != nil || feedback.FeedbackHash == "" {
		t.Fatalf("feedback=%#v err=%v", feedback, err)
	}
	envelope, err := feedback.Envelope()
	if err != nil || len(envelope.Interactions.Variants) != 1 {
		t.Fatalf("envelope=%#v err=%v", envelope, err)
	}
	if err := feedback.Cancel(now); err != nil {
		t.Fatal(err)
	}
	if err := round.Restore(previousStatus, now); err != nil {
		t.Fatal(err)
	}
	if round.Status != RoundStatusResultsReady ||
		feedback.Status != FeedbackStatusCancelled {
		t.Fatalf("round=%#v feedback=%#v", round, feedback)
	}
}

func TestCandidateConfigurationCanonicalizesMapOrderAndPreservesSource(t *testing.T) {
	now := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	discovery := VariantDiscovery{
		SchemaVersion: VariantDiscoverySchemaV1,
		Status:        VariantDiscoveryObservedPartial,
		Fields: []VariantField{
			{
				Key: "size", Label: "Size", InputKind: VariantInputEnumOrValue,
				Required: true,
				KnownValues: []VariantKnownValue{
					{Value: "l", Label: "L"}, {Value: "m", Label: "M"},
				},
				Source:          VariantFieldAgentObservation,
				DiscoveryStatus: VariantDiscoveryObservedPartial,
			},
			{
				Key: "color", Label: "Color", InputKind: VariantInputEnum,
				Required: true,
				KnownValues: []VariantKnownValue{
					{Value: "white", Label: "White"},
					{Value: "black", Label: "Black"},
				},
				Source:          VariantFieldAgentObservation,
				DiscoveryStatus: VariantDiscoveryObservedPartial,
			},
		},
	}
	first, err := NewCandidateConfiguration(
		"configuration-1", "session-1", "candidate-1", "candidate-hash",
		"user-1", discovery,
		CandidateConfigurationInput{
			Fields: []VariantField{discovery.Fields[0], discovery.Fields[1], {
				Key: "voltage", Label: "Voltage", InputKind: VariantInputValue,
				Required: true,
			}},
			Selections: map[string]string{
				"voltage": "220V", "color": "BLACK", "size": "l",
			},
		},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCandidateConfiguration(
		"configuration-2", "session-1", "candidate-1", "candidate-hash",
		"user-1", discovery,
		CandidateConfigurationInput{
			Fields: []VariantField{{
				Key: "ｖｏｌｔａｇｅ", Label: "Ｖｏｌｔａｇｅ", InputKind: VariantInputValue,
				Required: true,
			}, discovery.Fields[1], discovery.Fields[0]},
			Selections: map[string]string{
				"size": "l", "color": "black", "ｖｏｌｔａｇｅ": "２２０Ｖ",
			},
		},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConfigurationHash != second.ConfigurationHash {
		t.Fatalf("map/field order changed hash: %s != %s",
			first.ConfigurationHash, second.ConfigurationHash)
	}
	if len(first.Selections) != 3 ||
		first.Selections[0].Key != "color" ||
		first.Selections[0].Value != "black" ||
		first.Selections[0].Source != VariantSelectionUserConfirmed ||
		first.Selections[2].Key != "voltage" ||
		first.Selections[2].Source != VariantSelectionUser {
		t.Fatalf("canonical selections=%#v", first.Selections)
	}
}

func TestCandidateConfigurationRequiresFieldsOrExplicitNoOptions(t *testing.T) {
	now := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	_, err := NewCandidateConfiguration(
		"configuration-1", "session-1", "candidate-1", "candidate-hash",
		"user-1", VariantDiscovery{},
		CandidateConfigurationInput{}, now,
	)
	if !errors.Is(err, ErrConfigurationInvalid) {
		t.Fatalf("missing configuration should fail: %v", err)
	}
	confirmed, err := NewCandidateConfiguration(
		"configuration-1", "session-1", "candidate-1", "candidate-hash",
		"user-1", VariantDiscovery{},
		CandidateConfigurationInput{ConfirmsNoOptions: true}, now,
	)
	if err != nil || !confirmed.ConfirmsNoOptions ||
		len(confirmed.Fields) != 0 || len(confirmed.Selections) != 0 {
		t.Fatalf("no-options confirmation=%#v err=%v", confirmed, err)
	}
}
