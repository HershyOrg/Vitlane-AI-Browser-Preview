package domain

import (
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"sort"
	"strings"
)

const CriteriaSchema = "vitlane.target-criteria.v1"

type ResearchAxis struct {
	AxisID             string `json:"axisId"`
	Label              string `json:"label"`
	Definition         string `json:"definition"`
	Importance         int    `json:"importance"`
	UsesPrice          bool   `json:"usesPrice"`
	UsesVisualEvidence bool   `json:"usesVisualEvidence"`
	Origin             string `json:"origin"`
}

type ResearchSubject struct {
	Label       string `json:"label"`
	ProductType string `json:"productType"`
}

// Criteria are mutable Curation settings. PlanTarget and Candidate snapshots
// retain the values with which they were created.
type TargetCriteriaSetV1 struct {
	SchemaVersion string          `json:"schemaVersion"`
	Version       int64           `json:"version"`
	Subject       ResearchSubject `json:"subject"`
	Axes          []ResearchAxis  `json:"axes"`
	Exclusions    []string        `json:"exclusions"`
}

func (c TargetCriteriaSetV1) Validate() error {
	invalid := func() error { return fault.New(fault.InvalidInput, "RESEARCH_CRITERIA_INVALID", false) }
	if c.SchemaVersion != CriteriaSchema || c.Version < 1 || len(c.Axes) < 1 || len(c.Axes) > 8 || strings.TrimSpace(c.Subject.Label) == "" || len(c.Subject.Label) > 300 || strings.TrimSpace(c.Subject.ProductType) == "" || len(c.Subject.ProductType) > 300 || len(c.Exclusions) > 20 {
		return invalid()
	}
	seen := map[string]bool{}
	for _, a := range c.Axes {
		if a.AxisID == "" || len(a.AxisID) > 100 || seen[a.AxisID] || strings.TrimSpace(a.Label) == "" || len(a.Label) > 120 || strings.TrimSpace(a.Definition) == "" || len(a.Definition) > 600 || a.Importance < 1 || a.Importance > 5 {
			return invalid()
		}
		if a.Origin != "REQUEST" && a.Origin != "FEEDBACK" && a.Origin != "USER_EDIT" {
			return invalid()
		}
		seen[a.AxisID] = true
	}
	for _, e := range c.Exclusions {
		if strings.TrimSpace(e) == "" || len(e) > 300 {
			return invalid()
		}
	}
	return nil
}

// An axis ID represents meaning, never position or its translated label.
func (c TargetCriteriaSetV1) ValidateSuccessor(next TargetCriteriaSetV1) error {
	if err := next.Validate(); err != nil {
		return err
	}
	if c.Subject.ProductType != next.Subject.ProductType {
		return fault.New(fault.InvalidInput, "RESEARCH_SUBJECT_CHANGED", false)
	}
	for _, old := range c.Axes {
		for _, a := range next.Axes {
			if old.AxisID == a.AxisID && (old.Definition != a.Definition || old.UsesPrice != a.UsesPrice || old.UsesVisualEvidence != a.UsesVisualEvidence) {
				return fault.New(fault.InvalidInput, "RESEARCH_AXIS_MEANING_CHANGED", false)
			}
		}
	}
	return nil
}

// Largest remainders allocate exactly 100 integer points. Stable axis IDs
// break ties, independently of labels and floating point rounding.
func (c TargetCriteriaSetV1) Weights() []int {
	weights := make([]int, len(c.Axes))
	total := 0
	for _, a := range c.Axes {
		total += a.Importance
	}
	if total <= 0 {
		return weights
	}
	order := make([]int, len(c.Axes))
	allocated := 0
	for i, a := range c.Axes {
		order[i] = i
		weights[i] = 100 * a.Importance / total
		allocated += weights[i]
	}
	sort.SliceStable(order, func(i, j int) bool {
		left, right := (100*c.Axes[order[i]].Importance)%total, (100*c.Axes[order[j]].Importance)%total
		if left == right {
			return c.Axes[order[i]].AxisID < c.Axes[order[j]].AxisID
		}
		return left > right
	})
	for i := 0; i < 100-allocated; i++ {
		weights[order[i]]++
	}
	return weights
}
