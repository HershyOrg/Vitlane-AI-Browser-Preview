package app

import planningdomain "github.com/vitlane/vitlane/server/internal/curation/planning/domain"

var (
	ErrIntentRequired         = planningdomain.ErrIntentRequired
	ErrBudgetNonPositive      = planningdomain.ErrBudgetNonPositive
	ErrPlanningModeInvalid    = planningdomain.ErrPlanningModeInvalid
	ErrExecutionModeInvalid   = planningdomain.ErrExecutionModeInvalid
	ErrLiveCountryUnsupported = planningdomain.ErrLiveCountryUnsupported
	ErrPriceRangeInvalid      = planningdomain.ErrPriceRangeInvalid
	ErrURLModeInvalid         = planningdomain.ErrURLModeInvalid
	ErrAgentModeInvalid       = planningdomain.ErrAgentModeInvalid
)

type ShoppingPlan = planningdomain.ShoppingPlan
type ShoppingPlanID = planningdomain.ShoppingPlanID
type UserID = planningdomain.UserID
type PlanningMode = planningdomain.PlanningMode
type ExecutionMode = planningdomain.ExecutionMode
type AgentMode = planningdomain.AgentMode
type URLMode = planningdomain.URLMode
type ResearchScope = planningdomain.ResearchScope
type NewShoppingPlanInput = planningdomain.NewShoppingPlanInput

const (
	PlanningModeSingle = planningdomain.PlanningModeSingle
	PlanningModeAuto   = planningdomain.PlanningModeAuto
)

func NewShoppingPlan(input NewShoppingPlanInput) (ShoppingPlan, error) {
	return planningdomain.NewShoppingPlan(input)
}
