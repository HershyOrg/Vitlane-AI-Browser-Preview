package app

import (
	"fmt"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
)

type JourneyStepKey string

const (
	JourneyStepIntent   JourneyStepKey = "INTENT"
	JourneyStepCuration JourneyStepKey = "CURATION"
)

type JourneyStepState string

const (
	JourneyStepCompleted JourneyStepState = "COMPLETED"
	JourneyStepCurrent   JourneyStepState = "CURRENT"
	JourneyStepLocked    JourneyStepState = "LOCKED"
)

type JourneyStep struct {
	Key      JourneyStepKey   `json:"key"`
	Label    string           `json:"label"`
	State    JourneyStepState `json:"state"`
	Path     string           `json:"path,omitempty"`
	ReadOnly bool             `json:"readOnly"`
}

type SessionStatusCounts struct {
	Ready       int `json:"ready"`
	Researching int `json:"researching"`
	Reviewing   int `json:"reviewing"`
}

type PlanJourney struct {
	CurrentStep  JourneyStepKey      `json:"currentStep"`
	CurrentStage string              `json:"currentStage"`
	ResumePath   string              `json:"resumePath"`
	NextAction   string              `json:"nextAction"`
	Steps        []JourneyStep       `json:"steps"`
	SessionCount SessionStatusCounts `json:"sessionCount"`
}

func BuildPlanJourney(
	plan curationdomain.PlanSnapshot,
	curation curationdomain.Curation,
	sessions []shoppingsessiondomain.ShoppingSession,
) PlanJourney {
	counts := countSessionStatuses(sessions)
	return BuildPlanJourneySummary(string(plan.ID), curation.Phase, counts)
}

func BuildPlanJourneySummary(
	planID string,
	phase curationdomain.CurationPhase,
	counts SessionStatusCounts,
) PlanJourney {
	currentStep, currentStage, nextAction := projectCurrentStep(phase, counts)
	resumePath := journeyStepPath(planID, currentStep)

	ordered := []struct {
		key   JourneyStepKey
		label string
	}{
		{JourneyStepIntent, "요청"},
		{JourneyStepCuration, "큐레이션"},
	}
	currentIndex := journeyStepIndex(currentStep)
	steps := make([]JourneyStep, 0, len(ordered))
	for index, item := range ordered {
		step := JourneyStep{Key: item.key, Label: item.label}
		stepPath := journeyStepPath(planID, item.key)
		switch {
		case index < currentIndex:
			step.State = JourneyStepCompleted
			step.Path = stepPath
			step.ReadOnly = true
		case index == currentIndex:
			step.State = JourneyStepCurrent
			step.Path = stepPath
		default:
			step.State = JourneyStepLocked
		}
		steps = append(steps, step)
	}

	return PlanJourney{
		CurrentStep: currentStep, CurrentStage: currentStage,
		ResumePath: resumePath, NextAction: nextAction,
		Steps: steps, SessionCount: counts,
	}
}

func countSessionStatuses(sessions []shoppingsessiondomain.ShoppingSession) SessionStatusCounts {
	var counts SessionStatusCounts
	for _, session := range sessions {
		switch session.Status {
		case shoppingsessiondomain.SessionStatusReady:
			counts.Ready++
		case shoppingsessiondomain.SessionStatusResearching:
			counts.Researching++
		case shoppingsessiondomain.SessionStatusReviewing:
			counts.Reviewing++
		}
	}
	return counts
}

func projectCurrentStep(
	phase curationdomain.CurationPhase,
	counts SessionStatusCounts,
) (JourneyStepKey, string, string) {
	switch phase {
	case curationdomain.CurationPhasePlanning:
		return JourneyStepCuration, "PLANNING", "Codex가 조사할 상품군을 구성하고 있습니다."
	case curationdomain.CurationPhaseCurating:
		switch {
		case counts.Researching > 0:
			return JourneyStepCuration, "RESEARCHING", "Codex가 상품을 조사하고 있습니다."
		case counts.Reviewing > 0:
			return JourneyStepCuration, "REVIEWING", "도착한 후보를 비교해 주세요."
		case counts.Ready > 0:
			return JourneyStepCuration, "READY", "상품군별 조사를 시작합니다."
		default:
			return JourneyStepCuration, "CURATING", "큐레이션 결과를 확인해 주세요."
		}
	default:
		return JourneyStepIntent, "PLANNING", "계획 상태를 확인해 주세요."
	}
}

func journeyStepPath(planID string, step JourneyStepKey) string {
	base := fmt.Sprintf("/plans/%s", planID)
	switch step {
	case JourneyStepIntent, JourneyStepCuration:
		return base
	default:
		return base
	}
}

func journeyStepIndex(step JourneyStepKey) int {
	switch step {
	case JourneyStepIntent:
		return 0
	case JourneyStepCuration:
		return 1
	default:
		return 0
	}
}
