package app

import (
	"context"
	"time"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
)

// These types are the workflow's own view of the work it runs. They exist so
// intelligence/app never imports curation, research or a model vendor: the
// adapters in infra translate. That is what keeps both the products and the
// providers replaceable.

const (
	WorkKindPlanning = "PLANNING"
	WorkKindResearch = "RESEARCH"
)

type Money struct {
	Amount   string
	Currency string
}

type PlanningContext struct {
	ContentLocale          string
	UnifiedAuto            bool
	BudgetResolved         bool
	BudgetDecision         *PlanningBudgetDecision
	BudgetInferenceAllowed bool
	ResolvedBudget         *Money
	BudgetVersion          int64
	BudgetPolicy           bool
	BudgetEnabled          bool
	BudgetCurrency         string
	BudgetAllocationMode   string
	TaskID                 string
	PlanID                 string
	ContextVersion         int64
	ContextHash            string
	ProposalSchema         string
	OriginalIntent         string
	PlanningMode           string
	TotalBudget            Money
	Country                string
	City                   string
	Category               string
	AllowedItems           []string
	BlockedItems           []string
	MinPrice               *Money
	MaxPrice               *Money
	ReferenceURL           string
	URLMode                string
	MinimumTargets         int
	MaximumTargets         int
	// InitialRun may resolve an AUTO baseline using the recorded budget decision.
	// Expansion allocates the new membership while preserving the saved total.
	InitialRun bool
}

type ResearchPurchasedVariant struct {
	ProductID   string `json:"productId,omitempty"`
	Source      string `json:"source"`
	Marketplace string `json:"marketplace"`
	VariantID   string `json:"variantId,omitempty"`
}

type ResearchBudget struct {
	Enabled     bool
	Quantity    int
	Amount      Money
	MaximumUnit *Money
}

type ResearchContext struct {
	CatalogLanguages        []CatalogLanguageRequirement
	CatalogLanguage         string
	ProductVertical         string
	AllowFeedbackCriteria   bool
	CachedQuery             *CatalogQueryPayload
	ExecutionCheckpoint     bool
	ContentLocale           string
	Criteria                *ResearchCriteria
	QuerySeeds              []string
	Budget                  *ResearchBudget
	PurchaseFeedbackVersion int64
	AlreadyPurchased        []ResearchPurchasedVariant
	RoundID                 string
	UserID                  string
	PlanID                  string
	ContextVersion          int64
	ContextHash             string
	ContextSchema           string
	TargetTitle             string
	TargetIntent            string
	Category                string
	AllocatedBudget         Money
	Country                 string
	City                    string
	AllowedItems            []string
	BlockedItems            []string
	MinPrice                *Money
	MaxPrice                *Money
	CandidateMinimum        int
	CandidateMaximum        int
	// FeedbackRequired distinguishes a feedback re-research round from an
	// initial round without inferring state from prose. The exact feedback
	// version and hash must be echoed by the v4 submission.
	FeedbackRequired bool
	FeedbackSummary  string
	FeedbackVersion  int64
	FeedbackHash     string
}

// ProposedTarget is one shopping item the planning step produced. Budget is
// an estimate weight for initial allocation, or a new Target spending goal.
// Curation validates the proposal and owns all ledger writes.
type ProposedTarget struct {
	ProductVertical string
	ContentLocale   string
	Criteria        *ResearchCriteria
	Quantity        int
	Title           string
	Category        string
	SearchQuery     string
	Rationale       string
	Budget          Money
}

type SubmissionOutcome struct {
	Accepted   bool
	ResultID   string
	ReasonCode string
}

// ResearchCandidateObservation is the provider-neutral, response-scoped view
// of one Shopify product that passed Research-owned hard filters. The model may
// rank these observations and write semantic copy, but it cannot return any
// product fact that is later persisted as authority.
type ResearchCandidateObservation struct {
	ImageURL             string
	FactIDs              []string
	ObservationID        string
	Name                 string
	Description          string
	Merchant             string
	PriceMinimum         Money
	PriceMaximum         Money
	ServerIntentPoint    string
	ServerFeatures       []string
	ServerSpecifications []string
}

// ResearchRankedCandidate is the only model-authored value accepted after the
// Shopify read. ObservationID must name an item in the exact input slice; all
// Shopify facts remain on the server-owned observation.
type ResearchRankedCandidate struct {
	AxisScores     []AxisScore
	ObservationID  string
	IntentPoint    string
	Features       []string
	Specifications []string
}

// ResearchCandidateRanker runs inside the Intelligence pipeline after
// Research has completed Shopify search/hard filtering but before the
// CandidatePool transaction. This preserves Attempt/Step/cost ownership in
// Intelligence without letting Research import a model provider.
type ResearchCandidateRanker func(
	context.Context,
	[]ResearchCandidateObservation,
	int,
) ([]ResearchRankedCandidate, error)

type ResearchExecutionOutcome struct {
	CandidateCount int
	NoResults      bool
	PoolVersion    int64
}

// CompletionRequest is one provider call. It carries no product identity beyond
// the user, and no cost or credential material in either direction: everything
// a provider needs to bill, authorize or route is the provider's own business.
type CompletionRequest struct {
	Images []CompletionImage
	UserID string
	// RequestKey is the provider dispatch idempotency key. A provider that can
	// be asked twice for the same key must answer with the same result rather
	// than executing again.
	RequestKey   string
	JobID        string
	AttemptID    string
	ModelKey     string
	SystemPrompt string
	UserPrompt   string
	SchemaName   string
	Schema       map[string]any
	Deadline     time.Time
}

// CompletionResult deliberately carries only the schema-shaped JSON. Token
// usage and cost stay inside the provider so the common workflow can never
// branch on them.
type CompletionResult struct {
	Content string
}

// Provider is the whole seam between the workflow and an intelligence source.
// Managed runs Vitlane's own credentials and cost ledger; the Desktop Connector
// proxies the user's local CLI agent. Both answer the same call and report the
// same shared fault codes.
type Provider interface {
	Kind() intelligencedomain.ProviderKind
	// Available reports whether this provider can accept work for the user
	// right now. A quota that is already spent answers QUOTA_EXCEEDED here
	// rather than after a dispatch.
	Available(ctx context.Context, userID string) error
	Complete(
		ctx context.Context, request CompletionRequest,
	) (CompletionResult, error)
}

// Sweeper is an optional provider hook for periodic internal recovery, such as
// classifying cost reservations a crashed process left held.
type Sweeper interface {
	Sweep(ctx context.Context, now time.Time) error
}

// ProductPort is the seam onto curation and research. Everything a job does to
// user state goes through here, and every write is validated by the owning
// product exactly as it validates a user's own command.
type ProductPort interface {
	ReadPlanningContext(
		ctx context.Context, userID string, taskID string,
	) (PlanningContext, error)
	ReadResearchContext(
		ctx context.Context, userID string, jobID string, roundID string,
	) (ResearchContext, error)
	SubmitPlanning(
		ctx context.Context, userID string, jobID string,
		planningContext PlanningContext, targets []ProposedTarget,
	) (SubmissionOutcome, error)
	RunCatalogResearch(
		ctx context.Context, userID string, jobID string, attemptID string,
		roundID string, query CatalogQueryPayload,
		ranker ResearchCandidateRanker,
	) (ResearchExecutionOutcome, error)
	// FailResearchTarget consumes only a final (non-reopened) Job failure.
	// Research releases its Round/Session projection without mutating the
	// previous CandidatePool.
	FailResearchTarget(
		ctx context.Context, userID string, roundID string,
		reasonCode string, retryable bool,
	) error
	// StartCurating advances a plan from PLANNING to CURATING without a second
	// user action. It reports whether it actually transitioned so the caller
	// can tell "already curating" from a real failure.
	StartCurating(
		ctx context.Context, userID string,
		planID string, curationActionID string,
	) (bool, error)
	// StartReadySessions opens research for every session still sitting READY
	// on this plan. It returns how many it started so a no-op sweep stays
	// silent in the log.
	//
	// The sweep is Server-issued and records no action of its own, so the
	// calling job's action travels with it: ADR-0038 projects derived work
	// through parent action lineage rather than inventing a user action that
	// never happened.
	StartReadySessions(
		ctx context.Context, userID string,
		planID string, curationActionID string,
	) (int, error)
	// CancelTarget closes the product row behind a job the user cancelled.
	CancelTarget(
		ctx context.Context, userID string, target intelligencedomain.JobTarget,
	) error
}

// ClaimPolicy is the execution admission the dispatcher applies at claim
// time. Research rounds are the expensive, contended work: they are capped per
// user, per curation and globally so a burst of targets queues instead of
// saturating the catalog APIs and the model. Every other job kind (planning,
// interpretation) has its own short lane so research can never starve it.
// Saturation is the maximum over the three research caps.
type ClaimPolicy struct {
	OtherLane           int
	ResearchPerUser     int
	ResearchPerCuration int
	ResearchGlobal      int
}

func DefaultClaimPolicy() ClaimPolicy {
	return ClaimPolicy{OtherLane: 4, ResearchPerUser: 3, ResearchPerCuration: 3, ResearchGlobal: 6}
}

func (p ClaimPolicy) Valid() bool {
	return p.OtherLane > 0 && p.ResearchPerUser > 0 && p.ResearchPerCuration > 0 &&
		p.ResearchGlobal > 0 && p.ResearchPerCuration <= p.ResearchPerUser
}

// ClaimedJob is one job with the attempt that was opened to run it.
type ClaimedJob struct {
	Job     intelligencedomain.Job
	Attempt intelligencedomain.Attempt
}

// JobProgress is the browser projection. It is assembled from the job, its
// active attempt and that attempt's observation steps, and replaces the
// separate agent-work and runner-progress polls.
type JobProgress struct {
	JobID               string                           `json:"jobId"`
	ActionID            string                           `json:"actionId"`
	TargetKind          string                           `json:"targetKind"`
	TargetID            string                           `json:"targetId"`
	Provider            string                           `json:"provider"`
	Status              string                           `json:"status"`
	FailureCode         string                           `json:"failureCode,omitempty"`
	Retryable           bool                             `json:"retryable"`
	Attempt             int                              `json:"attempt"`
	Steps               []intelligencedomain.Step        `json:"steps"`
	Target              intelligencedomain.JobTarget     `json:"-"`
	LatestAttemptStatus intelligencedomain.AttemptStatus `json:"-"`
	// QueueReason, NotBefore and QueueAhead describe a PENDING job's wait: why
	// it is not running yet, when it may be claimed again, and how many of
	// the user's research jobs are ahead of it in the claim order.
	QueueReason string     `json:"queueReason,omitempty"`
	NotBefore   *time.Time `json:"notBefore,omitempty"`
	QueueAhead  *int       `json:"queueAhead,omitempty"`
}

type Repository interface {
	InsertJob(ctx context.Context, job intelligencedomain.Job) error
	FindJobByTarget(
		ctx context.Context, userID string,
		target intelligencedomain.JobTarget,
	) (intelligencedomain.Job, bool, error)
	GetJob(
		ctx context.Context, userID string, jobID string,
	) (intelligencedomain.Job, error)
	// ClaimPending selects PENDING jobs for one provider within the claim
	// policy and opens an attempt for each in the same transaction. Jobs whose
	// not-before time has not passed, and research jobs beyond the per-user,
	// per-curation or global cap, stay PENDING. SKIP LOCKED keeps concurrent
	// workers from competing for the same row.
	ClaimPending(
		ctx context.Context, kind intelligencedomain.ProviderKind,
		policy ClaimPolicy, deadline time.Duration, now time.Time,
	) ([]ClaimedJob, error)
	// DeferJob closes the attempt as DEFERRED and returns the job to PENDING
	// with a not-before time. It is not a failure and is not counted as one.
	DeferJob(
		ctx context.Context, job intelligencedomain.Job,
		attempt intelligencedomain.Attempt, notBefore time.Time,
		reason string, now time.Time,
	) error
	CloseAttempt(
		ctx context.Context, attempt intelligencedomain.Attempt,
	) error
	CloseJob(
		ctx context.Context, jobID string,
		status intelligencedomain.JobStatus,
		failureCode string, retryable bool, now time.Time,
	) error
	// ReopenJob returns a closed job to PENDING for another attempt. A
	// non-nil notBefore keeps it out of the claim until the backoff passes.
	ReopenJob(ctx context.Context, jobID string, notBefore *time.Time, now time.Time) error
	OverdueAttempts(
		ctx context.Context, now time.Time, limit int,
	) ([]intelligencedomain.Attempt, error)
	// OrphanRunningJobs lists RUNNING jobs that have no RUNNING attempt and
	// were last updated before the given time: a parked EFFECT_UNKNOWN or a
	// finalize write that never completed. They are closed by the reconciler
	// so a job can never block its curation until someone cancels it.
	OrphanRunningJobs(
		ctx context.Context, before time.Time, limit int,
	) ([]OrphanJob, error)
	InsertStep(ctx context.Context, step intelligencedomain.Step) error
	UpdateStep(ctx context.Context, step intelligencedomain.Step) error
	ListCurationJobs(
		ctx context.Context, userID string, curationID string,
	) ([]JobProgress, error)
	// CountActiveActions and PlanHasActiveJobs answer the concurrency limits
	// the user experiences. They read the same job rows the dispatcher does,
	// so there is no second place where "still working" is recorded.
	// LockActionAdmission serializes the count-and-create decision for one user
	// for the lifetime of the caller's product transaction.
	LockActionAdmission(ctx context.Context, userID string) error
	CountActiveActions(ctx context.Context, userID string) (int, error)
	PlanHasActiveJobs(
		ctx context.Context, userID string, planID string,
	) (bool, error)
	CurationHasActiveJobs(
		ctx context.Context, userID string, curationID string,
	) (bool, error)
	CancelAction(
		ctx context.Context, userID string, curationActionID string,
		now time.Time,
	) ([]CancelledTarget, error)
}

// OrphanJob is a RUNNING job without a RUNNING attempt, with what its latest
// attempt recorded so the closure can carry the same reason.
type OrphanJob struct {
	Job                 intelligencedomain.Job
	LatestAttemptStatus intelligencedomain.AttemptStatus
	FailureCode         string
}

// CancelledTarget is a job the cancel closed together with the product row it
// was working on, so the caller can close both in one transaction.
type CancelledTarget struct {
	JobID  string
	Target intelligencedomain.JobTarget
}

type CompletionImage struct {
	ObservationID string
	URL           string
}
