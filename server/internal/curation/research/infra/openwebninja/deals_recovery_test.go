package openwebninja

import (
	"context"
	a "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type exhaustedDealUsage struct{ *testUsage }

func (u exhaustedDealUsage) ReadAmazonUsage(context.Context) (a.CatalogAPIUsage, error) {
	remaining := int64(0)
	return a.CatalogAPIUsage{Quota: u.quota, EstimatedRemaining: &remaining}, nil
}
func TestExhaustedDealsDoNotPollUsageBeforeReset(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	usage := exhaustedDealUsage{freshUsage()}
	g, err := New(Config{APIKey: "test-only", Client: server.Client(), Usage: usage, Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Poll(context.Background()); err == nil || calls != 0 || usage.calls != 0 {
		t.Fatalf("exhausted feed called provider: %d %v", calls, err)
	}
	if g.PollInterval() != 24*time.Hour {
		t.Fatal("unexpected background interval")
	}
}
