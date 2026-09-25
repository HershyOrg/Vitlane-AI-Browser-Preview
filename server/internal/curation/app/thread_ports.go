package app

import (
	"context"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
)

type ThreadRepository interface {
	LockThreadCuration(context.Context, string, string) error
	ReadControlMode(context.Context, string, string) (d.ControlMode, error)
	SaveControlMode(context.Context, string, string, d.ControlMode) (d.ControlMode, error)
	ReadThread(context.Context, string, string) (d.CurationThread, error)
	InsertThread(context.Context, d.CurationThread) error
	SaveThread(context.Context, d.CurationThread) error
	ListThreads(context.Context, string, string) (d.ControlMode, []d.CurationThread, error)
	PendingThreads(context.Context) ([]d.CurationThread, error)
	GuardThreadMutation(context.Context, string, string) error
	ActionJobs(context.Context, string, string) ([]d.ActionJobResult, error)
	// ThreadForAction resolves (curationID, threadID) for an Action that belongs
	// to a Thread; found is false for pre-Thread Actions.
	ThreadForAction(context.Context, string, string) (string, string, bool, error)
}
type ThreadTarget struct {
	ID             string                 `json:"id"`
	Title          string                 `json:"title"`
	Intent         string                 `json:"intent"`
	SessionID      string                 `json:"sessionId"`
	SessionVersion int64                  `json:"sessionVersion"`
	Researchable   bool                   `json:"researchable"`
	OrderIndex     int                    `json:"orderIndex"`
	Criteria       *d.TargetCriteriaSetV1 `json:"criteria"`
}
type ThreadContext struct {
	OriginalIntent string          `json:"originalIntent"`
	Phase          d.CurationPhase `json:"phase"`

	JobID         string            `json:"-"`
	AttemptID     string            `json:"-"`
	ActionID      string            `json:"-"`
	InputRevision int64             `json:"-"`
	Answers       []d.ThreadAnswer  `json:"answers"`
	ManualAction  *d.CurationAction `json:"manualAction,omitempty"`

	Thread   d.CurationThread `json:"thread"`
	Targets  []ThreadTarget   `json:"targets"`
	Budget   d.BudgetLedger   `json:"budget"`
	ModelKey string           `json:"-"`
	Locale   string           `json:"locale"`
}
type ThreadInterpretation = d.ActionPlan
type ThreadInterpreter interface {
	Interpret(context.Context, ThreadContext) (ThreadInterpretation, error)
}
type ThreadPrimitives interface {
	StartInterpretation(context.Context, d.CurationThread, d.CurationAction) error

	ResearchThreadTarget(context.Context, d.CurationThread, d.CurationAction, ThreadTarget) error
	StartThreadResearch(context.Context, d.CurationThread, string) error
	CancelThreadAction(context.Context, string, string) error
}

type controlModeReader interface {
	ReadControlMode(context.Context, string, string) (d.ControlMode, error)
}
