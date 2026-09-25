// Package research adapts the Research product onto the intelligence ports.
package research

import (
	"context"
	"fmt"
	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"

	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type idGenerator interface {
	NewID() string
}

type researchService interface {
	GetContextForIntelligence(
		context.Context, string, string, string,
	) (researchapp.ContextResult, error)
	StartReadySessionResearch(
		context.Context, researchapp.StartReadySessionResearchInput,
	) (researchapp.StartReadySessionResearchResult, error)
	StartResearch(
		context.Context, researchapp.StartResearchInput,
	) (researchapp.StartResearchResult, error)
	CancelResearchRound(context.Context, string, string) error
	FailResearchRound(context.Context, string, string, string, bool) error
	RunCatalogResearchForIntelligence(
		context.Context, string, string, string, string,
		researchapp.CatalogIntelligenceCatalogQuery,
		researchapp.CatalogIntelligenceCandidateRanker,
	) (researchapp.CatalogIntelligenceResearchResult, error)
}

func (a *Adapter) FailResearchTarget(
	ctx context.Context,
	userID string,
	roundID string,
	reasonCode string,
	retryable bool,
) error {
	return a.research.FailResearchRound(
		ctx, userID, roundID, reasonCode, retryable,
	)
}

// JobCreator lets Research open an intelligence job inside its own product
// transaction, so a round and the job that runs it commit together.
type JobCreator struct {
	intelligence *intelligenceapp.Service
}

func NewJobCreator(intelligence *intelligenceapp.Service) *JobCreator {
	return &JobCreator{intelligence: intelligence}
}

func (c *JobCreator) CreateResearchJob(
	ctx context.Context,
	input researchapp.CreateResearchJobInput,
) error {
	_, err := c.intelligence.CreateJob(ctx, intelligenceapp.CreateJobInput{
		UserID: input.UserID, CurationID: input.CurationID,
		CurationActionID: input.CurationActionID, PlanID: input.PlanID,
		Target: intelligencedomain.JobTarget{
			Kind: intelligencedomain.TargetResearchRound,
			ID:   input.ResearchRoundID,
		},
		Provider: intelligencedomain.ProviderKind(input.Provider),
		ModelKey: input.ModelKey,
	})
	return err
}

// LockActionAdmission, PlanHasActiveWork and ActiveActionCount let Research enforce the action
// limits inside the same transaction that would open the next round's job.
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

// Adapter implements the research half of the intelligence ProductPort.
// Research owns Shopify facts and CandidatePool finalization; Intelligence
// owns managed query/ranking and execution lifecycle.
type Adapter struct {
	research researchService
	ids      idGenerator
}

func NewAdapter(
	research researchService,
	ids idGenerator,
) *Adapter {
	return &Adapter{research: research, ids: ids}
}

func (a *Adapter) ReadResearchContext(
	ctx context.Context,
	userID string,
	jobID string,
	roundID string,
) (intelligenceapp.ResearchContext, error) {
	// The job is the authority for which round may be read; it is passed as the
	// provenance so the product's audit line names the real requester.
	result, err := a.research.GetContextForIntelligence(
		ctx, userID, jobID, roundID,
	)
	if err != nil {
		return intelligenceapp.ResearchContext{}, err
	}
	return toResearchContext(userID, roundID, result), nil
}

func (a *Adapter) RunCatalogResearch(
	ctx context.Context,
	userID string,
	jobID string,
	attemptID string,
	roundID string,
	query intelligenceapp.CatalogQueryPayload,
	ranker intelligenceapp.ResearchCandidateRanker,
) (intelligenceapp.ResearchExecutionOutcome, error) {
	if ranker == nil {
		return intelligenceapp.ResearchExecutionOutcome{}, fault.New(
			fault.InvalidInput, "PHASE8_MANAGED_RANKER_REQUIRED", false,
		)
	}
	result, err := a.research.RunCatalogResearchForIntelligence(
		ctx, userID, jobID, attemptID, roundID,
		researchapp.CatalogIntelligenceCatalogQuery{
			ExecutionVersion: query.ExecutionVersion, QueryInputHash: query.QueryInputHash, QueryProjections: toProjections(query.Projections),
			Criteria: toCriteria(query.Criteria), QuerySeeds: query.QuerySeeds, ModelKey: query.ModelKey, Query: query.Query,
			ProductVertical: query.ProductVertical,
			MustInclude:     append([]string(nil), query.MustInclude...),
			MustExclude:     append([]string(nil), query.MustExclude...),
		},
		func(
			rankContext context.Context,
			observations []researchapp.CatalogIntelligenceCandidateObservation,
			maximum int,
		) ([]researchapp.CatalogIntelligenceRankedCandidate, error) {
			mapped := make([]intelligenceapp.ResearchCandidateObservation, 0, len(observations))
			for _, observation := range observations {
				mapped = append(mapped, intelligenceapp.ResearchCandidateObservation{
					ImageURL: observation.ImageURL, FactIDs: observation.FactIDs, ObservationID: observation.ObservationID,
					Name: observation.Name, Description: observation.Description,
					Merchant: observation.Merchant,
					PriceMinimum: intelligenceapp.Money{
						Amount: observation.PriceMinimumAmount, Currency: observation.Currency,
					},
					PriceMaximum: intelligenceapp.Money{
						Amount: observation.PriceMaximumAmount, Currency: observation.Currency,
					},
					ServerIntentPoint:    observation.ServerIntentPoint,
					ServerFeatures:       append([]string(nil), observation.ServerFeatures...),
					ServerSpecifications: append([]string(nil), observation.ServerSpecifications...),
				})
			}
			ranked, rankErr := ranker(rankContext, mapped, maximum)
			if rankErr != nil {
				return nil, rankErr
			}
			result := make([]researchapp.CatalogIntelligenceRankedCandidate, 0, len(ranked))
			for _, value := range ranked {
				result = append(result, researchapp.CatalogIntelligenceRankedCandidate{
					AxisScores: toScores(value.AxisScores), ObservationID: value.ObservationID,
					IntentPoint:    value.IntentPoint,
					Features:       append([]string(nil), value.Features...),
					Specifications: append([]string(nil), value.Specifications...),
				})
			}
			return result, nil
		},
	)
	if err != nil {
		return intelligenceapp.ResearchExecutionOutcome{}, err
	}
	return intelligenceapp.ResearchExecutionOutcome{
		CandidateCount: result.CandidateCount,
		NoResults:      result.NoResults,
		PoolVersion:    result.PoolVersion,
	}, nil
}

func (a *Adapter) StartReadySessions(
	ctx context.Context,
	userID string,
	planID string,
	curationActionID string,
) (int, error) {
	if scope := curationapp.ThreadExecutionFrom(ctx); scope.ActionID != "" {
		curationActionID = scope.ActionID
	}
	// The sweep records no action of its own, so the calling job's action is
	// the lineage its rounds inherit. The idempotency key stays separate: it
	// bounds this product command, not the user action above it.
	started, err := a.research.StartReadySessionResearch(
		ctx, researchapp.StartReadySessionResearchInput{
			UserID: userID, PlanID: planID,
			CurationActionID: curationActionID,
			IdempotencyKey:   a.ids.NewID(),
		},
	)
	if err != nil {
		return 0, err
	}
	return len(started.RoundIDs), nil
}

func (a *Adapter) StartResearchForPlan(
	ctx context.Context,
	userID string,
	planID string,
	curationID string,
	curationVersion int64,
	sessionIDs []string,
) error {
	// The action id doubles as the idempotency key, so a retried attempt
	// converges on the same transition instead of opening a second one.
	actionID := a.ids.NewID()
	if scope := curationapp.ThreadExecutionFrom(ctx); scope.ActionID != "" {
		actionID = scope.ActionID
	}
	_, err := a.research.StartResearch(
		ctx, researchapp.StartResearchInput{
			UserID: userID, ServerIssued: true,
			PlanID: planID, CurationID: curationID,
			CurationActionID:        actionID,
			ExpectedCurationVersion: curationVersion,
			SessionIDs:              sessionIDs,
			IdempotencyKey:          actionID,
		},
	)
	return err
}

func (a *Adapter) CancelRound(
	ctx context.Context,
	userID string,
	roundID string,
) error {
	return a.research.CancelResearchRound(ctx, userID, roundID)
}

func toResearchContext(
	userID string,
	roundID string,
	source researchapp.ContextResult,
) intelligenceapp.ResearchContext {
	scope := source.Context.ResearchScope
	purchased := []intelligenceapp.ResearchPurchasedVariant{}
	for _, record := range source.Context.AlreadyPurchased {
		if record.Checked {
			if record.ProductRef != nil {
				purchased = append(purchased, intelligenceapp.ResearchPurchasedVariant{Source: string(record.ProductRef.Source), Marketplace: record.ProductRef.Marketplace, ProductID: record.ProductRef.ProductID})
				continue
			}
			purchased = append(purchased, intelligenceapp.ResearchPurchasedVariant{Source: string(record.VariantRef.Source), Marketplace: record.VariantRef.Marketplace, VariantID: record.VariantRef.ASIN})
		}
	}
	var budget *intelligenceapp.ResearchBudget
	if source.Context.Budget != nil {
		saved := source.Context.Budget
		budget = &intelligenceapp.ResearchBudget{Enabled: saved.Budget.Enabled, Quantity: saved.Budget.Quantity, Amount: intelligenceapp.Money{Amount: "0", Currency: saved.Budget.Currency}}
		if saved.Budget.Amount != nil {
			budget.Amount.Amount = *saved.Budget.Amount
		}
		if saved.MaximumUnitMinor != nil {
			budget.MaximumUnit = &intelligenceapp.Money{Amount: budgetMinorAmount(*saved.MaximumUnitMinor, saved.ProviderCurrency), Currency: saved.ProviderCurrency}
		}
	}
	return intelligenceapp.ResearchContext{
		CatalogLanguages: fromDiscoveryRequirements(source.Context.DiscoveryRequirements),
		CachedQuery:      fromQuery(source.Context.CachedQuery), ExecutionCheckpoint: source.Context.ExecutionCheckpoint, ContentLocale: source.Context.ContentLocale, Criteria: fromCriteria(source.Context.Criteria), Budget: budget,
		PurchaseFeedbackVersion: source.Context.PurchaseFeedbackVersion, AlreadyPurchased: purchased,
		RoundID:        roundID,
		UserID:         userID,
		PlanID:         source.Context.PlanID,
		ContextVersion: source.ContextVersion,
		ContextHash:    source.ContextHash,
		ContextSchema:  source.ContextSchema,
		TargetTitle:    source.Context.Target.Title, ProductVertical: source.Context.Target.ProductVertical,
		TargetIntent:     source.Context.Target.NormalizedIntent,
		Category:         source.Context.Target.Category,
		AllocatedBudget:  toMoney(source.Context.Target.AllocatedBudget),
		Country:          scope.Country,
		City:             scope.City,
		AllowedItems:     scope.AllowedItems,
		BlockedItems:     scope.BlockedItems,
		MinPrice:         optionalMoney(scope.MinPrice),
		MaxPrice:         optionalMoney(scope.MaxPrice),
		CandidateMinimum: source.Context.CandidateMinimum,
		CandidateMaximum: source.Context.CandidateMaximum,
		FeedbackRequired: source.FeedbackRequired,
		FeedbackSummary:  source.FeedbackSummary,
		FeedbackVersion:  source.FeedbackVersion,
		FeedbackHash:     source.FeedbackHash,
	}
}

func toMoney(value shareddomain.Money) intelligenceapp.Money {
	return intelligenceapp.Money{
		Amount: value.Amount, Currency: string(value.Currency),
	}
}

func optionalMoney(value *shareddomain.Money) *intelligenceapp.Money {
	if value == nil {
		return nil
	}
	money := toMoney(*value)
	return &money
}

func budgetMinorAmount(n int64, currency string) string {
	if currency == "USD" {
		return fmt.Sprintf("%d.%02d", n/100, n%100)
	}
	return fmt.Sprintf("%d", n)
}
