package domain

import (
	c "github.com/vitlane/vitlane/server/internal/curation/domain"
	"testing"
	"time"
)

func TestFrozenAxisAssessment(t *testing.T) {
	criteria := c.TargetCriteriaSetV1{SchemaVersion: c.CriteriaSchema, Version: 1, Subject: c.ResearchSubject{Label: "pen", ProductType: "pen"}, Axes: []c.ResearchAxis{{AxisID: "a", Label: "a", Definition: "a", Importance: 5, Origin: "REQUEST"}, {AxisID: "b", Label: "b", Definition: "b", Importance: 4, Origin: "REQUEST"}, {AxisID: "c", Label: "c", Definition: "c", Importance: 3, Origin: "REQUEST"}}}
	scores := []AxisScoreV1{{AxisID: "a", ScorePercent: 90, Basis: "UNKNOWN", Explanation: "unknown"}, {AxisID: "b", ScorePercent: 80, Basis: "INFERRED", Explanation: "inferred"}, {AxisID: "c", ScorePercent: 60, Basis: "PROVIDED", Explanation: "listed", FactIDs: []string{"name"}}}
	result, err := NewAxisAssessment(criteria, scores, "ko-KR", "round", "model", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalScore != 79 || result.TotalBasisPoints != 7920 {
		t.Fatal(result)
	}
	criteria.Axes[0].Importance = 1
	scores[0].ScorePercent = 1
	if result.Criteria.Axes[0].Importance != 5 || result.Scores[0].ScorePercent != 90 {
		t.Fatal("mutable snapshot")
	}
	scores[0].AxisID = "b"
	if _, err := NewAxisAssessment(criteria, scores, "en-US", "r", "m", time.Now()); err == nil {
		t.Fatal("duplicate axis accepted")
	}
}
