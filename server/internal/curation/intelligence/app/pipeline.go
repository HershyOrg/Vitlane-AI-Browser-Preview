package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const (
	maximumPlannedTargets = 5
)

// run executes one claimed job end to end. Each pipeline is a fixed sequence —
// planning uses one call plus optional budget estimates, research uses two
// plus a catalog search — so the
// call count, and therefore the worst-case cost of a job, is known before
// anything runs. There is no tool loop and no self-directed retry: a failure
// closes the attempt and the job's own retry policy decides what happens next.
func (s *Service) run(ctx context.Context, claimed ClaimedJob) error {
	if repo, ok := s.repository.(interface {
		ThreadExecutionContext(context.Context, string, ...string) (context.Context, error)
	}); ok {
		var err error
		ctx, err = repo.ThreadExecutionContext(ctx, claimed.Job.ID, claimed.Attempt.ID, claimed.Job.ExecutionActionID)
		if err != nil {
			return err
		}
	}
	tracker := &stepTracker{service: s, job: claimed.Job, attempt: claimed.Attempt}
	var err error
	switch claimed.Job.Target.Kind {
	case intelligencedomain.TargetActionInterpretation:
		tracker.begin(ctx, intelligencedomain.StepInterpreting)
		if s.actionInterpreter == nil {
			err = fault.New(fault.InternalFailure, "AUTO_INTERPRETER_UNAVAILABLE", false)
		} else {
			err = s.actionInterpreter.RunActionInterpretation(ctx, claimed.Job.UserID, claimed.Job.CurationID, claimed.Job.ExecutionActionID, claimed.Job.ID, claimed.Attempt.ID, claimed.Job.Target.Revision)
		}
		if err == nil {
			tracker.succeed(ctx)
		}
	case intelligencedomain.TargetPlanningTask:
		err = s.runPlanning(ctx, claimed, tracker)
	case intelligencedomain.TargetResearchRound:
		err = s.runResearch(ctx, claimed, tracker)
	default:
		err = fault.New(
			fault.InternalFailure,
			intelligencedomain.ReasonInternalFailure,
			false,
		)
	}
	if err != nil {
		// Close whatever step was open when the pipeline gave up, so the Web
		// shows which phase stopped rather than one stuck on RUNNING forever.
		reasonCode, _ := classify(err)
		tracker.fail(ctx, reasonCode)
	}
	return err
}

func (s *Service) runPlanning(
	ctx context.Context,
	claimed ClaimedJob,
	tracker *stepTracker,
) error {
	tracker.begin(ctx, intelligencedomain.StepInterpreting)
	planningContext, err := s.products.ReadPlanningContext(
		ctx, claimed.Job.UserID, claimed.Job.Target.ID,
	)
	if err != nil {
		return err
	}
	planningContext.UnifiedAuto = s.threadContinuations && s.hasThread(ctx, claimed.Job.ID)
	maximum := planningContext.MaximumTargets
	if maximum <= 0 || maximum > maximumPlannedTargets {
		maximum = maximumPlannedTargets
	}
	// SINGLE means the user asked for the whole intent to stay one item, so the
	// provider is not allowed to split it no matter what the text looks like.
	if planningContext.PlanningMode == "SINGLE" {
		maximum = 1
	}

	var payload PlanningTargetsPayload
	if err := s.complete(
		ctx, claimed, planningSystemPromptForContext(planningContext),
		planningPrompt(planningContext, maximum),
		SchemaPlanningTargets, planningSchema(planningContext, maximum), &payload,
	); err != nil {
		return err
	}
	if planningContext.UnifiedAuto && planningContext.InitialRun && planningContext.BudgetInferenceAllowed {
		if err := applyInitialAutoBudget(&planningContext, payload); err != nil {
			return err
		}
	}
	if planningContext.InitialRun && planningContext.BudgetInferenceAllowed && payload.Budget != nil {
		allowed := false
		for _, currency := range inferredBudgetCurrencies(planningContext) {
			allowed = allowed || currency == payload.Budget.Currency
		}
		if !allowed {
			return fmt.Errorf("%w: inferred currency conflicts with request", intelligencedomain.ErrProviderResponse)
		}
		if err := validateInferredBudget(payload.Budget.Amount, payload.Budget.Currency); err != nil {
			return fmt.Errorf("%w: invalid inferred budget", intelligencedomain.ErrProviderResponse)
		}
		planningContext.ResolvedBudget = &Money{Amount: payload.Budget.Amount, Currency: payload.Budget.Currency}
		planningContext.BudgetEnabled = true
		planningContext.BudgetCurrency = payload.Budget.Currency
	}
	targets, err := planTargets(payload, planningContext, maximum)
	if err != nil {
		return err
	}
	if planningContext.BudgetEnabled {
		if planningContext.BudgetAllocationMode == "EQUAL" {
			for i := range targets {
				targets[i].Budget = Money{Amount: "1", Currency: planningContext.BudgetCurrency}
			}
		} else {
			items := make([]BudgetEstimateTarget, len(targets))
			for i, t := range targets {
				items[i] = BudgetEstimateTarget{Title: t.Title, Quantity: t.Quantity}
			}
			estimates := BudgetEstimatesPayload{Estimates: payload.Estimates}
			if !planningContext.UnifiedAuto {
				if err := s.complete(ctx, claimed, BudgetEstimatesSystemPrompt, BudgetEstimatesPrompt(planningContext.BudgetCurrency, items), BudgetEstimatesSchemaName, BudgetEstimatesSchema(len(items)), &estimates); err != nil {
					return err
				}
			}
			if err := ValidateBudgetEstimates(estimates, len(items), planningContext.BudgetCurrency); err != nil {
				return fmt.Errorf("%w: budget estimates invalid (%v; estimates=%d targets=%d currency=%s)", intelligencedomain.ErrProviderResponse, err, len(estimates.Estimates), len(items), planningContext.BudgetCurrency)
			}
			for i, a := range estimates.Estimates {
				targets[i].Budget = Money{Amount: a.Amount, Currency: planningContext.BudgetCurrency}
			}
		}
	}
	tracker.succeed(ctx)

	tracker.begin(ctx, intelligencedomain.StepSubmitting)
	outcome, err := s.products.SubmitPlanning(
		ctx, claimed.Job.UserID, claimed.Job.ID, planningContext, targets,
	)
	if err != nil {
		return err
	}
	if !outcome.Accepted {
		return fault.New(
			fault.Conflict,
			intelligencedomain.ReasonProposalRejected,
			false,
		)
	}
	tracker.succeed(ctx)

	if s.threadContinuations && s.hasThread(ctx, claimed.Job.ID) {
		return nil
	}

	// ADR-0032 section 8, kept by ADR-0038: the Server issues the next command
	// itself rather than making the user press a second button for a step it
	// can already take. A failure here does not undo the accepted proposal —
	// the curation stays in PLANNING and the user can start it by hand.
	if started, err := s.products.StartCurating(
		ctx, claimed.Job.UserID, planningContext.PlanID,
		claimed.Job.CurationActionID,
	); err != nil {
		s.logger.WarnContext(ctx, "auto start curating failed",
			"event", "intelligence.auto_start_curating_failed",
			"job_id", claimed.Job.ID, "error", err.Error())
	} else if started {
		s.logger.InfoContext(ctx, "started curating",
			"event", "intelligence.auto_start_curating",
			"job_id", claimed.Job.ID)
	}
	return nil
}

func (s *Service) runResearch(
	ctx context.Context,
	claimed ClaimedJob,
	tracker *stepTracker,
) error {
	tracker.begin(ctx, intelligencedomain.StepInterpreting)
	// A user whose daily allowance cannot cover a query and one evaluation
	// batch is refused here, before the round spends any catalog call.
	if provider, ok := s.providers.Lookup(claimed.Job.Provider); ok {
		if err := provider.Available(ctx, claimed.Job.UserID); err != nil {
			return err
		}
	}
	researchContext, err := s.products.ReadResearchContext(
		ctx, claimed.Job.UserID, claimed.Job.ID, claimed.Job.Target.ID,
	)
	if err != nil {
		return err
	}
	researchContext.AllowFeedbackCriteria = false
	query, err := s.generateResearchQuery(ctx, claimed, researchContext)
	if err != nil {
		return err
	}
	if researchContext.ProductVertical != "" && query.ProductVertical == "" && strings.TrimSpace(researchContext.FeedbackSummary) == "" {
		query.ProductVertical = researchContext.ProductVertical
	}
	query.ProductVertical = normalizeRoutingVertical(query.ProductVertical)
	if (s.threadContinuations && s.criteriaAlreadyDecided(ctx, claimed.Job.ID) || strings.TrimSpace(researchContext.FeedbackSummary) == "") && researchContext.Criteria != nil {
		query.Criteria = researchContext.Criteria
	}

	query.Query = strings.TrimSpace(query.Query)
	query.ModelKey = claimed.Job.ModelKey
	if query.Criteria != nil {
		query.Criteria.SchemaVersion = "vitlane.target-criteria.v1"
		query.Criteria.Version = 1
		if researchContext.Criteria != nil {
			query.Criteria.Version = researchContext.Criteria.Version
		}
		researchContext.Criteria = query.Criteria
	}
	if !validProviderCatalogPhrase(queryCountry(researchContext), query.Query) {
		return fault.New(
			fault.ProviderRejected,
			intelligencedomain.ReasonProviderResponse,
			true,
		)
	}
	tracker.succeed(ctx)

	tracker.begin(ctx, intelligencedomain.StepSearchingCatalog)
	rankingStarted := false
	outcome, err := s.products.RunCatalogResearch(
		ctx, claimed.Job.UserID, claimed.Job.ID, claimed.Attempt.ID,
		claimed.Job.Target.ID, query,
		func(
			rankContext context.Context,
			observations []ResearchCandidateObservation,
			maximum int,
		) ([]ResearchRankedCandidate, error) {
			tracker.succeed(rankContext)
			tracker.begin(rankContext, intelligencedomain.StepRanking)
			rankingStarted = true
			if len(observations) == 0 {
				tracker.succeed(rankContext)
				tracker.begin(rankContext, intelligencedomain.StepSubmitting)
				return []ResearchRankedCandidate{}, nil
			}
			maximum = len(observations)
			axisCount := 0
			if researchContext.Criteria != nil {
				axisCount = len(researchContext.Criteria.Axes)
			}
			// The evaluation policy splits a large round into parallel
			// batches when it can hold the model slots for them.
			ranked, evaluateErr := s.evaluateObservations(
				rankContext, claimed, researchContext, observations, axisCount,
			)
			if evaluateErr != nil {
				return nil, evaluateErr
			}
			dropped := len(observations) - len(ranked)
			if missing := unrankedObservations(observations, ranked); researchContext.Criteria != nil && len(missing) > 0 {
				// One bounded supplementary call for the products the first
				// answer skipped or scored badly. Its failure leaves those
				// products unevaluated; it never fails the Round, and its own
				// request key keeps the ledger from replaying the first call.
				// It never exceeds one batch, so a round that lost a whole
				// batch does not pay for it twice in one attempt.
				if limit := s.evaluation.BatchLimit; limit > 0 && len(missing) > limit {
					missing = missing[:limit]
				}
				var more CandidateRankingPayload
				supplementErr := s.completeKeyed(
					rankContext, claimed, ":supplement", researchSystemPrompt(rankingSystemPrompt, researchContext),
					rankingPrompt(researchContext, missing, len(missing)),
					SchemaCandidateRanking, CandidateRankingSchema(len(missing), axisCount), &more, researchImages(researchContext, missing),
				)
				extra := 0
				if supplementErr == nil {
					if accepted, _, err := acceptOfferedObservations(more, missing, len(missing)); err == nil {
						ranked = append(ranked, accepted...)
						extra = len(accepted)
					}
				}
				s.logger.InfoContext(rankContext, "catalog ranking supplemented",
					"event", "intelligence.phase8_ranking_supplemented",
					"job_id", claimed.Job.ID, "offered", len(observations), "dropped", dropped,
					"missing", len(missing), "recovered", extra,
					"supplement_failed", supplementErr != nil)
			}
			tracker.succeed(rankContext)
			tracker.begin(rankContext, intelligencedomain.StepSubmitting)
			return ranked, nil
		},
	)
	if err != nil {
		return err
	}
	if !rankingStarted {
		return fault.New(
			fault.InternalFailure,
			intelligencedomain.ReasonInternalFailure,
			true,
		)
	}
	s.logger.InfoContext(ctx, "catalog candidate pool completed",
		"event", "intelligence.phase8_research_completed",
		"job_id", claimed.Job.ID,
		"candidate_count", outcome.CandidateCount,
		"no_results", outcome.NoResults,
		"pool_version", outcome.PoolVersion)
	tracker.succeed(ctx)
	if !s.threadContinuations || !s.hasThread(ctx, claimed.Job.ID) {
		s.sweepReadySessions(ctx, claimed, researchContext.PlanID)
	}
	return nil
}

// sweepReadySessions starts research for any sibling session still sitting
// READY on this user's curation.
//
// A READY session has no round, and the workspace only offers re-research from
// REVIEWING, so one missed continuation would strand that Target with no way
// back for the user. Running the sweep after every completed round makes the
// path self-healing: the next piece of work on the curation picks up whatever
// an earlier failure left behind.
func (s *Service) sweepReadySessions(
	ctx context.Context,
	claimed ClaimedJob,
	planID string,
) {
	if planID == "" {
		return
	}
	started, err := s.products.StartReadySessions(
		ctx, claimed.Job.UserID, planID, claimed.Job.CurationActionID,
	)
	if err != nil {
		s.logger.WarnContext(ctx, "ready-session sweep failed",
			"event", "intelligence.ready_sweep_failed",
			"job_id", claimed.Job.ID, "error", err.Error())
		return
	}
	if started > 0 {
		s.logger.InfoContext(ctx, "started stranded sessions",
			"event", "intelligence.ready_sweep_started",
			"job_id", claimed.Job.ID, "sessions", started)
	}
}

// complete runs one provider call and decodes it into target. The provider owns
// everything about how the answer was produced — model, credentials, cost — so
// the only thing crossing back is schema-shaped JSON.
func (s *Service) complete(
	ctx context.Context,
	claimed ClaimedJob,
	systemPrompt string,
	userPrompt string,
	schemaName string,
	schema map[string]any,
	target any,
	images ...[]CompletionImage,
) error {
	return s.completeKeyed(ctx, claimed, "", systemPrompt, userPrompt, schemaName, schema, target, images...)
}

// completeKeyed is complete with a request-key suffix, for the rare second
// call of the same schema within one attempt (the ranking supplement). The
// ledger keys reservations by request key, so a repeated key would replay.
func (s *Service) completeKeyed(
	ctx context.Context,
	claimed ClaimedJob,
	keySuffix string,
	systemPrompt string,
	userPrompt string,
	schemaName string,
	schema map[string]any,
	target any,
	images ...[]CompletionImage,
) error {
	// The provider slot covers this call only. A job that is collecting from
	// catalogs or writing its result does not occupy the model's concurrency,
	// and a slot that stays busy defers the job rather than failing it.
	release, err := s.providers.Acquire(ctx, claimed.Job.Provider, s.modelSlotWait)
	if err != nil {
		return err
	}
	defer release()
	return s.completeHeld(ctx, claimed, keySuffix, systemPrompt, userPrompt, schemaName, schema, target, images...)
}

// completeHeld runs one provider call with a model slot the caller already
// holds. Batch evaluation takes its slots up front so a round never splits
// into more calls than it can run side by side.
func (s *Service) completeHeld(
	ctx context.Context,
	claimed ClaimedJob,
	keySuffix string,
	systemPrompt string,
	userPrompt string,
	schemaName string,
	schema map[string]any,
	target any,
	images ...[]CompletionImage,
) error {
	provider, ok := s.providers.Lookup(claimed.Job.Provider)
	if !ok {
		return fault.New(
			fault.ProviderUnavailable,
			intelligencedomain.ReasonProviderUnavailable,
			true,
		)
	}
	var imageParts []CompletionImage
	if len(images) > 0 {
		imageParts = images[0]
	}
	result, err := provider.Complete(ctx, CompletionRequest{Images: imageParts,
		UserID:       claimed.Job.UserID,
		RequestKey:   claimed.Attempt.RequestKey + ":" + schemaName + keySuffix,
		JobID:        claimed.Job.ID,
		AttemptID:    claimed.Attempt.ID,
		ModelKey:     claimed.Job.ModelKey,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		SchemaName:   schemaName,
		Schema:       schema,
		Deadline:     claimed.Attempt.DeadlineAt,
	})
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(result.Content), target); err != nil {
		return fault.Wrap(
			fmt.Errorf("%w: decode %s",
				intelligencedomain.ErrProviderResponse, schemaName),
			fault.ProviderRejected,
			intelligencedomain.ReasonProviderResponse,
			true,
		)
	}
	return nil
}

// classify maps a pipeline failure onto the closed reason code the Web renders
// and decides whether another attempt is worth the user's allowance. A quota
// cap is final because retrying cannot succeed until the cap resets; an unknown
// external effect is never retried, because repeating it could duplicate work
// that already happened.
func classify(err error) (string, bool) {
	if fail, ok := fault.As(err); ok {
		reason := fail.Reason
		if reason == "" {
			reason = string(fail.Code)
		}
		return reason, fail.Retryable
	}
	if errors.Is(err, intelligencedomain.ErrProviderResponse) {
		return intelligencedomain.ReasonProviderResponse, true
	}
	// An unclassified error is a defect rather than a known outcome, so it is
	// reported as such instead of being dressed up as a provider problem.
	return intelligencedomain.ReasonInternalFailure, true
}
