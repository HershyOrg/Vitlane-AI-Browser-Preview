package app

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	d "github.com/vitlane/vitlane/server/internal/curation/domain"
)

type combinationStateReader interface {
	CombinationState(context.Context, string, string) (string, error)
}

func (s *ThreadService) SetCombinationCart(cart *CatalogCartServiceV2) { s.combinationCart = cart }

// prepareCombinations gives the writer a bounded set of alternatives. No model
// scores are rewritten, no new research runs, and Cart rows stay user choices.
func (s *ThreadService) prepareCombinations(ctx context.Context, out *ResponseContext, snapshot ThreadContext, saved map[string][]SavedCandidate) error {
	cart, err := s.combinationCart.Get(ctx, snapshot.Thread.UserID, snapshot.Thread.CurationID)
	if err != nil {
		return err
	}
	state := ""
	for _, candidates := range saved {
		for _, candidate := range candidates {
			if candidate.SourceState != "" {
				state = candidate.SourceState
				break
			}
		}
	}
	if reader, ok := s.responseSource.(combinationStateReader); ok && state == "" {
		state, err = reader.CombinationState(ctx, snapshot.Thread.UserID, snapshot.Thread.CurationID)
		if err != nil {
			return err
		}
	}
	for ti := range out.Targets {
		target := &out.Targets[ti]
		quantity := 1
		for _, a := range snapshot.Budget.Allocations {
			if a.TargetID == target.ID {
				quantity = a.Quantity
			}
		}
		for i := range target.Candidates {
			target.Candidates[i].Quantity = quantity
		}

	}
	out.Combinations = BuildCombinations(*out, snapshot.Budget, snapshot.Targets, state, cart.Version)
	return nil
}

// BuildCombinations matches the supplied top three candidates per Target,
// with at most sixteen combinations. Existing Cart rows do not choose representatives.
func BuildCombinations(input ResponseContext, budget d.BudgetLedger, targets []ThreadTarget, state string, cartVersion int64) []d.CombinationResponse {
	groups := make([][]ResponseCandidate, len(input.Targets))
	for ti, target := range input.Targets {
		if len(target.Candidates) > 0 {
			groups[ti] = []ResponseCandidate{target.Candidates[0]}
		}
	}
	currency := budget.Currency
	if currency != "USD" && currency != "KRW" {
		currency = "USD"
		for _, g := range groups {
			if len(g) > 0 && g[0].Price != nil {
				currency = g[0].Price.Currency
				break
			}
		}
	}
	plans := []d.CombinationResponse{}
	identities := map[string]bool{}
	appendPlan := func(id, kind string, choice [][]ResponseCandidate) {
		plan := d.CombinationResponse{SchemaVersion: "vitlane.combination.v1", ID: id, Kind: kind, Currency: currency, Items: []d.CombinationItem{}, MissingTargetIDs: []string{}, SourceState: state, BudgetVersion: budget.Version, CartVersion: cartVersion, CriteriaVersions: map[string]int64{}, Compatibility: "UNVERIFIED", Reasons: []string{}, Tips: []d.CombinationTip{}, Cautions: []string{}, PriceBasis: "EXACT", BudgetStatus: "NO_LIMIT"}
		var minimum, maximum int64
		known, ranged, estimated := true, false, false
		keys := []string{}
		for ti, group := range choice {
			targetID := input.Targets[ti].ID
			if len(group) == 0 {
				plan.MissingTargetIDs = append(plan.MissingTargetIDs, targetID)
				known = false
			}
			for _, c := range group {
				qty := c.Quantity
				if qty < 1 {
					qty = 1
				}
				lo, hi, converted, ok := combinationPrice(c.Price, currency)
				valid := ok && lo >= 0 && hi >= lo && hi <= math.MaxInt64/int64(qty) && maximum <= math.MaxInt64-hi*int64(qty)
				fit := "UNKNOWN"
				if valid {
					minimum += lo * int64(qty)
					maximum += hi * int64(qty)
					ranged = ranged || lo != hi || c.Price.Basis == "PRODUCT_RANGE"
					estimated = estimated || converted || c.Price.Basis == "CART_PREVIEW"
					for _, a := range budget.Allocations {
						if budget.Enabled && a.TargetID == targetID {
							if limit, good := moneyMinor(a.Amount, budget.Currency); good {
								fit = "WITHIN"
								if hi*int64(qty) > limit {
									fit = "OVER"
									if lo*int64(qty) <= limit {
										fit = "UNKNOWN"
									}
								}
							}
						}
					}
				} else {
					known = false
				}
				plan.Items = append(plan.Items, d.CombinationItem{Ref: c.Ref, TargetID: targetID, CandidateID: c.CandidateID, VariantID: c.VariantID, ConfigurationVersion: c.ConfigurationVersion, Quantity: qty, CartItemID: c.CartItemID, CheckoutEligible: c.Source == "SHOPIFY", AllocationFit: fit})
				keys = append(keys, fmt.Sprintf("%s:%s:%s:%d", targetID, c.CandidateID, c.VariantID, qty))
			}
		}
		if len(plan.Items) == 0 {
			return
		}
		sort.Strings(keys)
		key := strings.Join(keys, "|")
		if identities[key] {
			return
		}
		identities[key] = true
		for _, target := range targets {
			v := int64(0)
			if target.Criteria != nil {
				v = target.Criteria.Version
			}
			plan.CriteriaVersions[target.ID] = v
		}
		if known {
			plan.MinimumMinor = &minimum
			plan.MaximumMinor = &maximum
		} else {
			plan.PriceBasis = "UNKNOWN"
		}
		plan.Estimated = estimated
		if known && ranged {
			plan.PriceBasis = "RANGE"
		} else if known && estimated {
			plan.PriceBasis = "ESTIMATED"
		}
		if budget.Enabled {
			if total, ok := moneyMinor(budget.TotalAmount, budget.Currency); ok {
				plan.BudgetMinor = &total
				plan.BudgetStatus = "UNKNOWN"
				if known {
					if maximum <= total {
						plan.BudgetStatus = "WITHIN"
					} else if minimum > total {
						plan.BudgetStatus = "OVER"
					}
					if minimum == maximum {
						difference := total - minimum
						plan.DifferenceMinor = &difference
					}
				}
			}
		}
		plans = append(plans, plan)
	}
	// Match only the three supplied leaders per Target. Keep the search bounded
	// at sixteen tuples; do not turn individual scores into a claimed fit score.
	choices := [][][]ResponseCandidate{groups}
	for ti, target := range input.Targets {
		previous := append([][][]ResponseCandidate(nil), choices...)
		for rank := 1; rank < len(target.Candidates) && rank < 3; rank++ {
			for _, old := range previous {
				if len(choices) >= 16 {
					break
				}
				next := make([][]ResponseCandidate, len(old))
				for i, group := range old {
					next[i] = append([]ResponseCandidate(nil), group...)
				}
				next[ti] = []ResponseCandidate{target.Candidates[rank]}
				choices = append(choices, next)
			}
		}
	}
	for i, choice := range choices {
		id, kind := fmt.Sprintf("match-%d", i), "MATCH"
		if i == 0 {
			id, kind = "pick", "PICK"
		}
		appendPlan(id, kind, choice)
	}
	return plans
}

func combinationPrice(price *ResponsePrice, currency string) (int64, int64, bool, bool) {
	if price == nil {
		return 0, 0, false, false
	}
	if price.Currency == currency {
		return price.MinimumMinor, price.MaximumMinor, false, true
	}
	minimum, ok := price.ConvertedMinor[currency]
	if !ok {
		return 0, 0, false, false
	}
	maximum, ok := price.ConvertedMaximumMinor[currency]
	if !ok && price.MinimumMinor == price.MaximumMinor {
		maximum, ok = minimum, true
	}
	return minimum, maximum, true, ok
}
