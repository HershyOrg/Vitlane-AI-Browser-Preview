// Package worker dispatches intelligence jobs.
//
// There is no queue infrastructure by design: at this scale a PostgreSQL poll
// with SKIP LOCKED gives the same delivery guarantees as a broker without a
// second durable system to operate, back up and reason about.
package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

const (
	// A three-second idle claim cadence removes continuous empty transactions
	// while keeping a newly submitted job comfortably inside the interactive
	// progress window. Running jobs are unaffected.
	dispatchInterval = 3 * time.Second
	reconcileTick    = 30 * time.Second
	sweepTick        = time.Minute
	reconcileLimit   = 100
	tickTimeout      = 10 * time.Second
)

type Worker struct {
	service  *intelligenceapp.Service
	registry intelligenceapp.ProviderRegistry
	logger   *slog.Logger
}

func New(
	service *intelligenceapp.Service,
	registry intelligenceapp.ProviderRegistry,
	logger *slog.Logger,
) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{service: service, registry: registry, logger: logger}
}

func (w *Worker) Run(ctx context.Context) error {
	dispatch := time.NewTicker(dispatchInterval)
	defer dispatch.Stop()
	reconcile := time.NewTicker(reconcileTick)
	defer reconcile.Stop()
	sweep := time.NewTicker(sweepTick)
	defer sweep.Stop()

	var running sync.WaitGroup
	defer running.Wait()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-dispatch.C:
			w.dispatch(ctx, &running)
		case <-reconcile.C:
			tickContext, cancel := runtimepolicy.WithTimeout(ctx, tickTimeout)
			if _, err := w.service.ExpireOverdueAttempts(
				tickContext, reconcileLimit,
			); err != nil {
				w.logger.WarnContext(tickContext, "expire sweep failed",
					"event", "intelligence.expire_sweep_failed",
					"error", err.Error())
			}
			if _, err := w.service.ReconcileOrphanJobs(
				tickContext, reconcileLimit,
			); err != nil {
				w.logger.WarnContext(tickContext, "orphan sweep failed",
					"event", "intelligence.orphan_sweep_failed",
					"error", err.Error())
			}
			cancel()
		case <-sweep.C:
			for _, sweeper := range w.registry.Sweepers() {
				tickContext, cancel := runtimepolicy.WithTimeout(ctx, tickTimeout)
				if err := sweeper.Sweep(tickContext, time.Now().UTC()); err != nil {
					w.logger.WarnContext(tickContext, "provider sweep failed",
						"event", "intelligence.provider_sweep_failed",
						"error", err.Error())
				}
				cancel()
			}
		}
	}
}

// dispatch claims by execution admission: the claim policy caps how many
// research rounds run per user, per curation and in total, and gives every
// other job kind its own lane. Provider slots are no longer taken here; a
// model call takes one for its own duration inside the pipeline, so a job
// collecting from catalogs never holds the model's concurrency.
func (w *Worker) dispatch(ctx context.Context, running *sync.WaitGroup) {
	for _, kind := range w.registry.Kinds() {
		claimContext, cancelClaim := runtimepolicy.WithTimeout(ctx, tickTimeout)
		claimed, err := w.service.ClaimDue(claimContext, kind)
		cancelClaim()
		if err != nil {
			w.logger.WarnContext(ctx, "claim failed",
				"event", "intelligence.claim_failed",
				"provider", kind, "error", err.Error())
			continue
		}
		for _, job := range claimed {
			running.Add(1)
			go func(job intelligenceapp.ClaimedJob) {
				defer running.Done()
				jobContext, cancel := runtimepolicy.WithDeadline(
					ctx, job.Attempt.DeadlineAt,
				)
				defer cancel()
				if err := w.service.Execute(jobContext, job); err != nil {
					w.logger.ErrorContext(jobContext, "execute failed",
						"event", "intelligence.execute_failed",
						"job_id", job.Job.ID, "error", err.Error())
				}
			}(job)
		}
	}
}
