package app

import (
	"context"
	"errors"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"strings"
	"time"
)

type ThreadService struct {
	curation    *Service
	repo        ThreadRepository
	interpreter ThreadInterpreter
	primitives  ThreadPrimitives
	// The reply is optional equipment (ADR-0086): without a responder a Thread
	// ends exactly as it did before, with its fixed sentences.
	responder       ThreadResponder
	responseSource  ThreadResponseSource
	combinationCart *CatalogCartServiceV2
}

func NewThreadService(c *Service, r ThreadRepository, i ThreadInterpreter, p ThreadPrimitives) *ThreadService {
	return &ThreadService{curation: c, repo: r, interpreter: i, primitives: p}
}

type SubmitThreadInput struct {
	SessionID                   string `json:"sessionId,omitempty"`
	ExpectedSessionVersion      *int64 `json:"expectedSessionVersion,omitempty"`
	ExpectedCriteriaVersion     *int64 `json:"expectedCriteriaVersion,omitempty"`
	ExpectedConversationVersion *int64 `json:"expectedConversationVersion,omitempty"`
	ID                          string `json:"clientRequestId"`
	Request                     string `json:"request"`
	ExpectedCurationVersion     int64  `json:"expectedCurationVersion"`
	// Manual routing is an explicit primitive choice, never inferred from text.
	Kind     string `json:"kind,omitempty"`
	TargetID string `json:"targetId,omitempty"`
	// KeepCriteria marks Request as search feedback the Server wrote for an
	// accepted proposal: research runs with it and the criteria stay as they
	// are, so no criteria interpretation runs first. Clients cannot set it.
	KeepCriteria bool `json:"-"`
}

func (s *ThreadService) Mode(ctx context.Context, user, id string) (d.ControlMode, error) {
	return s.repo.ReadControlMode(ctx, user, id)
}
func (s *ThreadService) ChangeMode(ctx context.Context, user, id string, m d.ControlMode) (d.ControlMode, error) {
	var out d.ControlMode
	err := s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		var e error
		out, e = s.repo.SaveControlMode(tx, user, id, m)
		return e
	})
	return out, err
}

// List returns the control mode with the Threads, so one read restores both.
func (s *ThreadService) List(ctx context.Context, user, id string) (d.ControlMode, []d.CurationThread, error) {
	return s.repo.ListThreads(ctx, user, id)
}
func (s *ThreadService) Submit(ctx context.Context, user, auth, id string, in SubmitThreadInput) (d.CurationThread, error) {
	var t d.CurationThread
	in.Request = strings.TrimSpace(in.Request)
	if !uuidPattern.MatchString(in.ID) || (in.Request == "" && in.Kind != "RESEARCH_AGAIN" && in.Kind != "COMBINATION") || len(in.Request) > 2000 || in.ExpectedCurationVersion < 1 {
		return t, fault.New(fault.InvalidInput, "CURATION_THREAD_INVALID", false)
	}
	hash, err := shareddomain.CanonicalJSONHash(struct {
		Curation string
		Input    SubmitThreadInput
	}{id, in})
	if err != nil {
		return t, err
	}
	err = s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if e := s.repo.LockThreadCuration(tx, user, id); e != nil {
			return e
		}
		old, e := s.repo.ReadThread(tx, user, in.ID)
		if e == nil {
			if old.CurationID != id || old.RequestHash != hash {
				return fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", false)
			}
			t = old
			return nil
		}
		if f, ok := fault.As(e); !ok || f.Reason != "CURATION_THREAD_NOT_FOUND" {
			return e
		}
		plan, e := s.curation.GetByCuration(tx, user, id)
		if e != nil {
			return e
		}
		if plan.Curation.Version != in.ExpectedCurationVersion {
			return d.ErrVersionConflict
		}
		if e = s.repo.GuardThreadMutation(tx, user, id); e != nil {
			return e
		}
		if e = s.curation.guardActionConcurrency(tx, user, string(plan.Plan.ID)); e != nil {
			return e
		}
		mode, e := s.repo.ReadControlMode(tx, user, id)
		if e != nil {
			return e
		}
		now := s.curation.clock.Now()
		t = d.CurationThread{SchemaVersion: d.ThreadSchema, ExpectedConversationVersion: in.ExpectedConversationVersion, ID: in.ID, UserID: user, AuthSessionID: auth, CurationID: id, PlanID: string(plan.Plan.ID), Request: in.Request, RequestHash: hash, Mode: mode.Mode, Origin: "REQUEST", Revision: 1, ExpectedCurationVersion: in.ExpectedCurationVersion, Status: "INTERPRETING", Actions: []d.CurationAction{}, CreatedAt: now, UpdatedAt: now}
		if in.Kind != "" {
			if in.Kind != "ADD_TARGET" && in.Kind != "RESEARCH_AGAIN" && in.Kind != "COMBINATION" {
				return fault.New(fault.InvalidInput, "CURATION_THREAD_INVALID", false)
			}
			t.Origin = "MANUAL"
			decisions := []d.ActionDecision{{ID: s.curation.ids.NewID(), Kind: "ACTION", Result: in.Kind, Source: "MANUAL", ReasonCode: "EXPLICIT_COMMAND"}, {ID: s.curation.ids.NewID(), Kind: "TARGET", Result: in.TargetID, TargetID: in.TargetID, Source: "MANUAL", ReasonCode: "EXPLICIT_COMMAND"}, {ID: s.curation.ids.NewID(), Kind: "BUDGET", Result: "KEEP", Source: "DETERMINISTIC", ReasonCode: "MANUAL_PRIMITIVE_SCOPE"}}
			t.Status = "RUNNING"
			t.Actions = []d.CurationAction{{ID: s.curation.ids.NewID(), Type: d.ActionTypeForPrimitive(in.Kind), TargetID: in.TargetID, Instruction: in.Request, Status: "PENDING", Jobs: []d.ActionJobResult{}, Effects: []d.ActionEffect{}}}
			if in.Kind == "COMBINATION" {
				t.Actions[0].Type = d.CurationActionResponse
				t.Actions[0].Instruction = "COMBINATION"
				t.Actions[0].InputRevision = 1
			}
			if in.Kind == "RESEARCH_AGAIN" && in.TargetID == "" {
				return fault.New(fault.InvalidInput, "CURATION_THREAD_INVALID", false)
			}
			if in.Kind == "ADD_TARGET" && plan.Curation.Phase == d.CurationPhasePlanning {
				t.Actions[0].Type = d.CurationActionPlanningAddTargets
			}
			t.Actions[0].Decisions = decisions
			s.identifyPlan(&d.ActionPlan{Actions: t.Actions, Decisions: decisions})
		} else if mode.Mode != "AUTO" {
			return fault.New(fault.InvalidInput, "CURATION_MANUAL_ACTION_REQUIRED", false)
		}

		if len(t.Actions) == 0 {
			t.Actions = []d.CurationAction{{ID: s.curation.ids.NewID(), Type: d.CurationActionAutoStart, Instruction: t.Request, Status: "PENDING", InputRevision: 1}}
		}
		// A selected manual research action owns bounded condition interpretation.
		if t.Origin == "MANUAL" && in.Kind == "RESEARCH_AGAIN" && in.Request != "" && !in.KeepCriteria {
			t.Actions = append([]d.CurationAction{{ID: s.curation.ids.NewID(), Type: d.CurationActionCriteriaChange, TargetID: in.TargetID, Instruction: in.Request, Status: "PENDING", InputRevision: 1}}, t.Actions...)
		}
		if in.Kind == "RESEARCH_AGAIN" && (in.SessionID != "" || in.ExpectedSessionVersion != nil || in.ExpectedCriteriaVersion != nil) {
			snapshot, e := s.snapshot(tx, t)
			if e != nil {
				return e
			}
			var selected *ThreadTarget
			for i := range snapshot.Targets {
				if snapshot.Targets[i].ID == in.TargetID {
					selected = &snapshot.Targets[i]
					break
				}
			}
			if selected == nil || !selected.Researchable {
				return d.ErrTargetNotFound
			}
			if in.SessionID != "" && selected.SessionID != in.SessionID {
				return fault.New(fault.Conflict, "RESEARCH_SESSION_CHANGED", false)
			}
			if in.ExpectedSessionVersion != nil && selected.SessionVersion != *in.ExpectedSessionVersion {
				return fault.New(fault.Conflict, "RESEARCH_SESSION_CHANGED", false)
			}
			v := int64(0)
			if selected.Criteria != nil {
				v = selected.Criteria.Version
			}
			if in.ExpectedCriteriaVersion != nil && v != *in.ExpectedCriteriaVersion {
				return fault.New(fault.Conflict, "RESEARCH_CRITERIA_CHANGED", false)
			}
		}
		if e = s.repo.InsertThread(tx, t); e != nil {
			return e
		}
		t, e = s.repo.ReadThread(tx, user, t.ID)
		return e
	})
	return t, err
}

func (s *ThreadService) Cancel(ctx context.Context, user, curation, id string) (d.CurationThread, error) {
	var t d.CurationThread
	err := s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if e := s.repo.LockThreadCuration(tx, user, curation); e != nil {
			return e
		}
		var e error
		t, e = s.repo.ReadThread(tx, user, id)
		if e != nil {
			return e
		}
		if t.CurationID != curation {
			return d.ErrCurationNotFound
		}
		if !t.Active() {
			return nil
		}
		if e = s.cancelCurrentAction(tx, &t); e != nil {
			return e
		}
		t.Cancel()
		return s.repo.SaveThread(tx, t)
	})
	return t, err
}

// Cancelling the current Action stops its dependent tail. Completed receipts remain.
func (s *ThreadService) CancelAction(ctx context.Context, user, curation, thread, action string) (d.CurationThread, error) {
	var t d.CurationThread
	err := s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if e := s.repo.LockThreadCuration(tx, user, curation); e != nil {
			return e
		}
		var e error
		t, e = s.repo.ReadThread(tx, user, thread)
		if e != nil {
			return e
		}
		if t.CurationID != curation {
			return d.ErrCurationNotFound
		}
		for _, a := range t.Actions {
			if a.ID == action && a.Terminal() {
				return nil
			}
		}
		a := t.CurrentAction()
		if a == nil || a.ID != action {
			return fault.New(fault.Conflict, "CURATION_ACTION_CHANGED", false)
		}
		if e = s.cancelCurrentAction(tx, &t); e != nil {
			return e
		}
		t.Cancel()
		return s.repo.SaveThread(tx, t)
	})
	return t, err
}

// CancelActionByID serves the legacy POST /curation-actions/{id}/cancel route.
// When the Action belongs to a Thread, cancelling through the Thread keeps
// Thread, Action and Job status in one transaction, so the workspace stops
// reporting an active Thread the moment the cancel returns. It reports how
// many of the Action's Jobs are cancelled; zero when the Action had already
// finished. Pre-Thread Actions return handled=false for the legacy path.
func (s *ThreadService) CancelActionByID(ctx context.Context, user, action string) (int, bool, error) {
	curation, thread, found, err := s.repo.ThreadForAction(ctx, user, action)
	if err != nil || !found {
		return 0, false, err
	}
	t, err := s.CancelAction(ctx, user, curation, thread, action)
	if err != nil {
		return 0, true, err
	}
	cancelled := 0
	for _, a := range t.Actions {
		if a.ID != action {
			continue
		}
		for _, job := range a.Jobs {
			if job.Status == "CANCELLED" {
				cancelled++
			}
		}
	}
	return cancelled, true, nil
}

func (s *ThreadService) Answer(ctx context.Context, user, curation, id string, answer d.ThreadAnswer) (d.CurationThread, error) {
	var t d.CurationThread
	err := s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if e := s.repo.LockThreadCuration(tx, user, curation); e != nil {
			return e
		}
		var e error
		t, e = s.repo.ReadThread(tx, user, id)
		if e != nil {
			return e
		}
		if t.CurationID != curation {
			return d.ErrCurationNotFound
		}
		for _, a := range t.Actions {
			for _, old := range a.Answers {
				if old == answer {
					return nil
				}
			}
		}
		a := t.CurrentAction()
		if a == nil || a.Status != "WAITING_SELECTION" || t.Revision != answer.Revision || a.Question == nil || a.Question.ID != answer.QuestionID {
			return fault.New(fault.Conflict, "CURATION_SELECTION_CHANGED", false)
		}
		if len(a.Answers) >= 8 || len(answer.Text) > 2000 || (answer.OptionID == "") == (strings.TrimSpace(answer.Text) == "") {
			return fault.New(fault.InvalidInput, "CURATION_SELECTION_INVALID", false)
		}
		if answer.OptionID != "" {
			var plan *d.ActionPlan
			for _, o := range a.Question.Options {
				if o.ID == answer.OptionID {
					plan = &d.ActionPlan{Actions: o.Actions, Decisions: o.Decisions}
					plan.Decisions = append(plan.Decisions, d.ActionDecision{ID: s.curation.ids.NewID(), Kind: "TARGET", Result: o.ID, Source: "USER_SELECTION", Evidence: o.Label, ReasonCode: "USER_SELECTED_OPTION"})
					break
				}
			}
			if plan == nil {
				return fault.New(fault.InvalidInput, "CURATION_SELECTION_INVALID", false)
			}
			a.Answers = append(a.Answers, answer)
			a.Question = nil
			if e = s.appendDecisionActions(&t, a, *plan); e != nil {
				return e
			}
		} else {
			a.Answers = append(a.Answers, answer)
			a.Question = nil
			a.InputRevision++
			a.InterpretationStartedAt = nil
			a.Status = "PENDING"
		}
		t.Revision++
		t.RefreshStatus()
		return s.repo.SaveThread(tx, t)
	})
	return t, err
}
func (s *ThreadService) snapshot(ctx context.Context, t d.CurationThread) (ThreadContext, error) {
	out := ThreadContext{Thread: t, Targets: []ThreadTarget{}}
	p, err := s.curation.GetByCuration(ctx, t.UserID, t.CurationID)
	if err != nil {
		return out, err
	}
	out.Phase = p.Curation.Phase
	out.OriginalIntent = p.Plan.OriginalIntent
	out.ModelKey = p.Plan.ModelKey
	out.Locale, err = s.curation.PlanContentLocale(ctx, t.UserID, t.PlanID)
	if err != nil {
		return out, err
	}
	out.Budget, err = s.curation.Budget(ctx, t.UserID, t.CurationID)
	if err != nil {
		return out, err
	}
	for _, target := range p.Targets {
		if target.RemovedAt != nil {
			continue
		}
		v := ThreadTarget{ID: string(target.ID), Title: target.Title, Intent: target.NormalizedIntent, OrderIndex: target.OrderIndex}
		for _, session := range p.Sessions {
			if string(session.PlanTargetID) == string(target.ID) {
				v.SessionID = string(session.ID)
				v.SessionVersion = session.Version
				v.Researchable = string(session.Status) == "REVIEWING"
			}
		}
		v.Criteria, err = s.curation.TargetCriteria(ctx, t.UserID, t.CurationID, v.ID)
		if err != nil {
			return out, err
		}
		out.Targets = append(out.Targets, v)
	}
	return out, nil
}

// Thread worker advances one Action boundary; provider work belongs to Job workers.
func (s *ThreadService) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	slots := make(chan struct{}, 3)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			list, err := s.repo.PendingThreads(ctx)
			if err != nil {
				s.curation.logger.ErrorContext(ctx, "thread poll failed", "error", err)
				continue
			}
			for _, t := range list {
				select {
				case slots <- struct{}{}:
					go func(t d.CurationThread) {
						defer func() { <-slots }()
						if e := s.advance(ctx, t); e != nil && !errors.Is(e, context.Canceled) {
							s.curation.logger.ErrorContext(ctx, "thread advance failed", "thread_id", t.ID, "error", e)
						}
					}(t)
				default:
				}
			}
		}
	}
}

// threadContinuationChain bounds how many generated continuations one poll may
// run back to back. The chain is short by construction — a product-planning
// Action generates exactly one research Action — so this only guards a defect.
const threadContinuationChain = 4

// advance runs one Action per transaction so an earlier primitive's committed
// effect never rolls back with a later Action's failure. A continuation the
// pass generated still runs immediately, in its own transaction: product
// planning commits its Targets while the Curation is still PLANNING, and
// waiting for the next tick would publish that state for about a second, which
// the workspace draws as the manual recovery screen.
func (s *ThreadService) advance(ctx context.Context, ref d.CurationThread) error {
	for range threadContinuationChain {
		generated, err := s.advanceOnce(ctx, &ref)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			return s.failAdvance(ctx, ref, err)
		}
		if !generated {
			return nil
		}
		fresh, err := s.repo.ReadThread(ctx, ref.UserID, ref.ID)
		if err != nil {
			return err
		}
		ref = fresh
	}
	return nil
}

// advanceOnce reports whether the Action it advanced generated a follow-up that
// is ready to run now. Only a continuation this Action produced counts: Actions
// the interpreter planned keep their own tick, so a user can still observe and
// cancel between them.
func (s *ThreadService) advanceOnce(ctx context.Context, ref *d.CurationThread) (bool, error) {
	generated := false
	err := s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		generated = false
		if e := s.repo.LockThreadCuration(tx, ref.UserID, ref.CurationID); e != nil {
			return e
		}
		t, e := s.repo.ReadThread(tx, ref.UserID, ref.ID)
		if e != nil {
			return e
		}
		if !t.Active() {
			return nil
		}
		a := t.CurrentAction()
		if a == nil {
			t.RefreshStatus()
			return s.repo.SaveThread(tx, t)
		}
		actionID := a.ID
		if e = s.advanceAction(tx, &t, a); e != nil {
			return e
		}
		// Every planned Action has succeeded and the research added candidates:
		// the Thread writes its comment before it closes. The bar shows this as
		// its last step, so nothing else has to report the wait.
		if s.responder != nil && t.CurrentAction() == nil && t.NeedsResponseComment() {
			t.AppendResponseAction(s.curation.ids.NewID())
		}
		if next := t.CurrentAction(); next != nil && next.ID != actionID &&
			next.Status == "PENDING" && next.GeneratedByActionID == actionID {
			generated = true
		}
		t.RefreshStatus()
		return s.repo.SaveThread(tx, t)
	})
	if err != nil {
		return false, err
	}
	return generated, nil
}

func (s *ThreadService) failAdvance(ctx context.Context, ref d.CurationThread, failure error) error {
	return s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if e := s.repo.LockThreadCuration(tx, ref.UserID, ref.CurationID); e != nil {
			return e
		}
		t, e := s.repo.ReadThread(tx, ref.UserID, ref.ID)
		if e != nil {
			return e
		}
		if !t.Active() || t.Revision != ref.Revision || !t.UpdatedAt.Equal(ref.UpdatedAt) {
			return nil
		}
		a := t.CurrentAction()
		if a == nil {
			return nil
		}
		a.Status = "FAILED"
		a.ReasonCode = "CURATION_PRIMITIVE_FAILED"
		if f, ok := fault.As(failure); ok {
			a.ReasonCode = f.Reason
		} else if errors.Is(failure, d.ErrExpansionInProgress) {
			a.ReasonCode = d.ErrExpansionInProgress.Error()
		}
		s.curation.logger.ErrorContext(ctx, "curation action failed", "thread_id", t.ID, "action_id", a.ID, "reason_code", a.ReasonCode)
		t.RefreshStatus()
		return s.repo.SaveThread(tx, t)
	})
}
func (s *ThreadService) identifyPlan(t *d.ActionPlan) {
	if t.Actions == nil {
		t.Actions = []d.CurationAction{}
	}
	if t.Decisions == nil {
		t.Decisions = []d.ActionDecision{}
	}
	fill := func(steps []d.CurationAction) {
		for i := range steps {
			if steps[i].ID == "" {
				steps[i].ID = s.curation.ids.NewID()
			}
			if steps[i].Budget != nil {
				steps[i].Budget.CommandID = steps[i].ID
				steps[i].Budget.SchemaVersion = d.BudgetSchema
			}
			steps[i].Status = "PENDING"
			steps[i].Jobs = []d.ActionJobResult{}
			steps[i].Effects = []d.ActionEffect{}
		}
	}
	fill(t.Actions)
	if t.Question != nil {
		t.Question.ID = s.curation.ids.NewID()
		for i := range t.Question.Options {
			t.Question.Options[i].ID = s.curation.ids.NewID()
			fill(t.Question.Options[i].Actions)
			optionPlan := d.ActionPlan{Actions: t.Question.Options[i].Actions, Decisions: t.Question.Options[i].Decisions}
			s.identifyPlan(&optionPlan)
			t.Question.Options[i].Decisions = optionPlan.Decisions
		}
	}
	for i := range t.Decisions {
		if t.Decisions[i].ID == "" {
			t.Decisions[i].ID = s.curation.ids.NewID()
		}
		if t.Decisions[i].ActionIDs == nil {
			t.Decisions[i].ActionIDs = []string{}
			for _, step := range t.Actions {
				match := t.Decisions[i].Kind == "ACTION" || t.Decisions[i].Kind == "TARGET" || t.Decisions[i].Kind == "BUDGET" && step.Type == "BUDGET_CHANGE" || t.Decisions[i].Kind == "CONDITIONS" && step.Type == "CRITERIA_CHANGE"
				if match {
					t.Decisions[i].ActionIDs = append(t.Decisions[i].ActionIDs, step.ID)
				}
			}
		}
	}
}
