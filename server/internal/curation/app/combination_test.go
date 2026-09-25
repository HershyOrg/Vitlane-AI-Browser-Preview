package app

import (
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	"math"
	"testing"
)

func comboCandidate(ref, target string, amount int64) ResponseCandidate {
	return ResponseCandidate{Ref: ref, CandidateID: ref, TargetID: target, Source: "SHOPIFY", Quantity: 1, Price: &ResponsePrice{MinimumMinor: amount, MaximumMinor: amount, Currency: "USD", Basis: "SELECTED_VARIANT"}}
}
func TestCombinationMoneyAndCoverage(t *testing.T) {
	total := "100.00"
	a, b := comboCandidate("a", "a", 3000), comboCandidate("b", "b", 2000)
	a.Quantity = 2
	budget := d.BudgetLedger{Enabled: true, Currency: "USD", TotalAmount: &total, Version: 3}
	input := ResponseContext{Targets: []ResponseTarget{{ID: "a", Candidates: []ResponseCandidate{a}}, {ID: "b", Candidates: []ResponseCandidate{b}}}}
	check := func(status, basis string, min, max *int64) {
		t.Helper()
		p := BuildCombinations(input, budget, nil, "state", 4)[0]
		if p.BudgetStatus != status || p.PriceBasis != basis {
			t.Fatalf("%+v", p)
		}
		if min == nil {
			if p.MinimumMinor != nil || p.DifferenceMinor != nil {
				t.Fatal("unknown became zero")
			}
		} else if p.MinimumMinor == nil || *p.MinimumMinor != *min || *p.MaximumMinor != *max {
			t.Fatalf("%+v", p)
		}
	}
	check("WITHIN", "EXACT", minor(8000), minor(8000))
	a.Price.MaximumMinor = 5000
	check("UNKNOWN", "RANGE", minor(8000), minor(12000))
	a.Price.MinimumMinor = 5000
	check("OVER", "EXACT", minor(12000), minor(12000))
	a.Price.Currency = "KRW"
	a.Price.ConvertedMinor = map[string]int64{"USD": 3000}
	a.Price.ConvertedMaximumMinor = map[string]int64{"USD": 5000}
	check("UNKNOWN", "RANGE", minor(8000), minor(12000))
	if !BuildCombinations(input, budget, nil, "state", 4)[0].Estimated {
		t.Fatal("FX must be approximate")
	}
	a.Price.ConvertedMaximumMinor = nil
	a.Price.MaximumMinor = 6000
	check("UNKNOWN", "UNKNOWN", nil, nil)
	a.Price.Currency = "USD"
	a.Price.MinimumMinor = math.MaxInt64
	a.Price.MaximumMinor = math.MaxInt64
	check("UNKNOWN", "UNKNOWN", nil, nil)
	input.Targets[1].Candidates = nil
	p := BuildCombinations(input, budget, nil, "state", 4)[0]
	if len(p.MissingTargetIDs) != 1 || p.MissingTargetIDs[0] != "b" {
		t.Fatalf("%+v", p)
	}
}
func TestCombinationMatchesTopCandidatesIndependentlyOfCart(t *testing.T) {
	input := ResponseContext{Targets: []ResponseTarget{
		{ID: "a", Candidates: []ResponseCandidate{comboCandidate("a1", "a", 30), comboCandidate("a2", "a", 20), comboCandidate("a3", "a", 10), comboCandidate("a4", "a", 1)}},
		{ID: "b", Candidates: []ResponseCandidate{comboCandidate("b1", "b", 30), comboCandidate("b2", "b", 20), comboCandidate("b3", "b", 10)}},
	}}
	input.Targets[1].Candidates[2].KeepCart = true
	plans := BuildCombinations(input, d.BudgetLedger{Currency: "USD"}, nil, "state", 0)
	if len(plans) != 9 {
		t.Fatal("expected all 3 x 3 leader matches", len(plans))
	}
	found := false
	for _, p := range plans {
		for _, item := range p.Items {
			if item.CandidateID == "a4" {
				t.Fatal("outside shortlist")
			}
		}
		found = found || (p.Items[0].CandidateID == "a2" && p.Items[1].CandidateID == "b3")
	}
	if !found || plans[0].Items[1].CandidateID != "b1" {
		t.Fatal("Cart must not choose initial representatives")
	}
	for n := 0; n < 8; n++ {
		input.Targets = append(input.Targets, input.Targets[0])
	}
	if got := len(BuildCombinations(input, d.BudgetLedger{Currency: "USD"}, nil, "state", 0)); got > 16 {
		t.Fatal(got)
	}
}
func TestCombinationMergeScopeAndMembership(t *testing.T) {
	row := func(target, id, variant string, q int) CatalogCartItemV2 {
		return CatalogCartItemV2{TargetID: target, Item: d.CartItemV2{ID: id, CandidateID: id, VariantID: variant, Quantity: q}}
	}
	old := []CatalogCartItemV2{row("a", "old", "v1", 2), row("b", "other", "v2", 1)}
	plan := d.CombinationResponse{Items: []d.CombinationItem{{TargetID: "a", CandidateID: "new", CheckoutEligible: true}, {TargetID: "a", CandidateID: "old", CartItemID: "old", CheckoutEligible: true}, {TargetID: "c", CandidateID: "external", CheckoutEligible: false}}}
	next, err := MergeCombinationCart(old, plan, "REPLACE", []CatalogCartItemV2{row("a", "new", "v3", 3)})
	if err != nil || len(next) != 2 || next[0].Item.CandidateID != "other" || next[1].Item.Quantity != 3 {
		t.Fatal(next, err)
	}
	next, err = MergeCombinationCart(old, plan, "ADD", []CatalogCartItemV2{row("a", "old", "v1", 2)})
	if err != nil || len(next) != 2 || next[0].Item.Quantity != 2 {
		t.Fatal("duplicate preserved row", next, err)
	}
	for _, bad := range []CatalogCartItemV2{row("x", "new", "v3", 1), row("c", "external", "v", 1), row("a", "new", "", 1)} {
		if _, err = MergeCombinationCart(old, plan, "ADD", []CatalogCartItemV2{bad}); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if old[0].Item.Quantity != 2 {
		t.Fatal("mutated original")
	}
}

func TestCombinationKeepsMultipleExistingVariantsOfOneProduct(t *testing.T) {
	current := []CatalogCartItemV2{
		{TargetID: "a", Item: d.CartItemV2{ID: "first", CandidateID: "same", VariantID: "v1", Quantity: 2}},
		{TargetID: "a", Item: d.CartItemV2{ID: "second", CandidateID: "same", VariantID: "v2", Quantity: 3}},
	}
	plan := d.CombinationResponse{Items: []d.CombinationItem{{TargetID: "a", CandidateID: "same", CartItemID: "first", CheckoutEligible: true}, {TargetID: "a", CandidateID: "same", CartItemID: "second", CheckoutEligible: true}}}
	next, err := MergeCombinationCart(current, plan, "ADD", current)
	if err != nil || len(next) != 2 || next[0].Item.Quantity != 2 || next[1].Item.Quantity != 3 {
		t.Fatal(next, err)
	}
}

func TestCombinationRejectsQuantityAndCartLineLimits(t *testing.T) {
	plan := d.CombinationResponse{Items: []d.CombinationItem{{TargetID: "a", CandidateID: "new", CheckoutEligible: true}}}
	row := CatalogCartItemV2{TargetID: "a", Item: d.CartItemV2{CandidateID: "new", VariantID: "v", Quantity: 1}}
	for _, q := range []int{0, 100} {
		bad := row
		bad.Item.Quantity = q
		if _, err := MergeCombinationCart(nil, plan, "ADD", []CatalogCartItemV2{bad}); err == nil {
			t.Fatal("invalid quantity accepted", q)
		}
	}
	if _, err := MergeCombinationCart(nil, plan, "ADD", []CatalogCartItemV2{row, row}); err == nil {
		t.Fatal("duplicate accepted")
	}
	existing := make([]CatalogCartItemV2, 10)
	for i := range existing {
		existing[i] = CatalogCartItemV2{TargetID: "outside", Item: d.CartItemV2{CandidateID: "old", VariantID: "old", Quantity: 1}}
	}
	if _, err := MergeCombinationCart(existing, plan, "ADD", []CatalogCartItemV2{row}); err == nil {
		t.Fatal("more than ten lines accepted")
	}
}

func TestCombinationAddPreservesExistingVariantAndBindsRecommendation(t *testing.T) {
	plan := d.CombinationResponse{Items: []d.CombinationItem{{TargetID: "t", CandidateID: "c", VariantID: "v", Quantity: 2, CheckoutEligible: true}}}
	row := CatalogCartItemV2{TargetID: "t", Item: d.CartItemV2{CandidateID: "c", VariantID: "v", Quantity: 2}}
	existing := row
	existing.Item.Quantity = 4
	got, err := MergeCombinationCart([]CatalogCartItemV2{existing}, plan, "ADD", []CatalogCartItemV2{row})
	if err != nil || len(got) != 1 || got[0].Item.Quantity != 4 {
		t.Fatalf("already added variant changed: %v %v", got, err)
	}
	wrong := row
	wrong.Item.VariantID = "other"
	if _, err = MergeCombinationCart(nil, plan, "ADD", []CatalogCartItemV2{wrong}); err == nil {
		t.Fatal("accepted an unoffered variant")
	}
	wrong = row
	wrong.Item.Quantity = 3
	if _, err = MergeCombinationCart(nil, plan, "ADD", []CatalogCartItemV2{wrong}); err == nil {
		t.Fatal("accepted an unoffered quantity")
	}
}
