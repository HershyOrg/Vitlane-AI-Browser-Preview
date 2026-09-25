package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

type PlanSnapshot struct {
	BudgetRequest   *InitialBudgetRequest        `json:"budgetRequest,omitempty"`
	ID              ShoppingPlanID               `json:"id"`
	UserID          UserID                       `json:"userId"`
	OriginalIntent  string                       `json:"originalIntent"`
	PlanningMode    PlanningMode                 `json:"planningMode"`
	ExecutionMode   ExecutionMode                `json:"executionMode"`
	TotalBudget     shareddomain.Money           `json:"totalBudget"`
	LocationContext shareddomain.LocationContext `json:"locationContext"`
	ResearchScope   ResearchScope                `json:"researchScope"`
	// AgentMode and ModelKey are part of the immutable settings snapshot.
	// ADR-0032 fixes both at submission: switching execution owner mid-run
	// would change the claim subject of attempts already in flight.
	AgentMode AgentMode `json:"agentMode"`
	ModelKey  string    `json:"modelKey,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// AgentMode says who executes this plan's Agent work.
type AgentMode string

const (
	AgentModeManaged  AgentMode = "MANAGED"
	AgentModeExternal AgentMode = "EXTERNAL"
)

func (m AgentMode) Valid() bool {
	return m == AgentModeManaged || m == AgentModeExternal
}

type PlanTarget struct {
	ProductVertical        string             `json:"productVertical,omitempty"`
	ID                     PlanTargetID       `json:"id"`
	CurationID             CurationID         `json:"curationId"`
	UserID                 UserID             `json:"userId"`
	PlanID                 ShoppingPlanID     `json:"planId"`
	CreatedByCurationRunID *CurationRunID     `json:"createdByCurationRunId,omitempty"`
	Title                  string             `json:"title"`
	NormalizedIntent       string             `json:"normalizedIntent"`
	Category               string             `json:"category,omitempty"`
	AllocatedBudget        shareddomain.Money `json:"allocatedBudget"`
	ResearchScope          ResearchScope      `json:"researchScope"`
	OrderIndex             int                `json:"orderIndex"`
	ConfirmedAt            *time.Time         `json:"confirmedAt,omitempty"`
	TargetHash             TargetHash         `json:"targetHash,omitempty"`
	TargetHashSchema       string             `json:"targetHashSchema"`
	Version                int64              `json:"version"`
	CreatedAt              time.Time          `json:"createdAt"`
	UpdatedAt              time.Time          `json:"updatedAt"`
	RemovedAt              *time.Time         `json:"removedAt,omitempty"`
	RemovedByUserID        *UserID            `json:"removedByUserId,omitempty"`
}

type PlanningTask struct {
	ID                  PlanningTaskID     `json:"id"`
	PlanID              ShoppingPlanID     `json:"planId"`
	UserID              UserID             `json:"userId"`
	ContextVersion      int64              `json:"contextVersion"`
	ContextHash         string             `json:"contextHash"`
	Status              PlanningTaskStatus `json:"status"`
	ExpiresAt           time.Time          `json:"expiresAt"`
	CompletedAt         *time.Time         `json:"completedAt,omitempty"`
	FirstDiscoveredAt   *time.Time         `json:"firstDiscoveredAt,omitempty"`
	FirstContextReadAt  *time.Time         `json:"firstContextReadAt,omitempty"`
	LastAgentActivityAt *time.Time         `json:"lastAgentActivityAt,omitempty"`
	CreatedAt           time.Time          `json:"createdAt"`
	UpdatedAt           time.Time          `json:"updatedAt"`
}

type PlanningProposal struct {
	ID                    PlanningProposalID       `json:"id"`
	TaskID                PlanningTaskID           `json:"taskId"`
	PlanID                ShoppingPlanID           `json:"planId"`
	UserID                UserID                   `json:"userId"`
	IntelligenceJobID     string                   `json:"intelligenceJobId,omitempty"`
	ClientProposalID      string                   `json:"clientProposalId"`
	ContextVersion        int64                    `json:"contextVersion"`
	ContextHash           string                   `json:"contextHash"`
	SchemaVersion         string                   `json:"schemaVersion"`
	ProposalHash          string                   `json:"proposalHash"`
	Payload               json.RawMessage          `json:"payload"`
	ValidationStatus      ProposalValidationStatus `json:"validationStatus"`
	ValidationReasonCodes []string                 `json:"validationReasonCodes"`
	SubmittedAt           time.Time                `json:"submittedAt"`
}

type CurationRun struct {
	ID             CurationRunID     `json:"id"`
	CurationID     CurationID        `json:"curationId"`
	PlanID         ShoppingPlanID    `json:"planId"`
	UserID         UserID            `json:"userId"`
	Kind           CurationRunKind   `json:"kind"`
	Instruction    string            `json:"instruction"`
	PlanningTaskID PlanningTaskID    `json:"planningTaskId"`
	Status         CurationRunStatus `json:"status"`
	IdempotencyKey string            `json:"-"`
	RequestHash    []byte            `json:"-"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
	CompletedAt    *time.Time        `json:"completedAt,omitempty"`
}

type NewPlanSnapshotInput struct {
	BudgetRequest   *InitialBudgetRequest
	PlanID          ShoppingPlanID
	TargetID        PlanTargetID
	TaskID          PlanningTaskID
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
	TaskExpiresAt   time.Time
}

func NewPlanSnapshot(input NewPlanSnapshotInput) (PlanSnapshot, []PlanTarget, *PlanningTask, error) {
	if input.BudgetRequest != nil {
		if err := input.BudgetRequest.Validate(); err != nil {
			return PlanSnapshot{}, nil, nil, err
		}
	}
	intent := strings.TrimSpace(input.OriginalIntent)
	if intent == "" {
		return PlanSnapshot{}, nil, nil, ErrIntentRequired
	}
	if input.TotalBudget.Sign() < 0 || (input.BudgetRequest == nil && input.TotalBudget.Sign() == 0) {
		return PlanSnapshot{}, nil, nil, ErrBudgetNonPositive
	}
	if input.PlanningMode != PlanningModeSingle && input.PlanningMode != PlanningModeAuto {
		return PlanSnapshot{}, nil, nil, fmt.Errorf("%w: %q", ErrPlanningModeInvalid, input.PlanningMode)
	}
	if input.ExecutionMode != ExecutionModeExperiment && input.ExecutionMode != ExecutionModeLive {
		return PlanSnapshot{}, nil, nil, fmt.Errorf("%w: %q", ErrExecutionModeInvalid, input.ExecutionMode)
	}
	if input.ExecutionMode == ExecutionModeLive && input.LocationContext.Country != "US" && input.LocationContext.Country != "KR" {
		return PlanSnapshot{}, nil, nil, ErrLiveCountryUnsupported
	}
	researchScope := input.ResearchScope
	if input.PlanningMode == PlanningModeSingle &&
		strings.TrimSpace(researchScope.Category) == "" {
		researchScope.Category = makeTitle(intent)
	}
	if err := researchScope.Validate(); err != nil {
		return PlanSnapshot{}, nil, nil, err
	}
	if err := validatePriceCurrency(researchScope, input.TotalBudget); err != nil {
		return PlanSnapshot{}, nil, nil, err
	}
	agentMode := input.AgentMode
	if agentMode == "" {
		agentMode = AgentModeExternal
	}
	if !agentMode.Valid() {
		return PlanSnapshot{}, nil, nil, fmt.Errorf(
			"%w: %q", ErrAgentModeInvalid, agentMode)
	}
	modelKey := strings.TrimSpace(input.ModelKey)
	// A MANAGED plan without a model is unexecutable, and a model key on an
	// EXTERNAL plan would imply the server picked something it never runs.
	if (agentMode == AgentModeManaged) != (modelKey != "") {
		return PlanSnapshot{}, nil, nil, ErrAgentModeInvalid
	}

	plan := PlanSnapshot{
		BudgetRequest: input.BudgetRequest,
		ID:            input.PlanID, UserID: input.UserID, OriginalIntent: intent,
		PlanningMode: input.PlanningMode, ExecutionMode: input.ExecutionMode,
		TotalBudget: input.TotalBudget, LocationContext: input.LocationContext,
		ResearchScope: researchScope,
		AgentMode:     agentMode, ModelKey: modelKey,
		CreatedAt: input.Now,
	}

	contextHash, err := plan.ContextHash()
	if err != nil {
		return PlanSnapshot{}, nil, nil, err
	}
	task := &PlanningTask{
		ID: input.TaskID, PlanID: input.PlanID, UserID: input.UserID,
		ContextVersion: 1, ContextHash: contextHash,
		Status: PlanningTaskStatusRequested, ExpiresAt: input.TaskExpiresAt,
		CreatedAt: input.Now, UpdatedAt: input.Now,
	}
	return plan, []PlanTarget{}, task, nil
}

// MaterializeInitialTargets validates and confirms the first Agent-planned
// target set. SINGLE requires exactly one target while AUTO permits many.
// without mutating ShoppingPlan. PlanningTask owns the execution lifecycle;
// Curation owns the membership version advanced by the application service.
func MaterializeInitialTargets(
	plan PlanSnapshot,
	task *PlanningTask,
	targets []PlanTarget,
	runID CurationRunID,
	now time.Time,
	resolvedBudget ...*InitialBudgetRequest,
) ([]PlanTarget, error) {
	if plan.PlanningMode != PlanningModeAuto &&
		plan.PlanningMode != PlanningModeSingle {
		return nil, ErrPlanningRequired
	}
	if task == nil ||
		task.PlanID != plan.ID ||
		task.Status != PlanningTaskStatusRequested ||
		!now.Before(task.ExpiresAt) {
		return nil, ErrPlanningTaskClosed
	}
	contextHash, err := plan.ContextHash()
	if err != nil {
		return nil, err
	}
	if task.ContextVersion < 1 || task.ContextHash != contextHash {
		return nil, ErrPlanningContextStale
	}
	if len(resolvedBudget) > 0 {
		plan, err = plan.ResolveInitialBudget(resolvedBudget[0])
		if err != nil {
			return nil, err
		}
	}
	if err := plan.ValidateTargetSet(targets); err != nil {
		return nil, err
	}
	created := slices.Clone(targets)
	for index := range created {
		target := &created[index]
		target.PlanID = plan.ID
		target.OrderIndex = index
		target.TargetHashSchema = PlanTargetHashSchemaV1
		if target.Version == 0 {
			target.Version = 1
		}
		if target.CreatedAt.IsZero() {
			target.CreatedAt = now
		}
		target.UpdatedAt = now
		if strings.TrimSpace(target.Category) == "" {
			return nil, fmt.Errorf("%w: category is required", ErrTargetIncomplete)
		}
		if err := target.ResearchScope.ValidateForConfirmation(); err != nil {
			return nil, err
		}
		hash, err := target.Hash()
		if err != nil {
			return nil, err
		}
		target.TargetHash = hash
		confirmedAt := now
		target.ConfirmedAt = &confirmedAt
		if runID != "" {
			value := runID
			target.CreatedByCurationRunID = &value
		}
	}
	task.Status = PlanningTaskStatusCompleted
	task.CompletedAt = &now
	task.UpdatedAt = now
	return created, nil
}

// AppendConfirmedTargets materializes only newly proposed expansion targets.
// PlanningMode controls initial intent decomposition only; an explicit user +
// request may expand both AUTO and SINGLE Curations. Existing targets remain
// immutable and ShoppingPlan remains unchanged.
func AppendConfirmedTargets(
	plan PlanSnapshot,
	current []PlanTarget,
	additions []PlanTarget,
	runID CurationRunID,
	now time.Time,
) ([]PlanTarget, []PlanTarget, error) {
	if len(additions) == 0 || len(current)+len(additions) > MaxPlanTargets {
		return nil, nil, ErrTargetCountInvalid
	}
	next := slices.Clone(current)
	created := make([]PlanTarget, 0, len(additions))
	nextOrderIndex := 0
	for _, target := range current {
		if target.OrderIndex >= nextOrderIndex {
			nextOrderIndex = target.OrderIndex + 1
		}
	}
	for index := range additions {
		target := additions[index]
		target.PlanID = plan.ID
		target.OrderIndex = nextOrderIndex + index
		target.TargetHashSchema = PlanTargetHashSchemaV1
		target.Version = 1
		target.CreatedAt = now
		target.UpdatedAt = now
		target.CreatedByCurationRunID = &runID
		if err := validateDraftTarget(target, plan.TotalBudget.Currency); err != nil {
			return nil, nil, err
		}
		if comparison, err := target.AllocatedBudget.Compare(plan.TotalBudget); plan.BudgetRequest == nil && (err != nil || comparison > 0) {
			return nil, nil, ErrTargetBudgetInvalid
		}
		if strings.TrimSpace(target.Category) == "" {
			return nil, nil, ErrTargetIncomplete
		}
		if err := target.ResearchScope.ValidateForConfirmation(); err != nil {
			return nil, nil, err
		}
		hash, err := target.Hash()
		if err != nil {
			return nil, nil, err
		}
		target.TargetHash = hash
		target.ConfirmedAt = &now
		created = append(created, target)
		next = append(next, target)
	}
	return next, created, nil
}

func (p PlanSnapshot) ContextHash() (string, error) {
	canonical := struct {
		BudgetRequest   *InitialBudgetRequest        `json:"budgetRequest,omitempty"`
		OriginalIntent  string                       `json:"originalIntent"`
		PlanningMode    PlanningMode                 `json:"planningMode"`
		ExecutionMode   ExecutionMode                `json:"executionMode"`
		TotalBudget     shareddomain.Money           `json:"totalBudget"`
		LocationContext shareddomain.LocationContext `json:"locationContext"`
		ResearchScope   ResearchScope                `json:"researchScope"`
	}{
		BudgetRequest: p.BudgetRequest, OriginalIntent: p.OriginalIntent, PlanningMode: p.PlanningMode,
		ExecutionMode: p.ExecutionMode, TotalBudget: p.TotalBudget,
		LocationContext: p.LocationContext, ResearchScope: p.ResearchScope,
	}
	return hashJSON(canonical)
}

func (t PlanTarget) Hash() (TargetHash, error) {
	canonical := struct {
		Schema           string             `json:"schema"`
		Title            string             `json:"title"`
		NormalizedIntent string             `json:"normalizedIntent"`
		Category         string             `json:"category"`
		AllocatedBudget  shareddomain.Money `json:"allocatedBudget"`
		ResearchScope    ResearchScope      `json:"researchScope"`
	}{
		Schema: PlanTargetHashSchemaV1, Title: t.Title,
		NormalizedIntent: t.NormalizedIntent, Category: t.Category,
		AllocatedBudget: t.AllocatedBudget, ResearchScope: t.ResearchScope,
	}
	value, err := hashJSON(canonical)
	return TargetHash(value), err
}

func HashProposal(payload any) (string, json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("marshal proposal: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), body, nil
}

// ValidateTargetSet checks a proposed initial decomposition against the
// immutable Plan snapshot. It performs no state transition.
func (p PlanSnapshot) ValidateTargetSet(targets []PlanTarget) error {
	if len(targets) == 0 || len(targets) > MaxPlanTargets {
		return ErrTargetCountInvalid
	}
	if p.PlanningMode == PlanningModeSingle && len(targets) != 1 {
		return ErrSingleTargetRequired
	}
	total := new(big.Rat)
	for _, target := range targets {
		if target.PlanID != "" && target.PlanID != p.ID {
			return ErrTargetNotFound
		}
		if err := validateDraftTarget(target, p.TotalBudget.Currency); err != nil {
			return err
		}
		value, ok := new(big.Rat).SetString(target.AllocatedBudget.Amount)
		if !ok {
			return ErrTargetBudgetInvalid
		}
		total.Add(total, value)
	}
	budget, ok := new(big.Rat).SetString(p.TotalBudget.Amount)
	if !ok || (p.BudgetRequest == nil && total.Cmp(budget) > 0) {
		return ErrTargetBudgetInvalid
	}
	return nil
}

func validateDraftTarget(target PlanTarget, currency shareddomain.CurrencyCode) error {
	if strings.TrimSpace(target.Title) == "" || strings.TrimSpace(target.NormalizedIntent) == "" {
		return ErrTargetIncomplete
	}
	if target.AllocatedBudget.Sign() < 0 || target.AllocatedBudget.Currency != currency {
		return ErrTargetBudgetInvalid
	}
	if err := target.ResearchScope.Validate(); err != nil {
		return err
	}
	if target.ResearchScope.Country == "" {
		return ErrTargetIncomplete
	}
	if err := validatePriceCurrency(target.ResearchScope, target.AllocatedBudget); err != nil {
		return err
	}
	return nil
}

func hashJSON(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("hash JSON: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
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
