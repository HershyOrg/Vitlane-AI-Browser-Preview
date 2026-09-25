package app

type CatalogQueryProjection struct {
	Policy      string   `json:"policy"`
	Language    string   `json:"language"`
	Query       string   `json:"query"`
	Seeds       []string `json:"seeds"`
	MustInclude []string `json:"mustInclude"`
	MustExclude []string `json:"mustExclude"`
}
