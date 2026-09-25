package app

import "strings"

// Intelligence's port DTOs; Curation validates and owns the durable settings.
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
type ResearchCriteria struct {
	SchemaVersion string          `json:"schemaVersion"`
	Version       int64           `json:"version"`
	Subject       ResearchSubject `json:"subject"`
	Axes          []ResearchAxis  `json:"axes"`
	Exclusions    []string        `json:"exclusions"`
}
type AxisScore struct {
	AxisID       string   `json:"axisId"`
	ScorePercent int      `json:"scorePercent"`
	Basis        string   `json:"basis"`
	Explanation  string   `json:"explanation"`
	FactIDs      []string `json:"factIds"`
}

func criteriaSchema() map[string]any {
	str := func() map[string]any { return map[string]any{"type": "string"} }
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"subject", "axes", "exclusions"}, "properties": map[string]any{
		"subject":    map[string]any{"type": "object", "additionalProperties": false, "required": []string{"label", "productType"}, "properties": map[string]any{"label": str(), "productType": str()}},
		"exclusions": map[string]any{"type": "array", "maxItems": 20, "items": str()},
		"axes": map[string]any{"type": "array", "minItems": 1, "maxItems": 8, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"axisId", "label", "definition", "importance", "usesPrice", "usesVisualEvidence", "origin"}, "properties": map[string]any{
			"axisId": str(), "label": str(), "definition": str(), "importance": map[string]any{"type": "integer", "minimum": 1, "maximum": 5}, "usesPrice": map[string]any{"type": "boolean"}, "usesVisualEvidence": map[string]any{"type": "boolean"}, "origin": map[string]any{"type": "string", "enum": []string{"REQUEST", "FEEDBACK", "USER_EDIT"}},
		}}},
	}}
}
func axisScoreSchema() map[string]any {
	return map[string]any{"type": "array", "minItems": 1, "maxItems": 8, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"axisId", "scorePercent", "basis", "explanation", "factIds"}, "properties": map[string]any{
		"axisId": map[string]any{"type": "string"}, "scorePercent": map[string]any{"type": "integer", "minimum": 0, "maximum": 100}, "basis": map[string]any{"type": "string", "enum": []string{"PROVIDED", "INFERRED", "UNKNOWN"}}, "explanation": map[string]any{"type": "string", "minLength": 1, "maxLength": 400}, "factIds": map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string"}},
	}}}
}

const criteriaInstructions = `
Create explicit comparison criteria. Subject.label is only the short product name; subject.productType is the common retail product type in the source search language. Axes capture the user's wishes, not generic hidden scoring factors. Each axis has importance 1..5. Only an explicitly requested price/value axis usesPrice=true. Mark visible design/color axes usesVisualEvidence=true. Without any preference create one honest general suitability axis. Use stable semantic axisId strings. Existing criteria are immutable input for this research: copy the entire subject, axes, importance, definitions, flags and exclusions exactly. Feedback can refine product discovery, never modify these saved settings, even when it explicitly asks to add or remove axes or change importance. Only the separate settings editor can change existing criteria. Exclusions are hard rejections, not scoring axes. Ignore pin/like/dislike. Create REQUEST axes only when no criteria exist for a new target. Copy the existing subject (including productType) exactly; do not translate or replace it. Copy existing axis labels and definitions exactly even when contentLocale changes. Only newly generated labels/definitions and assessment explanations follow contentLocale, independently of source query language.
`

func researchImages(c ResearchContext, items []ResearchCandidateObservation) []CompletionImage {
	if c.Criteria == nil {
		return nil
	}
	visual := false
	for _, a := range c.Criteria.Axes {
		visual = visual || a.UsesVisualEvidence
	}
	if !visual {
		return nil
	}
	images := []CompletionImage{}
	for _, o := range items {
		if o.ImageURL != "" {
			images = append(images, CompletionImage{ObservationID: o.ObservationID, URL: o.ImageURL})
		}
	}
	return images
}

func researchCriteriaInstructions(feedback bool) string {
	if !feedback {
		return criteriaInstructions
	}
	return strings.Replace(criteriaInstructions, "Existing criteria are immutable input for this research: copy the entire subject, axes, importance, definitions, flags and exclusions exactly. Feedback can refine product discovery, never modify these saved settings, even when it explicitly asks to add or remove axes or change importance. Only the separate settings editor can change existing criteria.", "This explicitly selected manual research primitive may apply requested feedback to criteria. Preserve unchanged axes, subject.productType, definitions and flags. Change importance or add FEEDBACK axes only when requested; remove axes only when explicitly requested. Changed meaning requires a new axisId. Budget changes are outside this primitive and must not be expressed as numerical price criteria.", 1)
}
