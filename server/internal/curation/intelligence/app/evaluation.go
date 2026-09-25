package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// EvaluationPolicy decides how one Round's candidate evaluation is split into
// model calls. The 2026-09-17 measurement (research-assessment-batch-cost)
// showed call latency of about 7s + 2.1s per candidate, a fixed cost of
// roughly 1,130 prompt tokens per extra call, and no slowdown from running
// calls in parallel. Splitting therefore pays only when the batches actually
// run side by side: two parallel halves of a 50-candidate round finished in
// 60s instead of 112s at +0~4% cost, while a sequential split was slower than
// a single call. A round never splits into more batches than it holds model
// slots for.
type EvaluationPolicy struct {
	// BatchLimit is the largest batch evaluated in one call. Rounds with more
	// candidates split into the fewest balanced batches at or under it.
	BatchLimit int
	// Parallel caps how many batches run at once, and therefore how many
	// model slots one round may hold during evaluation.
	Parallel int
	// ExtraSlotWait is how long the round waits for each additional model
	// slot before evaluating in fewer batches instead.
	ExtraSlotWait time.Duration
	// BatchTimeout bounds one batch call. A forced single call over more than
	// BatchLimit candidates gets a proportionally longer bound.
	BatchTimeout time.Duration
}

func DefaultEvaluationPolicy() EvaluationPolicy {
	return EvaluationPolicy{BatchLimit: 25, Parallel: 2, ExtraSlotWait: 10 * time.Second, BatchTimeout: 120 * time.Second}
}

func (p EvaluationPolicy) Valid() bool {
	return p.BatchLimit >= 5 && p.BatchLimit <= 50 && p.Parallel >= 1 && p.Parallel <= 4 &&
		p.ExtraSlotWait > 0 && p.BatchTimeout >= 30*time.Second && p.BatchTimeout <= 10*time.Minute
}

// ConfigureEvaluation sets how candidate evaluation is batched.
func (s *Service) ConfigureEvaluation(policy EvaluationPolicy) error {
	if !policy.Valid() {
		return fmt.Errorf("evaluation policy is invalid")
	}
	s.evaluation = policy
	return nil
}

// balancedBatches splits count into parts whose sizes differ by at most one.
func balancedBatches(count, parts int) []int {
	if count <= 0 {
		return nil
	}
	parts = max(1, min(parts, count))
	base, remainder := count/parts, count%parts
	sizes := make([]int, parts)
	for index := range sizes {
		sizes[index] = base
		if index < remainder {
			sizes[index]++
		}
	}
	return sizes
}

// evaluationBatches is the number of batches the policy asks for: the fewest
// batches at or under BatchLimit, never more than may run in parallel.
func evaluationBatches(count int, policy EvaluationPolicy) int {
	if count <= policy.BatchLimit || policy.BatchLimit <= 0 {
		return 1
	}
	wanted := (count + policy.BatchLimit - 1) / policy.BatchLimit
	return max(1, min(wanted, policy.Parallel))
}

type evaluationBatchResult struct {
	ranked  []ResearchRankedCandidate
	dropped int
	err     error
	retried bool
}

// evaluateObservations runs the ranking call for one Round. With more
// candidates than the batch limit it takes one model slot per batch, up to
// the parallel cap, and evaluates the batches side by side; when a further
// slot is not free within ExtraSlotWait it evaluates in fewer batches rather
// than splitting sequentially. A failed batch in a multi-batch round is
// retried once and then leaves its products unevaluated; a single-batch round
// keeps the job-level retry semantics.
func (s *Service) evaluateObservations(
	ctx context.Context,
	claimed ClaimedJob,
	researchContext ResearchContext,
	observations []ResearchCandidateObservation,
	axisCount int,
) ([]ResearchRankedCandidate, error) {
	policy := s.evaluation
	if !policy.Valid() {
		policy = DefaultEvaluationPolicy()
	}
	wanted := evaluationBatches(len(observations), policy)
	release, err := s.providers.Acquire(ctx, claimed.Job.Provider, s.modelSlotWait)
	if err != nil {
		return nil, err
	}
	releases := []func(){release}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for len(releases) < wanted {
		extra, err := s.providers.Acquire(ctx, claimed.Job.Provider, policy.ExtraSlotWait)
		if err != nil {
			break
		}
		releases = append(releases, extra)
	}
	sizes := balancedBatches(len(observations), len(releases))
	batches := make([][]ResearchCandidateObservation, 0, len(sizes))
	offset := 0
	for _, size := range sizes {
		batches = append(batches, observations[offset:offset+size])
		offset += size
	}
	started := s.clock.Now()

	if len(batches) == 1 {
		timeout := policy.BatchTimeout * time.Duration((len(observations)+policy.BatchLimit-1)/policy.BatchLimit)
		result := s.rankBatch(ctx, claimed, researchContext, batches[0], axisCount, "", timeout, false)
		if result.err != nil {
			return nil, result.err
		}
		s.logEvaluation(ctx, claimed, sizes, 0, started, wanted)
		return result.ranked, nil
	}

	results := make([]evaluationBatchResult, len(batches))
	var group sync.WaitGroup
	for index, batch := range batches {
		group.Add(1)
		go func(index int, batch []ResearchCandidateObservation) {
			defer group.Done()
			results[index] = s.rankBatch(
				ctx, claimed, researchContext, batch, axisCount,
				fmt.Sprintf(":b%d", index+1), policy.BatchTimeout, true,
			)
		}(index, batch)
	}
	group.Wait()
	var ranked []ResearchRankedCandidate
	failures := 0
	var firstErr error
	for _, result := range results {
		if result.err != nil {
			failures++
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		ranked = append(ranked, result.ranked...)
	}
	s.logEvaluation(ctx, claimed, sizes, failures, started, wanted)
	if failures == len(results) {
		return nil, firstErr
	}
	return ranked, nil
}

// rankBatch runs one evaluation call over a batch, with an optional single
// retry for a retryable failure whose wait is short enough to be worth it.
func (s *Service) rankBatch(
	ctx context.Context,
	claimed ClaimedJob,
	researchContext ResearchContext,
	batch []ResearchCandidateObservation,
	axisCount int,
	keySuffix string,
	timeout time.Duration,
	retryOnce bool,
) evaluationBatchResult {
	call := func(suffix string) evaluationBatchResult {
		batchContext, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var payload CandidateRankingPayload
		if err := s.completeHeld(
			batchContext, claimed, suffix, researchSystemPrompt(rankingSystemPrompt, researchContext),
			rankingPrompt(researchContext, batch, len(batch)),
			SchemaCandidateRanking, CandidateRankingSchema(len(batch), axisCount), &payload,
			researchImages(researchContext, batch),
		); err != nil {
			return evaluationBatchResult{err: err}
		}
		ranked, dropped, err := acceptOfferedObservations(payload, batch, len(batch))
		if err != nil {
			// Nothing in the answer named an offered product: a provider
			// glitch, so the job's retry policy may try again.
			return evaluationBatchResult{err: fault.Wrap(
				err, fault.ProviderRejected,
				intelligencedomain.ReasonProviderResponse, true,
			)}
		}
		return evaluationBatchResult{ranked: ranked, dropped: dropped}
	}
	result := call(keySuffix)
	if result.err == nil || !retryOnce || !fault.Retryable(result.err) {
		return result
	}
	wait := 2 * time.Second
	if failure, ok := fault.As(result.err); ok && failure.RetryAfter > 0 {
		if failure.RetryAfter > 10*time.Second {
			return result
		}
		wait = failure.RetryAfter
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return result
	case <-timer.C:
	}
	again := call(keySuffix + ":retry")
	again.retried = true
	if again.err != nil {
		again.err = result.err
	}
	return again
}

func (s *Service) logEvaluation(ctx context.Context, claimed ClaimedJob, sizes []int, failures int, started time.Time, wanted int) {
	s.logger.InfoContext(ctx, "catalog ranking batches",
		"event", "intelligence.phase8_ranking_batches",
		"job_id", claimed.Job.ID, "batches", len(sizes), "wanted", wanted,
		"sizes", fmt.Sprint(sizes), "failed_batches", failures,
		"duration_ms", s.clock.Now().Sub(started).Milliseconds())
}
