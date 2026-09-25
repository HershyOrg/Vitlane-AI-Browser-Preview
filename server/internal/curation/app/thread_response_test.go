package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	d "github.com/vitlane/vitlane/server/internal/curation/domain"
)

func minor(v int64) *int64 { return &v }

func TestVitlanePickIsTheBestScoreThatFitsTheBudget(t *testing.T) {
	pool := []SavedCandidate{
		{CandidateID: "unscored", Title: "Unscored"},
		{CandidateID: "over", Assessed: true, TotalScore: 91, PriceMinor: minor(378000), Currency: "KRW"},
		{CandidateID: "fit", Assessed: true, TotalScore: 90, PriceMinor: minor(259000), Currency: "KRW"},
		{CandidateID: "cheap", Assessed: true, TotalScore: 81, PriceMinor: minor(168000), Currency: "KRW"},
	}
	ranked := RankSavedCandidates(pool, 280000, "KRW", true)
	if len(ranked) != 3 || ranked[0].CandidateID != "fit" || ranked[1].CandidateID != "over" || ranked[2].CandidateID != "cheap" {
		t.Fatalf("the pick fits the budget, the rest keep score order: %+v", ranked)
	}
	if free := RankSavedCandidates(pool, 0, "KRW", false); free[0].CandidateID != "over" {
		t.Fatalf("without a budget the best score leads: %+v", free)
	}
	if none := RankSavedCandidates(pool, 100000, "KRW", true); none[0].CandidateID != "over" {
		t.Fatalf("when nothing fits, the best score still leads: %+v", none)
	}
	if other := RankSavedCandidates(pool, 200, "USD", true); other[0].CandidateID != "over" {
		t.Fatalf("a budget in another currency is not compared: %+v", other)
	}
}

func TestHistoryKeepsTheNewestTurnsAndDropsWholeOldOnes(t *testing.T) {
	base := time.Date(2026, 9, 21, 6, 0, 0, 0, time.UTC)
	reply := func(body string) []d.CurationAction {
		return []d.CurationAction{{Type: d.CurationActionResponse, Status: "SUCCEEDED", Response: &d.ActionResponse{Body: body, References: []d.ResponseReference{{Ref: "c1", Title: "LAMY 2000"}}}}}
	}
	threads := []d.CurationThread{
		{ID: "old", Request: strings.Repeat("오래된 요청 ", 20), Status: "SUCCEEDED", CreatedAt: base, Actions: reply(strings.Repeat("오래된 답 ", 40))},
		{ID: "mid", Request: "만년필 찾아줘", Status: "SUCCEEDED", CreatedAt: base.Add(time.Minute), Actions: reply("[[c1]] 후보를 1순위로 뒀어요.")},
		{ID: "new", Request: "잉크도 추가해줘", Status: "SUCCEEDED", CreatedAt: base.Add(2 * time.Minute), TargetLabels: map[string]string{"ink": "잉크"}, Actions: []d.CurationAction{{Type: d.CurationActionStartResearch, Status: "SUCCEEDED", Jobs: []d.ActionJobResult{{Effects: []d.ActionEffect{{Kind: "CANDIDATES_ADDED", TargetID: "ink", Count: 4}}}}}}},
		{ID: "current", Request: "뭐가 나아?", Status: "RUNNING", CreatedAt: base.Add(3 * time.Minute)},
	}
	all := responseHistory(threads, "current", 100000)
	if len(all) != 3 || all[0].Request != threads[0].Request[:len(all[0].Request)] || all[2].Request != "잉크도 추가해줘" {
		t.Fatalf("turns come back oldest first without the current request: %+v", all)
	}
	if all[1].Reply != "LAMY 2000 후보를 1순위로 뒀어요." {
		t.Fatalf("a saved reply is shown with its references resolved: %q", all[1].Reply)
	}
	if !strings.Contains(all[2].Reply, "added 4 candidates for 잉크") {
		t.Fatalf("a Thread without a reply is summarised from its receipts: %q", all[2].Reply)
	}
	limited := responseHistory(threads, "current", len("만년필 찾아줘")+len("LAMY 2000 후보를 1순위로 뒀어요.")+len("잉크도 추가해줘")+len(all[2].Reply))
	if len(limited) != 2 || limited[0].Request != "만년필 찾아줘" {
		t.Fatalf("the oldest turn is dropped whole when the budget is short: %+v", limited)
	}
}

// Vitlane saves no display facts of a Shopify product, so a reference to one has
// no title. An earlier turn that named it must still read as a sentence.
func TestHistoryNamesAnUntitledReferenceByItsProductGroup(t *testing.T) {
	thread := d.CurationThread{ID: "earlier", Request: "camping chair", Status: "SUCCEEDED", TargetLabels: map[string]string{"chair": "camping chair"},
		Actions: []d.CurationAction{{Type: d.CurationActionResponse, Status: "SUCCEEDED", Response: &d.ActionResponse{Body: "[[c1]] leads, and [[c2]] is close.",
			References: []d.ResponseReference{{Ref: "c1", TargetID: "chair"}, {Ref: "c2", TargetID: "gone"}}}}}}
	if got := threadReplySummary(thread); got != "a camping chair candidate leads, and a candidate is close." {
		t.Fatalf("an untitled reference reads as its product group's candidate: %q", got)
	}
}

func TestPickPromotesAllFittingTiesAndUsesObservedFX(t *testing.T) {
	pool := []SavedCandidate{
		{CandidateID: "over", Assessed: true, TotalScore: 95, PriceMinor: minor(50000), Currency: "USD", Price: &ResponsePrice{ConvertedMinor: map[string]int64{"KRW": 650000}}},
		{CandidateID: "fit-usd", Assessed: true, TotalScore: 90, PriceMinor: minor(10000), Currency: "USD", Price: &ResponsePrice{ConvertedMinor: map[string]int64{"KRW": 130000}}},
		{CandidateID: "fit-krw", Assessed: true, TotalScore: 90, PriceMinor: minor(130000), Currency: "KRW"},
		{CandidateID: "unknown", Assessed: true, TotalScore: 99},
	}
	ranked := RankSavedCandidates(pool, 150000, "KRW", true)
	for i, id := range []string{"fit-usd", "fit-krw", "unknown", "over"} {
		if ranked[i].CandidateID != id {
			t.Fatalf("rank %d: got %s want %s", i, ranked[i].CandidateID, id)
		}
	}
	pool[1].Price = nil
	if got := RankSavedCandidates(pool, 150000, "KRW", true)[0].CandidateID; got != "fit-krw" {
		t.Fatalf("missing FX must not invent a comparison: %s", got)
	}
}

type initialCombinationPorts struct {
	ThreadRepository
	CatalogCartRepositoryV2
}

func (initialCombinationPorts) ListThreads(context.Context, string, string) (d.ControlMode, []d.CurationThread, error) {
	return d.ControlMode{}, nil, nil
}
func (initialCombinationPorts) GetCatalogCartV2(context.Context, string, string) (CatalogCartStateV2, error) {
	return CatalogCartStateV2{}, nil
}
func (initialCombinationPorts) SavedCandidates(_ context.Context, _, _ string, targets []string) (map[string][]SavedCandidate, error) {
	out := map[string][]SavedCandidate{}
	for _, target := range targets {
		for i := 0; i < 5; i++ {
			out[target] = append(out[target], SavedCandidate{CandidateID: fmt.Sprintf("%s-%d", target, i), Source: "SHOPIFY", Assessed: true, TotalScore: 100 - i, SourceState: "initial"})
		}
	}
	return out, nil
}
func TestInitialCommentSuppliesCombinationWithoutSeparateRequest(t *testing.T) {
	ports := initialCombinationPorts{}
	service := &ThreadService{repo: ports, responseSource: ports, combinationCart: &CatalogCartServiceV2{repository: ports}}
	thread := d.CurationThread{ID: "initial", UserID: "user", CurationID: "curation", Request: "일상 필기용 만년필과 잉크를 찾아줘"}
	snapshot := ThreadContext{Thread: thread, OriginalIntent: thread.Request, Locale: "ko-KR", Targets: []ThreadTarget{{ID: "pen"}, {ID: "ink"}}}
	out, err := service.buildResponseContext(context.Background(), thread, d.CurationAction{Instruction: d.ResponseKindComment}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != "COMMENT" || len(out.Combinations) != 9 || len(out.Targets[0].Candidates) != 3 || len(out.Targets[1].Candidates) != 3 {
		t.Fatalf("%+v", out)
	}
	if out.Request != thread.Request {
		t.Fatal("must not manufacture a combination request")
	}
}

func TestResponseCombinationScopeAfterTargetChanges(t *testing.T) {
	ports := initialCombinationPorts{}
	service := &ThreadService{repo: ports, responseSource: ports, combinationCart: &CatalogCartServiceV2{repository: ports}}
	for _, tc := range []struct {
		name         string
		targets      []ThreadTarget
		researched   string
		combinations int
	}{
		{"single target stays individual", []ThreadTarget{{ID: "pen"}}, "pen", 0},
		{"added target matches existing target too", []ThreadTarget{{ID: "pen"}, {ID: "ink"}}, "ink", 9},
		{"research again matches untouched target too", []ThreadTarget{{ID: "pen"}, {ID: "ink"}}, "pen", 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			thread := d.CurationThread{ID: "changed", UserID: "user", CurationID: "curation", Actions: []d.CurationAction{{Type: d.CurationActionStartResearch, Status: "SUCCEEDED", Jobs: []d.ActionJobResult{{Kind: "RESEARCH_ROUND", Status: "SUCCEEDED", TargetID: tc.researched}}}}}
			out, err := service.buildResponseContext(context.Background(), thread, d.CurationAction{Instruction: d.ResponseKindComment}, ThreadContext{Thread: thread, Targets: tc.targets})
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Combinations) != tc.combinations || len(out.Targets) != len(tc.targets) {
				t.Fatalf("unexpected response scope: %+v", out)
			}
			for _, target := range out.Targets {
				if len(target.Candidates) != 3 || target.Researched != (target.ID == tc.researched) {
					t.Fatalf("existing pool and new research must remain distinct: %+v", target)
				}
			}
		})
	}
}
