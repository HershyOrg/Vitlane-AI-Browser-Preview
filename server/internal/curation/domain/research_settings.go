package domain

// ResearchSettings is a seed for the next Round, never a binding applied to
// running jobs, existing observations, or the plan's immutable price bounds.
type ResearchSettings struct {
	SchemaVersion string `json:"schemaVersion"`
	Version       int64  `json:"version"`
	Country       string `json:"country"`
}

func SupportedResearchCountry(country string) bool { return country == "KR" || country == "US" }
