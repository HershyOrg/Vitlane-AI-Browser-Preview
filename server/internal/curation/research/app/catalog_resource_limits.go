package app

import (
	"encoding/json"
	"fmt"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

type CatalogLocalLimits struct {
	RequestsPerMinute int `json:"requestsPerMinute"`
	DailyLimit        int `json:"dailyLimit"`
	MaxConcurrent     int `json:"maxConcurrent"`
}

func ParseCatalogLocalLimits(raw string) (map[string]CatalogLocalLimits, error) {
	limits := map[string]CatalogLocalLimits{}
	if raw == "" {
		return limits, nil
	}
	if err := json.Unmarshal([]byte(raw), &limits); err != nil {
		return nil, fmt.Errorf("CATALOG_API_LIMITS_JSON is invalid")
	}
	for id, l := range limits {
		d, ok := CatalogAPIDefinitionFor(id)
		if !ok || l.RequestsPerMinute < 1 || l.RequestsPerMinute > 1000 || l.DailyLimit < 1 || l.DailyLimit > 100000 || l.MaxConcurrent < 1 || l.MaxConcurrent > 8 || (d.Keyless && l.MaxConcurrent > 1) {
			return nil, fmt.Errorf("CATALOG_API_LIMITS_JSON contains unsupported limits for %s", id)
		}
		if id == "DAISOMALL_HTML" && l.RequestsPerMinute > 2 {
			return nil, fmt.Errorf("Daiso rate must remain at most 2/minute")
		}
	}
	return limits, nil
}

// Mall hosts are rate-limit resources, not a second scheduler.
func CatalogRateResource(id string) string {
	for _, mall := range researchdomain.KoreanMalls() {
		if mall.DetailAPI == id && mall.DetailHost != "" {
			return "host:" + mall.DetailHost
		}
	}
	return "api:" + id
}
func CatalogActorReservationMicros(id string) int64 {
	switch id {
	case "APIFY_MUSINSA", "APIFY_29CM":
		return 25000
	case "APIFY_GMARKET":
		return 10000
	}
	return 0
}
