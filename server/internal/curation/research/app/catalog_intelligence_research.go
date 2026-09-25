package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	"strconv"
	"strings"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// CatalogIntelligenceCatalogQuery is the Research-owned view of the managed
// query step. It contains search semantics only; provider request fields are
// still compiled deterministically from the Target profile by Research.
type CatalogIntelligenceCatalogQuery struct {
	ExecutionVersion string
	QueryInputHash   string
	QueryProjections []CatalogQueryProjection
	Country          string
	Criteria         *curationdomain.TargetCriteriaSetV1
	QuerySeeds       []string
	ModelKey         string
	Query            string
	MustInclude      []string
	MustExclude      []string
	// ProductVertical picks which specialised Korean malls the Round asks
	// directly. Empty or unknown reaches only general malls.
	ProductVertical string
}

// CatalogIntelligenceCandidateObservation is the only response-scoped Shopify
// data made visible to the managed ranking call. It contains no provider
// credentials and is never accepted back as product authority.
type CatalogIntelligenceCandidateObservation struct {
	ImageURL             string
	FactIDs              []string
	ObservationID        string
	Name                 string
	Description          string
	Merchant             string
	PriceMinimumAmount   string
	PriceMaximumAmount   string
	Currency             string
	ServerIntentPoint    string
	ServerFeatures       []string
	ServerSpecifications []string
}

type CatalogIntelligenceRankedCandidate struct {
	AxisScores     []researchdomain.AxisScoreV1
	ObservationID  string
	IntentPoint    string
	Features       []string
	Specifications []string
}

type CatalogIntelligenceCandidateRanker func(
	context.Context,
	[]CatalogIntelligenceCandidateObservation,
	int,
) ([]CatalogIntelligenceRankedCandidate, error)

// CatalogIntelligenceResearchResult is the durable outcome projected back to
// the IntelligenceJob. Candidate IDs are not copied into the job: the
// CandidatePool remains their only authority.
type CatalogIntelligenceResearchResult struct {
	CandidateCount int
	NoResults      bool
	PoolVersion    int64
}

// RunCatalogResearchForIntelligence keeps the existing ResearchRound/Job
// control plane and replaces only its data plane. Shopify search and managed
// ranking run outside a DB transaction. CandidatePool finalize, Round terminal
// and ShoppingSession REVIEWING then share one Research transaction, whose
// initial Round lock fences cancellation and late results.
func (s *Service) RunCatalogResearchForIntelligence(
	ctx context.Context,
	userID, jobID, attemptID, roundID string,
	query CatalogIntelligenceCatalogQuery,
	ranker CatalogIntelligenceCandidateRanker,
) (CatalogIntelligenceResearchResult, error) {
	if s.liveCatalog == nil || ranker == nil || strings.TrimSpace(userID) == "" ||
		strings.TrimSpace(jobID) == "" || strings.TrimSpace(attemptID) == "" ||
		strings.TrimSpace(roundID) == "" || strings.TrimSpace(query.Query) == "" {
		return CatalogIntelligenceResearchResult{}, fault.New(
			fault.ProviderUnavailable, "PHASE8_RESEARCH_UNAVAILABLE", true,
		)
	}
	contextResult, err := s.GetContextForIntelligence(ctx, userID, jobID, roundID)
	if err != nil {
		return CatalogIntelligenceResearchResult{}, err
	}
	researchContext := contextResult.Context
	curationID := strings.TrimSpace(researchContext.Target.CurationID)
	targetID := strings.TrimSpace(researchContext.Target.ID)
	if curationID == "" || targetID == "" ||
		strings.TrimSpace(researchContext.SessionID) == "" {
		return CatalogIntelligenceResearchResult{}, fault.New(
			fault.InternalFailure, "PHASE8_RESEARCH_CONTEXT_INVALID", false,
		)
	}

	query, err = s.checkpointCriteria(ctx, userID, roundID, researchContext, query)
	if err != nil {
		return CatalogIntelligenceResearchResult{}, err
	}
	stored, err := s.liveCatalog.workspace.LoadCatalogWorkspaceStateV2(
		ctx, userID, curationID,
	)
	if err != nil {
		return CatalogIntelligenceResearchResult{}, err
	}
	expectedPoolVersion := int64(0)
	for _, pool := range stored.Pools {
		if pool.TargetID == targetID {
			expectedPoolVersion = pool.Version
			break
		}
	}
	input := CatalogWorkspaceSearchInputV2{
		ResumeRoundID:    roundID,
		ExecutionVersion: query.ExecutionVersion, QueryInputHash: query.QueryInputHash, QueryProjections: query.QueryProjections,
		QuerySeeds: query.QuerySeeds, Criteria: query.Criteria, UserID: userID, CurationID: curationID, TargetID: targetID,
		Mode: CatalogResearchAppendV2, ExpectedPoolVersion: expectedPoolVersion,
		IdempotencyKey:  attemptID,
		ProductVertical: query.ProductVertical,
		Search: LiveCatalogReviewSearchInputV2{
			Query: strings.TrimSpace(query.Query), Limit: CandidateUpdateSize,
			PreferredTerms: append(
				[]string(nil), query.MustInclude...,
			),
			PreferredExclusions: append([]string(nil), query.MustExclude...),
		},
	}
	input.ExistingExternalProductKeys = map[string]bool{}
	input.ExistingBrowserSourceCounts = map[researchdomain.Source]int{}
	for _, candidate := range stored.Candidates {
		if candidate.PlanTargetID != targetID || candidate.ExternalObservation == nil {
			continue
		}
		observation := candidate.ExternalObservation
		input.ExistingExternalProductKeys[observation.ProductRef.IdentityKey()] = true
		if observation.Provenance.DiscoveryChannel == "SERVER_BROWSER_PUBLIC" {
			input.ExistingBrowserSourceCounts[observation.ProductRef.Source]++
		}
	}

	fingerprint := researchSearchFingerprint(query, researchContext)
	if repo, ok := s.repository.(sourceProgressRepository); ok {
		input.Progress, err = repo.ReadSourceProgress(ctx, userID, curationID, targetID, fingerprint)
		if err != nil {
			return CatalogIntelligenceResearchResult{}, err
		}
	}
	profileReader, ok := s.liveCatalog.workspace.(catalogTargetSearchProfileReaderV2)
	if !ok {
		return CatalogIntelligenceResearchResult{}, fault.New(
			fault.InternalFailure, "PHASE8_TARGET_SEARCH_PROFILE_UNAVAILABLE", false,
		)
	}
	profile, err := profileReader.CatalogTargetSearchProfileV2(
		ctx, userID, curationID, targetID,
	)
	if err != nil {
		return CatalogIntelligenceResearchResult{}, err
	}
	profile.NormalizedIntent = query.Query
	// The Round is the authority even if the user changed the next research
	// country while this job was queued or running. Budget currency is unchanged.
	profile.Market.Country = string(researchContext.ResearchScope.Country)
	input.Budget = researchContext.Budget
	profile = applyResearchBudget(profile, input.Budget)
	input.Search.Country = profile.Market.Country
	input.Search.Currency = profile.Market.Currency
	exponent := workspaceCurrencyExponentV2(profile.Market.Currency)
	input.Search.MinimumMinor, err = catalogProfileMinorBoundV2(profile.MinimumPrice, exponent)
	if err != nil {
		return CatalogIntelligenceResearchResult{}, err
	}
	input.Search.MaximumMinor, err = catalogProfileMinorBoundV2(profile.MaximumPrice, exponent)
	if err != nil {
		return CatalogIntelligenceResearchResult{}, err
	}
	command, err := NewCatalogCandidatePoolCommandV2(input, expectedPoolVersion, attemptID)
	if err != nil {
		return CatalogIntelligenceResearchResult{}, err
	}
	preflight, err := s.liveCatalog.PreflightWorkspaceSearchV2(ctx, command)
	if err != nil {
		return CatalogIntelligenceResearchResult{}, err
	}
	if preflight.Replay {
		return CatalogIntelligenceResearchResult{}, fault.New(
			fault.Conflict, "PHASE8_RESEARCH_LIFECYCLE_REPLAY_INVALID", false,
		)
	}
	command.FencingToken = preflight.FencingToken

	// An automatic retry of this Round evaluates what an earlier attempt already
	// collected instead of paying every source again (observation_checkpoint.go).
	inputHash := observationInputHash(input, profile)
	result, reused := s.observations.take(roundID, inputHash, s.clock.Now())
	if reused {
		if s.logger != nil {
			s.logger.InfoContext(ctx, "research observations reused",
				"event", "research.observation_checkpoint_reused", "result", "success",
				"round_id", roundID, "attempt_id", attemptID,
				"products", len(result.Search.Products), "sources", len(result.Metrics.SourceCoverage))
		}
	} else {
		result, err = s.liveCatalog.searchWorkspacePlanForProfileV2(ctx, input, profile)
		if err != nil {
			return CatalogIntelligenceResearchResult{}, s.abortCatalogIntelligenceResearchV2(
				ctx, command, err,
			)
		}
		s.observations.put(roundID, inputHash, result, s.clock.Now())
	}

	// Everything the sources returned this Round, before identity dedupe. The
	// Round outcome keeps it so operators can compare observed and admitted.
	observedCount := max(result.Search.RawCount, len(result.Search.Products))
	// Compare durable source identity before paying for evaluation. Hidden rows
	// still count as existing and cannot be revived by Research Again. A row
	// this Round already published unevaluated (a retried attempt) is fresh
	// again: it still waits for this Round's evaluation.
	fresh, duplicateCount, count := catalogFreshProductsForRoundV2(stored.Candidates, result.Search.Products, targetID, roundID)
	duplicateCount += result.DiscoveryDuplicateCount
	result.Search.Products = catalogSelectRoundProducts(stored.Candidates, fresh, targetID, roundID, count)

	// Staged publication (ADR-0083): the observed products enter the pool
	// unevaluated as soon as the sources answer, so the cards appear while
	// the model is still working. The command stays fenced and leased; the
	// Round and its Session stay open, so no other writer touches the Target
	// until the explicit finalize or cancel.
	staged := len(result.Search.Products) > 0
	var published CatalogPoolMetadataV2
	poolVersion := expectedPoolVersion
	if staged {
		var unevaluated []CatalogCandidateReferenceV2
		result, unevaluated = catalogCandidateReferencesForSearchResultV2(input, result, s.clock.Now())
		for index := range unevaluated {
			unevaluated[index].EvaluationRoundID = roundID
		}
		interim := result.Metrics
		interim.DiscoveryOutcome = &ResearchDiscoveryOutcome{SchemaVersion: "vitlane.research-discovery-outcome.v1", AddedCount: len(unevaluated), DuplicateCount: duplicateCount, RejectedCount: result.DiscardedNoLocatorCount + result.ProviderRejectedCount, UnevaluatedCount: len(unevaluated), Status: "EVALUATING"}
		published, err = s.liveCatalog.PublishObservedCandidatesV2(ctx, command, unevaluated, interim)
		if err != nil {
			s.releaseObservationsUnlessRetryable(roundID, err)
			return CatalogIntelligenceResearchResult{}, s.abortCatalogIntelligenceResearchV2(ctx, command, err)
		}
		s.liveCatalog.responseHandoff.put(input, result, published.CandidateIDBindings, s.clock.Now())
		poolVersion = published.Version
		if s.logger != nil {
			s.logger.InfoContext(ctx, "research candidates published before evaluation",
				"event", "research.candidates_published", "result", "success",
				"round_id", roundID, "attempt_id", attemptID, "candidates", len(published.AdmittedCandidateIDs), "pool_version", poolVersion)
		}
	}
	result, err = catalogApplyManagedRankingV2(ctx, result, exponent, ranker, catalogAssessmentContext{Criteria: query.Criteria, Locale: researchContext.ContentLocale, RoundID: roundID, ModelKey: query.ModelKey, Now: s.clock.Now()})
	if err != nil {
		s.releaseObservationsUnlessRetryable(roundID, err)
		return CatalogIntelligenceResearchResult{}, s.abortCatalogIntelligenceResearchV2(
			ctx, command, err,
		)
	}
	var references []CatalogCandidateReferenceV2
	if !staged {
		result, references = catalogCandidateReferencesForSearchResultV2(
			input, result, s.clock.Now(),
		)
	}

	outcomeStatus := "ADDED"
	if len(result.Search.Products) == 0 {
		outcomeStatus = "NO_NEW_CANDIDATES"
	}
	result.Metrics.DiscoveryOutcome = &ResearchDiscoveryOutcome{SchemaVersion: "vitlane.research-discovery-outcome.v1", AddedCount: len(result.Search.Products), DuplicateCount: duplicateCount, RejectedCount: result.DiscardedNoLocatorCount + result.ProviderRejectedCount, EvaluatedCount: result.EvaluatedCount, UnevaluatedCount: result.UnevaluatedCount, Status: outcomeStatus}
	// The attempt deadline owns new work, not the short durable write that
	// records work already done. An evaluation that finished a second before
	// the deadline is committed rather than thrown away; the Round lock still
	// fences a cancelled or superseded Round. Thread execution scope values
	// survive WithoutCancel, so the mutation guard sees the same owner.
	finalizeContext, cancelFinalize := context.WithTimeout(context.WithoutCancel(ctx), catalogFinalizeTimeout)
	defer cancelFinalize()
	var pool CatalogPoolMetadataV2
	err = s.transactor.WithinTransaction(finalizeContext, func(txContext context.Context) error {
		round, lockErr := s.repository.GetRound(txContext, userID, roundID, true)
		if lockErr != nil {
			return lockErr
		}
		if round.Status != researchdomain.RoundStatusRequested ||
			round.ShoppingSessionID != researchContext.SessionID {
			return researchdomain.ErrRoundClosed
		}
		if staged {
			// Stage two and three share this transaction: the assessments fill
			// the rows published earlier exactly once, then the Round closes.
			filledVersion, _, fillErr := s.liveCatalog.FillCandidateAssessmentsV2(
				txContext, command, poolVersion, result.CandidateAssessments, s.clock.Now(),
			)
			if fillErr != nil {
				return fillErr
			}
			pool, lockErr = s.liveCatalog.FinalizeStagedSearchV2(
				txContext, command, filledVersion, roundID, published, result.Metrics,
			)
		} else {
			pool, lockErr = s.liveCatalog.CompleteWorkspaceSearchV2(
				txContext, command, references, result.Metrics,
			)
		}
		if lockErr != nil {
			return lockErr
		}
		hasResults := len(pool.AdmittedCandidateIDs) > 0
		if lockErr = round.CompleteFromCandidatePool(hasResults, s.clock.Now()); lockErr != nil {
			return lockErr
		}
		if lockErr = s.repository.UpdateRound(txContext, round); lockErr != nil {
			return lockErr
		}
		if repo, ok := s.repository.(roundOutcomeRepository); ok {
			admitted := len(pool.AdmittedCandidateIDs)
			evaluated := min(result.EvaluatedCount, admitted)
			if lockErr = repo.SaveRoundOutcome(txContext, ResearchRoundOutcome{
				RoundID: roundID, UserID: userID, CurationID: curationID, TargetID: targetID,
				AttemptID: attemptID, Country: string(researchContext.ResearchScope.Country),
				Mode: string(input.Mode), ObservedCount: observedCount, DuplicateCount: duplicateCount,
				RejectedCount:    result.DiscardedNoLocatorCount + result.ProviderRejectedCount,
				AdmittedCount:    admitted,
				EvaluatedCount:   evaluated,
				UnevaluatedCount: admitted - evaluated,
				SourceCoverage:   result.Metrics.SourceCoverage, Duration: result.Metrics.Duration,
				CompletedAt: s.clock.Now(),
			}); lockErr != nil {
				return lockErr
			}
		}
		if repo, ok := s.repository.(sourceProgressRepository); ok && len(result.NextProgress) > 0 {
			if err := repo.SaveSourceProgress(txContext, userID, curationID, targetID, fingerprint, result.NextProgress); err != nil {
				return err
			}
		}
		_, lockErr = s.sessions.CompleteResearch(
			txContext, userID, researchContext.SessionID, roundID,
		)
		return lockErr
	})
	if err != nil {
		s.releaseObservationsUnlessRetryable(roundID, err)
		return CatalogIntelligenceResearchResult{}, s.abortCatalogIntelligenceResearchV2(
			ctx, command, err,
		)
	}
	s.observations.drop(roundID)
	return CatalogIntelligenceResearchResult{
		CandidateCount: len(pool.AdmittedCandidateIDs),
		NoResults:      len(pool.AdmittedCandidateIDs) == 0,
		PoolVersion:    pool.Version,
	}, nil
}

// releaseObservationsUnlessRetryable drops the Round's checkpoint when no
// automatic retry will follow. Unclassified errors count as retryable, the
// same way the intelligence job classifies them.
func (s *Service) releaseObservationsUnlessRetryable(roundID string, cause error) {
	if failure, ok := fault.As(cause); ok && !failure.Retryable {
		s.observations.drop(roundID)
	}
}

// catalogFinalizeTimeout bounds the completion and abort writes that run on a
// context detached from the attempt deadline.
const catalogFinalizeTimeout = 15 * time.Second

func (s *Service) abortCatalogIntelligenceResearchV2(
	ctx context.Context,
	command CatalogCandidatePoolCommandV2,
	cause error,
) error {
	// The abort releases the pool command lease so the next attempt can start
	// at once; it must run even when the attempt context is already dead.
	abortContext, cancelAbort := context.WithTimeout(context.WithoutCancel(ctx), catalogFinalizeTimeout)
	defer cancelAbort()
	if abortErr := s.liveCatalog.abortWorkspaceSearchAfterFailureV2(abortContext, command); abortErr != nil {
		return fault.Wrap(
			fmt.Errorf("%v: %w", cause, abortErr),
			fault.InternalFailure, catalogCandidatePoolAbortFailedV2, false,
		)
	}
	return cause
}

func catalogApplyManagedRankingV2(
	ctx context.Context,
	result LiveCatalogReviewResultV2,
	exponent uint8,
	ranker CatalogIntelligenceCandidateRanker,
	evaluation ...catalogAssessmentContext,
) (LiveCatalogReviewResultV2, error) {
	observations := make([]CatalogIntelligenceCandidateObservation, 0, len(result.Search.Products))
	products := make(map[string]CatalogProductObservation, len(result.Search.Products))
	for _, product := range result.Search.Products {
		id := strings.TrimSpace(product.ProviderProductID)
		if id == "" {
			return LiveCatalogReviewResultV2{}, fault.New(
				fault.InternalFailure, "PHASE8_MANAGED_RANKING_OBSERVATION_INVALID", false,
			)
		}
		if _, duplicate := products[id]; duplicate {
			return LiveCatalogReviewResultV2{}, fault.New(
				fault.InternalFailure, "PHASE8_MANAGED_RANKING_OBSERVATION_DUPLICATE", false,
			)
		}
		products[id] = product
		assessment := result.CandidateAssessments[id]
		priceExponent := workspaceCurrencyExponentV2(product.PriceRange.Minimum.Currency)
		observations = append(observations, CatalogIntelligenceCandidateObservation{
			ObservationID: id,
			Name:          strings.TrimSpace(product.Title),
			Description:   catalogManagedRankingDescriptionV2(product.Description),
			Merchant:      catalogManagedRankingMerchantV2(product),
			PriceMinimumAmount: catalogMinorAmountV2(
				product.PriceRange.Minimum.AmountMinor, priceExponent,
			),
			PriceMaximumAmount: catalogMinorAmountV2(
				product.PriceRange.Maximum.AmountMinor, priceExponent,
			),
			Currency:             product.PriceRange.Minimum.Currency,
			ServerIntentPoint:    assessment.IntentPoint,
			ServerFeatures:       append([]string(nil), assessment.Features...),
			ServerSpecifications: append([]string(nil), assessment.Specifications...),
		})
		o := &observations[len(observations)-1]
		if o.Name != "" {
			o.FactIDs = append(o.FactIDs, "name")
		}
		if o.Description != "" {
			o.FactIDs = append(o.FactIDs, "description")
		}
		if o.PriceMinimumAmount != "" {
			o.FactIDs = append(o.FactIDs, "price")
		}
		for _, m := range product.Media {
			if safeAssessmentImageURL(m.URL) {
				o.ImageURL = m.URL
				break
			}
		}
		if _, _, _, known := plannedObservedPriceV3(product); !known {
			observations[len(observations)-1].PriceMinimumAmount = ""
			observations[len(observations)-1].PriceMaximumAmount = ""
			observations[len(observations)-1].Currency = ""
			observations[len(observations)-1].FactIDs = removePriceFact(observations[len(observations)-1].FactIDs)
		}
	}
	maximum := len(observations)

	ranked, err := ranker(ctx, observations, maximum)
	if err != nil {
		return LiveCatalogReviewResultV2{}, err
	}
	if len(observations) > 0 && len(ranked) == 0 || len(ranked) > maximum {
		return LiveCatalogReviewResultV2{}, fault.New(
			fault.ProviderRejected, "PHASE8_MANAGED_RANKING_INVALID", false,
		)
	}
	// With explicit criteria every observed product enters the pool. An item
	// the model skipped, or scored against facts it never saw, is admitted
	// without an assessment ("not evaluated") instead of costing the Round.
	partial := len(evaluation) > 0 && evaluation[0].Criteria != nil
	seen := make(map[string]struct{}, len(ranked))
	ordered := make([]CatalogProductObservation, 0, len(observations))
	assessments := make(map[string]LiveCandidateAssessmentV2, len(observations))
	evaluated := 0
	for _, value := range ranked {
		id := strings.TrimSpace(value.ObservationID)
		product, found := products[id]
		if !found {
			return LiveCatalogReviewResultV2{}, fault.New(
				fault.ProviderRejected, "PHASE8_MANAGED_RANKING_UNOBSERVED_PRODUCT", false,
			)
		}
		if _, duplicate := seen[id]; duplicate {
			return LiveCatalogReviewResultV2{}, fault.New(
				fault.ProviderRejected, "PHASE8_MANAGED_RANKING_DUPLICATE_PRODUCT", false,
			)
		}
		seen[id] = struct{}{}
		product.ProviderOrder = len(ordered)
		ordered = append(ordered, product)
		var axes *researchdomain.AxisAssessmentV1
		allowed := map[string]bool{}
		for _, o := range observations {
			if o.ObservationID == id {
				for _, f := range o.FactIDs {
					allowed[f] = true
				}
				if len(evaluation) > 0 && evaluation[0].Criteria != nil && evaluation[0].ModelKey == "gpt-5.6-luna" && o.ImageURL != "" {
					for _, a := range evaluation[0].Criteria.Axes {
						if a.UsesVisualEvidence {
							allowed["image"] = true
						}
					}
				}
			}
		}
		var itemErr error
		for _, score := range value.AxisScores {
			for _, id := range score.FactIDs {
				if id == "price" && partial {
					for _, axis := range evaluation[0].Criteria.Axes {
						if axis.AxisID == score.AxisID && !axis.UsesPrice {
							itemErr = fault.New(fault.ProviderRejected, "RESEARCH_PRICE_AXIS_REQUIRED", false)
						}
					}
				}
				if !allowed[id] {
					itemErr = fault.New(fault.ProviderRejected, "RESEARCH_UNOBSERVED_FACT", false)
				}
			}
		}
		if itemErr == nil && partial {
			e := evaluation[0]
			axes, itemErr = researchdomain.NewAxisAssessment(*e.Criteria, value.AxisScores, e.Locale, e.RoundID, e.ModelKey, e.Now)
		}
		if itemErr != nil {
			if !partial {
				return LiveCatalogReviewResultV2{}, itemErr
			}
			// The product stays a candidate; only this assessment is discarded.
			axes = nil
		}
		if axes != nil {
			evaluated++
			for _, o := range observations {
				if o.ObservationID == id {
					raw, _ := json.Marshal(o)
					hash := sha256.Sum256(raw)
					axes.ObservationHash = hex.EncodeToString(hash[:])
					axes.Source = string(product.Source())
					break
				}
			}
		}
		assessments[id] = LiveCandidateAssessmentV2{AxisAssessment: axes,
			IntentPoint:    strings.TrimSpace(value.IntentPoint),
			Features:       append([]string{}, value.Features...),
			Specifications: append([]string{}, value.Specifications...),
		}
	}
	if partial {
		// Products the model left out keep their provider order after the
		// assessed ones and enter the pool unevaluated.
		for _, product := range result.Search.Products {
			id := strings.TrimSpace(product.ProviderProductID)
			if _, done := seen[id]; done {
				continue
			}
			seen[id] = struct{}{}
			product.ProviderOrder = len(ordered)
			ordered = append(ordered, product)
			assessments[id] = LiveCandidateAssessmentV2{Features: []string{}, Specifications: []string{}}
		}
	} else {
		evaluated = len(ordered)
	}
	result.Search.Products = ordered
	result.CandidateAssessments = assessments
	result.CandidateEligibleCount = len(ordered)
	result.EvaluatedCount = evaluated
	result.UnevaluatedCount = len(ordered) - evaluated
	if len(observations) > 0 {
		result.Metrics.AICallCount++
	}
	return result, nil
}

func catalogManagedRankingDescriptionV2(value CatalogDescription) string {
	if plain := strings.TrimSpace(value.Plain); plain != "" {
		return plain
	}
	if markdown := strings.TrimSpace(value.Markdown); markdown != "" {
		return markdown
	}
	return strings.TrimSpace(value.HTML)
}

func catalogManagedRankingMerchantV2(product CatalogProductObservation) string {
	if product.PreviewVariant == nil || product.PreviewVariant.Seller == nil {
		return ""
	}
	if name := strings.TrimSpace(product.PreviewVariant.Seller.Name); name != "" {
		return name
	}
	return strings.TrimSpace(product.PreviewVariant.Seller.Domain)
}

func catalogMinorAmountV2(value int64, exponent uint8) string {
	if exponent == 0 {
		return strconv.FormatInt(value, 10)
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := strconv.FormatInt(value, 10)
	for len(digits) <= int(exponent) {
		digits = "0" + digits
	}
	point := len(digits) - int(exponent)
	amount := digits[:point] + "." + digits[point:]
	if negative {
		return "-" + amount
	}
	return amount
}

type catalogAssessmentContext struct {
	Criteria                  *curationdomain.TargetCriteriaSetV1
	Locale, RoundID, ModelKey string
	Now                       time.Time
}

// The deterministic merge preserves each source's relevance order without
// letting source registration order decide which candidates reach evaluation.
func catalogSelectNewProducts(products []CatalogProductObservation, limit int) []CatalogProductObservation {
	return catalogSelectNewProductsForRound(products, "", limit)
}
func catalogSelectNewProductsForRound(products []CatalogProductObservation, round string, limit int) []CatalogProductObservation {
	merged := mergeDiscoveryProducts(products, round)
	return append([]CatalogProductObservation{}, merged[:min(len(merged), max(0, limit))]...)
}

// catalogFreshProductsForRoundV2 splits the observed products into the ones the
// pool does not hold yet and duplicates. A candidate the same Round already
// published unevaluated counts as fresh, so a retried attempt evaluates it
// instead of dropping it as a duplicate. The returned count is how many
// candidates the Target already holds, for the pool capacity.
func catalogFreshProductsForRoundV2(
	stored []CatalogCandidateReferenceV2,
	products []CatalogProductObservation,
	targetID, roundID string,
) ([]CatalogProductObservation, int, int) {
	existing := map[string]bool{}
	pending := map[string]bool{}
	count := 0
	for _, c := range stored {
		if c.PlanTargetID != targetID {
			continue
		}
		key := c.ProductRef().IdentityKey()
		existing[key] = true
		count++
		if c.EvaluationRoundID != "" && c.EvaluationRoundID == roundID && c.Assessment.AxisAssessment == nil {
			pending[key] = true
		}
	}
	duplicateCount := 0
	fresh := make([]CatalogProductObservation, 0, len(products))
	for _, p := range products {
		key := p.ProductRef().IdentityKey()
		if !existing[key] || pending[key] {
			existing[key] = true
			delete(pending, key)
			fresh = append(fresh, p)
		} else {
			duplicateCount++
		}
	}
	return fresh, duplicateCount, count
}

// Published, unevaluated rows already occupy pool slots. A retry can evaluate
// them even when the pool is full; only genuinely new identities use capacity.
func catalogSelectRoundProducts(stored []CatalogCandidateReferenceV2, products []CatalogProductObservation, target, round string, existingCount int) []CatalogProductObservation {
	pending := map[string]bool{}
	for _, c := range stored {
		if c.PlanTargetID == target && c.EvaluationRoundID == round && c.Assessment.AxisAssessment == nil {
			pending[c.ProductRef().IdentityKey()] = true
		}
	}
	resume, fresh := []CatalogProductObservation{}, []CatalogProductObservation{}
	for _, p := range products {
		if pending[p.ProductRef().IdentityKey()] {
			resume = append(resume, p)
		} else {
			fresh = append(fresh, p)
		}
	}
	resume = catalogSelectNewProductsForRound(resume, round, CandidateUpdateSize)
	fresh = catalogSelectNewProductsForRound(fresh, round, max(0, min(CandidateUpdateSize-len(resume), 50-existingCount)))
	return append(resume, fresh...)
}
