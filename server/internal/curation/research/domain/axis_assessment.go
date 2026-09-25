package domain

import (
	c "github.com/vitlane/vitlane/server/internal/curation/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"strings"
	"time"
)

type AxisScoreV1 struct {
	AxisID       string   `json:"axisId"`
	ScorePercent int      `json:"scorePercent"`
	Basis        string   `json:"basis"`
	Explanation  string   `json:"explanation"`
	FactIDs      []string `json:"factIds"`
}
type AxisAssessmentV1 struct {
	FindingID        string                `json:"findingId,omitempty"`
	ObservationHash  string                `json:"observationHash"`
	Source           string                `json:"source"`
	SchemaVersion    string                `json:"schemaVersion"`
	Criteria         c.TargetCriteriaSetV1 `json:"criteria"`
	Weights          []int                 `json:"weights"`
	Scores           []AxisScoreV1         `json:"scores"`
	TotalBasisPoints int                   `json:"totalBasisPoints"`
	TotalScore       int                   `json:"totalScore"`
	ContentLocale    string                `json:"contentLocale"`
	RoundID          string                `json:"roundId"`
	ModelKey         string                `json:"modelKey"`
	CreatedAt        time.Time             `json:"createdAt"`
}

func NewAxisAssessment(criteria c.TargetCriteriaSetV1, scores []AxisScoreV1, locale, round, model string, now time.Time) (*AxisAssessmentV1, error) {
	invalid := func() (*AxisAssessmentV1, error) {
		return nil, fault.New(fault.ProviderRejected, "RESEARCH_AXIS_ASSESSMENT_INVALID", false)
	}
	if criteria.Validate() != nil || len(scores) != len(criteria.Axes) || (locale != "ko-KR" && locale != "en-US") {
		return invalid()
	}
	byID := map[string]AxisScoreV1{}
	for _, s := range scores {
		if _, dup := byID[s.AxisID]; dup || s.ScorePercent < 0 || s.ScorePercent > 100 || strings.TrimSpace(s.Explanation) == "" || len(s.Explanation) > 1600 {
			return invalid()
		}
		if s.Basis != "PROVIDED" && s.Basis != "INFERRED" && s.Basis != "UNKNOWN" {
			return invalid()
		}
		if s.Basis == "UNKNOWN" && len(s.FactIDs) > 0 {
			return nil, fault.New(fault.ProviderRejected, "RESEARCH_AXIS_UNKNOWN_HAS_FACTS", false)
		}
		if s.Basis == "PROVIDED" && len(s.FactIDs) == 0 {
			return nil, fault.New(fault.ProviderRejected, "RESEARCH_AXIS_PROVIDED_WITHOUT_FACTS", false)
		}
		byID[s.AxisID] = s
	}
	// Deep copies detach the persisted assessment from future settings edits.
	criteria.Axes = append([]c.ResearchAxis(nil), criteria.Axes...)
	criteria.Exclusions = append([]string{}, criteria.Exclusions...)
	a := &AxisAssessmentV1{SchemaVersion: "vitlane.axis-assessment.v1", Criteria: criteria, Weights: criteria.Weights(), ContentLocale: locale, RoundID: round, ModelKey: model, CreatedAt: now}
	for i, axis := range criteria.Axes {
		s, ok := byID[axis.AxisID]
		if !ok {
			return invalid()
		}
		s.FactIDs = append([]string{}, s.FactIDs...)
		a.Scores = append(a.Scores, s)
		a.TotalBasisPoints += a.Weights[i] * s.ScorePercent
	}
	a.TotalScore = (a.TotalBasisPoints + 50) / 100
	return a, nil
}
