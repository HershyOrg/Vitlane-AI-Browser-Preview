package koreancatalog

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

type fakeActor struct {
	mu         sync.Mutex
	configured bool
	requests   []researchapp.ActorSearchRequest
	result     researchapp.ActorSearchResult
	err        error
}

func (a *fakeActor) Configured() bool { return a.configured }

func (a *fakeActor) SearchMallActor(_ context.Context, request researchapp.ActorSearchRequest) (researchapp.ActorSearchResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, request)
	return a.result, a.err
}

func actorObservation(t *testing.T, source researchdomain.Source, id string, won int64) researchdomain.ExternalProductObservation {
	t.Helper()
	ref := researchdomain.SourceProductRef{Source: source, ProductID: id, Marketplace: "KR"}
	canonical, err := ref.ExternalProductURL()
	if err != nil {
		t.Fatal(err)
	}
	return researchdomain.ExternalProductObservation{
		SchemaVersion: "vitlane.external-product-observation.v1", ProductRef: ref, ProductURL: canonical,
		Title: "테스트 상품", Price: researchdomain.VariantObservedPrice{Kind: "OBSERVED", AmountMinor: &won, Currency: "KRW"},
		PriceScope: "PRODUCT", Seller: researchdomain.ObservedSeller{Kind: "UNKNOWN"},
		Provenance: researchdomain.ProductProvenance{APIProvider: "Apify", APIProduct: "kdatafactory~musinsa-scraper",
			DiscoveryChannel: "ACTOR_SEARCH", Country: "KR", QueryLanguage: "ko"},
		ObservedAt: time.Date(2026, 9, 16, 4, 0, 0, 0, time.UTC),
	}
}

// A fashion Round asks the Actor malls with one run key per attempt and mall,
// and their products join the Round with their own coverage row.
func TestFashionRoundAsksActorMallsOncePerAttempt(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/usage":
			return usageBody(), nil
		case r.URL.Path == "/realtime-product-search/v2/search":
			return ownEmpty(), nil
		case r.URL.Path == "/search/v1/webkr":
			return response(200, `{"items":[]}`, "text/plain"), nil
		case r.URL.Path == "/realtime-web-search/search":
			return response(200, `{"status":"OK","data":{"organic_results":[]}}`, "application/json"), nil
		}
		t.Fatalf("unexpected request %s%s", r.URL.Host, r.URL.Path)
		return nil, nil
	})
	actor := &fakeActor{configured: true, result: researchapp.ActorSearchResult{
		Products: []researchdomain.ExternalProductObservation{actorObservation(t, researchdomain.SourceMusinsa, "5198233", 39000)},
		Items:    5, Rejected: 4,
	}}
	g, err := New(Config{Enabled: true, OWNKey: "k", NaverClientID: "n", NaverClientSecret: "s",
		Control: &admissionControl{}, Client: &http.Client{Transport: transport}, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	result, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{
		Query: "반팔 티셔츠", Country: "KR", Vertical: "FASHION", Seeds: []string{"반팔 티셔츠"},
		UserID: "user-1", AttemptKey: "attempt-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, request := range actor.requests {
		keys[request.RunKey] = true
		if request.UserID != "user-1" || request.Query != "반팔 티셔츠" {
			t.Fatalf("actor request=%+v", request)
		}
	}
	if !keys["attempt-1:MUSINSA"] || !keys["attempt-1:TWENTYNINECM"] || len(actor.requests) != len(keys) {
		t.Fatalf("run keys=%v requests=%d", keys, len(actor.requests))
	}
	if len(result.Observations) != len(actor.requests) {
		t.Fatalf("observations=%d requests=%d", len(result.Observations), len(actor.requests))
	}
	musinsa := false
	for _, coverage := range result.Coverage {
		if coverage.Source == researchdomain.SourceMusinsa {
			musinsa = coverage.Status == "SUCCEEDED" && coverage.CandidateCount > 0
		}
	}
	if !musinsa {
		t.Fatalf("coverage=%+v", result.Coverage)
	}
	if _, advanced := result.NextProgress["APIFY_MUSINSA"]; !advanced {
		t.Fatalf("actor progress not recorded: %+v", result.NextProgress)
	}
}

// Without a configured Actor, or without an attempt to bill the run to, the
// Actor malls are not asked at all and carry no coverage row.
func TestActorMallsAreSkippedWithoutAnActorOrAttempt(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/usage":
			return usageBody(), nil
		case "/realtime-product-search/v2/search":
			return ownEmpty(), nil
		case "/search/v1/webkr":
			return response(200, `{"items":[]}`, "text/plain"), nil
		case "/realtime-web-search/search":
			return response(200, `{"status":"OK","data":{"organic_results":[]}}`, "application/json"), nil
		}
		t.Fatalf("unexpected request %s", r.URL.Path)
		return nil, nil
	})
	for _, test := range []struct {
		name      string
		actor     *fakeActor
		attempt   string
		user      string
		wantAsked bool
	}{
		{"no actor", nil, "attempt-1", "user-1", false},
		{"actor without a token", &fakeActor{configured: false}, "attempt-1", "user-1", false},
		{"no attempt key", &fakeActor{configured: true}, "", "user-1", false},
		{"no user", &fakeActor{configured: true}, "attempt-1", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := Config{Enabled: true, OWNKey: "k", NaverClientID: "n", NaverClientSecret: "s",
				Control: &admissionControl{}, Client: &http.Client{Transport: transport}}
			if test.actor != nil {
				config.Actor = test.actor
			}
			g, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			result, err := g.SearchExternalMalls(context.Background(), researchapp.KoreanSearchRequest{
				Query: "반팔 티셔츠", Country: "KR", Vertical: "FASHION", Seeds: []string{"반팔 티셔츠"},
				UserID: test.user, AttemptKey: test.attempt,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, coverage := range result.Coverage {
				if coverage.Source == researchdomain.SourceMusinsa || coverage.Source == researchdomain.SourceTwentyNineCM {
					t.Fatalf("%s: unasked Actor mall kept a coverage row: %+v", test.name, coverage)
				}
			}
			if test.actor != nil && len(test.actor.requests) != 0 {
				t.Fatalf("%s: actor was called %d times", test.name, len(test.actor.requests))
			}
			if strings.TrimSpace(test.attempt) == "" && len(result.Observations) != 0 {
				t.Fatalf("%s: observations=%d", test.name, len(result.Observations))
			}
		})
	}
}
