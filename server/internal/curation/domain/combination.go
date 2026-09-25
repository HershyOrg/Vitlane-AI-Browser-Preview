package domain

// CombinationResponse is a historical recommendation, never a checkout quote.
// Versions fence Cart application; listing prices are not a catalog cache.
type CombinationResponse struct {
	SchemaVersion    string            `json:"schemaVersion"`
	ID               string            `json:"id"`
	Kind             string            `json:"kind"`
	Items            []CombinationItem `json:"items"`
	Currency         string            `json:"currency"`
	MinimumMinor     *int64            `json:"minimumMinor"`
	MaximumMinor     *int64            `json:"maximumMinor"`
	BudgetMinor      *int64            `json:"budgetMinor"`
	DifferenceMinor  *int64            `json:"differenceMinor"`
	BudgetStatus     string            `json:"budgetStatus"`
	Estimated        bool              `json:"estimated"`
	PriceBasis       string            `json:"priceBasis"`
	MissingTargetIDs []string          `json:"missingTargetIds"`
	SourceState      string            `json:"sourceState"`
	BudgetVersion    int64             `json:"budgetVersion"`
	CartVersion      int64             `json:"cartVersion"`
	CriteriaVersions map[string]int64  `json:"criteriaVersions"`
	Compatibility    string            `json:"compatibility"`
	Reasons          []string          `json:"reasons"`
	Tips             []CombinationTip  `json:"tips"`
	Cautions         []string          `json:"cautions"`
	BudgetAdvice     string            `json:"budgetAdvice"`
}

type CombinationItem struct {
	Ref                  string `json:"ref"`
	TargetID             string `json:"targetId"`
	CandidateID          string `json:"candidateId"`
	VariantID            string `json:"variantId,omitempty"`
	ConfigurationVersion int64  `json:"configurationVersion"`
	Quantity             int    `json:"quantity"`
	CartItemID           string `json:"cartItemId,omitempty"`
	CheckoutEligible     bool   `json:"checkoutEligible"`
	AllocationFit        string `json:"allocationFit"`
}

type CombinationTip struct {
	Label string `json:"label"`
	Body  string `json:"body"`
}
