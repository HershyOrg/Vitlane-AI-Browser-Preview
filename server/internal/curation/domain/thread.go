package domain

import (
	"encoding/json"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"time"
)

const ThreadSchema = "vitlane.curation-thread.v2"

type ControlMode struct {
	Mode    string `json:"mode"`
	Version int64  `json:"version"`
}
type ActionDecision struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Result      string   `json:"result"`
	TargetID    string   `json:"targetId,omitempty"`
	TargetLabel string   `json:"targetLabel,omitempty"`
	Source      string   `json:"source"`
	Evidence    string   `json:"evidence,omitempty"`
	ReasonCode  string   `json:"reasonCode"`
	ActionIDs   []string `json:"actionIds"`
}
type ActionEffect struct {
	Kind        string          `json:"kind"`
	TargetID    string          `json:"targetId,omitempty"`
	TargetLabel string          `json:"targetLabel,omitempty"`
	Before      json.RawMessage `json:"before,omitempty"`
	After       json.RawMessage `json:"after,omitempty"`
	Count       int             `json:"count,omitempty"`
}
type ActionJobResult struct {
	AttemptID  string         `json:"attemptId,omitempty"`
	JobID      string         `json:"jobId"`
	ActionID   string         `json:"actionId"`
	Kind       string         `json:"kind"`
	TargetID   string         `json:"targetId,omitempty"`
	Status     string         `json:"status"`
	Effects    []ActionEffect `json:"effects"`
	ReasonCode string         `json:"reasonCode,omitempty"`
	// Facts is the Round outcome of a completed research Job.
	Facts *ResearchFacts `json:"facts,omitempty"`
}
type ThreadOption struct {
	ID        string           `json:"id"`
	Label     string           `json:"label"`
	TargetID  string           `json:"targetId,omitempty"`
	Actions   []CurationAction `json:"actions,omitempty"`
	Decisions []ActionDecision `json:"decisions"`
}
type ThreadQuestion struct {
	ID      string         `json:"id"`
	Prompt  string         `json:"prompt"`
	Options []ThreadOption `json:"options"`
}
type ThreadAnswer struct {
	Revision   int64  `json:"revision"`
	QuestionID string `json:"questionId"`
	OptionID   string `json:"optionId,omitempty"`
	Text       string `json:"text,omitempty"`
}
type CurationThread struct {
	ExpectedConversationVersion *int64            `json:"expectedConversationVersion,omitempty"`
	RetryOfThreadID             string            `json:"retryOfThreadId,omitempty"`
	RetryOfJobID                string            `json:"retryOfJobId,omitempty"`
	SchemaVersion               string            `json:"schemaVersion"`
	TargetLabels                map[string]string `json:"targetLabels"`
	ID                          string            `json:"id"`
	UserID                      string            `json:"-"`
	CurationID                  string            `json:"curationId"`
	PlanID                      string            `json:"-"`
	AuthSessionID               string            `json:"-"`
	Mode                        string            `json:"mode"`
	Origin                      string            `json:"origin"`
	Request                     string            `json:"request"`
	RequestHash                 string            `json:"-"`
	ExpectedCurationVersion     int64             `json:"expectedCurationVersion"`
	Revision                    int64             `json:"revision"`
	Status                      string            `json:"status"`
	Actions                     []CurationAction  `json:"actions"`
	ReasonCode                  string            `json:"reasonCode,omitempty"`
	CreatedAt                   time.Time         `json:"createdAt"`
	UpdatedAt                   time.Time         `json:"updatedAt"`
}

func (t CurationThread) Active() bool {
	return t.Status == "INTERPRETING" || t.Status == "WAITING_SELECTION" || t.Status == "RUNNING"
}
func (t *CurationThread) CurrentAction() *CurationAction {
	for i := range t.Actions {
		if !t.Actions[i].Terminal() {
			return &t.Actions[i]
		}
	}
	return nil
}
func (t *CurationThread) Cancel() {
	t.Status = "CANCELLED"
	t.Revision++
	for i := range t.Actions {
		a := &t.Actions[i]
		if a.Status == "PENDING" {
			a.Status = "SKIPPED"
		} else if !a.Terminal() {
			a.Status = "CANCELLED"
			a.Question = nil
		}
	}
}
func (t *CurationThread) RefreshStatus() {
	if !t.Active() {
		return
	}
	for i := range t.Actions {
		a := &t.Actions[i]
		if a.Status == "FAILED" || a.Status == "CANCELLED" {
			t.Status = a.Status
			t.ReasonCode = a.ReasonCode
			for j := i + 1; j < len(t.Actions); j++ {
				if !t.Actions[j].Terminal() {
					t.Actions[j].Status = "SKIPPED"
				}
			}
			return
		}
		if a.Terminal() {
			continue
		}
		t.Status = "RUNNING"
		if a.Status == "WAITING_SELECTION" {
			t.Status = "WAITING_SELECTION"
		} else if a.Type == CurationActionAutoStart {
			t.Status = "INTERPRETING"
		}
		return
	}
	t.Status = "SUCCEEDED"
}

type ActionPlan struct {
	Actions   []CurationAction
	Decisions []ActionDecision
	Question  *ThreadQuestion
}

func (t ActionPlan) ValidatePlan() error {
	invalid := func() error { return fault.New(fault.ProviderRejected, "AUTO_PLAN_INVALID", false) }
	if len(t.Actions) > 24 || len(t.Decisions) > 64 {
		return invalid()
	}
	if t.Question != nil {
		if len(t.Actions) != 0 || len(t.Question.Options) < 2 || len(t.Question.Options) > 3 || t.Question.Prompt == "" {
			return invalid()
		}
		for _, option := range t.Question.Options {
			if option.Label == "" {
				return invalid()
			}
			if err := (ActionPlan{Actions: option.Actions, Decisions: option.Decisions}).ValidatePlan(); err != nil {
				return err
			}
		}
	}
	for _, s := range t.Actions {
		switch s.Type {
		case "BUDGET_CHANGE":
			if s.Budget == nil {
				return invalid()
			}
		case "CRITERIA_CHANGE":
			if s.Criteria == nil || s.Criteria.Validate() != nil || s.TargetID == "" {
				return invalid()
			}
		case "TARGET_RESEARCH_AGAIN":
			if s.TargetID == "" {
				return invalid()
			}
		case "CURATION_ADD_TARGETS", "PLANNING_ADD_TARGETS":
			if s.Instruction == "" {
				return invalid()
			}
		case CurationActionResponse:
			// A planned RESPONSE is the answer to a question and runs alone.
			if (s.Instruction != ResponseKindAnswer && s.Instruction != "COMBINATION") || len(t.Actions) != 1 {
				return invalid()
			}
		default:
			return invalid()
		}
	}
	for _, d := range t.Decisions {
		if d.Kind != "ACTION" && d.Kind != "TARGET" && d.Kind != "CONDITIONS" && d.Kind != "BUDGET" {
			return invalid()
		}
	}
	if t.Question == nil && len(t.Actions) == 0 {
		return invalid()
	}
	return nil
}
