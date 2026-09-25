package domain

import (
	"errors"
	"testing"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

func TestTargetRequirementProfileV2KeepsPriceNoneAndMarketSeparate(t *testing.T) {
	profile := catalogProfileFixture(
		t,
		shareddomain.NoResearchPriceConstraint(),
		nil,
	)
	if profile.EffectiveResearchPriceConstraint.Kind !=
		shareddomain.ResearchPriceConstraintNone ||
		profile.PriceConstraintSource != TargetPriceConstraintPlanInheritedV2 ||
		profile.MarketContext.Currency != "USD" ||
		profile.ShippingCountry != "US" || profile.ProfileHash == "" {
		t.Fatalf("profile=%#v", profile)
	}
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTargetRequirementProfileV2DerivesMaxOnlyAndTargetMinMax(t *testing.T) {
	maximum := catalogCurationMoney(t, "300", "USD")
	maxOnly, err := shareddomain.NewExplicitResearchPriceConstraint(nil, &maximum)
	if err != nil {
		t.Fatal(err)
	}
	profile := catalogProfileFixture(t, maxOnly, nil)
	if profile.EffectiveResearchPriceConstraint.Min != nil ||
		profile.EffectiveResearchPriceConstraint.Max == nil ||
		profile.PriceConstraintSource != TargetPriceConstraintPlanInheritedV2 {
		t.Fatalf("max-only profile=%#v", profile)
	}

	minimum := catalogCurationMoney(t, "100", "USD")
	targetConstraint, err := shareddomain.NewExplicitResearchPriceConstraint(
		&minimum, &maximum,
	)
	if err != nil {
		t.Fatal(err)
	}
	overridden := catalogProfileFixture(t, maxOnly, &targetConstraint)
	if overridden.PriceConstraintSource != TargetPriceConstraintExplicitV2 ||
		overridden.EffectiveResearchPriceConstraint.Min == nil ||
		overridden.PriceConstraintSourceRef != "target-setting:price" {
		t.Fatalf("target override=%#v", overridden)
	}
}

func TestTargetRequirementProfileV2AcceptsEnglishSourcedConstraints(t *testing.T) {
	profile := catalogProfileFixture(
		t,
		shareddomain.NoResearchPriceConstraint(),
		nil,
	)
	if profile.ProductType != "trail running shoes" ||
		len(profile.Attributes.Sizes) != 1 ||
		profile.Attributes.Sizes[0].SizingSystem != "US" {
		t.Fatalf("profile=%#v", profile)
	}
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTargetRequirementProfileV2RejectsNonEnglishSemanticFields(t *testing.T) {
	input := catalogProfileInput(
		t,
		shareddomain.NoResearchPriceConstraint(),
		nil,
	)
	input.ProductType = "러닝화"
	input.Provenance[0].Value = "러닝화"
	_, err := NewTargetRequirementProfileV2(input)
	if !errors.Is(err, ErrTargetRequirementProfileNotEnglish) {
		t.Fatalf("error=%v", err)
	}
}

func TestTargetRequirementProfileV2RejectsInventedProtectedFacts(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
		apply func(*NewTargetRequirementProfileV2Input)
	}{
		{
			name: "brand", field: "brandTerms", value: "Acme",
			apply: func(input *NewTargetRequirementProfileV2Input) {
				input.BrandTerms = []string{"Acme"}
			},
		},
		{
			name: "target gender", field: "attributes.targetGender", value: "Women",
			apply: func(input *NewTargetRequirementProfileV2Input) {
				input.Attributes.TargetGender = []string{"Women"}
			},
		},
		{
			name: "condition", field: "condition", value: "NEW",
			apply: func(input *NewTargetRequirementProfileV2Input) {
				input.Condition = []string{"NEW"}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := catalogProfileInput(
				t,
				shareddomain.NoResearchPriceConstraint(),
				nil,
			)
			test.apply(&input)
			input.Provenance = append(input.Provenance, ConstraintProvenanceV2{
				Field: test.field, Value: test.value,
				Origin:    ConstraintOriginModelDerivedV2,
				SourceRef: "model:output",
			})
			_, err := NewTargetRequirementProfileV2(input)
			if !errors.Is(err, ErrTargetRequirementInvented) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestTargetRequirementProfileV2RejectsForgedUserProvenance(t *testing.T) {
	input := catalogProfileInput(
		t,
		shareddomain.NoResearchPriceConstraint(),
		nil,
	)
	input.BrandTerms = []string{"Acme"}
	input.Provenance = append(input.Provenance, ConstraintProvenanceV2{
		Field: "brandTerms", Value: "Acme",
		Origin: ConstraintOriginUserExplicitV2, SourceRef: "intent:forged",
	})
	_, err := NewTargetRequirementProfileV2(input)
	if !errors.Is(err, ErrTargetRequirementInvented) {
		t.Fatalf("error=%v", err)
	}
}

func TestTargetRequirementProfileV2RejectsPriceCurrencyInvention(t *testing.T) {
	krwMaximum := catalogCurationMoney(t, "100000", "KRW")
	krwConstraint, err := shareddomain.NewExplicitResearchPriceConstraint(
		nil, &krwMaximum,
	)
	if err != nil {
		t.Fatal(err)
	}
	input := catalogProfileInput(t, krwConstraint, nil)
	_, err = NewTargetRequirementProfileV2(input)
	if !errors.Is(err, ErrTargetRequirementProfileInvalid) {
		t.Fatalf("error=%v", err)
	}
}

func TestTargetRequirementProfileV2HashDetectsTampering(t *testing.T) {
	profile := catalogProfileFixture(
		t,
		shareddomain.NoResearchPriceConstraint(),
		nil,
	)
	profile.ProfileHash = "tampered"
	if err := profile.Validate(); !errors.Is(
		err, ErrTargetRequirementProfileInvalid,
	) {
		t.Fatalf("tamper error=%v", err)
	}
}

func catalogProfileFixture(
	t *testing.T,
	planConstraint shareddomain.ResearchPriceConstraint,
	targetConstraint *shareddomain.ResearchPriceConstraint,
) TargetRequirementProfileV2 {
	t.Helper()
	profile, err := NewTargetRequirementProfileV2(
		catalogProfileInput(t, planConstraint, targetConstraint),
	)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func catalogProfileInput(
	t *testing.T,
	planConstraint shareddomain.ResearchPriceConstraint,
	targetConstraint *shareddomain.ResearchPriceConstraint,
) NewTargetRequirementProfileV2Input {
	t.Helper()
	market, err := NewMarketContextV2("US", "USD")
	if err != nil {
		t.Fatal(err)
	}
	planPriceSource := ""
	if planConstraint.Kind == shareddomain.ResearchPriceConstraintExplicit {
		planPriceSource = "plan-setting:price"
	}
	return NewTargetRequirementProfileV2Input{
		ProductType:      "trail running shoes",
		UseCases:         []string{"wet-weather trail running"},
		HardRequirements: []string{"waterproof"},
		SoftPreferences:  []string{"lightweight"},
		Attributes: TargetAttributesV2{
			Colors: []string{"Black"},
			Sizes:  []TargetSizeV2{{Value: "10", SizingSystem: "US"}},
		},
		MarketContext: market, MarketContextSourceRef: "policy:phase8-us-market",
		PlanPriceConstraint:           planConstraint,
		PlanPriceSourceRef:            planPriceSource,
		TargetExplicitPriceConstraint: targetConstraint,
		TargetExplicitPriceSourceRef: func() string {
			if targetConstraint != nil {
				return "target-setting:price"
			}
			return ""
		}(),
		TrustedProvenanceSourceRefs: []string{
			"intent:phrase-1", "intent:phrase-2",
			"intent:phrase-3", "intent:phrase-4",
		},
		Provenance: []ConstraintProvenanceV2{
			{Field: "productType", Value: "trail running shoes", Origin: ConstraintOriginModelDerivedV2, SourceRef: "interpretation:target-1"},
			{Field: "useCases", Value: "wet-weather trail running", Origin: ConstraintOriginUserExplicitV2, SourceRef: "intent:phrase-1"},
			{Field: "hardRequirements", Value: "waterproof", Origin: ConstraintOriginUserExplicitV2, SourceRef: "intent:phrase-2"},
			{Field: "softPreferences", Value: "lightweight", Origin: ConstraintOriginModelDerivedV2, SourceRef: "interpretation:target-1"},
			{Field: "attributes.colors", Value: "Black", Origin: ConstraintOriginUserExplicitV2, SourceRef: "intent:phrase-3"},
			{Field: "attributes.sizes", Value: "US:10", Origin: ConstraintOriginUserExplicitV2, SourceRef: "intent:phrase-4"},
		},
	}
}

func catalogCurationMoney(
	t *testing.T,
	amount, currency string,
) shareddomain.Money {
	t.Helper()
	money, err := shareddomain.NewMoney(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return money
}
