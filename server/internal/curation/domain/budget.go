package domain

import (
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const BudgetSchema = "vitlane.curation-budget.v1"
const maxBudgetMinor int64 = 9007199254740991

// InitialBudgetRequest is immutable planning input, not an unallocated balance.
// A nil total means no limit; untouched AUTO input may resolve an initial text cap.
// The active total is always derived from complete Target allocations.
type InitialBudgetRequest struct {
	// Omitted by older clients: preserve their explicit setting, including no limit.
	InputMode      string  `json:"inputMode,omitempty"`
	SchemaVersion  string  `json:"schemaVersion"`
	Currency       string  `json:"currency"`
	TotalAmount    *string `json:"totalAmount"`
	AllocationMode string  `json:"allocationMode"`
}

type TargetBudget struct {
	TargetID          string  `json:"targetId"`
	Quantity          int     `json:"quantity"`
	Amount            *string `json:"amount"`
	MinimumUnitAmount *string `json:"minimumUnitAmount,omitempty"`
}

type BudgetLedger struct {
	SchemaVersion   string         `json:"schemaVersion"`
	Version         int64          `json:"version"`
	ResearchVersion int64          `json:"researchVersion"`
	Enabled         bool           `json:"enabled"`
	Currency        string         `json:"currency"`
	TotalAmount     *string        `json:"totalAmount"`
	Allocations     []TargetBudget `json:"allocations"`
}

// BudgetResearchSnapshot deliberately cannot represent the view-only floor.
type BudgetResearchSnapshot struct {
	SchemaVersion string  `json:"schemaVersion"`
	Version       int64   `json:"version"`
	Enabled       bool    `json:"enabled"`
	Currency      string  `json:"currency"`
	TargetID      string  `json:"targetId"`
	Quantity      int     `json:"quantity"`
	Amount        *string `json:"amount"`
}

type BudgetCommand struct {
	UpdateMinimum     bool           `json:"updateMinimum,omitempty"`
	SchemaVersion     string         `json:"schemaVersion"`
	CommandID         string         `json:"commandId"`
	ExpectedVersion   int64          `json:"expectedVersion"`
	Kind              string         `json:"kind"`
	Currency          string         `json:"currency,omitempty"`
	TotalAmount       *string        `json:"totalAmount,omitempty"`
	AllocationMode    string         `json:"allocationMode,omitempty"`
	Allocations       []TargetBudget `json:"allocations,omitempty"`
	TargetID          string         `json:"targetId,omitempty"`
	Amount            *string        `json:"amount,omitempty"`
	Quantity          int            `json:"quantity,omitempty"`
	MinimumUnitAmount *string        `json:"minimumUnitAmount,omitempty"`
}

func budgetInvalid() error { return fault.New(fault.InvalidInput, "BUDGET_INVALID", false) }

var budgetDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,2})?$`)

func BudgetMinor(amount, currency string) (int64, error) {
	if len(amount) > 20 || !budgetDecimal.MatchString(amount) || (currency != "KRW" && currency != "USD") {
		return 0, budgetInvalid()
	}
	parts := strings.SplitN(amount, ".", 2)
	if currency == "KRW" && len(parts) > 1 {
		return 0, budgetInvalid()
	}
	value, ok := new(big.Rat).SetString(amount)
	if !ok {
		return 0, budgetInvalid()
	}
	if currency == "USD" {
		value.Mul(value, big.NewRat(100, 1))
	}
	if !value.IsInt() || !value.Num().IsInt64() || value.Sign() < 0 || value.Num().Int64() > maxBudgetMinor {
		return 0, budgetInvalid()
	}
	return value.Num().Int64(), nil
}

func BudgetAmount(minor int64, currency string) string {
	if currency == "USD" {
		return strconv.FormatInt(minor/100, 10) + "." + leftPadTwo(minor%100)
	}
	return strconv.FormatInt(minor, 10)
}
func leftPadTwo(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}
func budgetString(s string) *string { return &s }

func (r InitialBudgetRequest) AllowsInference() bool {
	return r.InputMode == "AUTO"
}

func (r InitialBudgetRequest) Validate() error {
	if r.InputMode != "" && r.InputMode != "AUTO" && r.InputMode != "EXPLICIT" {
		return budgetInvalid()
	}
	if r.SchemaVersion != BudgetSchema || (r.Currency != "KRW" && r.Currency != "USD") || (r.AllocationMode != "AUTO" && r.AllocationMode != "EQUAL") {
		return budgetInvalid()
	}
	if r.TotalAmount != nil {
		n, e := BudgetMinor(*r.TotalAmount, r.Currency)
		if e != nil || n <= 0 {
			return budgetInvalid()
		}
	}
	return nil
}

// Validate derives the total and enforces a bijection with active Target IDs.
func (b *BudgetLedger) Validate(targetIDs []string) error {
	if b.SchemaVersion != BudgetSchema || b.Version < 0 || b.ResearchVersion < 0 || (b.Currency != "KRW" && b.Currency != "USD") || len(targetIDs) != len(b.Allocations) {
		return budgetInvalid()
	}
	seen := map[string]bool{}
	total := int64(0)
	for i := range b.Allocations {
		a := &b.Allocations[i]
		if !slices.Contains(targetIDs, a.TargetID) || seen[a.TargetID] || a.Quantity < 1 || a.Quantity > 99 {
			return budgetInvalid()
		}
		seen[a.TargetID] = true
		if !b.Enabled {
			if a.Amount != nil || a.MinimumUnitAmount != nil {
				return budgetInvalid()
			}
			continue
		}
		if a.Amount == nil {
			return budgetInvalid()
		}
		n, err := BudgetMinor(*a.Amount, b.Currency)
		if err != nil || n > maxBudgetMinor-total {
			return budgetInvalid()
		}
		total += n
		a.Amount = budgetString(BudgetAmount(n, b.Currency))
		if a.MinimumUnitAmount != nil {
			floor, e := BudgetMinor(*a.MinimumUnitAmount, b.Currency)
			if e != nil || floor > n/int64(a.Quantity) {
				return budgetInvalid()
			}
			a.MinimumUnitAmount = budgetString(BudgetAmount(floor, b.Currency))
		}
	}
	if b.Enabled {
		b.TotalAmount = budgetString(BudgetAmount(total, b.Currency))
	} else {
		b.TotalAmount = nil
	}
	return nil
}

// AllocateBudget uses largest remainders, stable in Target order. No minor
// units disappear and no floating point numbers enter the ledger.
func AllocateBudget(total int64, weights []int64) ([]int64, error) {
	if total < 0 || total > maxBudgetMinor {
		return nil, budgetInvalid()
	}
	if len(weights) == 0 {
		if total != 0 {
			return nil, budgetInvalid()
		}
		return []int64{}, nil
	}
	sum := new(big.Int)
	for _, w := range weights {
		if w < 0 {
			return nil, budgetInvalid()
		}
		sum.Add(sum, big.NewInt(w))
	}
	if sum.Sign() == 0 {
		weights = slices.Repeat([]int64{1}, len(weights))
		sum.SetInt64(int64(len(weights)))
	}
	result := make([]int64, len(weights))
	remainders := make([]*big.Int, len(weights))
	order := make([]int, len(weights))
	left := total
	for i, w := range weights {
		q, r := new(big.Int).QuoRem(new(big.Int).Mul(big.NewInt(total), big.NewInt(w)), sum, new(big.Int))
		result[i] = q.Int64()
		remainders[i] = r
		order[i] = i
		left -= result[i]
	}
	slices.SortStableFunc(order, func(a, b int) int { return -remainders[a].Cmp(remainders[b]) })
	for i := int64(0); i < left; i++ {
		result[order[i]]++
	}
	return result, nil
}

func (b BudgetLedger) Apply(c BudgetCommand) (BudgetLedger, error) {
	if c.SchemaVersion != BudgetSchema || c.ExpectedVersion < 0 {
		return b, budgetInvalid()
	}
	if c.ExpectedVersion != b.Version {
		return b, fault.New(fault.Conflict, "BUDGET_VERSION_CONFLICT", false)
	}
	next := b
	next.Allocations = slices.Clone(b.Allocations)
	ids := make([]string, len(b.Allocations))
	index := -1
	for i, a := range b.Allocations {
		ids[i] = a.TargetID
		if a.TargetID == c.TargetID {
			index = i
		}
	}
	switch c.Kind {
	case "DISABLE":
		next.Enabled = false
		for i := range next.Allocations {
			next.Allocations[i].Amount = nil
			next.Allocations[i].MinimumUnitAmount = nil
		}
	case "ENABLE", "SET_TOTAL":
		if c.Kind == "ENABLE" && b.Enabled || c.Kind == "SET_TOTAL" && !b.Enabled || c.TotalAmount == nil {
			return b, budgetInvalid()
		}
		if c.Kind == "ENABLE" || c.Currency != "" {
			next.Currency = c.Currency
		}
		if next.Currency != b.Currency {
			// A currency edit commits complete, explicitly reviewed allocations.
			// Historical view-only floors cannot be relabelled as the new currency.
			for i := range next.Allocations {
				next.Allocations[i].MinimumUnitAmount = nil
			}
		}
		next.Enabled = true
		total, e := BudgetMinor(*c.TotalAmount, next.Currency)
		if e != nil {
			return b, e
		}
		if c.AllocationMode == "MANUAL" || c.AllocationMode == "AUTO" && c.Kind == "ENABLE" { // AI proposals remain drafts until this explicit save.
			if len(c.Allocations) != len(ids) {
				return b, budgetInvalid()
			}
			for i, a := range next.Allocations {
				found := false
				for _, proposal := range c.Allocations {
					if proposal.TargetID == a.TargetID {
						if found {
							return b, budgetInvalid()
						}
						found = true
						next.Allocations[i].Amount = proposal.Amount
						if proposal.Quantity != 0 {
							next.Allocations[i].Quantity = proposal.Quantity
						}
						if c.UpdateMinimum {
							next.Allocations[i].MinimumUnitAmount = proposal.MinimumUnitAmount
						}
					}
				}
				if !found {
					return b, budgetInvalid()
				}
			}
		} else if c.AllocationMode == "EQUAL" && c.Kind == "ENABLE" || c.AllocationMode == "PROPORTIONAL" && c.Kind == "SET_TOTAL" {
			weights := make([]int64, len(ids))
			for i, a := range next.Allocations {
				weights[i] = 1
				if c.AllocationMode == "PROPORTIONAL" {
					weights[i], e = BudgetMinor(*a.Amount, b.Currency)
					if e != nil {
						return b, e
					}
				}
			}
			values, e := AllocateBudget(total, weights)
			if e != nil {
				return b, e
			}
			for i, n := range values {
				next.Allocations[i].Amount = budgetString(BudgetAmount(n, next.Currency))
			}
		} else {
			return b, budgetInvalid()
		}
		if e = next.Validate(ids); e != nil {
			return b, e
		}
		actual, _ := BudgetMinor(*next.TotalAmount, next.Currency)
		if actual != total {
			return b, fault.New(fault.InvalidInput, "BUDGET_ALLOCATION_SUM_MISMATCH", false)
		}
	case "SET_TARGET", "SET_QUANTITY", "SET_MINIMUM":
		if index < 0 {
			return b, budgetInvalid()
		}
		switch c.Kind {
		case "SET_TARGET":
			if !b.Enabled || c.Amount == nil || c.Currency != "" && c.Currency != b.Currency {
				return b, budgetInvalid()
			}
			next.Allocations[index].Amount = c.Amount
			if c.Quantity != 0 {
				next.Allocations[index].Quantity = c.Quantity
			}
			if c.UpdateMinimum {
				next.Allocations[index].MinimumUnitAmount = c.MinimumUnitAmount
			}
		case "SET_QUANTITY":
			next.Allocations[index].Quantity = c.Quantity
		case "SET_MINIMUM":
			if !b.Enabled {
				return b, budgetInvalid()
			}
			next.Allocations[index].MinimumUnitAmount = c.MinimumUnitAmount
		}
	default:
		return b, budgetInvalid()
	}
	if e := next.Validate(ids); e != nil {
		return b, e
	}
	next.Version++
	changed := next.Enabled != b.Enabled || next.Currency != b.Currency
	for i, a := range next.Allocations {
		old := b.Allocations[i]
		if a.Quantity != old.Quantity || (a.Amount == nil) != (old.Amount == nil) || a.Amount != nil && old.Amount != nil && *a.Amount != *old.Amount {
			changed = true
		}
	}
	if changed {
		next.ResearchVersion++
	}
	return next, nil
}

func (b BudgetLedger) ResearchSnapshot(targetID string) (BudgetResearchSnapshot, error) {
	for _, a := range b.Allocations {
		if a.TargetID == targetID {
			var amount *string
			if a.Amount != nil {
				amount = budgetString(*a.Amount)
			}
			return BudgetResearchSnapshot{SchemaVersion: BudgetSchema, Version: b.ResearchVersion, Enabled: b.Enabled, Currency: b.Currency, TargetID: targetID, Quantity: a.Quantity, Amount: amount}, nil
		}
	}
	return BudgetResearchSnapshot{}, budgetInvalid()
}

// ResolveInitialBudget preserves the immutable request and validates a separate
// proposal result. AUTO may route a requested change over a manual baseline;
// EXPLICIT is the manual-only compatibility input.
func (p PlanSnapshot) ResolveInitialBudget(resolved *InitialBudgetRequest) (PlanSnapshot, error) {
	if resolved == nil {
		return p, nil
	}
	if p.BudgetRequest == nil || !p.BudgetRequest.AllowsInference() || resolved.InputMode != "EXPLICIT" {
		return p, budgetInvalid()
	}
	if err := resolved.Validate(); err != nil {
		return p, err
	}
	if resolved.AllocationMode != p.BudgetRequest.AllocationMode {
		return p, budgetInvalid()
	}
	p.BudgetRequest = resolved
	p.TotalBudget.Amount = "0"
	if resolved.TotalAmount != nil {
		p.TotalBudget.Amount = *resolved.TotalAmount
	}
	p.TotalBudget.Currency = shareddomain.CurrencyCode(resolved.Currency)
	return p, nil
}
