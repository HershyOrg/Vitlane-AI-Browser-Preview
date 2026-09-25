package domain

import (
	"errors"
	"strings"
	"time"
)

// ADR-0038: a Job is one durable unit of intelligence work for one executable
// product target. An Attempt is one provider execution of that Job. The Job row
// is the authority for what may run — there is no separate claim handle, and no
// batch aggregate above it.

type ProviderKind string

const (
	ProviderManaged ProviderKind = "MANAGED"
)

func (p ProviderKind) Valid() bool {
	return p == ProviderManaged
}

type TargetKind string

const (
	TargetActionInterpretation TargetKind = "ACTION_INTERPRETATION"
	TargetPlanningTask         TargetKind = "PLANNING_TASK"
	TargetResearchRound        TargetKind = "RESEARCH_ROUND"
)

func (k TargetKind) Valid() bool {
	return k == TargetPlanningTask || k == TargetResearchRound || k == TargetActionInterpretation
}

// JobTarget names the exact product row this job executes for. Exactly one job
// exists per target, so a replayed user command resolves to the same job rather
// than dispatching the work twice.
type JobTarget struct {
	Revision int64

	Kind TargetKind
	ID   string
}

type JobStatus string

const (
	JobPending   JobStatus = "PENDING"
	JobRunning   JobStatus = "RUNNING"
	JobSucceeded JobStatus = "SUCCEEDED"
	JobFailed    JobStatus = "FAILED"
	JobCancelled JobStatus = "CANCELLED"
)

func (s JobStatus) Terminal() bool {
	return s == JobSucceeded || s == JobFailed || s == JobCancelled
}

type AttemptStatus string

const (
	AttemptRunning       AttemptStatus = "RUNNING"
	AttemptSucceeded     AttemptStatus = "SUCCEEDED"
	AttemptFailed        AttemptStatus = "FAILED"
	AttemptCancelled     AttemptStatus = "CANCELLED"
	AttemptEffectUnknown AttemptStatus = "EFFECT_UNKNOWN"
	// AttemptDeferred closes an attempt that did no work because a resource it
	// needed (a catalog API slot, a model slot, a provider cooldown) was not
	// ready. The job returns to PENDING with a not-before time and the
	// deferral is not counted as a failed execution.
	AttemptDeferred AttemptStatus = "DEFERRED"
)

// MaximumAutomaticAttempts bounds how often the Server reopens a job by itself.
// A failure that survives three provider executions is not the kind a fourth
// resolves, and the user's allowance should not pay for the discovery.
const MaximumAutomaticAttempts = 3

// MaximumAttempts is the hard ceiling of executed attempts including
// user-initiated retries. Deferred attempts are not executions and do not
// count; the schema bounds the raw ordinal separately.
const MaximumAttempts = 10

// MaximumDeferrals bounds how often a job may wait for a resource before the
// wait itself is reported as a retryable failure. With waits capped at
// MaximumDeferralWait this is about ten minutes of queueing at most.
const MaximumDeferrals = 12

// MaximumDeferralWait caps a single deferral so a provider Retry-After of an
// hour cannot park a job for the whole hour unnoticed.
const MaximumDeferralWait = 90 * time.Second

// RetryBackoff spaces automatic retries of a real failure. The first retry is
// quick because most retryable failures are a malformed answer; later ones
// wait for the provider to recover instead of burning the allowance.
func RetryBackoff(executedAttempts int) time.Duration {
	switch {
	case executedAttempts <= 1:
		return 5 * time.Second
	case executedAttempts == 2:
		return 20 * time.Second
	default:
		return 60 * time.Second
	}
}

var (
	ErrInvalidJob     = errors.New("INTELLIGENCE_JOB_INVALID")
	ErrInvalidAttempt = errors.New("INTELLIGENCE_ATTEMPT_INVALID")
	ErrJobNotFound    = errors.New("INTELLIGENCE_JOB_NOT_FOUND")
	ErrJobClosed      = errors.New("INTELLIGENCE_JOB_CLOSED")
	ErrRetryExhausted = errors.New("INTELLIGENCE_RETRY_EXHAUSTED")
	// ErrProviderResponse marks output that did not match the requested schema.
	// It says nothing about which provider produced it, so the same handling
	// applies to a managed model and a user's local agent.
	ErrProviderResponse = errors.New("INTELLIGENCE_PROVIDER_RESPONSE_INVALID")
)

// Reason codes are the closed enum the Web renders. They describe what the user
// can act on, never provider internals.
const (
	ReasonProviderUnavailable = "PROVIDER_UNAVAILABLE"
	ReasonProviderResponse    = "PROVIDER_RESPONSE_INVALID"
	ReasonQuotaExceeded       = "QUOTA_EXCEEDED"
	ReasonDeadlineExceeded    = "DEADLINE_EXCEEDED"
	ReasonEffectUnknown       = "EXTERNAL_EFFECT_UNKNOWN"
	ReasonCatalogEmpty        = "CATALOG_NO_RESULTS"
	ReasonCatalogFailed       = "CATALOG_UNAVAILABLE"
	ReasonProposalRejected    = "PROPOSAL_REJECTED"
	ReasonSubmissionInvalid   = "SUBMISSION_REJECTED"
	ReasonInternalFailure     = "INTERNAL_FAILURE"
	// ReasonResourceWaitExhausted closes a job that was deferred more than
	// MaximumDeferrals times. It stays retryable: the resource may free up.
	ReasonResourceWaitExhausted = "RESOURCE_WAIT_EXHAUSTED"
	// ReasonModelSlotBusy defers a job whose model call could not obtain a
	// provider slot within the bounded wait.
	ReasonModelSlotBusy = "MODEL_SLOT_BUSY"
	// ReasonRetryBackoff is the queue reason of a job reopened by automatic
	// retry; it waits out the backoff before it can be claimed again.
	ReasonRetryBackoff = "RETRY_BACKOFF"
)

type Job struct {
	ID                string
	UserID            string
	CurationID        string
	CurationActionID  string
	ExecutionActionID string
	PlanID            string
	Target            JobTarget
	Provider          ProviderKind
	ModelKey          string
	Status            JobStatus
	FailureCode       string
	Retryable         bool
	AttemptCount      int
	// DeferCount is how many attempts closed DEFERRED. ExecutedAttempts is
	// what the retry ceilings compare against.
	DeferCount int
	// NotBefore keeps a PENDING job out of the claim until a resource wait or
	// retry backoff has passed; QueueReason says why it waits.
	NotBefore   *time.Time
	QueueReason string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

// ExecutedAttempts counts attempts that actually ran the provider pipeline.
func (j Job) ExecutedAttempts() int {
	return max(0, j.AttemptCount-j.DeferCount)
}

type NewJobInput struct {
	ID               string
	UserID           string
	CurationID       string
	CurationActionID string
	PlanID           string
	Target           JobTarget
	Provider         ProviderKind
	ModelKey         string
	Now              time.Time
}

func NewJob(input NewJobInput) (Job, error) {
	if strings.TrimSpace(input.ID) == "" ||
		strings.TrimSpace(input.UserID) == "" ||
		strings.TrimSpace(input.CurationID) == "" ||
		strings.TrimSpace(input.CurationActionID) == "" ||
		strings.TrimSpace(input.PlanID) == "" ||
		!input.Target.Kind.Valid() ||
		(input.Target.Kind == TargetActionInterpretation && input.Target.Revision < 1) ||
		strings.TrimSpace(input.Target.ID) == "" ||
		!input.Provider.Valid() ||
		input.Now.IsZero() {
		return Job{}, ErrInvalidJob
	}
	// Only MANAGED carries a model key: a Connector job's model belongs to the
	// user's own agent, so accepting one here would imply a control Vitlane
	// does not have.
	if (input.Provider == ProviderManaged) !=
		(strings.TrimSpace(input.ModelKey) != "") {
		return Job{}, ErrInvalidJob
	}
	return Job{
		ID: input.ID, UserID: input.UserID, CurationID: input.CurationID,
		CurationActionID: input.CurationActionID, ExecutionActionID: input.CurationActionID, PlanID: input.PlanID,
		Target: input.Target, Provider: input.Provider,
		ModelKey: strings.TrimSpace(input.ModelKey),
		Status:   JobPending, CreatedAt: input.Now, UpdatedAt: input.Now,
	}, nil
}

// Attempt fixes the provider, dispatch idempotency key and deadline for one
// execution. It is append-only: a retry creates the next ordinal rather than
// reopening this row.
type Attempt struct {
	ID          string
	JobID       string
	UserID      string
	Ordinal     int
	Provider    ProviderKind
	RequestKey  string
	Status      AttemptStatus
	FailureCode string
	Retryable   bool
	DeadlineAt  time.Time
	StartedAt   time.Time
	CompletedAt *time.Time
}

func NewAttempt(
	id string,
	job Job,
	requestKey string,
	ordinal int,
	deadline time.Duration,
	now time.Time,
) (Attempt, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(job.ID) == "" ||
		strings.TrimSpace(requestKey) == "" ||
		ordinal <= 0 || ordinal > MaximumAttempts ||
		deadline <= 0 || now.IsZero() {
		return Attempt{}, ErrInvalidAttempt
	}
	return Attempt{
		ID: id, JobID: job.ID, UserID: job.UserID, Ordinal: ordinal,
		Provider: job.Provider, RequestKey: requestKey,
		Status: AttemptRunning, DeadlineAt: now.Add(deadline), StartedAt: now,
	}, nil
}

type StepKind string

const (
	StepInterpreting     StepKind = "INTERPRETING"
	StepSearchingCatalog StepKind = "SEARCHING_CATALOG"
	StepRanking          StepKind = "RANKING"
	StepSubmitting       StepKind = "SUBMITTING"
)

func (k StepKind) Valid() bool {
	switch k {
	case StepInterpreting, StepSearchingCatalog, StepRanking, StepSubmitting:
		return true
	}
	return false
}

type StepStatus string

const (
	StepRunning   StepStatus = "RUNNING"
	StepSucceeded StepStatus = "SUCCEEDED"
	StepFailed    StepStatus = "FAILED"
)

// Step is an append-only observation. Losing every step row changes what the
// user sees while waiting but never changes the job outcome, so steps are
// deliberately not part of any transition guard.
type Step struct {
	ID          string     `json:"id"`
	JobID       string     `json:"jobId"`
	AttemptID   string     `json:"attemptId"`
	UserID      string     `json:"-"`
	Ordinal     int        `json:"ordinal"`
	Kind        StepKind   `json:"kind"`
	Status      StepStatus `json:"status"`
	ReasonCode  *string    `json:"reasonCode,omitempty"`
	StartedAt   time.Time  `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

func NewStep(
	id string,
	jobID string,
	attemptID string,
	userID string,
	ordinal int,
	kind StepKind,
	now time.Time,
) (Step, error) {
	if id == "" || jobID == "" || attemptID == "" || userID == "" ||
		ordinal <= 0 || !kind.Valid() || now.IsZero() {
		return Step{}, ErrInvalidAttempt
	}
	return Step{
		ID: id, JobID: jobID, AttemptID: attemptID, UserID: userID,
		Ordinal: ordinal, Kind: kind, Status: StepRunning, StartedAt: now,
	}, nil
}

func (s *Step) Succeed(now time.Time) {
	s.Status = StepSucceeded
	s.CompletedAt = &now
}

func (s *Step) Fail(reasonCode string, now time.Time) {
	s.Status = StepFailed
	if reasonCode != "" {
		s.ReasonCode = &reasonCode
	}
	s.CompletedAt = &now
}
