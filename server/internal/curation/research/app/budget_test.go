package app

import (
	"context"
	"encoding/json"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"testing"
	"time"
)

func TestResearchBudgetFreezesUnitCeilingAndExcludesMinimum(t *testing.T) {
	amount, floor := "100.01", "10.00"
	b := curationdomain.BudgetLedger{SchemaVersion: curationdomain.BudgetSchema, Version: 8, ResearchVersion: 4, Enabled: true, Currency: "USD", Allocations: []curationdomain.TargetBudget{{TargetID: "target", Amount: &amount, Quantity: 3, MinimumUnitAmount: &floor}}}
	snapshot, e := captureResearchBudget(context.Background(), b, "target", "US", nil)
	if e != nil || *snapshot.MaximumUnitMinor != 3333 || snapshot.Budget.Version != 4 {
		t.Fatalf("snapshot %+v %v", snapshot, e)
	}
	raw, _ := json.Marshal(snapshot)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	if _, ok := decoded["budget"].(map[string]any)["minimumUnitAmount"]; ok {
		t.Fatal("floor leaked")
	}
	oldMin, _ := shareddomain.NewMoney("999", "USD")
	profile := applyResearchBudget(CatalogTargetSearchProfileV2{MinimumPrice: &oldMin}, snapshot)
	if profile.MinimumPrice != nil || profile.MaximumPrice.Amount != "33.33" {
		t.Fatal("legacy range survived")
	}
	b.Allocations[0].Quantity = 1
	amount = "999.00"
	if *snapshot.MaximumUnitMinor != 3333 || *snapshot.Budget.Amount != "100.01" {
		t.Fatal("captured ceiling changed")
	}
}

func TestResearchBudgetNoLimitClearsLegacyBoundsAndMissingFXDoesNotInventRate(t *testing.T) {
	amount := "100000"
	b := curationdomain.BudgetLedger{SchemaVersion: curationdomain.BudgetSchema, Currency: "KRW", Allocations: []curationdomain.TargetBudget{{TargetID: "target", Quantity: 1}}}
	snapshot, e := captureResearchBudget(context.Background(), b, "target", "US", nil)
	if e != nil || snapshot.MaximumUnitMinor != nil {
		t.Fatal("no limit needed FX", e)
	}
	old, _ := shareddomain.NewMoney("1000", "KRW")
	profile := applyResearchBudget(CatalogTargetSearchProfileV2{MinimumPrice: &old, MaximumPrice: &old}, snapshot)
	if profile.MinimumPrice != nil || profile.MaximumPrice != nil || profile.Market.Currency != "USD" {
		t.Fatal("legacy bounds survived")
	}
	b.Enabled = true
	b.Allocations[0].Amount = &amount
	if _, e = captureResearchBudget(context.Background(), b, "target", "US", nil); e == nil {
		t.Fatal("missing FX admitted a budgeted research")
	}
}

func TestBudgetWholeDollarCeilingCompilesCanonicalMoney(t *testing.T) {
	amount := "105.00"
	b := curationdomain.BudgetLedger{SchemaVersion: curationdomain.BudgetSchema, Currency: "USD", Enabled: true, Allocations: []curationdomain.TargetBudget{{TargetID: "target", Amount: &amount, Quantity: 1}}}
	snapshot, e := captureResearchBudget(context.Background(), b, "target", "US", nil)
	if e != nil {
		t.Fatal(e)
	}
	profile := applyResearchBudget(CatalogTargetSearchProfileV2{TargetID: "target", NormalizedIntent: "fountain pen", Market: CatalogMarketContextV2{Country: "US", Currency: "USD"}}, snapshot)
	plan, e := BuildSearchPlanV3(CatalogWorkspaceSearchInputV2{TargetID: "target", Mode: CatalogResearchAppendV2}, profile)
	if e != nil {
		t.Fatal(e)
	}
	if plan.Required.MaximumMinor == nil || *plan.Required.MaximumMinor != 10500 {
		t.Fatalf("the budget ceiling is the plan's upper bound: %+v", plan.Required)
	}
	if _, _, e = shopifyProofIntentV2(profile, plan, "fountain pen"); e != nil {
		t.Fatal(e)
	}
}

type budgetRateRepository struct {
	rate researchdomain.DailyExchangeRate
}

func (r *budgetRateRepository) ReadExchangeRate(context.Context) (researchdomain.DailyExchangeRate, error) {
	return r.rate, nil
}
func (r *budgetRateRepository) ClaimExchangeRateRefresh(context.Context, time.Time) (bool, error) {
	return false, nil
}
func (r *budgetRateRepository) SaveExchangeRate(_ context.Context, rate researchdomain.DailyExchangeRate) error {
	r.rate = rate
	return nil
}

func TestBudgetDailyFXIsFrozenAndRoundsDownProviderCeiling(t *testing.T) {
	now := time.Now().UTC()
	repository := &budgetRateRepository{rate: researchdomain.DailyExchangeRate{Base: "USD", Quote: "KRW", Rate: "1400", AsOf: now.Format("2006-01-02"), ObservedAt: now, Source: "TEST"}}
	fx := &ExchangeRateService{Repository: repository}
	amount := "100000"
	b := curationdomain.BudgetLedger{SchemaVersion: curationdomain.BudgetSchema, Currency: "KRW", Enabled: true, Allocations: []curationdomain.TargetBudget{{TargetID: "target", Amount: &amount, Quantity: 3}}}
	snapshot, err := captureResearchBudget(context.Background(), b, "target", "US", fx)
	if err != nil || *snapshot.MaximumUnitMinor != 2380 {
		t.Fatalf("FX budget: %+v %v", snapshot, err)
	}
	repository.rate.Rate = "1500"
	if snapshot.ExchangeRate.Rate.Rate != "1400" || *snapshot.MaximumUnitMinor != 2380 {
		t.Fatal("saved exchange rate changed")
	}
	latest, err := captureResearchBudget(context.Background(), b, "target", "US", fx)
	if err != nil || *latest.MaximumUnitMinor != 2222 {
		t.Fatal("next research did not use current daily rate", err)
	}
}
