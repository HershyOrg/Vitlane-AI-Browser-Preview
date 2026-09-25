package app

import (
	"context"
	"errors"
	"strings"
	"time"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

const (
	CatalogCandidatePoolCapacityReachedV2       = "TARGET_CANDIDATE_LIMIT_REACHED"
	CatalogCandidatePoolVersionConflictV2       = "PHASE8_CANDIDATE_POOL_VERSION_CONFLICT"
	CatalogCandidatePoolIdempotencyConflictV2   = "PHASE8_CANDIDATE_POOL_IDEMPOTENCY_CONFLICT"
	CatalogCandidatePoolSearchInProgressV2      = "PHASE8_CANDIDATE_POOL_SEARCH_IN_PROGRESS"
	CatalogCandidatePoolReservationLostV2       = "PHASE8_CANDIDATE_POOL_RESERVATION_LOST"
	catalogCandidatePoolCommandInvalidV2        = "PHASE8_CANDIDATE_POOL_COMMAND_INVALID"
	catalogCandidatePoolRepositoryUnavailableV2 = "PHASE8_CANDIDATE_POOL_COMMAND_REPOSITORY_UNAVAILABLE"
	catalogCandidatePoolAbortFailedV2           = "PHASE8_CANDIDATE_POOL_COMMAND_ABORT_FAILED"
)

// CatalogCandidatePoolCommandV2 is the durable admission identity for one
// response-scoped catalog read. The request hash is issued by the application;
// callers cannot provide or alter it.
type CatalogCandidatePoolCommandV2 struct {
	ResumeRoundID       string
	Budget              *ResearchBudgetSnapshot
	UserID              string
	CurationID          string
	TargetID            string
	Mode                CatalogResearchModeV2
	ExpectedPoolVersion int64
	IdempotencyKey      string
	RequestHash         string
	// FencingToken is issued only by the command repository after a successful
	// preflight. It is intentionally excluded from the semantic request hash
	// and from HTTP contracts; provider results may finalize only while they
	// still own this exact opaque reservation.
	FencingToken string `json:"-"`
}

type CatalogCandidatePoolPreflightV2 struct {
	Replay         bool
	Pool           CatalogPoolMetadataV2
	FencingToken   string    `json:"-"`
	LeaseExpiresAt time.Time `json:"-"`
}

type CatalogCandidatePoolExecutionV2 struct {
	Replay bool
	Pool   CatalogPoolMetadataV2
}

type CatalogCandidatePoolProviderReadV2 func() (
	[]CatalogCandidateReferenceV2,
	LiveCatalogReviewMetricsV2,
	error,
)

// CatalogCandidatePoolCommandRepositoryV2 reserves at most one non-terminal
// Expand/Research Again per Target, then atomically finalizes CandidatePool
// state. Abort must remove only the matching non-terminal reservation; it must
// never mutate the pool, Candidate rows, or visibility.
type CatalogCandidatePoolCommandRepositoryV2 interface {
	PreflightCatalogSearchV2(
		context.Context,
		CatalogCandidatePoolCommandV2,
	) (CatalogCandidatePoolPreflightV2, error)
	CompleteCatalogSearchV2(
		context.Context,
		CatalogCandidatePoolCommandV2,
		[]CatalogCandidateReferenceV2,
		LiveCatalogReviewMetricsV2,
	) (CatalogPoolMetadataV2, error)
	AbortCatalogSearchV2(context.Context, CatalogCandidatePoolCommandV2) error
	// Staged publication (ADR-0083): PublishObservedCandidatesV2 admits the
	// observed products unevaluated while the command stays RUNNING under the
	// same fencing token; FillCandidateAssessmentsV2 writes assessments once
	// into rows that have none; FinalizeStagedSearchV2 closes the command.
	// Every stage is a CAS on the pool version the caller last saw.
	PublishObservedCandidatesV2(
		context.Context,
		CatalogCandidatePoolCommandV2,
		[]CatalogCandidateReferenceV2,
		LiveCatalogReviewMetricsV2,
	) (CatalogPoolMetadataV2, error)
	FillCandidateAssessmentsV2(
		context.Context,
		CatalogCandidatePoolCommandV2,
		int64,
		map[string]LiveCandidateAssessmentV2,
		time.Time,
	) (int64, int, error)
	FinalizeStagedSearchV2(
		context.Context,
		CatalogCandidatePoolCommandV2,
		int64,
		string,
		CatalogPoolMetadataV2,
		LiveCatalogReviewMetricsV2,
	) (CatalogPoolMetadataV2, error)
}

// NewCatalogCandidatePoolCommandV2 validates the caller-owned concurrency
// fields and hashes the complete semantic search request. Including the
// expected pool version prevents one idempotency key from silently changing
// its CAS basis.
func NewCatalogCandidatePoolCommandV2(
	input CatalogWorkspaceSearchInputV2,
	expectedPoolVersion int64,
	idempotencyKey string,
) (CatalogCandidatePoolCommandV2, error) {
	key := strings.TrimSpace(idempotencyKey)
	if strings.TrimSpace(input.UserID) == "" ||
		strings.TrimSpace(input.CurationID) == "" ||
		strings.TrimSpace(input.TargetID) == "" ||
		(input.Mode != CatalogResearchReplaceV2 && input.Mode != CatalogResearchAppendV2) ||
		expectedPoolVersion < 0 || key == "" || len(key) > 200 {
		return CatalogCandidatePoolCommandV2{}, fault.New(
			fault.InvalidInput, catalogCandidatePoolCommandInvalidV2, false,
		)
	}
	requestHash, err := shareddomain.CanonicalJSONHash(struct {
		ResumeRoundID       string                   `json:"resumeRoundId,omitempty"`
		ExecutionVersion    string                   `json:"executionVersion,omitempty"`
		QueryProjections    []CatalogQueryProjection `json:"queryProjections,omitempty"`
		PreferredExclusions []string                 `json:"preferredExclusions,omitempty"`
		Budget              *ResearchBudgetSnapshot  `json:"budget,omitempty"`
		ProductVertical     string                   `json:"productVertical,omitempty"`
		SchemaVersion       string                   `json:"schemaVersion"`
		UserID              string                   `json:"userId"`
		CurationID          string                   `json:"curationId"`
		TargetID            string                   `json:"targetId"`
		Mode                CatalogResearchModeV2    `json:"mode"`
		ExpectedPoolVersion int64                    `json:"expectedPoolVersion"`
		FeedbackEnglish     string                   `json:"feedbackEnglish,omitempty"`
		Country             string                   `json:"country"`
		Currency            string                   `json:"currency"`
		MinimumMinor        *int64                   `json:"minimumMinor,omitempty"`
		MaximumMinor        *int64                   `json:"maximumMinor,omitempty"`
		PreferredTerms      []string                 `json:"preferredTerms,omitempty"`
		HardLexicalTerms    []string                 `json:"hardLexicalTerms,omitempty"`
		Exclusions          []string                 `json:"exclusions,omitempty"`
		Limit               int                      `json:"limit"`
	}{
		ResumeRoundID: input.ResumeRoundID, ExecutionVersion: input.ExecutionVersion, QueryProjections: input.QueryProjections, PreferredExclusions: input.Search.PreferredExclusions,
		ProductVertical: strings.ToUpper(strings.TrimSpace(input.ProductVertical)),
		Budget:          input.Budget, SchemaVersion: "vitlane.phase8-candidate-pool-command.v1",
		UserID: strings.TrimSpace(input.UserID), CurationID: strings.TrimSpace(input.CurationID),
		TargetID: strings.TrimSpace(input.TargetID), Mode: input.Mode,
		ExpectedPoolVersion: expectedPoolVersion,
		FeedbackEnglish:     catalogCandidatePoolFeedbackV2(input),
		Country:             strings.ToUpper(strings.TrimSpace(input.Search.Country)),
		Currency:            strings.ToUpper(strings.TrimSpace(input.Search.Currency)),
		MinimumMinor:        cloneCatalogMinorV2(input.Search.MinimumMinor),
		MaximumMinor:        cloneCatalogMinorV2(input.Search.MaximumMinor),
		PreferredTerms:      append([]string(nil), input.Search.PreferredTerms...),
		HardLexicalTerms:    append([]string(nil), input.Search.HardLexicalTerms...),
		Exclusions:          append([]string(nil), input.Search.Exclusions...),
		Limit:               catalogCandidatePoolLimitV2(input.Search.Limit),
	})
	if err != nil {
		return CatalogCandidatePoolCommandV2{}, fault.Wrap(
			err, fault.InternalFailure, catalogCandidatePoolCommandInvalidV2, false,
		)
	}
	return CatalogCandidatePoolCommandV2{
		ResumeRoundID: input.ResumeRoundID,
		Budget:        input.Budget,
		UserID:        strings.TrimSpace(input.UserID), CurationID: strings.TrimSpace(input.CurationID),
		TargetID: strings.TrimSpace(input.TargetID), Mode: input.Mode,
		ExpectedPoolVersion: expectedPoolVersion, IdempotencyKey: key,
		RequestHash: requestHash,
	}, nil
}

func catalogCandidatePoolFeedbackV2(input CatalogWorkspaceSearchInputV2) string {
	if input.Mode != CatalogResearchReplaceV2 {
		return ""
	}
	return strings.ToLower(strings.Join(strings.Fields(input.Search.Query), " "))
}

func catalogCandidatePoolLimitV2(value int) int {
	if value == 0 {
		return CandidateUpdateSize
	}
	return min(value, CandidateUpdateSize)
}

func (service *LiveCatalogReviewServiceV2) PreflightWorkspaceSearchV2(
	ctx context.Context,
	command CatalogCandidatePoolCommandV2,
) (CatalogCandidatePoolPreflightV2, error) {
	repository, err := service.catalogCandidatePoolCommandRepositoryV2()
	if err != nil {
		return CatalogCandidatePoolPreflightV2{}, err
	}
	return repository.PreflightCatalogSearchV2(ctx, command)
}

// ExecuteWorkspaceCandidatePoolCommandV2 is the application boundary that
// guarantees preflight-before-provider and provider-fault mutation 0. The
// callback is never invoked for a capacity/version/in-flight conflict or a
// completed idempotency replay.
func (service *LiveCatalogReviewServiceV2) ExecuteWorkspaceCandidatePoolCommandV2(
	ctx context.Context,
	command CatalogCandidatePoolCommandV2,
	providerRead CatalogCandidatePoolProviderReadV2,
) (CatalogCandidatePoolExecutionV2, error) {
	if providerRead == nil {
		return CatalogCandidatePoolExecutionV2{}, fault.New(
			fault.InvalidInput, catalogCandidatePoolCommandInvalidV2, false,
		)
	}
	preflight, err := service.PreflightWorkspaceSearchV2(ctx, command)
	if err != nil {
		return CatalogCandidatePoolExecutionV2{}, err
	}
	if preflight.Replay {
		return CatalogCandidatePoolExecutionV2{Replay: true, Pool: preflight.Pool}, nil
	}
	if strings.TrimSpace(preflight.FencingToken) == "" || preflight.LeaseExpiresAt.IsZero() {
		return CatalogCandidatePoolExecutionV2{}, fault.New(
			fault.InternalFailure, catalogCandidatePoolRepositoryUnavailableV2, false,
		)
	}
	command.FencingToken = preflight.FencingToken
	candidates, metrics, providerErr := providerRead()
	if providerErr != nil {
		if abortErr := service.abortWorkspaceSearchAfterFailureV2(ctx, command); abortErr != nil {
			return CatalogCandidatePoolExecutionV2{}, fault.Wrap(
				errors.Join(providerErr, abortErr), fault.InternalFailure,
				catalogCandidatePoolAbortFailedV2, false,
			)
		}
		return CatalogCandidatePoolExecutionV2{}, providerErr
	}
	pool, completionErr := service.CompleteWorkspaceSearchV2(
		ctx, command, candidates, metrics,
	)
	if completionErr != nil {
		if abortErr := service.abortWorkspaceSearchAfterFailureV2(ctx, command); abortErr != nil {
			return CatalogCandidatePoolExecutionV2{}, fault.Wrap(
				errors.Join(completionErr, abortErr), fault.InternalFailure,
				catalogCandidatePoolAbortFailedV2, false,
			)
		}
		return CatalogCandidatePoolExecutionV2{}, completionErr
	}
	return CatalogCandidatePoolExecutionV2{Pool: pool}, nil
}

func (service *LiveCatalogReviewServiceV2) CompleteWorkspaceSearchV2(
	ctx context.Context,
	command CatalogCandidatePoolCommandV2,
	candidates []CatalogCandidateReferenceV2,
	metrics LiveCatalogReviewMetricsV2,
) (CatalogPoolMetadataV2, error) {
	repository, err := service.catalogCandidatePoolCommandRepositoryV2()
	if err != nil {
		return CatalogPoolMetadataV2{}, err
	}
	return repository.CompleteCatalogSearchV2(ctx, command, candidates, metrics)
}

// PublishObservedCandidatesV2 is stage one of a staged Round: the observed
// products enter the pool unevaluated so the cards appear before the model
// answers. The command stays RUNNING and its lease is renewed.
func (service *LiveCatalogReviewServiceV2) PublishObservedCandidatesV2(
	ctx context.Context,
	command CatalogCandidatePoolCommandV2,
	candidates []CatalogCandidateReferenceV2,
	metrics LiveCatalogReviewMetricsV2,
) (CatalogPoolMetadataV2, error) {
	repository, err := service.catalogCandidatePoolCommandRepositoryV2()
	if err != nil {
		return CatalogPoolMetadataV2{}, err
	}
	return repository.PublishObservedCandidatesV2(ctx, command, candidates, metrics)
}

// FillCandidateAssessmentsV2 is stage two: assessments are written once into
// candidates that have none. It never overwrites a saved assessment.
func (service *LiveCatalogReviewServiceV2) FillCandidateAssessmentsV2(
	ctx context.Context,
	command CatalogCandidatePoolCommandV2,
	expectedVersion int64,
	assessments map[string]LiveCandidateAssessmentV2,
	now time.Time,
) (int64, int, error) {
	repository, err := service.catalogCandidatePoolCommandRepositoryV2()
	if err != nil {
		return 0, 0, err
	}
	return repository.FillCandidateAssessmentsV2(ctx, command, expectedVersion, assessments, now)
}

// FinalizeStagedSearchV2 is stage three: coverage, outcome and metrics land,
// the Round's pending marks clear and the command completes.
func (service *LiveCatalogReviewServiceV2) FinalizeStagedSearchV2(
	ctx context.Context,
	command CatalogCandidatePoolCommandV2,
	expectedVersion int64,
	roundID string,
	published CatalogPoolMetadataV2,
	metrics LiveCatalogReviewMetricsV2,
) (CatalogPoolMetadataV2, error) {
	repository, err := service.catalogCandidatePoolCommandRepositoryV2()
	if err != nil {
		return CatalogPoolMetadataV2{}, err
	}
	return repository.FinalizeStagedSearchV2(ctx, command, expectedVersion, roundID, published, metrics)
}

func (service *LiveCatalogReviewServiceV2) AbortWorkspaceSearchV2(
	ctx context.Context,
	command CatalogCandidatePoolCommandV2,
) error {
	repository, err := service.catalogCandidatePoolCommandRepositoryV2()
	if err != nil {
		return err
	}
	return repository.AbortCatalogSearchV2(ctx, command)
}

func (service *LiveCatalogReviewServiceV2) abortWorkspaceSearchAfterFailureV2(
	ctx context.Context,
	command CatalogCandidatePoolCommandV2,
) error {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	return service.AbortWorkspaceSearchV2(cleanupContext, command)
}

func (service *LiveCatalogReviewServiceV2) catalogCandidatePoolCommandRepositoryV2() (
	CatalogCandidatePoolCommandRepositoryV2,
	error,
) {
	if service == nil || service.workspace == nil {
		return nil, fault.New(
			fault.InternalFailure, catalogCandidatePoolRepositoryUnavailableV2, false,
		)
	}
	repository, ok := service.workspace.(CatalogCandidatePoolCommandRepositoryV2)
	if !ok {
		return nil, fault.New(
			fault.InternalFailure, catalogCandidatePoolRepositoryUnavailableV2, false,
		)
	}
	return repository, nil
}
