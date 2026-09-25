package domain

import (
	"fmt"
	"net/url"
	"strings"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

type ShoppingPlanID string
type CurationID string
type PlanTargetID string
type PlanningTaskID string
type PlanningProposalID string
type CurationRunID string
type UserID string
type TargetHash string

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

// CurationPhase is the only persisted user-journey phase. HAVING_INTENT is a
// browser draft and AgencyOrder/Payment/Intelligence keep their own lifecycles.
type CurationPhase string

const (
	CurationPhasePlanning CurationPhase = "PLANNING"
	CurationPhaseCurating CurationPhase = "CURATING"
)

type PlanningTaskStatus string

const (
	PlanningTaskStatusRequested PlanningTaskStatus = "REQUESTED"
	PlanningTaskStatusCompleted PlanningTaskStatus = "COMPLETED"
	PlanningTaskStatusCancelled PlanningTaskStatus = "CANCELLED"
)

type CurationRunKind string

const (
	CurationRunInitial   CurationRunKind = "INITIAL"
	CurationRunExpansion CurationRunKind = "EXPANSION"
)

type CurationRunStatus string

const (
	CurationRunRequested     CurationRunStatus = "REQUESTED"
	CurationRunMaterializing CurationRunStatus = "MATERIALIZING"
	CurationRunCompleted     CurationRunStatus = "COMPLETED"
	CurationRunFailed        CurationRunStatus = "FAILED"
	CurationRunCancelled     CurationRunStatus = "CANCELLED"
)

type ProposalValidationStatus string

const (
	ProposalValidationAccepted ProposalValidationStatus = "ACCEPTED"
	ProposalValidationRejected ProposalValidationStatus = "REJECTED"
)

const (
	PlanningProposalSchemaV1 = "vitlane.planning-proposal.v1"
	PlanTargetHashSchemaV1   = "vitlane.plan-target.v1"
	MaxPlanTargets           = 10
	// MaxConcurrentUserActions bounds one user's share of the provider. A plan
	// already runs one action at a time, so this is the ceiling across the
	// plans a user has open at once.
	MaxConcurrentUserActions = 3
)

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
		return fmt.Errorf("%w: unknown mode %q", ErrURLModeInvalid, s.URLMode)
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

func (s ResearchScope) ValidateForConfirmation() error {
	if strings.TrimSpace(s.Category) == "" {
		return fmt.Errorf("%w: category is required", ErrTargetIncomplete)
	}
	return s.Validate()
}

func cleanItems(items []string) []string {
	if items == nil {
		return []string{}
	}
	seen := make(map[string]struct{}, len(items))
	cleaned := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, item)
	}
	return cleaned
}
