package app

import "testing"

func TestCatalogLocalLimitsValidateOperatorConfiguration(t *testing.T) {
	for _, raw := range []string{
		`{"UNKNOWN":{"requestsPerMinute":20,"dailyLimit":200,"maxConcurrent":4}}`,
		`{"ELEVENST_HTML":{"requestsPerMinute":20,"dailyLimit":200,"maxConcurrent":4}}`,
		`{"DAISOMALL_HTML":{"requestsPerMinute":3,"dailyLimit":50,"maxConcurrent":1}}`,
		`{"OWN_PRODUCT":{"requestsPerMinute":20,"dailyLimit":200,"maxConcurrent":0}}`,
		`{"OWN_PRODUCT":null}`, `[]`,
	} {
		if _, err := ParseCatalogLocalLimits(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	got, err := ParseCatalogLocalLimits(`{"OWN_PRODUCT":{"requestsPerMinute":20,"dailyLimit":200,"maxConcurrent":4}}`)
	if err != nil || got["OWN_PRODUCT"].MaxConcurrent != 4 {
		t.Fatalf("%+v %v", got, err)
	}
}
