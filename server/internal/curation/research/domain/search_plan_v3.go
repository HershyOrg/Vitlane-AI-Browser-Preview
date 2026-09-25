package domain

import (
	"errors"
	"strings"
	"unicode"

	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

const (
	SearchPlanSchemaV3 = "vitlane.research-search-plan.v3"
	// SearchPlanMaximumQueries is the primary query plus the model's seeds.
	SearchPlanMaximumQueries = 5
	// SearchPlanMaximumQueryLength is the longest phrase a source is ever handed.
	SearchPlanMaximumQueryLength = 300
)

var ErrSearchPlanInvalid = errors.New("RESEARCH_SEARCH_PLAN_INVALID")

// SearchLanguage is the language a source's catalog is searched in. A Korean
// mall is asked in Korean and a US catalog in English; the plan prepares the
// phrases per language so no source has to translate or guess.
type SearchLanguage string

const (
	SearchLanguageEnglish SearchLanguage = "en"
	SearchLanguageKorean  SearchLanguage = "ko"
)

func (language SearchLanguage) Valid() bool {
	return language == SearchLanguageEnglish || language == SearchLanguageKorean
}

// SearchPlanQuery is one phrase to search for, in one language. Order matters:
// the first query of a language is the primary one and the rest are the seeds
// a source moves on to when the primary runs out of pages.
type SearchPlanQuery struct {
	Language SearchLanguage `json:"language"`
	Text     string         `json:"text"`
}

// SearchPlanRequired is what every admitted product must satisfy, whichever
// source found it. A source may push a condition into its own request (a price
// filter, a ships-to country) but the same condition is checked again on the
// normalised observation, so a source that cannot filter is still held to it.
type SearchPlanRequired struct {
	Market shareddomain.MarketContext `json:"market"`
	// Price bounds are in the market currency's minor units. Nil means no bound.
	MinimumMinor *int64 `json:"minimumMinor,omitempty"`
	MaximumMinor *int64 `json:"maximumMinor,omitempty"`
	// Exclusions reject a listing that mentions one of them.
	Exclusions []string `json:"exclusions"`
	// MustContain are explicit user constraints a listing has to mention.
	MustContain []string `json:"mustContain"`
	// Conditions are product conditions a source that can filter should ask for.
	Conditions []string `json:"conditions"`
}

// SearchPlanPreferred never rejects a product. A source may use it to shape or
// rank its request; evaluation weighs it afterwards.
type SearchPlanPreferred struct {
	Exclusions      []string `json:"exclusions,omitempty"`
	Terms           []string `json:"terms"`
	ProductVertical string   `json:"productVertical,omitempty"`
	Categories      []string `json:"categories"`
}

// SearchPlan is the one provider-neutral description of what a Round looks
// for: the phrases, what is required and what is preferred. It is built once
// from the user's intent, the research criteria, the market and the budget.
// Every source adapter translates it into its own request format; none of
// them builds a query of its own or applies a rule the plan does not state.
type SearchPlan struct {
	SchemaVersion string              `json:"schemaVersion"`
	TargetID      string              `json:"targetId"`
	ProductType   string              `json:"productType"`
	Queries       []SearchPlanQuery   `json:"queries"`
	Required      SearchPlanRequired  `json:"required"`
	Preferred     SearchPlanPreferred `json:"preferred"`
	Limit         int                 `json:"limit"`
}

type NewSearchPlanInput struct {
	TargetID    string
	ProductType string
	Queries     []SearchPlanQuery
	Required    SearchPlanRequired
	Preferred   SearchPlanPreferred
	Limit       int
}

// NewSearchPlan normalises and validates a plan. Duplicate phrases of one
// language collapse onto their first position, because two sources must never
// disagree about which query is "the second one".
func NewSearchPlan(input NewSearchPlanInput) (SearchPlan, error) {
	plan := SearchPlan{
		SchemaVersion: SearchPlanSchemaV3,
		TargetID:      strings.TrimSpace(input.TargetID),
		ProductType:   strings.TrimSpace(input.ProductType),
		Queries:       []SearchPlanQuery{},
		Required: SearchPlanRequired{
			Market:       input.Required.Market,
			MinimumMinor: cloneSearchPlanMinor(input.Required.MinimumMinor),
			MaximumMinor: cloneSearchPlanMinor(input.Required.MaximumMinor),
			Exclusions:   cleanSearchPlanTerms(input.Required.Exclusions),
			MustContain:  cleanSearchPlanTerms(input.Required.MustContain),
			Conditions:   cleanSearchPlanTerms(input.Required.Conditions),
		},
		Preferred: SearchPlanPreferred{
			Terms:           cleanSearchPlanTerms(input.Preferred.Terms),
			Exclusions:      cleanSearchPlanTerms(input.Preferred.Exclusions),
			ProductVertical: strings.TrimSpace(input.Preferred.ProductVertical),
			Categories:      cleanSearchPlanTerms(input.Preferred.Categories),
		},
		Limit: input.Limit,
	}
	seen := map[string]bool{}
	for _, query := range input.Queries {
		text := strings.Join(strings.Fields(query.Text), " ")
		key := string(query.Language) + "\x00" + strings.ToLower(text)
		if text == "" || seen[key] {
			continue
		}
		seen[key] = true
		plan.Queries = append(plan.Queries, SearchPlanQuery{Language: query.Language, Text: text})
	}
	if err := plan.Validate(); err != nil {
		return SearchPlan{}, err
	}
	return plan, nil
}

// Validate states every rule a query has to meet, once, for all sources. There
// is deliberately no minimum word count: "sunscreen" is a complete query.
func (plan SearchPlan) Validate() error {
	if plan.SchemaVersion != SearchPlanSchemaV3 || plan.TargetID == "" ||
		plan.Limit <= 0 || plan.Limit > CatalogSearchPlanMaximumLimit ||
		len(plan.Queries) == 0 || len(plan.Queries) > 2*SearchPlanMaximumQueries {
		return ErrSearchPlanInvalid
	}
	market, err := shareddomain.NewMarketContext(
		string(plan.Required.Market.Country), string(plan.Required.Market.Currency),
	)
	if err != nil || market != plan.Required.Market {
		return ErrSearchPlanInvalid
	}
	perLanguage := map[SearchLanguage]int{}
	for _, query := range plan.Queries {
		perLanguage[query.Language]++
		if !query.Language.Valid() || perLanguage[query.Language] > SearchPlanMaximumQueries ||
			!validSearchPlanPhrase(query.Language, query.Text) {
			return ErrSearchPlanInvalid
		}
	}
	minimum, maximum := plan.Required.MinimumMinor, plan.Required.MaximumMinor
	if (minimum != nil && *minimum < 0) || (maximum != nil && *maximum < 0) ||
		(minimum != nil && maximum != nil && *minimum > *maximum) {
		return ErrSearchPlanInvalid
	}
	return nil
}

// QueriesFor returns the phrases prepared in one language, primary first.
func (plan SearchPlan) QueriesFor(language SearchLanguage) []string {
	queries := []string{}
	for _, query := range plan.Queries {
		if query.Language == language {
			queries = append(queries, query.Text)
		}
	}
	return queries
}

// HasPriceBound reports whether a product's price decides its admission.
func (plan SearchPlan) HasPriceBound() bool {
	return plan.Required.MinimumMinor != nil || plan.Required.MaximumMinor != nil
}

func validSearchPlanPhrase(language SearchLanguage, text string) bool {
	if text == "" || len(text) > SearchPlanMaximumQueryLength || text != strings.TrimSpace(text) {
		return false
	}
	hasLetterOrDigit := false
	for _, current := range text {
		if unicode.IsControl(current) {
			return false
		}
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			hasLetterOrDigit = true
		}
		// An English catalog is indexed in Latin script: a Korean phrase handed
		// to it finds nothing and must never be sent.
		if language == SearchLanguageEnglish && unicode.IsLetter(current) && !unicode.In(current, unicode.Latin) {
			return false
		}
	}
	return hasLetterOrDigit
}

func cleanSearchPlanTerms(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func cloneSearchPlanMinor(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
