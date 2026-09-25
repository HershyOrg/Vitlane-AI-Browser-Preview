package app

import (
	"context"
	"encoding/json"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// Action execution owns fan-out, receipts and primitive dispatch. Thread sees
// only its current Action and terminal state.
func (s *ThreadService) advanceAction(ctx context.Context, t *d.CurationThread, a *d.CurationAction) error {
	if a.Status == "WAITING_SELECTION" || a.Terminal() {
		return nil
	}
	if a.Status == "RUNNING" {
		jobs, e := s.repo.ActionJobs(ctx, t.ID, a.ID)
		if e != nil {
			return e
		}
		if a.Type == d.CurationActionResponse {
			// The runner records a written reply itself. A Job that ended any
			// other way leaves the Thread successful and the reply unavailable.
			a.SettleResponseJobs(jobs)
			return nil
		}
		a.ReconcileJobs(jobs)
		if a.Type == d.CurationActionAutoStart && a.Status == "SUCCEEDED" {
			return fault.New(fault.Conflict, "AUTO_RESULT_MISSING", false)
		}
		if a.Status == "SUCCEEDED" && (a.Type == d.CurationActionIntentNextStep || a.Type == d.CurationActionCurationAddTargets || a.Type == d.CurationActionPlanningAddTargets) {
			found := false
			for _, next := range t.Actions {
				if next.Type == d.CurationActionStartResearch && next.GeneratedByActionID == a.ID {
					found = true
				}
			}
			if !found {
				if t.CurrentAction() != nil {
					return fault.New(fault.Conflict, "CURATION_PLAN_ORDER_INVALID", false)
				}
				t.Actions = append(t.Actions, d.CurationAction{ID: s.curation.ids.NewID(), Type: d.CurationActionStartResearch, GeneratedByActionID: a.ID, Instruction: a.ID, Status: "PENDING"})
			}
		}
		return nil
	}
	if a.Type == d.CurationActionResponse && s.responder == nil {
		a.Status = "SUCCEEDED"
		a.ReasonCode = d.ReasonResponseUnavailable
		return nil
	}
	if a.Type == d.CurationActionAutoStart || a.Type == d.CurationActionResponse || (a.Type == d.CurationActionCriteriaChange && a.Criteria == nil && a.Instruction != "") {
		a.Status = "RUNNING"
		if a.InputRevision < 1 {
			a.InputRevision = 1
		}
		// Persist accepted Action identity before creating its FK-linked Job.
		if e := s.repo.SaveThread(ctx, *t); e != nil {
			return e
		}
		return s.primitives.StartInterpretation(WithThreadExecution(ctx, t.ID, a.ID), *t, *a)
	}
	return s.dispatchAction(ctx, t, a)
}
func (s *ThreadService) dispatchAction(ctx context.Context, t *d.CurationThread, step *d.CurationAction) error {
	tx := WithThreadExecution(ctx, t.ID, step.ID)
	snapshot, err := s.snapshot(tx, *t)
	if err != nil {
		return err
	}
	var target ThreadTarget
	for _, v := range snapshot.Targets {
		if v.ID == step.TargetID {
			target = v
		}
	}
	switch step.Type {
	case "BUDGET_CHANGE":
		if step.Budget == nil {
			return fault.New(fault.InvalidInput, "AUTO_PLAN_INVALID", false)
		}
		command := *step.Budget
		command.CommandID = step.ID
		command.ExpectedVersion = snapshot.Budget.Version
		command.SchemaVersion = d.BudgetSchema
		var after d.BudgetLedger
		after, err = s.curation.ChangeBudget(tx, t.UserID, t.CurationID, command)
		if err == nil {
			beforeRaw, _ := json.Marshal(snapshot.Budget)
			afterRaw, _ := json.Marshal(after)
			step.Effects = []d.ActionEffect{{Kind: "BUDGET_CHANGED", TargetID: command.TargetID, Before: beforeRaw, After: afterRaw}}
			step.Status = "SUCCEEDED"
		}
	case "CRITERIA_CHANGE":
		if target.ID == "" || step.Criteria == nil {
			err = d.ErrTargetNotFound
			break
		}
		version := int64(0)
		if target.Criteria != nil {
			version = target.Criteria.Version
		}
		p, e := s.curation.GetByCuration(tx, t.UserID, t.CurationID)
		if e != nil {
			return e
		}
		var after d.TargetCriteriaSetV1
		after, err = s.curation.ChangeTargetCriteria(tx, t.UserID, t.CurationID, target.ID, CriteriaCommand{SchemaVersion: "vitlane.criteria-command.v1", ExpectedCurationVersion: p.Curation.Version, ExpectedCriteriaVersion: version, IdempotencyKey: step.ID, Criteria: *step.Criteria})
		if err == nil {
			beforeRaw, _ := json.Marshal(target.Criteria)
			afterRaw, _ := json.Marshal(after)
			step.Effects = []d.ActionEffect{{Kind: "CRITERIA_CHANGED", TargetID: target.ID, TargetLabel: target.Title, Before: beforeRaw, After: afterRaw}}
			step.Status = "SUCCEEDED"
		}
	case "CURATION_ADD_TARGETS", "PLANNING_ADD_TARGETS":
		p, e := s.curation.GetByCuration(tx, t.UserID, t.CurationID)
		if e != nil {
			return e
		}
		kind := d.CurationActionCurationAddTargets
		if p.Curation.Phase == d.CurationPhasePlanning {
			kind = d.CurationActionPlanningAddTargets
		}
		_, err = s.curation.ExecuteExpansionAction(tx, ExecuteExpansionActionInput{ActionID: step.ID, UserID: t.UserID, AuthSessionID: t.AuthSessionID, CurationID: t.CurationID, PlanID: t.PlanID, Type: kind, Instruction: step.Instruction, ExpectedCurationVersion: p.Curation.Version})
		step.Status = "RUNNING"
	case "TARGET_RESEARCH_AGAIN":
		if target.ID == "" || !target.Researchable {
			err = d.ErrTargetNotFound
			break
		}
		err = s.primitives.ResearchThreadTarget(tx, *t, *step, target)
		step.Status = "RUNNING"
	case "START_RESEARCH":
		err = s.primitives.StartThreadResearch(tx, *t, step.Instruction)
		step.Status = "RUNNING"
		if err == nil {
			var jobs []d.ActionJobResult
			jobs, err = s.repo.ActionJobs(tx, t.ID, step.ID)
			step.Jobs = jobs
			if err == nil && len(jobs) == 0 {
				step.Status = "SUCCEEDED"
			}
		}
	default:
		err = fault.New(fault.InvalidInput, "AUTO_PLAN_INVALID", false)
	}
	if err != nil {
		return err
	}
	return nil
}
func (s *ThreadService) cancelCurrentAction(ctx context.Context, t *d.CurationThread) error {
	a := t.CurrentAction()
	if a == nil {
		return nil
	}
	jobs, e := s.repo.ActionJobs(ctx, t.ID, a.ID)
	if e != nil {
		return e
	}
	if a.Status == "RUNNING" && a.Type != d.CurationActionAutoStart && !(a.Type == d.CurationActionCriteriaChange && a.Criteria == nil) {
		a.ReconcileJobs(jobs)
	} else {
		a.Jobs = jobs
	}
	if a.Terminal() {
		return nil
	}
	if e = s.primitives.CancelThreadAction(ctx, t.UserID, a.ID); e != nil {
		return e
	}
	a.Jobs, e = s.repo.ActionJobs(ctx, t.ID, a.ID)
	return e
}

// This is the only decision -> command registration boundary. Its caller holds
// the Curation lock and persists the result and new Actions in the same TX.
func (s *ThreadService) appendDecisionActions(t *d.CurationThread, a *d.CurationAction, plan d.ActionPlan) error {
	if !t.Active() || a.Terminal() || t.CurrentAction() != a {
		return fault.New(fault.Conflict, "CURATION_ACTION_CHANGED", false)
	}
	if e := plan.ValidatePlan(); e != nil {
		return e
	}
	s.identifyPlan(&plan)
	a.Decisions = append(a.Decisions, plan.Decisions...)
	if plan.Question != nil {
		a.Question = plan.Question
		a.Questions = append(a.Questions, *plan.Question)
		a.Status = "WAITING_SELECTION"
		return nil
	}
	if a.Sequence+1 < len(t.Actions) {
		return fault.New(fault.Conflict, "CURATION_PLAN_ORDER_INVALID", false)
	}
	a.Status = "SUCCEEDED"
	a.Question = nil
	next := []d.CurationAction{}
	for _, action := range plan.Actions {
		action.GeneratedByActionID = a.ID
		for _, decision := range plan.Decisions {
			for _, id := range decision.ActionIDs {
				if id == action.ID {
					action.DecisionIDs = append(action.DecisionIDs, decision.ID)
				}
			}
		}
		next = append(next, action)
		if action.Type == d.CurationActionCurationAddTargets || action.Type == d.CurationActionPlanningAddTargets {
			next = append(next, d.CurationAction{ID: s.curation.ids.NewID(), Type: d.CurationActionStartResearch, GeneratedByActionID: action.ID, Instruction: action.ID, Status: "PENDING"})
		}
	}
	t.Actions = append(t.Actions, next...)
	return nil
}

// Called by the existing Intelligence Job worker with an actual claimed Attempt.
func (s *ThreadService) RunActionInterpretation(ctx context.Context, user, curation, action, job, attempt string, revision int64) error {
	scope := ThreadExecutionFrom(ctx)
	var t d.CurationThread
	var snapshot ThreadContext
	response := false
	err := s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if e := s.repo.LockThreadCuration(tx, user, curation); e != nil {
			return e
		}
		if e := s.repo.GuardThreadMutation(tx, user, curation); e != nil {
			return e
		}
		var e error
		t, e = s.repo.ReadThread(tx, user, scope.ThreadID)
		if e != nil {
			return e
		}
		a := t.CurrentAction()
		if !t.Active() || a == nil || a.ID != action || a.Status != "RUNNING" || a.InputRevision != revision {
			return fault.New(fault.Conflict, "CURATION_ACTION_CHANGED", false)
		}
		if a.Type == d.CurationActionResponse {
			response = true
			return nil
		}
		snapshot, e = s.snapshot(tx, t)
		if e != nil {
			return e
		}
		snapshot.JobID = job
		snapshot.AttemptID = attempt
		snapshot.ActionID = action
		snapshot.InputRevision = revision
		snapshot.Answers = a.Answers
		if a.Type == d.CurationActionCriteriaChange {
			copy := *a
			snapshot.ManualAction = &copy
		}
		return nil
	})
	if err != nil {
		return err
	}
	if response {
		return s.runResponse(ctx, user, curation, action, job, attempt, revision)
	}
	call, cancel := context.WithCancel(ctx)
	defer cancel()
	plan, err := s.interpreter.Interpret(call, snapshot)
	if err != nil {
		return err
	}
	return s.curation.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		if e := s.repo.LockThreadCuration(tx, user, curation); e != nil {
			return e
		}
		if e := s.repo.GuardThreadMutation(tx, user, curation); e != nil {
			return e
		}
		fresh, e := s.repo.ReadThread(tx, user, t.ID)
		if e != nil {
			return e
		}
		a := fresh.CurrentAction()
		if !fresh.Active() || a == nil || a.ID != action || a.Status != "RUNNING" || a.InputRevision != revision {
			return fault.New(fault.Conflict, "CURATION_ACTION_CHANGED", false)
		}
		for i := range plan.Actions {
			for _, target := range snapshot.Targets {
				if plan.Actions[i].TargetID == target.ID {
					plan.Actions[i].TargetLabel = target.Title
				}
			}
		}
		receipt := d.ActionJobResult{JobID: job, AttemptID: attempt, ActionID: action, Kind: "ACTION_INTERPRETATION", Status: "SUCCEEDED", Effects: []d.ActionEffect{}}
		replaced := false
		for n := range a.Jobs {
			if a.Jobs[n].JobID == job {
				a.Jobs[n] = receipt
				replaced = true
			}
		}
		if !replaced {
			a.Jobs = append(a.Jobs, receipt)
		}
		if snapshot.ManualAction != nil {
			if plan.Question != nil {
				return fault.New(fault.InvalidInput, "MANUAL_CRITERIA_AMBIGUOUS", false)
			}
			for _, proposed := range plan.Actions {
				if proposed.Type != d.CurationActionCriteriaChange || proposed.TargetID != a.TargetID {
					return fault.New(fault.InvalidInput, "MANUAL_SCOPE_CHANGED", false)
				}
			}
			a.Decisions = plan.Decisions
			if len(plan.Actions) > 1 {
				return fault.New(fault.InvalidInput, "MANUAL_SCOPE_CHANGED", false)
			}
			if len(plan.Actions) == 1 {
				value := plan.Actions[0]
				value.ID = a.ID
				if e = s.dispatchAction(tx, &fresh, &value); e != nil {
					return e
				}
				a.Effects = value.Effects
			}
			a.Status = "SUCCEEDED"
		} else if e = s.appendDecisionActions(&fresh, a, plan); e != nil {
			return e
		}
		fresh.Revision++
		fresh.RefreshStatus()
		return s.repo.SaveThread(tx, fresh)
	})
}
