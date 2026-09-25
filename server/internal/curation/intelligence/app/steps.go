package app

import (
	"context"

	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
)

// stepTracker writes the progress observations the Web shows while a job runs.
//
// Every write is best effort. A step row is a nicety for the user; the job
// outcome is decided entirely by the attempt. Failing the pipeline because a
// progress label could not be saved would trade a real result for a cosmetic
// one, so errors here are logged and swallowed.
type stepTracker struct {
	service *Service
	job     intelligencedomain.Job
	attempt intelligencedomain.Attempt
	ordinal int
	current *intelligencedomain.Step
}

func (t *stepTracker) begin(
	ctx context.Context,
	kind intelligencedomain.StepKind,
) {
	if t.current != nil {
		t.succeed(ctx)
	}
	t.ordinal++
	step, err := intelligencedomain.NewStep(
		t.service.ids.NewID(), t.job.ID, t.attempt.ID, t.job.UserID,
		t.ordinal, kind, t.service.clock.Now(),
	)
	if err != nil {
		t.service.logger.WarnContext(ctx, "step rejected",
			"event", "intelligence.step_invalid", "kind", kind)
		return
	}
	if err := t.service.repository.InsertStep(ctx, step); err != nil {
		t.service.logger.WarnContext(ctx, "step not recorded",
			"event", "intelligence.step_write_failed", "kind", kind)
		return
	}
	t.current = &step
}

func (t *stepTracker) succeed(ctx context.Context) {
	if t.current == nil {
		return
	}
	t.current.Succeed(t.service.clock.Now())
	t.flush(ctx)
}

func (t *stepTracker) fail(ctx context.Context, reasonCode string) {
	if t.current == nil {
		return
	}
	t.current.Fail(reasonCode, t.service.clock.Now())
	t.flush(ctx)
}

func (t *stepTracker) flush(ctx context.Context) {
	if err := t.service.repository.UpdateStep(ctx, *t.current); err != nil {
		t.service.logger.WarnContext(ctx, "step not updated",
			"event", "intelligence.step_update_failed",
			"kind", t.current.Kind)
	}
	t.current = nil
}
