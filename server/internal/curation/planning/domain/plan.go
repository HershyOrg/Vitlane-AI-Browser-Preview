package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

var (
	ErrPlanNotFound           = errors.New("PLAN_NOT_FOUND")
	ErrIdempotencyKeyReused   = errors.New("IDEMPOTENCY_KEY_REUSED")
	ErrIntentRequired         = errors.New("INTENT_REQUIRED")
	ErrBudgetNonPositive      = errors.New("BUDGET_NON_POSITIVE")
	ErrPlanningModeInvalid    = errors.New("PLANNING_MODE_INVALID")
	ErrExecutionModeInvalid   = errors.New("EXECUTION_MODE_INVALID")
	ErrLiveCountryUnsupported = errors.New("LIVE_COUNTRY_UNSUPPORTED")
	ErrPriceRangeInvalid      = errors.New("PRICE_RANGE_INVALID")
	ErrURLModeInvalid         = errors.New("URL_MODE_INVALID")
	ErrAgentModeInvalid       = errors.New("AGENT_MODE_INVALID")
)

type ShoppingPlanID string
type UserID string

type PlanningMode string

const (
	PlanningModeSingle PlanningMode = "SINGLE"
	PlanningModeAuto   PlanningMode = "AUTO"
)

type ExecutionMode string

const (
	ExecutionModeExperiment ExecutionMode = "EXPERIMENT"
	ExecutionModeLive       ExecutionMode = "LIVE"
)

type AgentMode string

const (
	AgentModeManaged  AgentMode = "MANAGED"
	AgentModeExternal AgentMode = "EXTERNAL"
)

func (m AgentMode) Valid() bool {
	return m == AgentModeManaged || m == AgentModeExternal
}

type URLMode string

const (
	URLModeNone         URLMode = "NONE"
	URLModeReference    URLMode = "REFERENCE"
	URLModeExactProduct URLMode = "EXACT_PRODUCT"
)

type ResearchScope struct {
	Category     string                   `json:"category,omitempty"`
	Country      shareddomain.CountryCode `json:"country"`
	City         string                   `json:"city,omitempty"`
	AllowedItems []string                 `json:"allowedItems"`
	BlockedItems []string                 `json:"blockedItems"`
	MinPrice     *shareddomain.Money      `json:"minPrice,omitempty"`
	MaxPrice     *shareddomain.Money      `json:"maxPrice,omitempty"`
	ReferenceURL string                   `json:"referenceUrl,omitempty"`
	URLMode      URLMode                  `json:"urlMode"`
}

func NewResearchScope(
	category string,
	location shareddomain.LocationContext,
	allowedItems, blockedItems []string,
	minPrice, maxPrice *shareddomain.Money,
	referenceURL string,
	urlMode URLMode,
) (ResearchScope, error) {
	scope := ResearchScope{
		Category:     strings.TrimSpace(category),
		Country:      location.Country,
		City:         strings.TrimSpace(location.City),
		AllowedItems: cleanItems(allowedItems),
		BlockedItems: cleanItems(blockedItems),
		MinPrice:     minPrice,
		MaxPrice:     maxPrice,
		ReferenceURL: strings.TrimSpace(referenceURL),
		URLMode:      urlMode,
	}
	if scope.URLMode == "" {
		scope.URLMode = URLModeNone
	}
	if err := scope.Validate(); err != nil {
		return ResearchScope{}, err
	}
	return scope, nil
}

func (s ResearchScope) Validate() error {
	switch s.URLMode {
	case URLModeNone:
		if s.ReferenceURL != "" {
			return fmt.Errorf("%w: NONE mode cannot include a URL", ErrURLModeInvalid)
		}
	case URLModeReference, URLModeExactProduct:
		parsed, err := url.ParseRequestURI(s.ReferenceURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("%w: URL is required for %s", ErrURLModeInvalid, s.URLMode)
		}
	default:
		return fmt.Errorf("%w: %q", ErrURLModeInvalid, s.URLMode)
	}
	if s.MinPrice != nil && s.MinPrice.Sign() < 0 {
		return ErrPriceRangeInvalid
	}
	if s.MaxPrice != nil && s.MaxPrice.Sign() < 0 {
		return ErrPriceRangeInvalid
	}
	if s.MinPrice != nil && s.MaxPrice != nil {
		comparison, err := s.MinPrice.Compare(*s.MaxPrice)
		if err != nil || comparison > 0 {
			return ErrPriceRangeInvalid
		}
	}
	return nil
}

type ShoppingPlan struct {
	ID              ShoppingPlanID               `json:"id"`
	UserID          UserID                       `json:"userId"`
	OriginalIntent  string                       `json:"originalIntent"`
	PlanningMode    PlanningMode                 `json:"planningMode"`
	ExecutionMode   ExecutionMode                `json:"executionMode"`
	TotalBudget     shareddomain.Money           `json:"totalBudget"`
	LocationContext shareddomain.LocationContext `json:"locationContext"`
	ResearchScope   ResearchScope                `json:"researchScope"`
	AgentMode       AgentMode                    `json:"agentMode"`
	ModelKey        string                       `json:"modelKey,omitempty"`
	CreatedAt       time.Time                    `json:"createdAt"`
}

type NewShoppingPlanInput struct {
	BudgetOptional  bool
	PlanID          ShoppingPlanID
	UserID          UserID
	OriginalIntent  string
	PlanningMode    PlanningMode
	ExecutionMode   ExecutionMode
	TotalBudget     shareddomain.Money
	LocationContext shareddomain.LocationContext
	ResearchScope   ResearchScope
	AgentMode       AgentMode
	ModelKey        string
	Now             time.Time
}

func NewShoppingPlan(input NewShoppingPlanInput) (ShoppingPlan, error) {
	intent := strings.TrimSpace(input.OriginalIntent)
	if intent == "" {
		return ShoppingPlan{}, ErrIntentRequired
	}
	if input.TotalBudget.Sign() < 0 || (!input.BudgetOptional && input.TotalBudget.Sign() == 0) {
		return ShoppingPlan{}, ErrBudgetNonPositive
	}
	if input.PlanningMode != PlanningModeSingle && input.PlanningMode != PlanningModeAuto {
		return ShoppingPlan{}, fmt.Errorf("%w: %q", ErrPlanningModeInvalid, input.PlanningMode)
	}
	if input.ExecutionMode != ExecutionModeExperiment && input.ExecutionMode != ExecutionModeLive {
		return ShoppingPlan{}, fmt.Errorf("%w: %q", ErrExecutionModeInvalid, input.ExecutionMode)
	}
	if input.ExecutionMode == ExecutionModeLive && input.LocationContext.Country != "US" && input.LocationContext.Country != "KR" {
		return ShoppingPlan{}, ErrLiveCountryUnsupported
	}
	scope := input.ResearchScope
	if input.PlanningMode == PlanningModeSingle && strings.TrimSpace(scope.Category) == "" {
		scope.Category = makeTitle(intent)
	}
	if err := scope.Validate(); err != nil {
		return ShoppingPlan{}, err
	}
	if err := validatePriceCurrency(scope, input.TotalBudget); err != nil {
		return ShoppingPlan{}, err
	}
	agentMode := input.AgentMode
	if agentMode == "" {
		agentMode = AgentModeExternal
	}
	if !agentMode.Valid() {
		return ShoppingPlan{}, fmt.Errorf("%w: %q", ErrAgentModeInvalid, agentMode)
	}
	modelKey := strings.TrimSpace(input.ModelKey)
	if (agentMode == AgentModeManaged) != (modelKey != "") {
		return ShoppingPlan{}, ErrAgentModeInvalid
	}
	return ShoppingPlan{
		ID: input.PlanID, UserID: input.UserID, OriginalIntent: intent,
		PlanningMode: input.PlanningMode, ExecutionMode: input.ExecutionMode,
		TotalBudget: input.TotalBudget, LocationContext: input.LocationContext,
		ResearchScope: scope, AgentMode: agentMode, ModelKey: modelKey,
		CreatedAt: input.Now,
	}, nil
}

func (p ShoppingPlan) ContextHash() (string, error) {
	canonical := struct {
		OriginalIntent  string                       `json:"originalIntent"`
		PlanningMode    PlanningMode                 `json:"planningMode"`
		ExecutionMode   ExecutionMode                `json:"executionMode"`
		TotalBudget     shareddomain.Money           `json:"totalBudget"`
		LocationContext shareddomain.LocationContext `json:"locationContext"`
		ResearchScope   ResearchScope                `json:"researchScope"`
	}{
		OriginalIntent: p.OriginalIntent, PlanningMode: p.PlanningMode,
		ExecutionMode: p.ExecutionMode, TotalBudget: p.TotalBudget,
		LocationContext: p.LocationContext, ResearchScope: p.ResearchScope,
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("hash JSON: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func cleanItems(items []string) []string {
	cleaned := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		value := strings.TrimSpace(item)
		key := strings.ToLower(value)
		if value == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func makeTitle(intent string) string {
	runes := []rune(strings.TrimSpace(intent))
	if len(runes) <= 40 {
		return string(runes)
	}
	return string(runes[:40]) + "…"
}

func validatePriceCurrency(scope ResearchScope, budget shareddomain.Money) error {
	if scope.MinPrice != nil && scope.MinPrice.Currency != budget.Currency {
		return fmt.Errorf("%w: min price currency must match budget", ErrPriceRangeInvalid)
	}
	if scope.MaxPrice != nil && scope.MaxPrice.Currency != budget.Currency {
		return fmt.Errorf("%w: max price currency must match budget", ErrPriceRangeInvalid)
	}
	return nil
}
