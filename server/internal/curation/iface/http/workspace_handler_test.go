package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"testing"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	agencyorderapp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

type workspacePlanStub struct {
	result   curationapp.PlanResult
	timeline []curationapp.CurationTimelineItem
}

func (s workspacePlanStub) GetByCuration(
	context.Context,
	string,
	string,
) (curationapp.PlanResult, error) {
	return s.result, nil
}

func (s workspacePlanStub) ListCurationTimeline(
	context.Context,
	string,
	string,
	int,
) ([]curationapp.CurationTimelineItem, error) {
	return s.timeline, nil
}

type workspaceResearchStub struct {
	result researchapp.PlanResearchResult
}

type workspaceIntelligenceStub struct {
	jobs []intelligenceapp.JobProgress
}

type workspaceCatalogResearchStub struct {
	view       researchapp.CatalogWorkspaceViewV2
	calls      *int
	freshValue *bool
}

func (s workspaceCatalogResearchStub) LoadWorkspaceV2(
	_ context.Context,
	_, _, _, _ string, fresh bool,
) (researchapp.CatalogWorkspaceViewV2, error) {
	if s.calls != nil {
		*s.calls++
	}
	if s.freshValue != nil {
		*s.freshValue = fresh
	}
	return s.view, nil
}

func (s workspaceIntelligenceStub) ListCurationJobs(
	context.Context,
	string,
	string,
) ([]intelligenceapp.JobProgress, error) {
	return s.jobs, nil
}

func (s workspaceResearchStub) GetPlanResearch(
	context.Context,
	string,
	string,
) (researchapp.PlanResearchResult, error) {
	return s.result, nil
}

type workspaceCartStub struct {
	view  curationdomain.CartView
	calls *int
}

type workspaceAgencyOrderStub struct {
	trace                  []agencyorderapp.CurationTrace
	agencyOrderedTargetIDs []string
}

func (s workspaceAgencyOrderStub) ListCurationAgencyOrderTrace(
	context.Context,
	string,
	string,
	int,
) ([]agencyorderapp.CurationTrace, error) {
	return s.trace, nil
}

func (s workspaceAgencyOrderStub) ListCurationAgencyOrderedTargetIDs(
	context.Context,
	string,
	string,
) ([]string, error) {
	return append([]string(nil), s.agencyOrderedTargetIDs...), nil
}

func (s workspaceCartStub) GetCurationCart(
	context.Context,
	string,
	string,
) (curationdomain.CartView, error) {
	*s.calls++
	return s.view, nil
}

func TestWorkspacePlanningUsesStatusFreeEmptyCartWithoutCallingCartStore(
	t *testing.T,
) {
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	calls := 0
	handler := NewWorkspaceHandler(
		workspacePlanStub{result: curationapp.PlanResult{
			Plan: curationdomain.PlanSnapshot{
				ID:             "plan-1",
				UserID:         "user-1",
				OriginalIntent: "업무용 책상",
				TotalBudget: shareddomain.Money{
					Amount:   "500",
					Currency: "KRW",
				},
			},
			Curation: curationdomain.Curation{
				ID:             "curation-1",
				ShoppingPlanID: "plan-1",
				UserID:         "user-1",
				Phase:          curationdomain.CurationPhasePlanning,
				Version:        1,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
			Targets: []curationdomain.PlanTarget{},
			AvailableActions: []curationdomain.CurationActionDescriptor{{
				ID:      curationdomain.CurationActionPlanningAddTargets,
				Alias:   "@TargetList-AddTarget",
				Enabled: true,
			}},
		}},
		workspaceResearchStub{result: researchapp.PlanResearchResult{
			PlanID: "plan-1",
			Groups: []researchapp.ResearchGroup{},
		}},
		workspaceCartStub{calls: &calls},
	)
	mux := nethttp.NewServeMux()
	mux.HandleFunc(
		"GET /api/v1/curations/{curationId}/workspace",
		handler.Get,
	)
	response := performJSON(
		t,
		mux,
		"GET",
		"/api/v1/curations/curation-1/workspace",
		"",
	)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		LatestArtifact  string                           `json:"latestArtifact"`
		Cart            curationdomain.CartView          `json:"cart"`
		CatalogResearch catalogResearchWorkspaceResponse `json:"catalogResearch"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if calls != 0 ||
		body.LatestArtifact != "TARGET_LIST" ||
		len(body.Cart.Selections) != 0 ||
		body.Cart.Total.Amount != "0" ||
		body.Cart.Total.Currency != "KRW" ||
		body.CatalogResearch.SchemaVersion != "vitlane.catalog-research-workspace.v4" {
		t.Fatalf("calls=%d body=%#v", calls, body)
	}
	var rawBody struct {
		Cart map[string]json.RawMessage `json:"cart"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &rawBody); err != nil {
		t.Fatal(err)
	}
	if _, found := rawBody.Cart["status"]; found {
		t.Fatal("CartView unexpectedly exposed a lifecycle status")
	}
}

func TestWorkspaceCuratingReadsCartProjection(t *testing.T) {
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	calls := 0
	handler := NewWorkspaceHandler(
		workspacePlanStub{result: curationapp.PlanResult{
			Plan: curationdomain.PlanSnapshot{
				ID:     "plan-1",
				UserID: "user-1",
				TotalBudget: shareddomain.Money{
					Amount:   "500",
					Currency: "USD",
				},
			},
			Curation: curationdomain.Curation{
				ID:             "curation-1",
				ShoppingPlanID: "plan-1",
				UserID:         "user-1",
				Phase:          curationdomain.CurationPhaseCurating,
				Version:        2,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		}},
		workspaceResearchStub{result: researchapp.PlanResearchResult{
			PlanID: "plan-1",
			Groups: []researchapp.ResearchGroup{},
		}},
		workspaceCartStub{
			calls: &calls,
			view: curationdomain.CartView{
				CurationID: "curation-1",
				Selections: []curationdomain.CartSelection{},
				Total: shareddomain.Money{
					Amount:   "0",
					Currency: "USD",
				},
				Warnings:  []curationdomain.CartWarning{},
				UpdatedAt: now,
			},
		},
	)
	catalogCalls := 0
	catalogFresh := true
	handler.EnableCatalogResearch(workspaceCatalogResearchStub{
		calls: &catalogCalls, freshValue: &catalogFresh,
		view: researchapp.CatalogWorkspaceViewV2{Pools: []researchapp.CatalogWorkspacePoolV2{{
			Metadata: researchapp.CatalogPoolMetadataV2{TargetID: "target-1", Version: 3},
		}}},
	})
	mux := nethttp.NewServeMux()
	mux.HandleFunc(
		"GET /api/v1/curations/{curationId}/workspace",
		handler.Get,
	)
	response := performJSON(
		t,
		mux,
		"GET",
		"/api/v1/curations/curation-1/workspace",
		"",
	)
	var body struct {
		CatalogResearch catalogResearchWorkspaceResponse `json:"catalogResearch"`
	}
	decodeErr := json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != nethttp.StatusOK ||
		calls != 1 || catalogCalls != 1 || catalogFresh || decodeErr != nil ||
		len(body.CatalogResearch.Pools) != 1 ||
		body.CatalogResearch.Pools[0].TargetID != "target-1" {
		t.Fatalf(
			"status=%d calls=%d body=%s",
			response.Code,
			calls,
			response.Body.String(),
		)
	}
}

func TestMapCatalogResearchWorkspaceEncodesEmptyCandidateClaimsAsArrays(
	t *testing.T,
) {
	mapped := mapCatalogResearchWorkspace(researchapp.CatalogWorkspaceViewV2{
		Pools: []researchapp.CatalogWorkspacePoolV2{{
			Metadata: researchapp.CatalogPoolMetadataV2{
				TargetID: "target-1",
				Version:  1,
			},
			Products: []researchapp.CatalogProductObservation{{
				ProviderProductID: "shopify-product-1",
			}},
			Assessments: map[string]researchapp.LiveCandidateAssessmentV2{},
		}},
	})
	payload, err := json.Marshal(mapped)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Pools []struct {
			Products []struct {
				Features       json.RawMessage `json:"features"`
				Specifications json.RawMessage `json:"specifications"`
			} `json:"products"`
		} `json:"pools"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Pools) != 1 || len(wire.Pools[0].Products) != 1 {
		t.Fatalf("unexpected workspace shape: %s", payload)
	}
	product := wire.Pools[0].Products[0]
	if string(product.Features) != "[]" || string(product.Specifications) != "[]" {
		t.Fatalf(
			"empty candidate claims must be JSON arrays: features=%s specifications=%s",
			product.Features,
			product.Specifications,
		)
	}
}

func TestProjectCurationActiveWorkIgnoresTerminalJobs(t *testing.T) {
	failed := intelligenceapp.JobProgress{
		JobID: "job-failed", ActionID: "action-1",
		TargetKind: string(intelligencedomain.TargetResearchRound),
		TargetID:   "round-requested", Status: string(intelligencedomain.JobFailed),
	}
	if got := projectCurationActiveWork([]intelligenceapp.JobProgress{failed}); got != nil {
		t.Fatalf("terminal job kept workspace active: %#v", got)
	}
	pending := failed
	pending.JobID = "job-pending"
	pending.Status = string(intelligencedomain.JobPending)
	got := projectCurationActiveWork([]intelligenceapp.JobProgress{failed, pending})
	if got == nil || got.WorkTargetID != "round-requested" || got.Status != "QUEUED" {
		t.Fatalf("pending job was not projected: %#v", got)
	}
}

func TestProjectCurationActiveWorkStopsPollingEffectUnknownAttempt(t *testing.T) {
	job := intelligenceapp.JobProgress{
		JobID: "job-unknown", ActionID: "action-1",
		TargetKind: string(intelligencedomain.TargetResearchRound),
		TargetID:   "round-unknown", Status: string(intelligencedomain.JobRunning),
		LatestAttemptStatus: intelligencedomain.AttemptEffectUnknown,
	}
	got := projectCurationActiveWork([]intelligenceapp.JobProgress{job})
	if got == nil || got.Status != "RESULT_CONFIRMATION_REQUIRED" {
		t.Fatalf("effect-unknown attempt must stop RUNNING polling: %#v", got)
	}
}

func TestProjectCurationActiveWorkPrefersActuallyRunningJobOverEffectUnknown(t *testing.T) {
	unknown := intelligenceapp.JobProgress{
		JobID: "job-unknown", ActionID: "action-1",
		TargetKind: string(intelligencedomain.TargetResearchRound),
		TargetID:   "round-unknown", Status: string(intelligencedomain.JobRunning),
		LatestAttemptStatus: intelligencedomain.AttemptEffectUnknown,
	}
	running := intelligenceapp.JobProgress{
		JobID: "job-running", ActionID: "action-1",
		TargetKind: string(intelligencedomain.TargetResearchRound),
		TargetID:   "round-running", Status: string(intelligencedomain.JobRunning),
		LatestAttemptStatus: intelligencedomain.AttemptRunning,
	}
	got := projectCurationActiveWork([]intelligenceapp.JobProgress{unknown, running})
	if got == nil || got.WorkTargetID != "round-running" || got.Status != "RUNNING" {
		t.Fatalf("actually running job must keep polling: %#v", got)
	}
}

func TestWorkspaceProjectsAgencyOrderCoverageActiveWorkAndTypedTranscript(
	t *testing.T,
) {
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	calls := 0
	targetID := "target-1"
	handler := NewWorkspaceHandler(
		workspacePlanStub{
			result: curationapp.PlanResult{
				Plan: curationdomain.PlanSnapshot{
					ID: "plan-1", UserID: "user-1",
					TotalBudget: shareddomain.Money{
						Amount: "500", Currency: "USD",
					},
				},
				Curation: curationdomain.Curation{
					ID: "curation-1", ShoppingPlanID: "plan-1",
					UserID:  "user-1",
					Phase:   curationdomain.CurationPhaseCurating,
					Version: 2, CreatedAt: now, UpdatedAt: now,
				},
				Targets: []curationdomain.PlanTarget{
					{ID: "target-1"},
					{ID: "target-2"},
				},
			},
			timeline: []curationapp.CurationTimelineItem{
				{
					Action: curationapp.CurationTimelineAction{
						ID:         "research-again-action",
						CurationID: "curation-1",
						Type:       curationdomain.CurationActionTargetResearchAgain,
						PhaseAtRequest: curationdomain.
							CurationActionPhaseCurating,
						SubjectType:             curationdomain.CurationActionSubjectTarget,
						SubjectID:               &targetID,
						EffectKind:              curationdomain.CurationActionEffectIntelligence,
						ExpectedCurationVersion: 2,
						CreatedAt:               now,
					},
					Result: &curationapp.CurationTimelineResult{
						Kind:       curationapp.CurationTimelineResultTargetResearched,
						Summary:    "업무용 의자 Target 재조사를 시작했습니다.",
						OccurredAt: now,
					},
				},
			},
		},
		workspaceResearchStub{result: researchapp.PlanResearchResult{
			PlanID: "plan-1",
			Groups: []researchapp.ResearchGroup{{
				Round: &researchdomain.ResearchRound{
					ID:     "round-1",
					Status: researchdomain.RoundStatusRequested,
				},
			}},
		}},
		workspaceCartStub{
			calls: &calls,
			view: curationdomain.CartView{
				CurationID: "curation-1",
				Selections: []curationdomain.CartSelection{},
				Total: shareddomain.Money{
					Amount: "0", Currency: "USD",
				},
				Warnings:  []curationdomain.CartWarning{},
				UpdatedAt: now,
			},
		},
		workspaceAgencyOrderStub{
			trace: []agencyorderapp.CurationTrace{{
				AgencyOrderID: "agency-order-1", CurationID: "curation-1",
				TargetID: "target-1", CandidateID: "candidate-1",
				State: "PROCESSING", IssuedAt: now, UpdatedAt: now,
			}},
			// Exact coverage is deliberately broader than the capped
			// rendering trace, as it would be after 500 newer AgencyOrders.
			agencyOrderedTargetIDs: []string{"target-1", "target-2"},
		},
	)
	handler.EnableIntelligence(workspaceIntelligenceStub{jobs: []intelligenceapp.JobProgress{{
		JobID: "job-1", ActionID: "action-1",
		TargetKind: string(intelligencedomain.TargetResearchRound),
		TargetID:   "round-1", Status: string(intelligencedomain.JobRunning),
	}}})
	mux := nethttp.NewServeMux()
	mux.HandleFunc(
		"GET /api/v1/curations/{curationId}/workspace",
		handler.Get,
	)
	response := performJSON(
		t,
		mux,
		"GET",
		"/api/v1/curations/curation-1/workspace",
		"",
	)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Coverage         curationWorkspaceCoverage          `json:"coverage"`
		Timeline         []curationapp.CurationTimelineItem `json:"timeline"`
		AgencyOrderTrace []agencyorderapp.CurationTrace     `json:"agencyOrderTrace"`
		ActiveWork       *curationWorkspaceActiveWork       `json:"activeWork"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Coverage != curationWorkspaceCoverageComplete ||
		len(body.Timeline) != 1 ||
		body.Timeline[0].Action.Type != curationdomain.CurationActionTargetResearchAgain ||
		body.Timeline[0].Result == nil ||
		body.Timeline[0].Result.Kind != curationapp.CurationTimelineResultTargetResearched ||
		len(body.AgencyOrderTrace) != 1 ||
		body.ActiveWork == nil ||
		body.ActiveWork.WorkTargetID != "round-1" ||
		body.ActiveWork.Status != "RUNNING" {
		t.Fatalf("unexpected workspace projection: %#v", body)
	}
}

func TestProjectCurationCoverageUsesActiveTargetsOnly(t *testing.T) {
	targets := []curationdomain.PlanTarget{
		{ID: "target-1"},
		{ID: "target-2"},
	}
	orderedTargetIDs := []string{
		"target-1",
		"removed-or-foreign-target",
	}
	if got := projectCurationCoverage(
		targets,
		orderedTargetIDs,
	); got != curationWorkspaceCoveragePartial {
		t.Fatalf("coverage=%s", got)
	}
	orderedTargetIDs = append(orderedTargetIDs, "target-2")
	if got := projectCurationCoverage(
		targets,
		orderedTargetIDs,
	); got != curationWorkspaceCoverageComplete {
		t.Fatalf("coverage=%s", got)
	}
}
