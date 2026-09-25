package apifyactor

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

// TestLiveActorRunReturnsUsableProducts spends real money, so it runs only
// when both APIFY_LIVE_ACTOR_TEST and APIFY_API_TOKEN are set. It is the one
// check that the mapper still matches what the Actor returns today; the
// benchmark numbers are documented beside the actor registry.
func TestLiveActorRunReturnsUsableProducts(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("APIFY_API_TOKEN"))
	if os.Getenv("APIFY_LIVE_ACTOR_TEST") == "" || token == "" {
		t.Skip("APIFY_LIVE_ACTOR_TEST and APIFY_API_TOKEN required: this test starts a paid Actor run")
	}
	client, err := sharedhttpclient.NewClient(http.DefaultTransport, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ledger := &memoryLedger{}
	control := &memoryControl{}
	gateway, err := New(Config{Token: token, MonthlyCapMicros: 1_000_000, Client: client, Control: control, Ledger: ledger})
	if err != nil {
		t.Fatal(err)
	}
	mall, _ := researchdomain.KoreanMall(researchdomain.SourceMusinsa)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := gateway.SearchMallActor(ctx, researchapp.ActorSearchRequest{
		Mall: mall, Query: "반팔 티셔츠", RunKey: "live-" + time.Now().UTC().Format("20060102150405") + ":MUSINSA",
		UserID: "00000000-0000-4000-8000-000000000001",
	})
	if err != nil {
		t.Fatalf("live run: %v", err)
	}
	if len(result.Products) == 0 {
		t.Fatalf("live run returned no usable product: items=%d rejected=%d", result.Items, result.Rejected)
	}
	for _, product := range result.Products {
		if product.Validate() != nil || product.ProductRef.Source != researchdomain.SourceMusinsa ||
			product.Price.Kind != "OBSERVED" || product.Title == "" {
			t.Fatalf("live observation=%+v", product)
		}
	}
	t.Logf("live run: items=%d usable=%d rejected=%d costMicros=%d", result.Items, len(result.Products), result.Rejected, result.CostMicros)
}
