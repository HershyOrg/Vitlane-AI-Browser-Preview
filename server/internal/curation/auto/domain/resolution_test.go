package domain

import "testing"

func TestLanguageGateUsesAnyNonLatinLetter(t *testing.T) {
	if NeedsEnglishNormalization("research 여행 adapter") != true {
		t.Fatal("mixed non-Latin input must be normalized by the server")
	}
	if NeedsEnglishNormalization("Research the travel adapter again") {
		t.Fatal("Latin-only English must pass through")
	}
}

func TestDeterministicEnglishFuzzyTargetMatch(t *testing.T) {
	semantic := ParseEnglish("Research the travel adaptor more")
	resolution, ok := ResolveDeterministically(semantic, []Target{
		{ID: "adapter", NormalizedIntent: "compact universal travel adapter", Researchable: true},
		{ID: "luggage", NormalizedIntent: "carry on travel luggage", Researchable: true},
	})
	if !ok || resolution.Decision != DecisionResearchAgain || resolution.TargetID != "adapter" {
		t.Fatalf("unexpected resolution: %#v, %t", resolution, ok)
	}
}

func TestDeterministicOrdinalAndAmbiguousFallback(t *testing.T) {
	targets := []Target{
		{ID: "first", OrderIndex: 0, Researchable: true},
		{ID: "second", OrderIndex: 1, Researchable: true},
	}
	resolution, ok := ResolveDeterministically(ParseEnglish("Research the second one again"), targets)
	if !ok || resolution.TargetID != "second" {
		t.Fatalf("unexpected ordinal resolution: %#v, %t", resolution, ok)
	}
	if _, ok := ResolveDeterministically(ParseEnglish("Find cheaper options"), targets); ok {
		t.Fatal("modifier-only multi-target input must fall through to Managed classification")
	}
}

func TestExplicitEnglishAddIsDeterministic(t *testing.T) {
	resolution, ok := ResolveDeterministically(ParseEnglish("Add a new travel charger target"), nil)
	if !ok || resolution.Decision != DecisionAddTarget {
		t.Fatalf("unexpected add resolution: %#v, %t", resolution, ok)
	}
}

func TestNewOrAnotherOptionDoesNotPretendToBeAnExplicitTargetAdd(t *testing.T) {
	for _, request := range []string{
		"Find new cheaper options for the travel adapter",
		"Research another waterproof option",
	} {
		semantic := ParseEnglish(request)
		if semantic.Operation == OperationAdd {
			t.Fatalf("%q must not bypass target matching as an explicit add", request)
		}
	}
}
