package research

import (
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	a "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	rd "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

func toCriteria(c *a.ResearchCriteria) *d.TargetCriteriaSetV1 {
	if c == nil {
		return nil
	}
	v := &d.TargetCriteriaSetV1{SchemaVersion: d.CriteriaSchema, Version: c.Version, Subject: d.ResearchSubject{Label: c.Subject.Label, ProductType: c.Subject.ProductType}, Exclusions: append([]string{}, c.Exclusions...)}
	for _, x := range c.Axes {
		v.Axes = append(v.Axes, d.ResearchAxis{AxisID: x.AxisID, Label: x.Label, Definition: x.Definition, Importance: x.Importance, UsesPrice: x.UsesPrice, UsesVisualEvidence: x.UsesVisualEvidence, Origin: x.Origin})
	}
	return v
}
func fromCriteria(c *d.TargetCriteriaSetV1) *a.ResearchCriteria {
	if c == nil {
		return nil
	}
	v := &a.ResearchCriteria{SchemaVersion: c.SchemaVersion, Version: c.Version, Subject: a.ResearchSubject{Label: c.Subject.Label, ProductType: c.Subject.ProductType}, Exclusions: append([]string{}, c.Exclusions...)}
	for _, x := range c.Axes {
		v.Axes = append(v.Axes, a.ResearchAxis{AxisID: x.AxisID, Label: x.Label, Definition: x.Definition, Importance: x.Importance, UsesPrice: x.UsesPrice, UsesVisualEvidence: x.UsesVisualEvidence, Origin: x.Origin})
	}
	return v
}
func toScores(scores []a.AxisScore) []rd.AxisScoreV1 {
	v := make([]rd.AxisScoreV1, 0, len(scores))
	for _, x := range scores {
		v = append(v, rd.AxisScoreV1{AxisID: x.AxisID, ScorePercent: x.ScorePercent, Basis: x.Basis, Explanation: x.Explanation, FactIDs: append([]string{}, x.FactIDs...)})
	}
	return v
}

func fromQuery(q *researchapp.CatalogIntelligenceCatalogQuery) *a.CatalogQueryPayload {
	if q == nil {
		return nil
	}
	return &a.CatalogQueryPayload{ExecutionVersion: q.ExecutionVersion, QueryInputHash: q.QueryInputHash, Projections: fromProjections(q.QueryProjections), Query: q.Query, QuerySeeds: append([]string{}, q.QuerySeeds...), MustInclude: append([]string{}, q.MustInclude...), MustExclude: append([]string{}, q.MustExclude...), Criteria: fromCriteria(q.Criteria), ProductVertical: q.ProductVertical}
}

func fromDiscoveryRequirements(values []researchapp.DiscoveryDescriptor) []a.CatalogLanguageRequirement {
	out := []a.CatalogLanguageRequirement{}
	for _, v := range values {
		out = append(out, a.CatalogLanguageRequirement{Policy: v.LanguagePolicy, Language: string(v.Language)})
	}
	return out
}
func toProjections(values []a.CatalogQueryProjection) []researchapp.CatalogQueryProjection {
	out := []researchapp.CatalogQueryProjection{}
	for _, v := range values {
		out = append(out, researchapp.CatalogQueryProjection{Policy: v.Policy, Language: v.Language, Query: v.Query, Seeds: v.Seeds, MustInclude: v.MustInclude, MustExclude: v.MustExclude})
	}
	return out
}
func fromProjections(values []researchapp.CatalogQueryProjection) []a.CatalogQueryProjection {
	out := []a.CatalogQueryProjection{}
	for _, v := range values {
		out = append(out, a.CatalogQueryProjection{Policy: v.Policy, Language: v.Language, Query: v.Query, Seeds: v.Seeds, MustInclude: v.MustInclude, MustExclude: v.MustExclude})
	}
	return out
}
