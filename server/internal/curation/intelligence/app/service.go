package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

// RegisteredProvider pairs a provider with how much work may run against it at
// once. Concurrency is per provider because their limits differ in kind: a
// managed API has a rate limit, a paired device has one user at a keyboard.
type RegisteredProvider struct {
	Provider    Provider
	Concurrency int
}

type ProviderRegistry struct {
	providers map[intelligencedomain.ProviderKind]Provider
	slots     map[intelligencedomain.ProviderKind]chan struct{}
}

func NewProviderRegistry(
	entries ...RegisteredProvider,
) (ProviderRegistry, error) {
	registry := ProviderRegistry{
		providers: map[intelligencedomain.ProviderKind]Provider{},
		slots:     map[intelligencedomain.ProviderKind]chan struct{}{},
	}
	for _, entry := range entries {
		if entry.Provider == nil || entry.Concurrency <= 0 {
			return ProviderRegistry{}, fmt.Errorf(
				"intelligence provider registration is incomplete",
			)
		}
		kind := entry.Provider.Kind()
		if _, exists := registry.providers[kind]; exists {
			return ProviderRegistry{}, fmt.Errorf(
				"duplicate intelligence provider %q", kind,
			)
		}
		registry.providers[kind] = entry.Provider
		registry.slots[kind] = make(chan struct{}, entry.Concurrency)
	}
	return registry, nil
}

func (r ProviderRegistry) Lookup(
	kind intelligencedomain.ProviderKind,
) (Provider, bool) {
	provider, ok := r.providers[kind]
	return provider, ok
}

func (r ProviderRegistry) Kinds() []intelligencedomain.ProviderKind {
	kinds := make([]intelligencedomain.ProviderKind, 0, len(r.providers))
	for kind := range r.providers {
		kinds = append(kinds, kind)
	}
	return kinds
}

// AcquireAvailable takes up to limit slots without blocking and returns one
// release per slot taken. The dispatcher must hold the slots before it claims,
// not after: a claim sized from a free-slot reading can exceed what is actually
// free by the time the work starts, and the excess then sits with its attempt
// deadline already running while it waits. Holding first makes the worker the
// slot, so the count cannot drift.
//
// The caller owns every returned release and must call each exactly once,
// including for slots it decides not to use.
func (r ProviderRegistry) AcquireAvailable(
	kind intelligencedomain.ProviderKind,
	limit int,
) []func() {
	slots, ok := r.slots[kind]
	if !ok || limit <= 0 {
		return nil
	}
	releases := make([]func(), 0, limit)
	for len(releases) < limit {
		select {
		case slots <- struct{}{}:
			releases = append(releases, func() { <-slots })
		default:
			return releases
		}
	}
	return releases
}

// Acquire takes one provider slot, waiting at most wait for one to free up.
// A model call holds a slot only for its own duration: catalog collection and
// the finalize transaction never occupy the provider's concurrency. When the
// wait elapses the caller gets a RATE_LIMITED fault with a short RetryAfter so
// the job is deferred instead of failed.
func (r ProviderRegistry) Acquire(
	ctx context.Context,
	kind intelligencedomain.ProviderKind,
	wait time.Duration,
) (func(), error) {
	slots, ok := r.slots[kind]
	if !ok {
		return nil, fault.New(
			fault.ProviderUnavailable,
			intelligencedomain.ReasonProviderUnavailable, true,
		)
	}
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	default:
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, fault.Wrap(
			ctx.Err(), fault.DeadlineExceeded,
			intelligencedomain.ReasonDeadlineExceeded, true,
		)
	case <-timer.C:
		busy := fault.New(
			fault.RateLimited, intelligencedomain.ReasonModelSlotBusy, true,
		)
		busy.RetryAfter = 5 * time.Second
		return nil, busy
	}
}

// Sweepers exposes providers with internal recovery work of their own, such as
// conservatively classifying reservations a crashed process left held.
func (r ProviderRegistry) Sweepers() []Sweeper {
	sweepers := make([]Sweeper, 0, len(r.providers))
	for _, provider := range r.providers {
		if sweeper, ok := provider.(Sweeper); ok {
			sweepers = append(sweepers, sweeper)
		}
	}
	return sweepers
}

type ActionInterpretationRunner interface {
	RunActionInterpretation(context.Context, string, string, string, string, string, int64) error
}

func (s *Service) SetActionInterpreter(r ActionInterpretationRunner) { s.actionInterpreter = r }

type Service struct {
	actionInterpreter ActionInterpretationRunner

	threadContinuations bool
	repository          Repository
	products            ProductPort
	providers           ProviderRegistry
	transactor          sharedapp.Transactor
	clock               sharedapp.Clock
	ids                 sharedapp.IDGenerator
	logger              *slog.Logger
	deadline            time.Duration
	finalizer           runtimepolicy.Finalizer
	claimPolicy         ClaimPolicy
	modelSlotWait       time.Duration
	evaluation          EvaluationPolicy
}

func NewService(
	repository Repository,
	products ProductPort,
	providers ProviderRegistry,
	transactor sharedapp.Transactor,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
	logger *slog.Logger,
	finalizer runtimepolicy.Finalizer,
) (*Service, error) {
	if repository == nil || products == nil || transactor == nil ||
		clock == nil || ids == nil || len(providers.providers) == 0 {
		return nil, fmt.Errorf("intelligence service dependencies are incomplete")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repository: repository, products: products,
		providers: providers, transactor: transactor,
		clock: clock, ids: ids, logger: logger,
		finalizer: finalizer,
		// Bounded so a provider that never answers cannot hold a job open. The
		// reconciler closes anything still running past this.
		deadline:      5 * time.Minute,
		claimPolicy:   DefaultClaimPolicy(),
		modelSlotWait: 30 * time.Second,
		evaluation:    DefaultEvaluationPolicy(),
	}, nil
}

// ConfigureAdmission sets the claim caps and how long a model call waits for
// a provider slot before the job is deferred.
func (s *Service) ConfigureAdmission(policy ClaimPolicy, modelSlotWait time.Duration) error {
	if !policy.Valid() || modelSlotWait <= 0 {
		return fmt.Errorf("intelligence admission policy is invalid")
	}
	s.claimPolicy = policy
	s.modelSlotWait = modelSlotWait
	return nil
}

// ClaimPolicy is the execution admission currently applied at claim time.
func (s *Service) ClaimPolicy() ClaimPolicy {
	return s.claimPolicy
}

type CreateJobInput struct {
	UserID           string
	CurationID       string
	CurationActionID string
	PlanID           string
	Target           intelligencedomain.JobTarget
	Provider         intelligencedomain.ProviderKind
	ModelKey         string
}

// JobRef is what a product command returns to the browser. It deliberately
// carries no execution material: the browser polls the workspace projection for
// progress rather than holding anything that could authorize work.
type JobRef struct {
	JobID  string `json:"jobId"`
	Replay bool   `json:"replay"`
}

// CreateJob joins the caller's ambient product transaction, so the product row
// and its job are committed together or not at all. It is idempotent per
// target: a replayed user command resolves to the existing job.
func (s *Service) CreateJob(
	ctx context.Context,
	input CreateJobInput,
) (JobRef, error) {
	existing, found, err := s.repository.FindJobByTarget(
		ctx, input.UserID, input.Target,
	)
	if err != nil {
		return JobRef{}, err
	}
	if found {
		return JobRef{JobID: existing.ID, Replay: true}, nil
	}
	job, err := intelligencedomain.NewJob(intelligencedomain.NewJobInput{
		ID: s.ids.NewID(), UserID: input.UserID,
		CurationID: input.CurationID, CurationActionID: input.CurationActionID,
		PlanID: input.PlanID, Target: input.Target,
		Provider: input.Provider, ModelKey: input.ModelKey,
		Now: s.clock.Now(),
	})
	if err != nil {
		return JobRef{}, err
	}
	if err := s.repository.InsertJob(ctx, job); err != nil {
		return JobRef{}, err
	}
	return JobRef{JobID: job.ID}, nil
}

// CancelJobForTarget closes the job behind a product row the user cancelled. A
// job that already finished is left alone: its result is the user's, and
// rewriting it as cancelled would lose that.
func (s *Service) CancelJobForTarget(
	ctx context.Context,
	userID string,
	target intelligencedomain.JobTarget,
) error {
	job, found, err := s.repository.FindJobByTarget(ctx, userID, target)
	if err != nil || !found {
		return err
	}
	if job.Status.Terminal() {
		return nil
	}
	return s.repository.CloseJob(
		ctx, job.ID, intelligencedomain.JobCancelled, "", false, s.clock.Now(),
	)
}

// RetryJob reopens a failed job on the user's request. Only a failure the
// Server classified as retryable is reopened, and the attempt ceiling still
// applies, so a user cannot spend their allowance on a failure that repeats.
func (s *Service) RetryJob(
	ctx context.Context,
	userID string,
	jobID string,
) (JobRef, error) {
	job, err := s.repository.GetJob(ctx, userID, jobID)
	if err != nil {
		return JobRef{}, err
	}
	if job.Status != intelligencedomain.JobFailed || !job.Retryable {
		return JobRef{}, intelligencedomain.ErrJobClosed
	}
	if job.ExecutedAttempts() >= intelligencedomain.MaximumAttempts {
		return JobRef{}, intelligencedomain.ErrRetryExhausted
	}
	if err := s.repository.ReopenJob(ctx, job.ID, nil, s.clock.Now()); err != nil {
		return JobRef{}, err
	}
	return JobRef{JobID: job.ID}, nil
}

func (s *Service) ListCurationJobs(
	ctx context.Context,
	userID string,
	curationID string,
) ([]JobProgress, error) {
	return s.repository.ListCurationJobs(ctx, userID, curationID)
}

// ClaimDue opens attempts for the pending jobs the claim policy admits. The
// claim is a single transaction per batch, so two workers racing the same row
// cannot both execute it. Research rounds are capped per user, per curation
// and globally; jobs waiting out a not-before time stay pending.
func (s *Service) ClaimDue(
	ctx context.Context,
	kind intelligencedomain.ProviderKind,
) ([]ClaimedJob, error) {
	provider, ok := s.providers.Lookup(kind)
	if !ok {
		return nil, nil
	}
	// A provider that already knows it cannot serve the user — an exhausted
	// quota, no paired device — is asked before the claim, so the job stays
	// PENDING instead of burning an attempt.
	if err := provider.Available(ctx, ""); err != nil {
		if code := fault.CodeOf(err); code == fault.ProviderUnavailable ||
			code == fault.QuotaExceeded {
			return nil, nil
		}
		return nil, err
	}
	return s.repository.ClaimPending(
		ctx, kind, s.claimPolicy, s.deadline, s.clock.Now(),
	)
}

// Execute runs one claimed job and closes its attempt. Any error after the
// claim becomes a closed attempt, so a crash in the middle never leaves a job
// running until its deadline.
//
// Execution admission happened at claim time; provider slots are taken per
// model call inside the pipeline, so a job waiting on a catalog never holds
// the model's concurrency.
func (s *Service) Execute(ctx context.Context, claimed ClaimedJob) error {
	pipelineErr := s.run(ctx, claimed)
	// The attempt deadline owns new work, not the short durable write that
	// records the outcome of work already performed. A lifecycle-owned bounded
	// context prevents an expired attempt or disconnected caller from leaving
	// the attempt RUNNING. Process shutdown still cancels this write.
	finalizeContext, cancelFinalize := s.finalizer.Context()
	defer cancelFinalize()
	now := s.clock.Now()
	attempt := claimed.Attempt
	if pipelineErr == nil {
		attempt.Status = intelligencedomain.AttemptSucceeded
		attempt.CompletedAt = &now
		if err := s.repository.CloseAttempt(finalizeContext, attempt); err != nil {
			return fmt.Errorf("close succeeded attempt: %w", err)
		}
		return s.repository.CloseJob(
			finalizeContext, claimed.Job.ID, intelligencedomain.JobSucceeded, "", false, now,
		)
	}

	reasonCode, retryable := classify(pipelineErr)
	// A resource wait is not a failure. The attempt closes DEFERRED, the job
	// goes back to PENDING with a not-before time, and none of the retry
	// ceilings move. Only a job that keeps waiting past MaximumDeferrals is
	// reported, and even then as retryable.
	if wait, deferred := deferralOf(pipelineErr); deferred {
		if claimed.Job.DeferCount < intelligencedomain.MaximumDeferrals {
			if err := s.repository.DeferJob(
				finalizeContext, claimed.Job, attempt, now.Add(wait), reasonCode, now,
			); err != nil {
				return fmt.Errorf("defer job: %w", err)
			}
			s.logger.InfoContext(ctx, "intelligence job deferred",
				"event", "intelligence.job_deferred",
				"job_id", claimed.Job.ID, "reason", reasonCode,
				"wait", wait.String(), "deferrals", claimed.Job.DeferCount+1)
			return nil
		}
		reasonCode, retryable = intelligencedomain.ReasonResourceWaitExhausted, true
	}
	// An unknown external effect is parked rather than closed: until a re-query
	// resolves whether the provider actually ran, dispatching again could
	// duplicate work the user already paid for.
	if fault.CodeOf(pipelineErr) == fault.ExternalEffectUnknown {
		attempt.Status = intelligencedomain.AttemptEffectUnknown
		attempt.FailureCode = reasonCode
		attempt.Retryable = false
		if err := s.repository.CloseAttempt(finalizeContext, attempt); err != nil {
			return fmt.Errorf("park unknown attempt: %w", err)
		}
		s.logger.WarnContext(ctx, "provider effect is unknown",
			"event", "intelligence.effect_unknown",
			"job_id", claimed.Job.ID, "reason", reasonCode)
		return nil
	}

	attempt.Status = intelligencedomain.AttemptFailed
	attempt.FailureCode = reasonCode
	attempt.Retryable = retryable
	attempt.CompletedAt = &now
	// The reason code stored on the job is a closed enum the Web renders. The
	// operator log also carries the underlying error, because every
	// unclassified failure otherwise collapses into one code and becomes
	// undiagnosable.
	s.logger.WarnContext(ctx, "intelligence job failed",
		"event", "intelligence.job_failed",
		"job_id", claimed.Job.ID, "target_kind", claimed.Job.Target.Kind,
		"reason", reasonCode, "retryable", retryable,
		"error", pipelineErr.Error())
	// A retryable failure has nobody to press retry while the user waits. Most
	// are a malformed provider response that a second call resolves, so the
	// Server reopens the job itself after a short backoff. The attempt bound
	// counts executed attempts only, so a deferred wait never spends it.
	executed := claimed.Job.ExecutedAttempts()
	if retryable && executed < intelligencedomain.MaximumAutomaticAttempts {
		notBefore := now.Add(intelligencedomain.RetryBackoff(executed))
		retry := func() error {
			if repository, ok := s.repository.(interface {
				RetryFailedAttempt(context.Context, intelligencedomain.Job, intelligencedomain.Attempt, *time.Time, time.Time) error
			}); ok {
				return repository.RetryFailedAttempt(finalizeContext, claimed.Job, attempt, &notBefore, now)
			}
			if err := s.repository.CloseAttempt(finalizeContext, attempt); err != nil {
				return err
			}
			if err := s.repository.CloseJob(finalizeContext, claimed.Job.ID, intelligencedomain.JobFailed, reasonCode, true, now); err != nil {
				return err
			}
			return s.repository.ReopenJob(finalizeContext, claimed.Job.ID, &notBefore, now)
		}
		if err := retry(); err != nil {
			s.logger.WarnContext(ctx, "auto retry failed", "event", "intelligence.auto_retry_failed", "job_id", claimed.Job.ID, "error", err.Error())
			return nil
		}
		s.logger.InfoContext(ctx, "reopened job",
			"event", "intelligence.auto_retry",
			"job_id", claimed.Job.ID, "attempt", attempt.Ordinal,
			"reason", reasonCode)
		return nil
	}

	// The last attempt and its Research projection close in one local DB
	// transaction. If that write fails, the RUNNING attempt remains eligible
	// for the existing deadline reconciler instead of leaving a terminal Job
	// beside an open Round forever.
	if err := s.transactor.WithinTransaction(
		finalizeContext,
		func(txContext context.Context) error {
			if err := s.repository.CloseAttempt(txContext, attempt); err != nil {
				return fmt.Errorf("close failed attempt: %w", err)
			}
			if err := s.repository.CloseJob(
				txContext, claimed.Job.ID, intelligencedomain.JobFailed,
				reasonCode, retryable, now,
			); err != nil {
				return fmt.Errorf("close failed job: %w", err)
			}
			if claimed.Job.Target.Kind == intelligencedomain.TargetResearchRound {
				if err := s.products.FailResearchTarget(
					txContext, claimed.Job.UserID, claimed.Job.Target.ID,
					reasonCode, retryable,
				); err != nil {
					return fmt.Errorf("close failed research target: %w", err)
				}
			}
			return nil
		},
	); err != nil {
		return err
	}
	return nil
}

// ExpireOverdueAttempts closes attempts whose provider never answered. A
// deadline is not evidence that the work failed, so the attempt is closed as
// DEADLINE_EXCEEDED and the job is reopened within its bound rather than being
// reported to the user as a rejection.
func (s *Service) ExpireOverdueAttempts(
	ctx context.Context,
	limit int,
) (int, error) {
	now := s.clock.Now()
	overdue, err := s.repository.OverdueAttempts(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	expired := 0
	for _, attempt := range overdue {
		attempt.Status = intelligencedomain.AttemptFailed
		attempt.FailureCode = intelligencedomain.ReasonDeadlineExceeded
		attempt.Retryable = true
		attempt.CompletedAt = &now
		job, jobErr := s.repository.GetJob(ctx, attempt.UserID, attempt.JobID)
		executed := attempt.Ordinal
		if jobErr == nil {
			executed = max(0, attempt.Ordinal-job.DeferCount)
		}
		if executed < intelligencedomain.MaximumAutomaticAttempts {
			if repository, ok := s.repository.(interface {
				RetryFailedAttempt(context.Context, intelligencedomain.Job, intelligencedomain.Attempt, *time.Time, time.Time) error
			}); ok {
				e := jobErr
				if e == nil {
					notBefore := now.Add(intelligencedomain.RetryBackoff(executed))
					e = repository.RetryFailedAttempt(ctx, job, attempt, &notBefore, now)
				}
				if e == nil {
					expired++
				} else {
					s.logger.WarnContext(ctx, "expire retry failed", "event", "intelligence.expire_reopen_failed", "job_id", attempt.JobID, "error", e.Error())
				}
				continue
			}
		}
		if err := s.repository.CloseAttempt(ctx, attempt); err != nil {
			s.logger.WarnContext(ctx, "expire attempt failed",
				"event", "intelligence.expire_failed",
				"attempt_id", attempt.ID, "error", err.Error())
			continue
		}
		if err := s.repository.CloseJob(
			ctx, attempt.JobID, intelligencedomain.JobFailed,
			intelligencedomain.ReasonDeadlineExceeded, true, now,
		); err != nil {
			s.logger.WarnContext(ctx, "expire job failed",
				"event", "intelligence.expire_job_failed",
				"job_id", attempt.JobID, "error", err.Error())
			continue
		}
		if executed < intelligencedomain.MaximumAutomaticAttempts {
			notBefore := now.Add(intelligencedomain.RetryBackoff(executed))
			if err := s.repository.ReopenJob(
				ctx, attempt.JobID, &notBefore, now,
			); err != nil && !errors.Is(
				err, intelligencedomain.ErrJobNotFound,
			) {
				s.logger.WarnContext(ctx, "expire reopen failed",
					"event", "intelligence.expire_reopen_failed",
					"job_id", attempt.JobID, "error", err.Error())
			}
		}
		expired++
	}
	return expired, nil
}

// orphanGrace is how long a RUNNING job may sit without a RUNNING attempt
// before the reconciler treats it as abandoned. The success path closes the
// attempt and the job in two short writes, so a couple of minutes is far
// beyond any legitimate gap.
const orphanGrace = 2 * time.Minute

// ReconcileOrphanJobs closes RUNNING jobs that have no RUNNING attempt. A job
// parked on an EFFECT_UNKNOWN attempt, or one whose closing write failed, would
// otherwise keep its curation "active" until the user cancelled it. The job
// closes as a retryable failure with the attempt's own reason; the budget
// reservation behind an unknown call stays UNKNOWN in the ledger regardless.
func (s *Service) ReconcileOrphanJobs(
	ctx context.Context,
	limit int,
) (int, error) {
	now := s.clock.Now()
	orphans, err := s.repository.OrphanRunningJobs(ctx, now.Add(-orphanGrace), limit)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, orphan := range orphans {
		reason := orphan.FailureCode
		if reason == "" {
			reason = intelligencedomain.ReasonEffectUnknown
		}
		job := orphan.Job
		err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
			if err := s.repository.CloseJob(
				txContext, job.ID, intelligencedomain.JobFailed, reason, true, now,
			); err != nil {
				return err
			}
			if job.Target.Kind == intelligencedomain.TargetResearchRound {
				return s.products.FailResearchTarget(
					txContext, job.UserID, job.Target.ID, reason, true,
				)
			}
			return nil
		})
		if err != nil {
			s.logger.WarnContext(ctx, "orphan job close failed",
				"event", "intelligence.orphan_close_failed",
				"job_id", job.ID, "error", err.Error())
			continue
		}
		s.logger.WarnContext(ctx, "orphan intelligence job closed",
			"event", "intelligence.orphan_job_closed",
			"job_id", job.ID, "latest_attempt", orphan.LatestAttemptStatus,
			"reason", reason)
		closed++
	}
	return closed, nil
}

// ActiveActionCount is how many CurationActions the user has still working.
func (s *Service) ActiveActionCount(
	ctx context.Context,
	userID string,
) (int, error) {
	return s.repository.CountActiveActions(ctx, userID)
}

// LockActionAdmission serializes the per-user limit decision in the ambient
// product transaction. The lock is deliberately separate from the count: the
// product owns the transaction that creates the action and its first job.
func (s *Service) LockActionAdmission(
	ctx context.Context,
	userID string,
) error {
	return s.repository.LockActionAdmission(ctx, userID)
}

// PlanHasActiveWork answers whether this plan is mid-action.
func (s *Service) PlanHasActiveWork(
	ctx context.Context,
	userID string,
	planID string,
) (bool, error) {
	return s.repository.PlanHasActiveJobs(ctx, userID, planID)
}

// CurationHasActiveWork is the foreground gate used by Curation commands.
// Product task/round rows intentionally stay open after a failed attempt so a
// user can retry; only PENDING/RUNNING Jobs mean execution is actually active.
func (s *Service) CurationHasActiveWork(
	ctx context.Context,
	userID string,
	curationID string,
) (bool, error) {
	return s.repository.CurationHasActiveJobs(ctx, userID, curationID)
}

// CancelAction stops the work a user's action started and closes the product
// rows it was holding, which is what frees the plan for the next action.
//
// There is no rollback. Whatever the action already produced — targets from an
// accepted proposal, candidates from a finished round — is the user's and
// stays. Cancelling means the rest will not be attempted, not that the work so
// far is undone.
func (s *Service) CancelAction(
	ctx context.Context,
	userID string,
	curationActionID string,
) (int, error) {
	var cancelled []CancelledTarget
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		var cancelErr error
		cancelled, cancelErr = s.repository.CancelAction(
			txContext, userID, curationActionID, s.clock.Now(),
		)
		if cancelErr != nil {
			return cancelErr
		}
		for _, item := range cancelled {
			if item.Target.ID == "" {
				continue
			}
			if cancelErr := s.products.CancelTarget(
				txContext, userID, item.Target,
			); cancelErr != nil {
				return fmt.Errorf(
					"close target for cancelled job %s: %w", item.JobID, cancelErr,
				)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.logger.InfoContext(
		ctx, "intelligence action cancelled",
		"event", "intelligence.action_cancelled",
		"action_id", curationActionID,
		"cancelled_jobs", len(cancelled),
	)
	return len(cancelled), nil
}

// EnableThreadContinuations moves follow-up admission to the durable curation thread.
func (s *Service) EnableThreadContinuations() { s.threadContinuations = true }

func (s *Service) hasThread(ctx context.Context, jobID string) bool {
	if repo, ok := s.repository.(interface {
		HasThread(context.Context, string) (bool, error)
	}); ok {
		yes, err := repo.HasThread(ctx, jobID)
		return err == nil && yes
	}
	return false
}

func (s *Service) criteriaAlreadyDecided(ctx context.Context, jobID string) bool {
	if repo, ok := s.repository.(interface {
		ThreadCriteriaDecided(context.Context, string) (bool, error)
	}); ok {
		v, err := repo.ThreadCriteriaDecided(ctx, jobID)
		return err == nil && v
	}
	return false
}

// deferralOf reports whether a pipeline failure is a resource wait: a
// RATE_LIMITED fault from a catalog admission, a provider cooldown or a busy
// model slot. The wait is the fault's RetryAfter, bounded on both sides so a
// missing value still spaces retries and a huge value cannot park the job.
func deferralOf(err error) (time.Duration, bool) {
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.RateLimited {
		return 0, false
	}
	wait := failure.RetryAfter
	if wait <= 0 {
		wait = 5 * time.Second
	}
	if wait > intelligencedomain.MaximumDeferralWait {
		wait = intelligencedomain.MaximumDeferralWait
	}
	return wait, true
}
