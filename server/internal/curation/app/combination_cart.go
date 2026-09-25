package app

import (
	"context"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	shared "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"strings"
	"time"
)

type CombinationCartCommand struct {
	CommandID       string
	ExpectedVersion int64
	Mode            string
	Items           []CatalogCartItemV2
}

type combinationCartCommands interface {
	ValidateCombinationCartSelections(context.Context, string, string, []CatalogCartItemV2) error
	ReadCombinationCartCommand(context.Context, string, string) (string, CatalogCartStateV2, bool, error)
	SaveCombinationCartCommand(context.Context, string, string, string, string, CatalogCartStateV2, time.Time) error
}

type CombinationStatus struct {
	SchemaVersion string `json:"schemaVersion"`
	Current       bool   `json:"current"`
	ReasonCode    string `json:"reasonCode,omitempty"`
	CartVersion   int64  `json:"cartVersion"`
}

func (s *ThreadService) readCombination(ctx context.Context, user, curation, thread string) (d.CurationThread, *d.CombinationResponse, error) {
	t, err := s.repo.ReadThread(ctx, user, thread)
	if err != nil {
		return t, nil, err
	}
	if t.CurationID != curation || t.Status != "SUCCEEDED" {
		return t, nil, fault.New(fault.Conflict, "COMBINATION_UNAVAILABLE", false)
	}
	a := t.ResponseAction()
	if a == nil || a.Response == nil || a.Response.Combination == nil {
		return t, nil, fault.New(fault.Conflict, "COMBINATION_UNAVAILABLE", false)
	}
	return t, a.Response.Combination, nil
}

func (s *ThreadService) combinationStatus(ctx context.Context, t d.CurationThread, plan *d.CombinationResponse) (CombinationStatus, error) {
	out := CombinationStatus{SchemaVersion: "vitlane.combination-status.v1", Current: true}
	if s.combinationCart == nil {
		return out, fault.New(fault.ProviderUnavailable, "COMBINATION_UNAVAILABLE", false)
	}
	cart, err := s.combinationCart.Get(ctx, t.UserID, t.CurationID)
	if err != nil {
		return out, err
	}
	out.CartVersion = cart.Version
	if cart.Version != plan.CartVersion {
		out.Current = false
		out.ReasonCode = "PHASE8_CART_VERSION_CONFLICT"
		return out, nil
	}
	snapshot, err := s.snapshot(ctx, t)
	if err != nil {
		return out, err
	}
	changed := snapshot.Budget.Version != plan.BudgetVersion || len(snapshot.Targets) != len(plan.CriteriaVersions)
	for _, target := range snapshot.Targets {
		v := int64(0)
		if target.Criteria != nil {
			v = target.Criteria.Version
		}
		stored, ok := plan.CriteriaVersions[target.ID]
		changed = changed || !ok || stored != v
	}
	reader, ok := s.responseSource.(combinationStateReader)
	if !ok {
		return out, fault.New(fault.ProviderUnavailable, "COMBINATION_UNAVAILABLE", false)
	}
	state, err := reader.CombinationState(ctx, t.UserID, t.CurationID)
	if err != nil {
		return out, err
	}
	if changed || state != plan.SourceState {
		out.Current = false
		out.ReasonCode = "COMBINATION_CHANGED"
	}
	return out, nil
}

func (s *ThreadService) CombinationStatus(ctx context.Context, user, curation, thread string) (CombinationStatus, error) {
	t, plan, err := s.readCombination(ctx, user, curation, thread)
	if err != nil {
		return CombinationStatus{}, err
	}
	return s.combinationStatus(ctx, t, plan)
}

func (s *ThreadService) ApplyCombinationCart(ctx context.Context, user, curation, thread string, in CombinationCartCommand) (CatalogCartStateV2, error) {
	var result CatalogCartStateV2
	if !uuidPattern.MatchString(in.CommandID) || in.ExpectedVersion < 0 || (in.Mode != "ADD" && in.Mode != "REPLACE") || len(in.Items) < 1 || len(in.Items) > 10 {
		return result, fault.New(fault.InvalidInput, "COMBINATION_CART_INVALID", false)
	}
	commands, ok := s.repo.(combinationCartCommands)
	if !ok || s.combinationCart == nil {
		return result, fault.New(fault.ProviderUnavailable, "COMBINATION_UNAVAILABLE", false)
	}
	hash, err := shared.CanonicalJSONHash(struct {
		Curation, Thread string
		Input            CombinationCartCommand
	}{curation, thread, in})
	if err != nil {
		return result, err
	}
	err = s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if err := s.repo.LockThreadCuration(tx, user, curation); err != nil {
			return err
		}
		oldHash, old, found, err := commands.ReadCombinationCartCommand(tx, user, in.CommandID)
		if err != nil {
			return err
		}
		if found {
			if oldHash != hash {
				return fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", false)
			}
			result = old
			return nil
		}
		t, plan, err := s.readCombination(tx, user, curation, thread)
		if err != nil {
			return err
		}
		status, err := s.combinationStatus(tx, t, plan)
		if err != nil {
			return err
		}
		if !status.Current {
			return fault.New(fault.Conflict, status.ReasonCode, false)
		}
		if in.ExpectedVersion != status.CartVersion {
			return fault.New(fault.Conflict, "PHASE8_CART_VERSION_CONFLICT", true)
		}
		if plan.Compatibility == "CONFLICT" {
			return fault.New(fault.Conflict, "COMBINATION_INCOMPATIBLE", false)
		}
		current, err := s.combinationCart.Get(tx, user, curation)
		if err != nil {
			return err
		}
		if err := commands.ValidateCombinationCartSelections(tx, user, curation, in.Items); err != nil {
			return err
		}
		next, err := MergeCombinationCart(current.Items, *plan, in.Mode, in.Items)
		if err != nil {
			return err
		}
		result, err = s.combinationCart.Replace(tx, user, curation, in.ExpectedVersion, next)
		if err != nil {
			return err
		}
		return commands.SaveCombinationCartCommand(tx, user, curation, in.CommandID, hash, result, s.curation.clock.Now())
	})
	return result, err
}

// MergeCombinationCart only touches explicitly selected supported targets.
// ADD keeps prior rows and does not increase an existing variant; REPLACE replaces the
// selected targets, never the entire Cart.
func MergeCombinationCart(current []CatalogCartItemV2, plan d.CombinationResponse, mode string, selected []CatalogCartItemV2) ([]CatalogCartItemV2, error) {
	invalid := func(reason string) ([]CatalogCartItemV2, error) {
		return nil, fault.New(fault.InvalidInput, reason, false)
	}
	allowed := map[string]d.CombinationItem{}
	for _, item := range plan.Items {
		allowed[item.TargetID+"\x00"+item.CandidateID] = item
	}
	targets := map[string]bool{}
	seen := map[string]bool{}
	for _, row := range selected {
		item, ok := allowed[row.TargetID+"\x00"+row.Item.CandidateID]
		if !ok {
			return invalid("COMBINATION_ITEM_NOT_OFFERED")
		}
		if !item.CheckoutEligible {
			return invalid("EXTERNAL_PRODUCT_CART_FORBIDDEN")
		}
		if item.VariantID != "" && item.VariantID != row.Item.VariantID {
			return invalid("COMBINATION_VARIANT_CHANGED")
		}
		if item.Quantity > 0 && item.Quantity != row.Item.Quantity {
			return invalid("COMBINATION_QUANTITY_CHANGED")
		}
		key := row.Item.CandidateID + "\x00" + row.Item.VariantID
		if seen[key] || strings.TrimSpace(row.Item.VariantID) == "" || row.Item.Quantity < 1 || row.Item.Quantity > 99 {
			return invalid("COMBINATION_CART_INVALID")
		}
		seen[key] = true
		targets[row.TargetID] = true
	}
	next := []CatalogCartItemV2{}
	for _, row := range current {
		if mode != "REPLACE" || !targets[row.TargetID] {
			next = append(next, row)
		}
	}
	for _, row := range selected {
		matched := -1
		for i, old := range next {
			if old.Item.CandidateID == row.Item.CandidateID && old.Item.VariantID == row.Item.VariantID {
				matched = i
				break
			}
		}
		if matched >= 0 {
			if next[matched].TargetID != row.TargetID {
				return invalid("COMBINATION_CART_INVALID")
			}
			if mode == "ADD" {
				continue
			}
			// An already selected row in the default plan is preserved, not added twice.
			preserved := false
			for _, offered := range plan.Items {
				preserved = preserved || (offered.TargetID == row.TargetID && offered.CandidateID == row.Item.CandidateID && offered.CartItemID == next[matched].Item.ID)
			}
			if preserved {
				continue
			}
			next[matched].Item.Quantity += row.Item.Quantity
		} else {
			row.Item.ID = "cart:" + row.Item.CandidateID + ":" + row.Item.VariantID
			for _, old := range current {
				if old.Item.CandidateID == row.Item.CandidateID && old.Item.VariantID == row.Item.VariantID {
					row.Item.ID = old.Item.ID
					break
				}
			}
			next = append(next, row)
		}
	}
	if len(next) > 10 {
		return invalid("COMBINATION_CART_LIMIT")
	}
	return next, nil
}

// ApplyRepresentativeCart adds the products the reader currently sees. The initial
// AI reply is provenance, not an authorization fence on later user selections.
func (s *ThreadService) ApplyRepresentativeCart(ctx context.Context, user, curation string, in CombinationCartCommand) (CatalogCartStateV2, error) {
	var result CatalogCartStateV2
	if !uuidPattern.MatchString(in.CommandID) || in.ExpectedVersion < 0 || in.Mode != "ADD" || len(in.Items) < 1 || len(in.Items) > 10 {
		return result, fault.New(fault.InvalidInput, "COMBINATION_CART_INVALID", false)
	}
	commands, ok := s.repo.(combinationCartCommands)
	if !ok || s.combinationCart == nil {
		return result, fault.New(fault.ProviderUnavailable, "COMBINATION_UNAVAILABLE", false)
	}
	hash, err := shared.CanonicalJSONHash(struct {
		Kind, Curation string
		Input          CombinationCartCommand
	}{"CURRENT_REPRESENTATIVES", curation, in})
	if err != nil {
		return result, err
	}
	err = s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if err := s.repo.LockThreadCuration(tx, user, curation); err != nil {
			return err
		}
		oldHash, old, found, err := commands.ReadCombinationCartCommand(tx, user, in.CommandID)
		if err != nil {
			return err
		}
		if found {
			if oldHash != hash {
				return fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", false)
			}
			result = old
			return nil
		}
		current, err := s.combinationCart.Get(tx, user, curation)
		if err != nil {
			return err
		}
		if current.Version != in.ExpectedVersion {
			return fault.New(fault.Conflict, "PHASE8_CART_VERSION_CONFLICT", false)
		}
		if err = commands.ValidateCombinationCartSelections(tx, user, curation, in.Items); err != nil {
			return err
		}
		plan := d.CombinationResponse{}
		targets := map[string]bool{}
		for _, row := range in.Items {
			if targets[row.TargetID] {
				return fault.New(fault.InvalidInput, "COMBINATION_CART_INVALID", false)
			}
			targets[row.TargetID] = true
			plan.Items = append(plan.Items, d.CombinationItem{TargetID: row.TargetID, CandidateID: row.Item.CandidateID, VariantID: row.Item.VariantID, Quantity: row.Item.Quantity, CheckoutEligible: true})
		}
		next, err := MergeCombinationCart(current.Items, plan, "ADD", in.Items)
		if err != nil {
			return err
		}
		result, err = s.combinationCart.Replace(tx, user, curation, in.ExpectedVersion, next)
		if err != nil {
			return err
		}
		return commands.SaveCombinationCartCommand(tx, user, curation, in.CommandID, hash, result, s.curation.clock.Now())
	})
	return result, err
}
