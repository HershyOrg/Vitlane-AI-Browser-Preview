package app

import (
	"context"
	"encoding/json"
	"errors"
	c "github.com/vitlane/vitlane/server/internal/curation/domain"
	i "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	id "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	d "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"testing"
	"time"
)

type batchRepository struct {
	BackgroundRepository
	routes    int
	jobs      []MatchJob
	decisions []MatchDecision
	published bool
}

func (*batchRepository) ExpireSubscriptions(context.Context) error { return nil }
func (*batchRepository) ClaimBackgroundTask(_ context.Context, name string, _ time.Duration) (bool, error) {
	return name == "broken-feed", nil
}
func (r *batchRepository) RouteDeals(context.Context) error                 { r.routes++; return nil }
func (r *batchRepository) ClaimMatches(context.Context) ([]MatchJob, error) { return r.jobs, nil }
func (r *batchRepository) FinishMatches(_ context.Context, _ []MatchJob, d []MatchDecision) error {
	r.decisions = d
	return nil
}
func (r *batchRepository) PublishFindings(context.Context) error { r.published = true; return nil }

type batchProvider struct {
	t     *testing.T
	calls int
}

func (*batchProvider) Kind() id.ProviderKind                   { return id.ProviderManaged }
func (*batchProvider) Available(context.Context, string) error { return nil }
func (p *batchProvider) Complete(_ context.Context, in i.CompletionRequest) (i.CompletionResult, error) {
	p.calls++
	if in.UserID != "" {
		p.t.Fatal("shared matching charged a user")
	}
	var payload struct {
		Products map[string]json.RawMessage `json:"products"`
		Terms    map[string]map[string]any  `json:"acceptedSubscriptions"`
		Pairs    []map[string]string        `json:"pairs"`
	}
	if e := json.Unmarshal([]byte(in.UserPrompt), &payload); e != nil {
		p.t.Fatal(e)
	}
	if len(payload.Products) != 2 || len(payload.Terms) != 1 || len(payload.Pairs) != 2 {
		p.t.Fatalf("not batched/deduplicated: %+v", payload)
	}
	for _, terms := range payload.Terms {
		if _, ok := terms["expiresAt"]; ok {
			p.t.Fatal("cached expiry entered shared relevance")
		}
	}
	return i.CompletionResult{Content: `{"decisions":[{"id":"1","match":true,"reason":"matches"},{"id":"2","match":false,"reason":"wrong kind"}]}`}, nil
}

type brokenFeed struct{}

func (brokenFeed) Name() string { return "broken-feed" }
func (brokenFeed) Poll(context.Context) ([]d.DealProduct, error) {
	return nil, errors.New("feed offline")
}
func TestBackgroundBatchesProductsAndContinuesAfterOneFeedFails(t *testing.T) {
	terms := c.SubscriptionTerms{Country: "KR", Currency: "KRW", Keywords: []string{"cookware"}, ExpiresAt: time.Now().Add(-time.Hour)}
	product := d.DealProduct{Provider: "TEST", ExternalID: "1", Title: "Pan", Country: "KR", ExpiresAt: time.Now().Add(time.Hour)}
	second := product
	second.ExternalID = "2"
	second.Title = "Shoes"
	repo := &batchRepository{jobs: []MatchJob{{ID: "1", Token: "t1", Product: product, Terms: terms}, {ID: "2", Token: "t2", Product: second, Terms: terms}}}
	provider := &batchProvider{t: t}
	service := BackgroundService{Repository: repo, Provider: provider, Model: "test", Enabled: true, Feeds: []DealFeed{brokenFeed{}}}
	if e := service.Tick(context.Background()); e == nil {
		t.Fatal("feed failure was hidden")
	}
	if provider.calls != 1 || len(repo.decisions) != 2 || !repo.published || repo.routes < 2 {
		t.Fatalf("incomplete batch: %+v calls=%d", repo, provider.calls)
	}
}
