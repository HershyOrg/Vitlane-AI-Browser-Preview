package domain

import (
	"errors"
	"testing"
)

func TestResearchContractVersionIsClosed(t *testing.T) {
	for _, version := range []ResearchContractVersion{
		ResearchContractVersionV1Legacy,
		CatalogResearchContractVersionV2,
	} {
		if !version.Valid() {
			t.Fatalf("version %q should be valid", version)
		}
	}
	if ResearchContractVersion("V2").Valid() {
		t.Fatal("unversioned alias must not be accepted")
	}
	if err := ResearchContractVersion("V2").Validate(); !errors.Is(
		err, ErrResearchContractVersionInvalid,
	) {
		t.Fatalf("invalid version error=%v", err)
	}
}

func TestResearchPriceConstraintPreservesNoneAndExplicitBounds(t *testing.T) {
	market := mustMarketContext(t, "US", "USD")
	if err := NoResearchPriceConstraint().ValidateForMarket(market); err != nil {
		t.Fatalf("NONE: %v", err)
	}

	zero := mustMoney(t, "0", "USD")
	maximum := mustMoney(t, "150", "USD")
	maxOnly, err := NewExplicitResearchPriceConstraint(nil, &maximum)
	if err != nil {
		t.Fatalf("max-only: %v", err)
	}
	minMax, err := NewExplicitResearchPriceConstraint(&zero, &maximum)
	if err != nil {
		t.Fatalf("min-max: %v", err)
	}
	if maxOnly.Kind != ResearchPriceConstraintExplicit || maxOnly.Min != nil ||
		maxOnly.Max == nil || maxOnly.Max.Amount != "150" {
		t.Fatalf("max-only constraint=%#v", maxOnly)
	}
	if minMax.Min == nil || minMax.Min.Amount != "0" {
		t.Fatalf("explicit zero was lost: %#v", minMax)
	}
	if minMax.Equal(NoResearchPriceConstraint()) {
		t.Fatal("explicit zero must not collapse to NONE")
	}
}

func TestResearchPriceConstraintRejectsMalformedShapes(t *testing.T) {
	usd10 := mustMoney(t, "10", "USD")
	usd20 := mustMoney(t, "20", "USD")
	krw20 := mustMoney(t, "20", "KRW")
	negative := mustMoney(t, "-1", "USD")

	tests := []ResearchPriceConstraint{
		{Kind: ResearchPriceConstraintNone, Max: &usd20},
		{Kind: ResearchPriceConstraintExplicit},
		{Kind: ResearchPriceConstraintExplicit, Min: &usd20, Max: &usd10},
		{Kind: ResearchPriceConstraintExplicit, Min: &usd10, Max: &krw20},
		{Kind: ResearchPriceConstraintExplicit, Min: &negative},
		{Kind: "INFERRED", Max: &usd20},
	}
	for _, constraint := range tests {
		if err := constraint.Validate(); !errors.Is(
			err, ErrResearchPriceConstraintInvalid,
		) {
			t.Fatalf("constraint=%#v error=%v", constraint, err)
		}
	}
}

func TestResearchPriceConstraintRequiresMarketCurrency(t *testing.T) {
	maximum := mustMoney(t, "100", "KRW")
	constraint, err := NewExplicitResearchPriceConstraint(nil, &maximum)
	if err != nil {
		t.Fatal(err)
	}
	if err := constraint.ValidateForMarket(
		mustMarketContext(t, "US", "USD"),
	); !errors.Is(err, ErrResearchPriceConstraintInvalid) {
		t.Fatalf("error=%v", err)
	}
}

func mustMoney(t *testing.T, amount, currency string) Money {
	t.Helper()
	value, err := NewMoney(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustMarketContext(t *testing.T, country, currency string) MarketContext {
	t.Helper()
	value, err := NewMarketContext(country, currency)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
