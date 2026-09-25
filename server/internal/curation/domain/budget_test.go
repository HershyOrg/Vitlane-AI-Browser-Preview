package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func testBudget() BudgetLedger {
	b := BudgetLedger{SchemaVersion: BudgetSchema, Currency: "KRW", Enabled: true, Version: 1, ResearchVersion: 1, Allocations: []TargetBudget{{TargetID: "pen", Quantity: 2, Amount: budgetString("60000")}, {TargetID: "ink", Quantity: 3, Amount: budgetString("30000")}}}
	_ = b.Validate([]string{"pen", "ink"})
	return b
}
func TestBudgetTargetEditsAreAtomicAndDoNotReallocateOtherTargets(t *testing.T) {
	b := testBudget()
	original, _ := json.Marshal(b)
	for _, tc := range []struct{ amount, total string }{{"80000", "110000"}, {"40000", "70000"}} {
		next, err := b.Apply(BudgetCommand{SchemaVersion: BudgetSchema, ExpectedVersion: 1, Kind: "SET_TARGET", TargetID: "pen", Amount: budgetString(tc.amount)})
		if err != nil || *next.TotalAmount != tc.total || !reflect.DeepEqual(next.Allocations[1], b.Allocations[1]) {
			t.Fatalf("next=%+v err=%v", next, err)
		}
	}
	after, _ := json.Marshal(b)
	if string(original) != string(after) {
		t.Fatal("editing mutated original ledger")
	}
	_, err := b.Apply(BudgetCommand{SchemaVersion: BudgetSchema, ExpectedVersion: 0, Kind: "DISABLE"})
	if err == nil {
		t.Fatal("stale version accepted")
	}
}
func TestBudgetQuantityAndViewOnlyMinimum(t *testing.T) {
	b := testBudget()
	next, e := b.Apply(BudgetCommand{SchemaVersion: BudgetSchema, ExpectedVersion: 1, Kind: "SET_QUANTITY", TargetID: "pen", Quantity: 4})
	if e != nil || *next.TotalAmount != *b.TotalAmount || *next.Allocations[0].Amount != "60000" || next.ResearchVersion != 2 {
		t.Fatalf("quantity: %+v %v", next, e)
	}
	before, _ := next.ResearchSnapshot("pen")
	withFloor, e := next.Apply(BudgetCommand{SchemaVersion: BudgetSchema, ExpectedVersion: 2, Kind: "SET_MINIMUM", TargetID: "pen", MinimumUnitAmount: budgetString("10000")})
	if e != nil {
		t.Fatal(e)
	}
	after, _ := withFloor.ResearchSnapshot("pen")
	if !reflect.DeepEqual(before, after) || withFloor.Version != 3 {
		t.Fatal("floor changed research snapshot")
	}
	raw, _ := json.Marshal(after)
	if strings.Contains(string(raw), "minimum") {
		t.Fatal("floor leaked to research")
	}
	if _, e = next.Apply(BudgetCommand{SchemaVersion: BudgetSchema, ExpectedVersion: 2, Kind: "SET_MINIMUM", TargetID: "pen", MinimumUnitAmount: budgetString("15001")}); e == nil {
		t.Fatal("invalid floor accepted")
	}
	combined, e := b.Apply(BudgetCommand{SchemaVersion: BudgetSchema, ExpectedVersion: 1, Kind: "SET_TARGET", TargetID: "pen", Amount: budgetString("60000"), Quantity: 2, UpdateMinimum: true, MinimumUnitAmount: budgetString("10000")})
	if e != nil || combined.ResearchVersion != b.ResearchVersion {
		t.Fatal("combined view-only edit changed research version", e)
	}
}
func TestBudgetEnableDisableReenableRequiresCompleteFreshAllocation(t *testing.T) {
	b := testBudget()
	disabled, e := b.Apply(BudgetCommand{SchemaVersion: BudgetSchema, ExpectedVersion: 1, Kind: "DISABLE"})
	if e != nil || disabled.TotalAmount != nil || disabled.Allocations[0].Amount != nil || disabled.Allocations[0].Quantity != 2 {
		t.Fatalf("disable: %+v %v", disabled, e)
	}
	command := BudgetCommand{SchemaVersion: BudgetSchema, ExpectedVersion: 2, Kind: "ENABLE", Currency: "USD", TotalAmount: budgetString("100.01"), AllocationMode: "MANUAL", Allocations: []TargetBudget{{TargetID: "pen", Amount: budgetString("75.00")}, {TargetID: "ink", Amount: budgetString("25.01")}}}
	next, e := disabled.Apply(command)
	if e != nil || *next.TotalAmount != "100.01" || next.Allocations[0].Quantity != 2 {
		t.Fatalf("manual enable: %+v %v", next, e)
	}
	command.Allocations = command.Allocations[:1]
	if _, e = disabled.Apply(command); e == nil {
		t.Fatal("partial allocation accepted")
	}
	command.Allocations = []TargetBudget{{TargetID: "pen", Amount: budgetString("75.00")}, {TargetID: "ink", Amount: budgetString("30.00")}}
	if _, e = disabled.Apply(command); e == nil {
		t.Fatal("overflow allocation accepted")
	}
}
func TestBudgetLargestRemainderPreservesEveryMinorUnit(t *testing.T) {
	for _, total := range []int64{0, 1, 2, 10001, 9007199254740991} {
		values, e := AllocateBudget(total, []int64{1, 1, 1})
		if e != nil {
			t.Fatal(e)
		}
		if values[0]+values[1]+values[2] != total || values[0]-values[2] > 1 {
			t.Fatalf("lost units %d %v", total, values)
		}
	}
	b := testBudget()
	next, e := b.Apply(BudgetCommand{SchemaVersion: BudgetSchema, ExpectedVersion: 1, Kind: "SET_TOTAL", TotalAmount: budgetString("100001"), AllocationMode: "PROPORTIONAL"})
	if e != nil || *next.TotalAmount != "100001" || *next.Allocations[0].Amount != "66667" || *next.Allocations[1].Amount != "33334" {
		t.Fatalf("proportions: %+v %v", next, e)
	}
}
func TestBudgetZeroTargetsAndPrecision(t *testing.T) {
	b := BudgetLedger{SchemaVersion: BudgetSchema, Currency: "USD", Enabled: true, Allocations: []TargetBudget{}}
	if e := b.Validate(nil); e != nil || *b.TotalAmount != "0.00" {
		t.Fatal("empty enabled budget", e)
	}
	for _, tc := range [][2]string{{"0.1", "KRW"}, {"1.001", "USD"}, {"-1", "USD"}, {"1e3", "USD"}, {"9007199254740992", "KRW"}} {
		if _, e := BudgetMinor(tc[0], tc[1]); e == nil {
			t.Fatalf("invalid money accepted %v", tc)
		}
	}
}

func TestBudgetCurrencyAndQuantitiesCommitWithCompleteAllocation(t *testing.T) {
	b := testBudget()
	b.Allocations[0].MinimumUnitAmount = budgetString("10000")
	before, _ := json.Marshal(b)
	command := BudgetCommand{SchemaVersion: BudgetSchema, ExpectedVersion: b.Version, Kind: "SET_TOTAL", Currency: "USD", TotalAmount: budgetString("90.00"), AllocationMode: "MANUAL", Allocations: []TargetBudget{{TargetID: "pen", Amount: budgetString("60.00"), Quantity: 4}, {TargetID: "ink", Amount: budgetString("30.00"), Quantity: 3}}}
	next, err := b.Apply(command)
	if err != nil || next.Currency != "USD" || *next.TotalAmount != "90.00" || next.Allocations[0].Quantity != 4 || next.Allocations[0].MinimumUnitAmount != nil || next.ResearchVersion != b.ResearchVersion+1 {
		t.Fatalf("currency edit: %+v %v", next, err)
	}
	command.Allocations = command.Allocations[:1]
	if _, err := b.Apply(command); err == nil {
		t.Fatal("partial currency edit accepted")
	}
	command.Allocations = nil
	command.AllocationMode = "PROPORTIONAL"
	proportional, err := b.Apply(command)
	if err != nil || *proportional.Allocations[0].Amount != "60.00" || proportional.Allocations[0].Quantity != 2 {
		t.Fatalf("proportions changed: %+v %v", proportional, err)
	}
	if _, err := b.Apply(BudgetCommand{SchemaVersion: BudgetSchema, ExpectedVersion: b.Version, Kind: "SET_TARGET", TargetID: "pen", Currency: "USD", Amount: budgetString("60")}); err == nil {
		t.Fatal("single target silently relabelled currency")
	}
	after, _ := json.Marshal(b)
	if string(before) != string(after) {
		t.Fatal("source ledger mutated")
	}
}
