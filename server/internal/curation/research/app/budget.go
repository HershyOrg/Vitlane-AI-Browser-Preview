package app

import (
	"context"
	"encoding/json"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"math/big"
	"time"
)

type BudgetReader interface {
	Budget(context.Context, string, string) (curationdomain.BudgetLedger, error)
	BudgetForPlan(context.Context, string, string) (curationdomain.BudgetLedger, error)
}

type ResearchBudgetSnapshot struct {
	Budget           curationdomain.BudgetResearchSnapshot `json:"budget"`
	ExchangeRate     ExchangeRateView                      `json:"exchangeRate"`
	ProviderCurrency string                                `json:"providerCurrency"`
	MaximumUnitMinor *int64                                `json:"maximumUnitMinor"`
}

func (s *LiveCatalogReviewServiceV2) EnableBudgetReader(reader BudgetReader) { s.budgetReader = reader }

func captureResearchBudget(ctx context.Context, b curationdomain.BudgetLedger, target, country string, fx *ExchangeRateService) (*ResearchBudgetSnapshot, error) {
	snapshot, err := b.ResearchSnapshot(target)
	if err != nil {
		return nil, err
	}
	result := &ResearchBudgetSnapshot{Budget: snapshot, ProviderCurrency: "USD", ExchangeRate: ExchangeRateView{SchemaVersion: "vitlane.exchange-rate.v1", Status: "UNAVAILABLE"}}
	if country == "KR" {
		result.ProviderCurrency = "KRW"
	}
	if !b.Enabled {
		return result, nil
	}
	minor, err := curationdomain.BudgetMinor(*snapshot.Amount, b.Currency)
	if err != nil {
		return nil, err
	}
	value := big.NewRat(minor, int64(snapshot.Quantity))
	if b.Currency != result.ProviderCurrency {
		if fx != nil {
			result.ExchangeRate, err = fx.View(ctx)
			if err != nil {
				return nil, err
			}
		}
		if result.ExchangeRate.Rate == nil || !result.ExchangeRate.Rate.Valid(time.Now().UTC()) {
			return nil, fault.New(fault.ProviderUnavailable, "BUDGET_EXCHANGE_RATE_UNAVAILABLE", true)
		}
		factor, _ := new(big.Rat).SetString(result.ExchangeRate.Rate.Rate)
		factor.Quo(factor, big.NewRat(100, 1))
		if b.Currency == "KRW" {
			factor.Inv(factor)
		}
		value.Mul(value, factor)
	}
	whole := new(big.Int).Quo(value.Num(), value.Denom())
	if !whole.IsInt64() || whole.Int64() > 9007199254740991 {
		return nil, fault.New(fault.InvalidInput, "BUDGET_INVALID", false)
	}
	maximum := whole.Int64()
	result.MaximumUnitMinor = &maximum
	return result, nil
}

// Both provider query bounds and admission use the frozen per-unit maximum.
// Legacy Range minimums are never carried into a budget-aware command.
func applyResearchBudget(profile CatalogTargetSearchProfileV2, snapshot *ResearchBudgetSnapshot) CatalogTargetSearchProfileV2 {
	if snapshot == nil {
		return profile
	}
	profile.MinimumPrice = nil
	profile.MaximumPrice = nil
	profile.Market.Currency = snapshot.ProviderCurrency
	if snapshot.MaximumUnitMinor != nil {
		amount := curationdomain.BudgetAmount(*snapshot.MaximumUnitMinor, snapshot.ProviderCurrency)
		value, _ := shareddomain.NewMoney(amount, snapshot.ProviderCurrency)
		profile.MaximumPrice = &value
	}
	return profile
}

type budgetCommandSnapshotRepository interface {
	ReadSearchBudgetSnapshot(context.Context, string, string, string, string) (*ResearchBudgetSnapshot, error)
}

func (s *LiveCatalogReviewServiceV2) captureAppendBudget(ctx context.Context, input CatalogWorkspaceSearchInputV2, profile CatalogTargetSearchProfileV2) (*ResearchBudgetSnapshot, error) {
	if s.budgetReader == nil {
		return nil, nil
	}
	if repo, ok := s.workspace.(budgetCommandSnapshotRepository); ok {
		saved, err := repo.ReadSearchBudgetSnapshot(ctx, input.UserID, input.CurationID, input.TargetID, input.IdempotencyKey)
		if err != nil || saved != nil {
			return saved, err
		}
	}
	b, err := s.budgetReader.Budget(ctx, input.UserID, input.CurationID)
	if err != nil {
		return nil, err
	}
	return captureResearchBudget(ctx, b, input.TargetID, profile.Market.Country, s.exchangeRate)
}

func (s *Service) attachResearchBudget(ctx context.Context, user, plan string, raw json.RawMessage) (json.RawMessage, error) {
	reader, ok := s.plans.(BudgetReader)
	if !ok {
		return raw, nil
	}
	var snapshot ResearchContext
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	b, err := reader.BudgetForPlan(ctx, user, plan)
	if err != nil {
		return nil, err
	}
	var fx *ExchangeRateService
	if s.liveCatalog != nil {
		fx = s.liveCatalog.exchangeRate
	}
	snapshot.Budget, err = captureResearchBudget(ctx, b, snapshot.Target.ID, string(snapshot.ResearchScope.Country), fx)
	if err != nil {
		return nil, err
	}
	snapshot.ResearchScope.MinPrice = nil
	snapshot.ResearchScope.MaxPrice = nil
	// Original Target snapshot is provenance. Consumers use Budget for current policy.
	return json.Marshal(snapshot)
}
