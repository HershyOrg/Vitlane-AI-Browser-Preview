package main

import (
	"strings"
	"testing"
)

func TestAmazonStubCannotStartInProductionOrFallbackWithoutFixture(t *testing.T) {
	for _, tc := range []struct{ env, mode, file, want string }{
		{"production", "stub", "/tmp/catalog.json", "forbidden in production"},
		{"development", "stub", "", "requires AMAZON_PRODUCT_SEARCH_STUB_FILE"},
		{"test", "invalid", "", "must be live or stub"},
	} {
		t.Run(tc.env+tc.mode, func(t *testing.T) {
			t.Setenv("APP_ENV", tc.env)
			t.Setenv("AMAZON_PRODUCT_SEARCH_MODE", tc.mode)
			t.Setenv("AMAZON_PRODUCT_SEARCH_STUB_FILE", tc.file)
			_, err := loadConfig()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}
