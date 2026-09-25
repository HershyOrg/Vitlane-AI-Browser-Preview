package app

import "context"

type threadExecutionKey struct{}
type ThreadExecution struct {
	ThreadID, ActionID, LegacyJobID string
	JobID, AttemptID                string
	AllowFeedbackCriteria           bool
}

// Only server orchestration supplies this context; no client header can grant it.
func WithThreadExecution(ctx context.Context, threadID, actionID string) context.Context {
	scope := ThreadExecutionFrom(ctx)
	if scope.ThreadID != threadID || scope.ActionID != actionID {
		scope = ThreadExecution{}
	}
	scope.ThreadID, scope.ActionID = threadID, actionID
	return context.WithValue(ctx, threadExecutionKey{}, scope)
}
func ThreadExecutionFrom(ctx context.Context) ThreadExecution {
	v, _ := ctx.Value(threadExecutionKey{}).(ThreadExecution)
	return v
}

// Legacy in-flight jobs may finish after migration; their own commits stay fenced.
func WithLegacyJobExecution(ctx context.Context, jobID string) context.Context {
	return context.WithValue(ctx, threadExecutionKey{}, ThreadExecution{LegacyJobID: jobID})
}

// The Job repository derives this permission from the persisted explicit primitive origin.
func WithThreadFeedbackCriteria(ctx context.Context) context.Context {
	scope := ThreadExecutionFrom(ctx)
	scope.AllowFeedbackCriteria = true
	return context.WithValue(ctx, threadExecutionKey{}, scope)
}

func WithActionAttempt(ctx context.Context, job, attempt string) context.Context {
	v := ThreadExecutionFrom(ctx)
	v.JobID = job
	v.AttemptID = attempt
	return context.WithValue(ctx, threadExecutionKey{}, v)
}
