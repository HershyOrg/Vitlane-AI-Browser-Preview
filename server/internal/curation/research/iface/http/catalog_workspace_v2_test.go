package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestCatalogWorkspaceSearchRequiresPoolCASAndIdempotency(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		key    string
		reason string
	}{
		{
			name:   "missing idempotency key",
			body:   `{"mode":"APPEND","expectedPoolVersion":0,"query":"bag","limit":10}`,
			reason: "IDEMPOTENCY_KEY_REQUIRED",
		},
		{
			name: "missing expected pool version",
			body: `{"mode":"APPEND","query":"bag","limit":10}`,
			key:  "search-command-1", reason: "PHASE8_CANDIDATE_POOL_VERSION_REQUIRED",
		},
		{
			name: "negative expected pool version",
			body: `{"mode":"APPEND","expectedPoolVersion":-1,"query":"bag","limit":10}`,
			key:  "search-command-1", reason: "PHASE8_CANDIDATE_POOL_VERSION_REQUIRED",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &catalogWorkspaceHTTPServiceFakeV2{}
			handler := &LiveCatalogReviewHandlerV2{workspace: service}
			request := catalogWorkspaceHTTPRequestV2(test.body, test.key)
			response := httptest.NewRecorder()
			handler.SearchWorkspace(response, request)
			if response.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, service.calls, response.Body.String())
			}
			var body struct {
				Error struct {
					ReasonCode string `json:"reasonCode"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.ReasonCode != test.reason {
				t.Fatalf("reason=%s want=%s", body.Error.ReasonCode, test.reason)
			}
		})
	}
}

func TestCatalogWorkspaceSearchRejectsPublicReplaceBeforeServiceCall(t *testing.T) {
	service := &catalogWorkspaceHTTPServiceFakeV2{}
	handler := &LiveCatalogReviewHandlerV2{workspace: service}
	request := catalogWorkspaceHTTPRequestV2(
		`{"mode":"REPLACE","expectedPoolVersion":3,"query":"bag","limit":10}`,
		"research-again-must-use-worker",
	)
	response := httptest.NewRecorder()
	handler.SearchWorkspace(response, request)

	if response.Code != http.StatusConflict || service.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, service.calls, response.Body.String())
	}
	var body struct {
		Error struct {
			ReasonCode string `json:"reasonCode"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.ReasonCode != "PHASE8_RESEARCH_REPLACE_REQUIRES_WORKER" {
		t.Fatalf("reason=%s", body.Error.ReasonCode)
	}
}

func TestCatalogWorkspaceSearchPassesServerOwnedCommandFieldsAndReturnsReplay(t *testing.T) {
	service := &catalogWorkspaceHTTPServiceFakeV2{result: researchapp.CatalogWorkspaceSearchResultV2{
		LiveCatalogReviewResultV2: researchapp.LiveCatalogReviewResultV2{
			Search: researchapp.CatalogProductSearchResult{
				Provider: "SHOPIFY_UCP", ProtocolVersion: "2026-04-08",
				Outcome: researchapp.CatalogOutcomeSuccess,
				Products: []researchapp.CatalogProductObservation{{
					ProviderProductID: "candidate-durable",
				}},
			},
			CandidateEligibleCount: 1,
			Metrics: researchapp.LiveCatalogReviewMetricsV2{
				PolicyVersion: "phase8-search-replay.v1", ExternalEffect: "NONE",
			},
		},
		Pool: researchapp.CatalogPoolMetadataV2{
			TargetID: "target-1", Version: 8, ExpandOrdinal: 3,
			LatestMode: researchapp.CatalogResearchAppendV2,
		},
		Replay: true,
	}}
	handler := &LiveCatalogReviewHandlerV2{workspace: service}
	request := catalogWorkspaceHTTPRequestV2(
		`{"mode":"APPEND","expectedPoolVersion":7,"query":"bag","intent":"travel bag","country":"KR","currency":"KRW","limit":10}`,
		"search-command-7",
	)
	response := httptest.NewRecorder()
	handler.SearchWorkspace(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store, max-age=0" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if service.calls != 1 || service.input.ExpectedPoolVersion != 7 ||
		service.input.IdempotencyKey != "search-command-7" ||
		service.input.UserID != "user-1" || service.input.CurationID != "curation-1" ||
		service.input.TargetID != "target-1" {
		t.Fatalf("calls=%d input=%#v", service.calls, service.input)
	}
	var body struct {
		PoolVersion int64 `json:"poolVersion"`
		Replay      bool  `json:"replay"`
		Products    []struct {
			ProviderProductID string `json:"candidateId"`
		} `json:"products"`
		Metrics struct {
			ShopifyCallCount int    `json:"shopifyCallCount"`
			ExternalEffect   string `json:"externalEffect"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.PoolVersion != 8 || !body.Replay || len(body.Products) != 1 ||
		body.Products[0].ProviderProductID != "candidate-durable" ||
		body.Metrics.ShopifyCallCount != 0 || body.Metrics.ExternalEffect != "NONE" {
		t.Fatalf("body=%#v", body)
	}
}

func TestCatalogWorkspaceSearchReturnsTypedCapacityConflict(t *testing.T) {
	service := &catalogWorkspaceHTTPServiceFakeV2{err: fault.New(
		fault.Conflict, researchapp.CatalogCandidatePoolCapacityReachedV2, false,
	)}
	handler := &LiveCatalogReviewHandlerV2{workspace: service}
	request := catalogWorkspaceHTTPRequestV2(
		`{"mode":"APPEND","expectedPoolVersion":50,"query":"bag","limit":10}`,
		"search-command-50",
	)
	response := httptest.NewRecorder()
	handler.SearchWorkspace(response, request)
	if response.Code != http.StatusConflict || service.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, service.calls, response.Body.String())
	}
	var body struct {
		Error struct {
			ReasonCode string `json:"reasonCode"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.ReasonCode != researchapp.CatalogCandidatePoolCapacityReachedV2 {
		t.Fatalf("reason=%s", body.Error.ReasonCode)
	}
}

func TestCatalogResearchHydrationPassesExplicitHiddenTargetScope(t *testing.T) {
	service := &catalogWorkspaceHTTPServiceFakeV2{}
	handler := &LiveCatalogReviewHandlerV2{workspace: service}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/curations/curation-1/catalog-research/hydrations",
		strings.NewReader(`{"scope":"HIDDEN_TARGET","targetId":"target-1"}`),
	)
	request.SetPathValue("curationId", "curation-1")
	request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "user-1"))
	response := httptest.NewRecorder()

	handler.HydrateWorkspace(response, request)

	if response.Code != http.StatusOK || service.hydrateCalls != 1 ||
		service.hydrationInput.UserID != "user-1" ||
		service.hydrationInput.CurationID != "curation-1" ||
		service.hydrationInput.Scope != researchapp.CatalogResearchHydrationHiddenTargetV2 ||
		service.hydrationInput.TargetID != "target-1" {
		t.Fatalf("status=%d calls=%d input=%+v body=%s",
			response.Code, service.hydrateCalls, service.hydrationInput, response.Body.String())
	}
}

func TestCatalogResearchHydrationReturnsPerCandidateStatusAndAcceptsCandidateRetryScope(t *testing.T) {
	pool := researchapp.CatalogWorkspacePoolV2{
		Metadata:    researchapp.CatalogPoolMetadataV2{TargetID: "target-1", Version: 12},
		Assessments: map[string]researchapp.LiveCandidateAssessmentV2{},
		Hydrations:  map[string]researchapp.CatalogCandidateHydrationV2{},
	}
	for index := 1; index <= 12; index++ {
		candidateID := fmt.Sprintf("candidate-%02d", index)
		product := researchapp.CatalogProductObservation{ProviderProductID: candidateID}
		hydration := researchapp.CatalogCandidateHydrationV2{
			Status: researchapp.CatalogCandidateHydrationReadyV2,
		}
		if index < 12 {
			product.Title = fmt.Sprintf("Product %02d", index)
		} else {
			hydration = researchapp.CatalogCandidateHydrationV2{
				Status:     researchapp.CatalogCandidateHydrationUnresolvedV2,
				ReasonCode: researchapp.CatalogCandidateHydrationUnresolvedReasonV2,
				Retryable:  true,
			}
		}
		pool.Products = append(pool.Products, product)
		pool.Hydrations[candidateID] = hydration
	}
	service := &catalogWorkspaceHTTPServiceFakeV2{hydrateResult: researchapp.CatalogWorkspaceViewV2{
		Pools: []researchapp.CatalogWorkspacePoolV2{pool},
	}}
	handler := &LiveCatalogReviewHandlerV2{workspace: service}
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		request.SetPathValue("curationId", "curation-1")
		request = request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "user-1"))
		handler.HydrateWorkspace(w, request)
	}))
	defer testServer.Close()
	postHydration := func(body string) (int, []byte) {
		t.Helper()
		response, err := http.Post(
			testServer.URL+"/api/v1/curations/curation-1/catalog-research/hydrations",
			"application/json", strings.NewReader(body),
		)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		bodyBytes, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, bodyBytes
	}
	status, bodyBytes := postHydration(
		`{"scope":"VISIBLE_TARGET","targetId":"target-1"}`,
	)

	if status != http.StatusOK || service.hydrateCalls != 1 ||
		service.hydrationInput.Scope != researchapp.CatalogResearchHydrationVisibleTargetV2 ||
		service.hydrationInput.TargetID != "target-1" ||
		service.hydrationInput.CandidateID != "" {
		t.Fatalf("status=%d calls=%d input=%+v body=%s",
			status, service.hydrateCalls, service.hydrationInput, string(bodyBytes))
	}
	var body struct {
		SchemaVersion string `json:"schemaVersion"`
		Pools         []struct {
			Products []struct {
				ProviderProductID string `json:"candidateId"`
				Hydration         struct {
					Status     string `json:"status"`
					ReasonCode string `json:"reasonCode"`
					Retryable  bool   `json:"retryable"`
				} `json:"hydration"`
			} `json:"products"`
		} `json:"pools"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatal(err)
	}
	ready := 0
	unresolved := 0
	for _, product := range body.Pools[0].Products {
		if product.Hydration.Status == "READY" {
			ready++
		}
		if product.Hydration.Status == "UNRESOLVED" {
			unresolved++
			if product.ProviderProductID != "candidate-12" ||
				product.Hydration.ReasonCode != researchapp.CatalogCandidateHydrationUnresolvedReasonV2 ||
				!product.Hydration.Retryable {
				t.Fatalf("unresolved product=%+v", product)
			}
		}
	}
	if body.SchemaVersion != "vitlane.catalog-research-hydration.v3" ||
		len(body.Pools) != 1 || ready != 11 || unresolved != 1 {
		t.Fatalf("body=%+v ready=%d unresolved=%d", body, ready, unresolved)
	}

	service.hydrateResult = researchapp.CatalogWorkspaceViewV2{Pools: []researchapp.CatalogWorkspacePoolV2{{
		Metadata: researchapp.CatalogPoolMetadataV2{TargetID: "target-1", Version: 12},
		Products: []researchapp.CatalogProductObservation{{
			ProviderProductID: "candidate-12", Title: "Recovered Product 12",
		}},
		Assessments: map[string]researchapp.LiveCandidateAssessmentV2{},
		Hydrations: map[string]researchapp.CatalogCandidateHydrationV2{
			"candidate-12": {Status: researchapp.CatalogCandidateHydrationReadyV2},
		},
	}}}
	status, bodyBytes = postHydration(
		`{"scope":"CANDIDATE","targetId":"target-1","candidateId":"candidate-12"}`,
	)
	if status != http.StatusOK || service.hydrateCalls != 2 ||
		service.hydrationInput.Scope != researchapp.CatalogResearchHydrationCandidateV2 ||
		service.hydrationInput.TargetID != "target-1" ||
		service.hydrationInput.CandidateID != "candidate-12" {
		t.Fatalf("retry status=%d calls=%d input=%+v body=%s",
			status, service.hydrateCalls, service.hydrationInput, string(bodyBytes))
	}
	var retryBody struct {
		Pools []struct {
			Products []struct {
				ProviderProductID string `json:"candidateId"`
				Title             string `json:"title"`
				Hydration         struct {
					Status string `json:"status"`
				} `json:"hydration"`
			} `json:"products"`
		} `json:"pools"`
	}
	if err := json.Unmarshal(bodyBytes, &retryBody); err != nil {
		t.Fatal(err)
	}
	if len(retryBody.Pools) != 1 || len(retryBody.Pools[0].Products) != 1 ||
		retryBody.Pools[0].Products[0].ProviderProductID != "candidate-12" ||
		retryBody.Pools[0].Products[0].Title != "Recovered Product 12" ||
		retryBody.Pools[0].Products[0].Hydration.Status != "READY" {
		t.Fatalf("retry body=%+v", retryBody)
	}
}

func catalogWorkspaceHTTPRequestV2(body, key string) *http.Request {
	request := httptest.NewRequest(
		http.MethodPost, "/api/v1/curations/curation-1/targets/target-1/research",
		strings.NewReader(body),
	)
	request.SetPathValue("curationId", "curation-1")
	request.SetPathValue("targetId", "target-1")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	return request.WithContext(sharedapp.WithAuthenticatedUserID(request.Context(), "user-1"))
}

type catalogWorkspaceHTTPServiceFakeV2 struct {
	catalogWorkspaceServiceV2
	result         researchapp.CatalogWorkspaceSearchResultV2
	input          researchapp.CatalogWorkspaceSearchInputV2
	err            error
	calls          int
	hydrateCalls   int
	hydrationInput researchapp.CatalogResearchHydrationInputV2
	hydrateResult  researchapp.CatalogWorkspaceViewV2
}

func (service *catalogWorkspaceHTTPServiceFakeV2) HydrateWorkspaceV2(
	_ context.Context,
	input researchapp.CatalogResearchHydrationInputV2,
) (researchapp.CatalogWorkspaceViewV2, error) {
	service.hydrateCalls++
	service.hydrationInput = input
	return service.hydrateResult, service.err
}

func (service *catalogWorkspaceHTTPServiceFakeV2) SearchWorkspaceV2(
	_ context.Context,
	input researchapp.CatalogWorkspaceSearchInputV2,
) (researchapp.CatalogWorkspaceSearchResultV2, error) {
	service.calls++
	service.input = input
	return service.result, service.err
}
