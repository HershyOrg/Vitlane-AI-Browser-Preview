package app

import (
	"fmt"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"math/big"
	"regexp"
	"strings"
)

// A narrow omission guard, not a second intent classifier. Only explicit cap
// syntax qualifies; quantities and product numbers never become target ordinals.
var krwCap = regexp.MustCompile(`([0-9][0-9,]*(?:\.[0-9]+)?)\s*(만|천)?\s*원\s*(이하|이내|미만|언더|넘지)`)
var usdCap = regexp.MustCompile(`(?i)(?:under|below|within|up to|at most|budget(?: of)?|max(?:imum)?)\s*\$\s*([0-9][0-9,]*(?:\.[0-9]{1,2})?)|\$\s*([0-9][0-9,]*(?:\.[0-9]{1,2})?)\s*(?:or less|or under|maximum|max|이하|이내|언더)`)
var BudgetChangeEvidence = regexp.MustCompile(`(?i)(예산|원|달러|budget|\$|₩|USD|KRW|dollar|spend|allocation|배분)`)
var UnlimitedBudget = regexp.MustCompile(`(?i)(예산(?:은|이)?\s*(?:제한\s*없|무제한)|no\s+budget\s+limit|unlimited\s+budget)`)

func ExplicitSpendingCap(text string) (*Money, bool) {
	if matches := krwCap.FindAllStringSubmatch(text, -1); len(matches) == 1 {
		m := matches[0]
		n, ok := new(big.Rat).SetString(strings.ReplaceAll(m[1], ",", ""))
		if !ok {
			return nil, false
		}
		multiplier := int64(1)
		if m[2] == "만" {
			multiplier = 10000
		}
		if m[2] == "천" {
			multiplier = 1000
		}
		n.Mul(n, big.NewRat(multiplier, 1))
		if !n.IsInt() {
			return nil, false
		}
		return &Money{Amount: n.Num().String(), Currency: "KRW"}, true
	}
	if matches := usdCap.FindAllStringSubmatch(text, -1); len(matches) == 1 {
		m := matches[0]
		amount := m[1]
		if amount == "" {
			amount = m[2]
		}
		return &Money{Amount: strings.ReplaceAll(amount, ",", ""), Currency: "USD"}, true
	}
	return nil, false
}
func SameMoney(a, b Money) bool {
	x, ok := new(big.Rat).SetString(a.Amount)
	y, yes := new(big.Rat).SetString(b.Amount)
	return ok && yes && a.Currency == b.Currency && x.Cmp(y) == 0
}
func applyInitialAutoBudget(c *PlanningContext, p PlanningTargetsPayload) error {
	invalid := func() error { return fault.New(fault.ProviderRejected, "AUTO_INITIAL_BUDGET_INVALID", false) }
	d := p.BudgetDecision
	if d == nil {
		return invalid()
	}
	if len(d.Evidence) > 240 || d.Evidence != "" && !strings.Contains(c.OriginalIntent, d.Evidence) {
		return invalid()
	}
	if cap, ok := ExplicitSpendingCap(c.OriginalIntent); ok {
		if p.Budget == nil || !SameMoney(*cap, Money{p.Budget.Amount, p.Budget.Currency}) {
			return fault.New(fault.ProviderRejected, "AUTO_EXPLICIT_BUDGET_MISSED", false)
		}
	}
	if UnlimitedBudget.MatchString(c.OriginalIntent) && (d.Kind != "DISABLE" || p.Budget != nil) {
		return invalid()
	}
	switch d.Kind {
	case "KEEP":
		if c.BudgetEnabled {
			if p.Budget == nil || !SameMoney(c.TotalBudget, Money{p.Budget.Amount, p.Budget.Currency}) {
				return invalid()
			}
		} else if p.Budget != nil {
			return invalid()
		}
	case "ENABLE", "SET_TOTAL":
		if p.Budget == nil || c.BudgetEnabled && !BudgetChangeEvidence.MatchString(d.Evidence) {
			return invalid()
		}
	case "DISABLE":
		if p.Budget != nil || c.BudgetEnabled && !UnlimitedBudget.MatchString(d.Evidence) {
			return invalid()
		}
		c.BudgetEnabled = false
	default:
		return invalid()
	}
	c.BudgetResolved = true
	c.BudgetDecision = d
	if p.Budget == nil {
		c.ResolvedBudget = nil
	} else if err := validateInferredBudget(p.Budget.Amount, p.Budget.Currency); err != nil {
		return fmt.Errorf("invalid budget: %w", err)
	}
	return nil
}
