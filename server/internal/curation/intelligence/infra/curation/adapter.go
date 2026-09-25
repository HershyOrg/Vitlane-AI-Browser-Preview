// Package curation adapts the Curation product onto the intelligence ports.
//
// The translation lives here so intelligence/app never imports a product type
// and curation never imports a provider type. Neither side can grow a
// dependency on the other's internals.
package curation

import (
	"context"
	"errors"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

// JobCreator lets Curation open an intelligence job inside its own product
// transaction, so a planning task and the job that runs it commit together.
type JobCreator struct {
	intelligence *intelligenceapp.Service
}

func NewJobCreator(intelligence *intelligenceapp.Service) *JobCreator {
	return &JobCreator{intelligence: intelligence}
}

func (c *JobCreator) CreatePlanningJob(
	ctx context.Context,
	input curationapp.CreatePlanningJobInput,
) (curationapp.IntelligenceJobRef, error) {
	ref, err := c.intelligence.CreateJob(ctx, intelligenceapp.CreateJobInput{
		UserID: input.UserID, CurationID: input.CurationID,
		CurationActionID: input.CurationActionID, PlanID: input.PlanID,
		Target: intelligencedomain.JobTarget{
			Kind: intelligencedomain.TargetPlanningTask,
			ID:   input.PlanningTaskID,
		},
		Provider: intelligencedomain.ProviderKind(input.Provider),
		ModelKey: input.ModelKey,
	})
	if err != nil {
		return curationapp.IntelligenceJobRef{}, err
	}
	return curationapp.IntelligenceJobRef{
		JobID: ref.JobID, Replay: ref.Replay,
	}, nil
}

// LockActionAdmission, PlanHasActiveWork and ActiveActionCount let Curation enforce the action
// limits inside the same transaction that would create the next one.
func (c *JobCreator) LockActionAdmission(
	ctx context.Context,
	userID string,
) error {
	return c.intelligence.LockActionAdmission(ctx, userID)
}

func (c *JobCreator) PlanHasActiveWork(
	ctx context.Context,
	userID string,
	planID string,
) (bool, error) {
	return c.intelligence.PlanHasActiveWork(ctx, userID, planID)
}

func (c *JobCreator) ActiveActionCount(
	ctx context.Context,
	userID string,
) (int, error) {
	return c.intelligence.ActiveActionCount(ctx, userID)
}

func (c *JobCreator) HasActiveCurationWork(
	ctx context.Context,
	userID string,
	curationID string,
) (bool, error) {
	return c.intelligence.CurationHasActiveWork(ctx, userID, curationID)
}

// ProductAdapter implements the planning half of the intelligence ProductPort.
// The research half is supplied separately and composed here, so a job that
// runs research never reaches curation code and the reverse.
type ProductAdapter struct {
	curation *curationapp.Service
	research ResearchProduct
}

// ResearchProduct is the research half of the ProductPort. It is an interface
// rather than a concrete service so this package does not import research.
type ResearchProduct interface {
	ReadResearchContext(
		ctx context.Context, userID string, jobID string, roundID string,
	) (intelligenceapp.ResearchContext, error)
	RunCatalogResearch(
		ctx context.Context, userID string, jobID string, attemptID string,
		roundID string, query intelligenceapp.CatalogQueryPayload,
		ranker intelligenceapp.ResearchCandidateRanker,
	) (intelligenceapp.ResearchExecutionOutcome, error)
	FailResearchTarget(
		ctx context.Context, userID string, roundID string,
		reasonCode string, retryable bool,
	) error
	StartReadySessions(
		ctx context.Context, userID string,
		planID string, curationActionID string,
	) (int, error)
	// StartResearchForPlan opens the first research round for every session on
	// a plan that just finished planning.
	StartResearchForPlan(
		ctx context.Context, userID string, planID string,
		curationID string, curationVersion int64, sessionIDs []string,
	) error
	CancelRound(ctx context.Context, userID string, roundID string) error
}

func NewProductAdapter(
	curation *curationapp.Service,
	research ResearchProduct,
) *ProductAdapter {
	return &ProductAdapter{curation: curation, research: research}
}

func (a *ProductAdapter) ReadPlanningContext(
	ctx context.Context,
	userID string,
	taskID string,
) (intelligenceapp.PlanningContext, error) {
	planningContext, err := a.curation.GetPlanningContextForIntelligence(
		ctx, userID, taskID,
	)
	if err != nil {
		return intelligenceapp.PlanningContext{}, err
	}
	return toPlanningContext(planningContext), nil
}

func (a *ProductAdapter) SubmitPlanning(
	ctx context.Context,
	userID string,
	jobID string,
	planningContext intelligenceapp.PlanningContext,
	targets []intelligenceapp.ProposedTarget,
) (intelligenceapp.SubmissionOutcome, error) {
	input := curationapp.SubmitPlanningProposalInput{
		TaskID:           planningContext.TaskID,
		ClientProposalID: jobID,
		ContextVersion:   planningContext.ContextVersion,
		ContextHash:      planningContext.ContextHash,
		SchemaVersion:    curationdomain.PlanningProposalSchemaV1,
		Targets:          toTargetInputs(targets),
	}
	if planningContext.BudgetResolved || planningContext.ResolvedBudget != nil {
		input.ResolvedBudget = &curationdomain.InitialBudgetRequest{SchemaVersion: curationdomain.BudgetSchema, InputMode: "EXPLICIT", Currency: planningContext.BudgetCurrency, AllocationMode: planningContext.BudgetAllocationMode}
		if planningContext.ResolvedBudget != nil {
			amount := planningContext.ResolvedBudget.Amount
			input.ResolvedBudget.TotalAmount = &amount
			input.ResolvedBudget.Currency = planningContext.ResolvedBudget.Currency
		}
	}
	if planningContext.BudgetDecision != nil {
		input.AutoBudgetDecision = &curationdomain.ActionDecision{Kind: "BUDGET", Result: planningContext.BudgetDecision.Kind, Source: "MANAGED", Evidence: planningContext.BudgetDecision.Evidence, ReasonCode: "INITIAL_AUTO_BUDGET"}
	}
	if planningContext.BudgetPolicy {
		input.BudgetVersion = &planningContext.BudgetVersion
	}
	result, err := a.curation.SubmitPlanningProposalForIntelligence(
		ctx, userID, jobID, input,
	)
	if err != nil {
		// A rejected proposal is a real answer about the content, not a
		// transport failure, so it is reported as an outcome rather than an
		// error the pipeline would classify as a provider fault.
		var rejected *curationapp.RejectedProposalError
		if errors.As(err, &rejected) {
			return intelligenceapp.SubmissionOutcome{
				Accepted:   false,
				ReasonCode: curationdomain.ErrProposalInvalid.Error(),
			}, nil
		}
		return intelligenceapp.SubmissionOutcome{}, err
	}
	if result.ValidationStatus != curationdomain.ProposalValidationAccepted {
		return intelligenceapp.SubmissionOutcome{
			Accepted:   false,
			ReasonCode: firstReason(result.ReasonCodes),
		}, nil
	}
	return intelligenceapp.SubmissionOutcome{
		Accepted: true, ResultID: result.ProposalID,
	}, nil
}

func (a *ProductAdapter) ReadResearchContext(
	ctx context.Context,
	userID string,
	jobID string,
	roundID string,
) (intelligenceapp.ResearchContext, error) {
	return a.research.ReadResearchContext(ctx, userID, jobID, roundID)
}

func (a *ProductAdapter) RunCatalogResearch(
	ctx context.Context,
	userID string,
	jobID string,
	attemptID string,
	roundID string,
	query intelligenceapp.CatalogQueryPayload,
	ranker intelligenceapp.ResearchCandidateRanker,
) (intelligenceapp.ResearchExecutionOutcome, error) {
	return a.research.RunCatalogResearch(
		ctx, userID, jobID, attemptID, roundID, query, ranker,
	)
}

func (a *ProductAdapter) FailResearchTarget(
	ctx context.Context,
	userID string,
	roundID string,
	reasonCode string,
	retryable bool,
) error {
	return a.research.FailResearchTarget(
		ctx, userID, roundID, reasonCode, retryable,
	)
}

// StartCurating advances a plan whose planning just finished. When the curation
// is already curating, the new Target left a READY session with no round, so
// research still has to start: planning and research run back to back for the
// same reason they do on the first pass — the user should not have to press a
// second button for a step the Server can take.
func (a *ProductAdapter) StartCurating(
	ctx context.Context,
	userID string,
	planID string,
	curationActionID string,
) (bool, error) {
	plan, err := a.curation.Get(ctx, userID, planID)
	if err != nil {
		return false, err
	}
	if plan.Curation.Phase != curationdomain.CurationPhasePlanning {
		started, err := a.research.StartReadySessions(
			ctx, userID, planID, curationActionID,
		)
		if err != nil {
			return false, err
		}
		return started > 0, nil
	}
	sessionIDs := make([]string, 0, len(plan.Sessions))
	for _, session := range plan.Sessions {
		sessionIDs = append(sessionIDs, string(session.ID))
	}
	if len(sessionIDs) == 0 {
		return false, nil
	}
	if err := a.research.StartResearchForPlan(
		ctx, userID, planID, string(plan.Curation.ID),
		plan.Curation.Version, sessionIDs,
	); err != nil {
		return false, err
	}
	return true, nil
}

func (a *ProductAdapter) StartReadySessions(
	ctx context.Context,
	userID string,
	planID string,
	curationActionID string,
) (int, error) {
	return a.research.StartReadySessions(
		ctx, userID, planID, curationActionID,
	)
}

func (a *ProductAdapter) CancelTarget(
	ctx context.Context,
	userID string,
	target intelligencedomain.JobTarget,
) error {
	if target.Kind == intelligencedomain.TargetActionInterpretation {
		return nil
	}
	if target.Kind == intelligencedomain.TargetPlanningTask {
		return a.curation.CancelPlanningTask(ctx, userID, target.ID)
	}
	return a.research.CancelRound(ctx, userID, target.ID)
}

func toPlanningContext(
	source curationapp.PlanningContext,
) intelligenceapp.PlanningContext {
	mode := "AUTO"
	if source.BudgetRequest != nil {
		mode = source.BudgetRequest.AllocationMode
	}
	planningMode := string(source.PlanningMode)
	if source.RunKind != curationdomain.CurationRunInitial {
		if source.ControlMode == "MANUAL" {
			mode = "EQUAL"
			planningMode = "SINGLE"
		} else if source.ControlMode == "AUTO" {
			mode = "AUTO"
			planningMode = "AUTO"
		}
	}
	return intelligenceapp.PlanningContext{
		ContentLocale:          source.ContentLocale,
		BudgetInferenceAllowed: source.RunKind == curationdomain.CurationRunInitial && source.BudgetRequest != nil && source.BudgetRequest.AllowsInference(),
		BudgetVersion:          source.BudgetVersion, BudgetPolicy: source.BudgetCurrency != "", BudgetEnabled: source.BudgetEnabled, BudgetCurrency: source.BudgetCurrency, BudgetAllocationMode: mode,
		TaskID:         source.TaskID,
		PlanID:         source.PlanID,
		ContextVersion: source.ContextVersion,
		ContextHash:    source.ContextHash,
		ProposalSchema: source.ProposalSchema,
		OriginalIntent: source.OriginalIntent,
		PlanningMode:   planningMode,
		TotalBudget:    toMoney(source.TotalBudget),
		Country:        string(source.Location.Country),
		City:           source.Location.City,
		Category:       source.ResearchScope.Category,
		AllowedItems:   source.ResearchScope.AllowedItems,
		BlockedItems:   source.ResearchScope.BlockedItems,
		MinPrice:       optionalMoney(source.ResearchScope.MinPrice),
		MaxPrice:       optionalMoney(source.ResearchScope.MaxPrice),
		ReferenceURL:   source.ResearchScope.ReferenceURL,
		URLMode:        string(source.ResearchScope.URLMode),
		MinimumTargets: source.MinimumTargetCount,
		MaximumTargets: source.MaximumTargetCount,
		InitialRun:     source.RunKind == curationdomain.CurationRunInitial,
	}
}

func toTargetInputs(
	targets []intelligenceapp.ProposedTarget,
) []curationapp.TargetInput {
	result := make([]curationapp.TargetInput, 0, len(targets))
	for _, target := range targets {
		result = append(result, curationapp.TargetInput{
			ContentLocale: target.ContentLocale, Criteria: toCriteria(target.Criteria), Quantity: target.Quantity,
			Title: target.Title, ProductVertical: target.ProductVertical,
			NormalizedIntent: target.SearchQuery,
			Category:         target.Category,
			AllocatedBudget: curationapp.MoneyInput{
				Amount: target.Budget.Amount, Currency: target.Budget.Currency,
			},
			AllowedItems: []string{}, BlockedItems: []string{},
			URLMode: "NONE",
		})
	}
	return result
}

func toMoney(source shareddomain.Money) intelligenceapp.Money {
	return intelligenceapp.Money{
		Amount: source.Amount, Currency: string(source.Currency),
	}
}

func optionalMoney(source *shareddomain.Money) *intelligenceapp.Money {
	if source == nil {
		return nil
	}
	money := toMoney(*source)
	return &money
}

func firstReason(codes []string) string {
	if len(codes) == 0 {
		return curationdomain.ErrProposalInvalid.Error()
	}
	return codes[0]
}
