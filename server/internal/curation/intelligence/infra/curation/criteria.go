package curation

import (
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	a "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
)

func toCriteria(c *a.ResearchCriteria) *d.TargetCriteriaSetV1 {
	if c == nil {
		return nil
	}
	v := &d.TargetCriteriaSetV1{SchemaVersion: d.CriteriaSchema, Version: 1, Subject: d.ResearchSubject{Label: c.Subject.Label, ProductType: c.Subject.ProductType}, Exclusions: append([]string{}, c.Exclusions...)}
	for _, x := range c.Axes {
		v.Axes = append(v.Axes, d.ResearchAxis{AxisID: x.AxisID, Label: x.Label, Definition: x.Definition, Importance: x.Importance, UsesPrice: x.UsesPrice, UsesVisualEvidence: x.UsesVisualEvidence, Origin: x.Origin})
	}
	return v
}
