package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

func TestShoppingPlanV2KeepsPriceNoneSeparateFromUSDCurrency(t *testing.T) {
	plan := catalogPlanFixture(t, shareddomain.NoResearchPriceConstraint())
	if plan.OriginalIntent != "개발용 가벼운 노트북" ||
		plan.MarketContext.Currency != "USD" ||
		plan.ResearchPriceConstraint.Kind !=
			shareddomain.ResearchPriceConstraintNone ||
		plan.ResearchContractVersion !=
			shareddomain.CatalogResearchContractVersionV2 ||
		plan.ContentHash == "" {
		t.Fatalf("plan=%#v", plan)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "totalBudget") ||
		strings.Contains(string(encoded), "allocatedBudget") {
		t.Fatalf("V2 leaked a legacy budget: %s", encoded)
	}
}

func TestShoppingPlanV2AcceptsMaxOnlyAndMinMax(t *testing.T) {
	maximum := catalogMoney(t, "250", "USD")
	maxOnly, err := shareddomain.NewExplicitResearchPriceConstraint(nil, &maximum)
	if err != nil {
		t.Fatal(err)
	}
	if got := catalogPlanFixture(t, maxOnly); got.ResearchPriceConstraint.Min != nil {
		t.Fatalf("max-only gained a minimum: %#v", got.ResearchPriceConstraint)
	}

	minimum := catalogMoney(t, "75", "USD")
	minMax, err := shareddomain.NewExplicitResearchPriceConstraint(
		&minimum, &maximum,
	)
	if err != nil {
		t.Fatal(err)
	}
	got := catalogPlanFixture(t, minMax)
	if got.ResearchPriceConstraint.Min == nil ||
		got.ResearchPriceConstraint.Max == nil {
		t.Fatalf("min-max was not retained: %#v", got.ResearchPriceConstraint)
	}
}

func TestShoppingPlanV2RejectsInventedOrMismatchedPrice(t *testing.T) {
	market := catalogMarket(t)
	none := shareddomain.NoResearchPriceConstraint()
	maximum := catalogMoney(t, "100", "USD")
	explicit, err := shareddomain.NewExplicitResearchPriceConstraint(nil, &maximum)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewExplicitSettingsSnapshotV2(none, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewShoppingPlanV2(NewShoppingPlanV2Input{
		PlanID: "plan-1", UserID: "user-1", OriginalIntent: "laptop",
		PlanningMode: PlanningModeAuto, ExecutionMode: ExecutionModeLive,
		MarketContext: market, ResearchPriceConstraint: explicit,
		ExplicitSettingsSnapshot:     snapshot,
		IntelligenceProviderSnapshot: catalogProviderSnapshot(),
		Now:                          catalogNow(),
	})
	if !errors.Is(err, ErrCatalogPlanInvalid) {
		t.Fatalf("mismatched model price error=%v", err)
	}

	if _, err := NewExplicitSettingsSnapshotV2(explicit, ""); !errors.Is(
		err, ErrExplicitSettingsSnapshotInvalid,
	) {
		t.Fatalf("explicit price without user source error=%v", err)
	}
}

func TestIntentInterpretationV2NormalizesKoreanIntentIntoEnglishArtifact(t *testing.T) {
	plan := catalogPlanFixture(t, shareddomain.NoResearchPriceConstraint())
	interpretation, err := NewIntentInterpretationV2(
		plan,
		NewIntentInterpretationV2Input{
			InterpretationID: "interpretation-1", PlanningTaskID: "task-1",
			NormalizedIntentEnglish: "A lightweight laptop for software development",
			TargetDrafts:            []TargetDraftV2{catalogTargetDraft()},
			SourceLanguage:          "ko", PolicyVersion: "intent-normalizer.v1",
			TrustedConstraintSourceRefs: catalogTrustedSourceRefs(),
			Now:                         catalogNow().Add(time.Second),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if interpretation.SourceLanguage != "ko" ||
		interpretation.NormalizedIntentEnglish == plan.OriginalIntent ||
		interpretation.ContentHash == "" {
		t.Fatalf("interpretation=%#v", interpretation)
	}
	if err := interpretation.Validate(plan); err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(interpretation.TargetDrafts[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"priceConstraint", "currency", "shippingCountry", "totalBudget",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("model draft can invent %s: %s", forbidden, encoded)
		}
	}
}

func TestIntentInterpretationV2RejectsNonEnglishArtifact(t *testing.T) {
	plan := catalogPlanFixture(t, shareddomain.NoResearchPriceConstraint())
	draft := catalogTargetDraft()
	draft.ProductType = "노트북"
	draft.Provenance[0].Value = "노트북"
	_, err := NewIntentInterpretationV2(
		plan,
		NewIntentInterpretationV2Input{
			InterpretationID: "interpretation-1", PlanningTaskID: "task-1",
			NormalizedIntentEnglish: "개발용 노트북",
			TargetDrafts:            []TargetDraftV2{draft}, SourceLanguage: "ko",
			PolicyVersion:               "intent-normalizer.v1",
			TrustedConstraintSourceRefs: catalogTrustedSourceRefs(),
			Now:                         catalogNow(),
		},
	)
	if !errors.Is(err, ErrIntentInterpretationNotEnglish) {
		t.Fatalf("error=%v", err)
	}
}

func TestIntentInterpretationV2RejectsInventedProtectedConstraint(t *testing.T) {
	plan := catalogPlanFixture(t, shareddomain.NoResearchPriceConstraint())
	draft := catalogTargetDraft()
	draft.BrandTerms = []string{"Acme"}
	draft.Provenance = append(draft.Provenance, InterpretationProvenanceV2{
		Field: "brandTerms", Value: "Acme",
		Origin: InterpretationOriginModelDerived, SourceRef: "model:output",
	})
	_, err := NewIntentInterpretationV2(
		plan,
		NewIntentInterpretationV2Input{
			InterpretationID: "interpretation-1", PlanningTaskID: "task-1",
			NormalizedIntentEnglish: "A laptop for software development",
			TargetDrafts:            []TargetDraftV2{draft}, SourceLanguage: "ko",
			PolicyVersion:               "intent-normalizer.v1",
			TrustedConstraintSourceRefs: catalogTrustedSourceRefs(),
			Now:                         catalogNow(),
		},
	)
	if !errors.Is(err, ErrIntentConstraintInvented) {
		t.Fatalf("error=%v", err)
	}
}

func TestIntentInterpretationV2RejectsForgedUserProvenance(t *testing.T) {
	plan := catalogPlanFixture(t, shareddomain.NoResearchPriceConstraint())
	draft := catalogTargetDraft()
	draft.BrandTerms = []string{"Acme"}
	draft.Provenance = append(draft.Provenance, InterpretationProvenanceV2{
		Field: "brandTerms", Value: "Acme",
		Origin: InterpretationOriginUserExplicit, SourceRef: "intent:forged",
	})
	_, err := NewIntentInterpretationV2(
		plan,
		NewIntentInterpretationV2Input{
			InterpretationID: "interpretation-1", PlanningTaskID: "task-1",
			NormalizedIntentEnglish: "A laptop for software development",
			TargetDrafts:            []TargetDraftV2{draft}, SourceLanguage: "ko",
			PolicyVersion:               "intent-normalizer.v1",
			TrustedConstraintSourceRefs: catalogTrustedSourceRefs(),
			Now:                         catalogNow(),
		},
	)
	if !errors.Is(err, ErrIntentConstraintInvented) {
		t.Fatalf("error=%v", err)
	}
}

func TestPlanningProposalV2BindsValidatedPlanAndInterpretationHashes(t *testing.T) {
	plan := catalogPlanFixture(t, shareddomain.NoResearchPriceConstraint())
	interpretation, err := NewIntentInterpretationV2(
		plan,
		NewIntentInterpretationV2Input{
			InterpretationID: "interpretation-1", PlanningTaskID: "task-1",
			NormalizedIntentEnglish: "A laptop for software development",
			TargetDrafts:            []TargetDraftV2{catalogTargetDraft()},
			SourceLanguage:          "ko", PolicyVersion: "intent-normalizer.v1",
			TrustedConstraintSourceRefs: catalogTrustedSourceRefs(),
			Now:                         catalogNow().Add(time.Second),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := NewPlanningProposalV2(
		plan, interpretation,
		NewPlanningProposalV2Input{
			ProposalID: "proposal-1", IntelligenceJobID: "job-1",
			ClientProposalID: "client-proposal-1",
			SubmittedAt:      catalogNow().Add(2 * time.Second),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := proposal.Validate(plan, interpretation); err != nil {
		t.Fatal(err)
	}
	proposal.InterpretationHash = "tampered"
	if err := proposal.Validate(plan, interpretation); !errors.Is(
		err, ErrPlanningProposalV2Invalid,
	) {
		t.Fatalf("tamper error=%v", err)
	}
}

func catalogPlanFixture(
	t *testing.T,
	constraint shareddomain.ResearchPriceConstraint,
) ShoppingPlanV2 {
	t.Helper()
	sourceRef := ""
	if constraint.Kind == shareddomain.ResearchPriceConstraintExplicit {
		sourceRef = "request:settings.price"
	}
	snapshot, err := NewExplicitSettingsSnapshotV2(constraint, sourceRef)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewShoppingPlanV2(NewShoppingPlanV2Input{
		PlanID: "plan-1", UserID: "user-1",
		OriginalIntent: "  개발용 가벼운 노트북  ",
		PlanningMode:   PlanningModeAuto, ExecutionMode: ExecutionModeLive,
		MarketContext: catalogMarket(t), ResearchPriceConstraint: constraint,
		ExplicitSettingsSnapshot:     snapshot,
		IntelligenceProviderSnapshot: catalogProviderSnapshot(),
		Now:                          catalogNow(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func catalogTargetDraft() TargetDraftV2 {
	return TargetDraftV2{
		ProductType: "laptop", UseCases: []string{"software development"},
		HardRequirements: []string{"lightweight"},
		SoftPreferences:  []string{"long battery life"},
		Provenance: []InterpretationProvenanceV2{
			{Field: "productType", Value: "laptop", Origin: InterpretationOriginModelDerived, SourceRef: "intent:phrase-1"},
			{Field: "useCases", Value: "software development", Origin: InterpretationOriginUserExplicit, SourceRef: "intent:phrase-1"},
			{Field: "hardRequirements", Value: "lightweight", Origin: InterpretationOriginUserExplicit, SourceRef: "intent:phrase-2"},
			{Field: "softPreferences", Value: "long battery life", Origin: InterpretationOriginModelDerived, SourceRef: "intent:phrase-2"},
		},
	}
}

func catalogTrustedSourceRefs() []string {
	return []string{"intent:phrase-1", "intent:phrase-2"}
}

func catalogMarket(t *testing.T) shareddomain.MarketContext {
	t.Helper()
	market, err := shareddomain.NewMarketContext("US", "USD")
	if err != nil {
		t.Fatal(err)
	}
	return market
}

func catalogMoney(t *testing.T, amount, currency string) shareddomain.Money {
	t.Helper()
	money, err := shareddomain.NewMoney(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return money
}

func catalogProviderSnapshot() IntelligenceProviderSnapshotV2 {
	return IntelligenceProviderSnapshotV2{
		AgentMode: AgentModeManaged, ModelKey: "gpt-5",
		PolicyVersion: "managed-research.v1",
	}
}

func catalogNow() time.Time {
	return time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)
}
