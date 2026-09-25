package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestFindingImportOnlyGetsBoundedLongerTimeout(t *testing.T) {
	for _, v := range []struct {
		method, path string
		want         time.Duration
	}{
		{"POST", "/api/v1/curations/a/findings/b/candidates", time.Minute},
		{"GET", "/api/v1/curations/a/findings/b/candidates", 0},
		{"POST", "/api/v1/curations/a/findings/b/hide", 0},
		{"POST", "/api/v1/curations/product-notices/sync", 0},
		{"POST", "/api/v1/shopping-plans", 0},
	} {
		if got := findingImportTimeout(httptest.NewRequest(v.method, v.path, nil)); got != v.want {
			t.Fatalf("%s %s: %v", v.method, v.path, got)
		}
	}
}
