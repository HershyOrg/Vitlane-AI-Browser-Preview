package domain

import "fmt"

func ActionTypeForPrimitive(kind string) CurationActionType {
	switch kind {
	case "BUDGET":
		return CurationActionBudgetChange
	case "CRITERIA":
		return CurationActionCriteriaChange
	case "ADD_TARGET":
		return CurationActionCurationAddTargets
	case "RESEARCH_AGAIN":
		return CurationActionTargetResearchAgain
	case "PLANNING":
		return CurationActionIntentNextStep
	default:
		return CurationActionType(kind)
	}
}

// ReasonPartialFailure marks an Action whose parallel Jobs ended with both
// successes and failures. The Thread still stops after it (later Actions are
// SKIPPED) while the succeeded Jobs' effects stay committed and reported.
const ReasonPartialFailure = "PARTIAL_FAILURE"

func (a CurationAction) Terminal() bool {
	return a.Status == "SUCCEEDED" || a.Status == "FAILED" || a.Status == "CANCELLED" || a.Status == "SKIPPED"
}
func (a *CurationAction) ReconcileJobs(jobs []ActionJobResult) {
	if a.Terminal() {
		return
	}
	a.Jobs = jobs
	if len(jobs) == 0 {
		return
	}
	terminal, failed, succeeded := true, false, false
	for _, j := range jobs {
		switch j.Status {
		case "FAILED", "CANCELLED":
			failed = true
		case "SUCCEEDED":
			succeeded = true
		default:
			terminal = false
		}
	}
	if terminal {
		a.Status = "SUCCEEDED"
		if failed {
			// The Action stops the Thread either way, but a partial failure
			// keeps every succeeded Job's receipt visible under its own reason.
			a.Status = "FAILED"
			a.ReasonCode = "CURATION_PRIMITIVE_FAILED"
			if succeeded {
				a.ReasonCode = ReasonPartialFailure
			}
		}
	}
}

func (a *CurationAction) Transition(next string) error {
	if next == a.Status {
		return nil
	}
	allowed := false
	switch a.Status {
	case "PENDING":
		allowed = next == "RUNNING" || next == "SUCCEEDED" || next == "FAILED" || next == "SKIPPED" || next == "CANCELLED"
	case "RUNNING":
		allowed = next == "SUCCEEDED" || next == "FAILED" || next == "CANCELLED" || next == "WAITING_SELECTION"
	case "WAITING_SELECTION":
		allowed = next == "PENDING" || next == "SUCCEEDED" || next == "CANCELLED"
	}
	if !allowed {
		return fmt.Errorf("CURATION_ACTION_TRANSITION_INVALID: %s -> %s", a.Status, next)
	}
	a.Status = next
	return nil
}
