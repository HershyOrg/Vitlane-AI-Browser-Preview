package app

// The runner constrains every model call to a closed JSON schema. This is what
// removes the need for a tool-calling loop: each step has one shape in and one
// shape out, so the number of calls per WorkOrder is fixed and the worst-case
// cost is known before the first token is spent.
//
// Only ordering and prose come from the model. Every factual field a Candidate
// carries — URL, price, seller, variant — is copied from a server-issued
// CatalogObservation, so the model has nothing to hallucinate into an order.
const (
	SchemaPlanningTargets  = "vitlane_planning_targets"
	SchemaCatalogQuery     = "vitlane_catalog_query"
	SchemaCandidateRanking = "vitlane_candidate_ranking"
)

func PlanningTargetsSchema(maximumTargets int) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"targets"},
		"properties": map[string]any{
			"targets": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": maximumTargets,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"title", "category", "searchQuery", "rationale", "productVertical",
					},
					"properties": map[string]any{
						"productVertical": productVerticalSchema(),
						"title": map[string]any{
							"type":        "string",
							"description": "Short shopping item name in the user's language.",
						},
						"category": map[string]any{
							"type":        "string",
							"description": "Product category for this item.",
						},
						"searchQuery": map[string]any{
							"type":        "string",
							"description": "English catalog search phrase using the common retail product name; product words only.",
							"minLength":   1,
							"pattern":     "^[A-Za-z0-9][A-Za-z0-9 .+'()/&-]*$",
						},
						"rationale": map[string]any{
							"type":        "string",
							"description": "One sentence on why this item belongs to the request.",
						},
					},
				},
			},
		},
	}
}

func CatalogQuerySchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"query", "mustInclude", "mustExclude", "criteria", "querySeeds", "productVertical"},
		"properties": map[string]any{
			"criteria":        criteriaSchema(),
			"querySeeds":      map[string]any{"type": "array", "minItems": 1, "maxItems": 4, "items": map[string]any{"type": "string"}},
			"productVertical": productVerticalSchema(),
			"query": map[string]any{
				"type":        "string",
				"description": "Catalog search phrase, product words only.",
			},
			"mustInclude": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"mustExclude": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	}
}

func CandidateRankingSchema(candidateCount int, axisCounts ...int) map[string]any {
	axes := axisScoreSchema()
	if len(axisCounts) > 0 && axisCounts[0] > 0 {
		axes["minItems"] = axisCounts[0]
		axes["maxItems"] = axisCounts[0]
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"ranked"},
		"properties": map[string]any{
			"ranked": map[string]any{
				"type":     "array",
				"minItems": candidateCount,
				"maxItems": candidateCount,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"observationId", "intentPoint", "features", "specifications", "axisScores",
					},
					"properties": map[string]any{
						"axisScores": axes,
						"observationId": map[string]any{
							"type":        "string",
							"description": "MUST be one of the observation ids listed in the prompt. Never invent one.",
						},
						"intentPoint": map[string]any{
							"type":        "string",
							"minLength":   1,
							"maxLength":   400,
							"description": "One concise sentence explaining the fit using only the listed observation.",
						},
						"features": map[string]any{
							"type":     "array",
							"maxItems": 8,
							"items": map[string]any{
								"type": "string", "minLength": 1, "maxLength": 240,
							},
						},
						"specifications": map[string]any{
							"type":     "array",
							"maxItems": 8,
							"items": map[string]any{
								"type": "string", "minLength": 1, "maxLength": 240,
							},
						},
					},
				},
			},
		},
	}
}

// Decoded shapes for the three calls.

type PlanningBudgetDecision struct {
	Kind     string `json:"kind"`
	Evidence string `json:"evidence"`
}
type PlanningTargetsPayload struct {
	BudgetDecision *PlanningBudgetDecision `json:"budgetDecision,omitempty"`
	Estimates      []BudgetEstimate        `json:"estimates,omitempty"`
	Budget         *struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"budget"`
	Targets []struct {
		ProductVertical string            `json:"productVertical"`
		Criteria        *ResearchCriteria `json:"criteria"`
		Quantity        int               `json:"quantity"`
		Title           string            `json:"title"`
		Category        string            `json:"category"`
		SearchQuery     string            `json:"searchQuery"`
		Rationale       string            `json:"rationale"`
	} `json:"targets"`
}

type CatalogQueryPayload struct {
	ExecutionVersion string                   `json:"-"`
	QueryInputHash   string                   `json:"-"`
	Projections      []CatalogQueryProjection `json:"-"`
	ModelKey         string                   `json:"-"`
	Criteria         *ResearchCriteria        `json:"criteria"`
	QuerySeeds       []string                 `json:"querySeeds"`
	Query            string                   `json:"query"`
	MustInclude      []string                 `json:"mustInclude"`
	MustExclude      []string                 `json:"mustExclude"`
	// ProductVertical is optional; an absent or unknown value reaches only
	// general malls. The registry maps it to malls deterministically.
	ProductVertical string `json:"productVertical,omitempty"`
}

type CandidateRankingPayload struct {
	Ranked []struct {
		AxisScores     []AxisScore `json:"axisScores"`
		ObservationID  string      `json:"observationId"`
		IntentPoint    string      `json:"intentPoint"`
		Features       []string    `json:"features"`
		Specifications []string    `json:"specifications"`
	} `json:"ranked"`
}

func planningSchema(context PlanningContext, maximum int) map[string]any {
	schema := PlanningTargetsSchemaForCountry(maximum, context.Country)
	properties0 := schema["properties"].(map[string]any)
	item0 := properties0["targets"].(map[string]any)["items"].(map[string]any)
	item0["properties"].(map[string]any)["criteria"] = criteriaSchema()
	item0["required"] = append(item0["required"].([]string), "criteria")
	if !context.BudgetPolicy {
		return schema
	}
	properties := schema["properties"].(map[string]any)
	item := properties["targets"].(map[string]any)["items"].(map[string]any)
	item["properties"].(map[string]any)["quantity"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 99, "description": "Requested product count; default 1. Never interpret model numbers as quantity."}
	item["required"] = append(item["required"].([]string), "quantity")
	if context.InitialRun && context.BudgetInferenceAllowed {
		properties["budget"] = map[string]any{"anyOf": []any{
			map[string]any{"type": "null"},
			map[string]any{"type": "object", "additionalProperties": false, "required": []string{"amount", "currency"}, "properties": map[string]any{
				"amount":   map[string]any{"type": "string", "pattern": "^[0-9]+(\\.[0-9]{1,2})?$"},
				"currency": map[string]any{"type": "string", "enum": inferredBudgetCurrencies(context)},
			}},
		}}
		schema["required"] = append(schema["required"].([]string), "budget")
	}
	if context.UnifiedAuto && context.BudgetPolicy {
		if context.BudgetAllocationMode != "EQUAL" {
			properties["estimates"] = map[string]any{"type": "array", "maxItems": maximum, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"amount"}, "properties": map[string]any{"amount": map[string]any{"type": "string", "pattern": "^[0-9]+(\\.[0-9]{1,2})?$"}}}}
			estimates := properties["estimates"].(map[string]any)
			if context.BudgetEnabled {
				estimates["minItems"] = 1
			}
			if context.BudgetCurrency == "KRW" {
				estimates["items"].(map[string]any)["properties"].(map[string]any)["amount"].(map[string]any)["pattern"] = "^[1-9][0-9]*$"
			}
			schema["required"] = append(schema["required"].([]string), "estimates")
		}
		if context.InitialRun && context.BudgetInferenceAllowed {
			properties["budgetDecision"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"kind", "evidence"}, "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"KEEP", "ENABLE", "SET_TOTAL", "DISABLE"}}, "evidence": map[string]any{"type": "string", "maxLength": 240}}}
			schema["required"] = append(schema["required"].([]string), "budgetDecision")
		}
	}
	return schema
}

func productVerticalSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"GENERAL", "FASHION", "BEAUTY", "FOOD", "LIVING", "ELECTRONICS"},
		"description": "Exactly one routing family: sneakers/cardigan FASHION; sunscreen BEAUTY; milk/water FOOD; storage/kitchenware LIVING; vacuum/laptop ELECTRONICS; ambiguous or mixed request GENERAL. Never add LIVING to a vacuum."}
}
