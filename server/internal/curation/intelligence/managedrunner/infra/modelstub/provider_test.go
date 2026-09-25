package modelstub_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"

	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	"github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/infra/modelstub"
)

func TestStubIsRefusedInProduction(t *testing.T) {
	// A stub that silently activated in production would serve fixture
	// candidates to real users while reporting success.
	if _, err := modelstub.New("production"); err == nil {
		t.Fatal("the stub provider must not be constructible in production")
	}
	for _, environment := range []string{"development", "test"} {
		if _, err := modelstub.New(environment); err != nil {
			t.Fatalf("stub in %s: %v", environment, err)
		}
	}
}

func TestStubRejectsAnUnknownSchema(t *testing.T) {
	provider, err := modelstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Complete(context.Background(), runnerapp.ModelRequest{
		SchemaName: "not_a_pipeline_step",
	}); err == nil {
		t.Fatal("unknown schema should not produce a response")
	}
}

func TestStubNormalizesKoreanAutoRequestAndSelectsExistingTarget(t *testing.T) {
	provider, err := modelstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Complete(context.Background(), runnerapp.ModelRequest{
		SchemaName: "vitlane_curation_auto_resolution",
		UserPrompt: `{"originalRequest":"여행용 어뎁터 좀더 조사해봐","targets":[{"id":"target-adapter","title":"여행용 멀티 어댑터","normalizedIntent":"compact universal travel adapter","orderIndex":0,"researchable":true}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["operation"] != "REFINE" || payload["decision"] != "RESEARCH_AGAIN" ||
		payload["targetId"] != "target-adapter" || payload["productReferenceEnglish"] != "travel adapter" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestStubRanksOnlyObservationsThePromptOffered(t *testing.T) {
	provider, err := modelstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Complete(context.Background(), runnerapp.ModelRequest{
		SchemaName: "vitlane_candidate_ranking",
		UserPrompt: "observationId=obs-1 price=10\nobservationId=obs-2 price=20\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Ranked []struct {
			ObservationID string `json:"observationId"`
		} `json:"ranked"`
	}
	if err := json.Unmarshal([]byte(response.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Ranked) != 2 {
		t.Fatalf("ranked %d observations, want 2", len(payload.Ranked))
	}
	// The pipeline discards ids it never offered. A stub that invented one
	// would be silently dropped and hide the same bug in a real model.
	for _, ranked := range payload.Ranked {
		if ranked.ObservationID != "obs-1" && ranked.ObservationID != "obs-2" {
			t.Fatalf("unexpected observation id %q", ranked.ObservationID)
		}
	}
}

func TestStubReportsUsageSoBudgetIsExercised(t *testing.T) {
	provider, err := modelstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Complete(context.Background(), runnerapp.ModelRequest{
		SchemaName: "vitlane_planning_targets",
		UserPrompt: "intentItem=캠핑 의자\nintentItem=충전식 랜턴\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Usage.OutputTokens <= 0 {
		t.Fatal("stub must report output tokens so settle() is exercised")
	}
	var payload struct {
		Targets []struct {
			Title       string `json:"title"`
			SearchQuery string `json:"searchQuery"`
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(response.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Targets) != 2 {
		t.Fatalf("planned %d targets, want 2", len(payload.Targets))
	}
	if payload.Targets[0].Title != "캠핑 의자" {
		t.Fatalf("first target = %q, want 캠핑 의자", payload.Targets[0].Title)
	}
	if payload.Targets[0].SearchQuery != "camping chair" {
		t.Fatalf("first search query = %q, want camping chair", payload.Targets[0].SearchQuery)
	}
	if payload.Targets[1].Title != "충전식 랜턴" ||
		payload.Targets[1].SearchQuery != "rechargeable camping lantern" {
		t.Fatalf("second target = %#v", payload.Targets[1])
	}
}

func TestStubUsageCostsSomethingUnderTheDefaultModel(t *testing.T) {
	provider, err := modelstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Complete(context.Background(), runnerapp.ModelRequest{
		SchemaName: "vitlane_catalog_query",
		UserPrompt: "intentItem=캠핑 의자\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	model := runnerdomain.Model{
		Key: "stub", ProviderModelID: "stub",
		InputMicrosPerMTok: 50_000, OutputMicrosPerMTok: 400_000,
		MaxOutputTokens: 2_000,
	}
	if model.Cost(response.Usage) <= 0 {
		t.Fatal("a stub call must still settle a non-zero cost")
	}
}

func TestStubNormalizesNaturalKoreanCampingIntentForLocalResearch(t *testing.T) {
	provider, err := modelstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Complete(context.Background(), runnerapp.ModelRequest{
		SchemaName: "vitlane_planning_targets",
		UserPrompt: "intentItem=캠핑 용품 조사해줘\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Targets []struct {
			SearchQuery string `json:"searchQuery"`
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(response.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Targets) != 1 || payload.Targets[0].SearchQuery != "camping gear" {
		t.Fatalf("targets = %#v, want one camping gear query", payload.Targets)
	}

	queryResponse, err := provider.Complete(context.Background(), runnerapp.ModelRequest{
		SchemaName: "vitlane_catalog_query",
		UserPrompt: "intentItem=가벼운 캠핑 의자 찾아줘\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	var query struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(queryResponse.Content), &query); err != nil {
		t.Fatal(err)
	}
	if query.Query != "camping chair" {
		t.Fatalf("query = %q, want camping chair", query.Query)
	}
}

func TestStubUnifiedInitialContract(t *testing.T) {
	p, _ := modelstub.New("test")
	r, e := p.Complete(context.Background(), runnerapp.ModelRequest{SchemaName: "vitlane_planning_targets", UserPrompt: "requestText=키보드 20만원 언더\nbudgetEnabled=false\nbudgetCurrency=KRW\nintentItem=키보드", Schema: map[string]any{"properties": map[string]any{"budget": true, "budgetDecision": true, "estimates": true}}})
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Budget         struct{ Amount string }
		BudgetDecision struct{ Kind string }
		Estimates      []any
	}
	if e = json.Unmarshal([]byte(r.Content), &v); e != nil {
		t.Fatal(e)
	}
	if v.Budget.Amount != "200000" || v.BudgetDecision.Kind != "ENABLE" || len(v.Estimates) != 1 {
		t.Fatalf("contract: %s", r.Content)
	}
}
func TestStubUnifiedAmbiguityHasExecutableOptions(t *testing.T) {
	p, _ := modelstub.New("test")
	r, e := p.Complete(context.Background(), runnerapp.ModelRequest{SchemaName: "vitlane_curation_thread_decision", UserPrompt: `{"thread":{"request":"다시 찾아줘"},"targets":[{"id":"a","title":"A","researchable":true},{"id":"b","title":"B","researchable":true}]}`})
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Question struct {
			Options []struct {
				Plan struct {
					Actions []struct {
						TargetID string `json:"targetId"`
					}
				}
			}
		}
	}
	if e = json.Unmarshal([]byte(r.Content), &v); e != nil {
		t.Fatal(e)
	}
	if len(v.Question.Options) != 2 || v.Question.Options[1].Plan.Actions[0].TargetID != "b" {
		t.Fatalf("options: %s", r.Content)
	}
}

// Local review reaches the direct mall paths only when the query step names a
// vertical; the stub does so for its fixture lexicon and nothing else.
func TestStubNamesAVerticalOnlyForItsMallFixtures(t *testing.T) {
	provider, err := modelstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ intent, query, vertical string }{
		{"우유 추천해줘", "fresh milk 900ml", "FOOD"},
		{"벨벳 밴딩 팬츠", "velour banding pants", "FASHION"},
		{"C타입 8핀 케이블", "usb c to lightning cable", "LIVING"},
		{"라미 사파리 만년필", "Lamy Safari fountain pen", ""},
	} {
		response, err := provider.Complete(context.Background(), runnerapp.ModelRequest{
			SchemaName: "vitlane_catalog_query",
			UserPrompt: "intentItem=" + test.intent + "\n",
		})
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(response.Content), &payload); err != nil {
			t.Fatal(err)
		}
		vertical, present := payload["productVertical"]
		if payload["query"] != test.query || (test.vertical == "" && present) || (test.vertical != "" && vertical != test.vertical) {
			t.Fatalf("%s: query=%v vertical=%v present=%v", test.intent, payload["query"], vertical, present)
		}
	}
}

func TestStubWritesAReplyThatPassesTheServerValidator(t *testing.T) {
	provider, err := modelstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	// The stub planner keeps the whole request as the product title, so a title can carry a budget
	// figure. A reply that echoed it would fail the price rule and the review stack would never show a reply.
	prompt := `{"kind":"COMMENT","locale":"ko-KR","products":[{"title":"필기감 좋은 만년필 30만원 안쪽으로","researchedInThisRequest":true,"candidates":[{"ref":"c1"},{"ref":"c2"}]},{"title":"잉크","researchedInThisRequest":false,"candidates":[{"ref":"c3"}]}]}`
	response, err := provider.Complete(context.Background(), runnerapp.ModelRequest{SchemaName: "vitlane_curation_thread_response", UserPrompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Body string `json:"body"`
	}
	if err = json.Unmarshal([]byte(response.Content), &reply); err != nil {
		t.Fatal(err)
	}
	offered := []curationdomain.ResponseReference{{Ref: "c1", CandidateID: "p-1"}, {Ref: "c2", CandidateID: "p-2"}, {Ref: "c3", CandidateID: "p-3"}}
	validated, err := curationdomain.NewActionResponse(curationdomain.ResponseKindComment, reply.Body, "ko-KR", "stub", offered, time.Now())
	if err != nil {
		t.Fatalf("the stub reply must pass the same validator as a live model: %v (%s)", err, reply.Body)
	}
	if len(validated.References) != 1 || validated.References[0].Ref != "c1" {
		t.Fatalf("a comment speaks only of products researched in this request: %+v", validated.References)
	}
}

func TestStubSeparatesAQuestionFromARequestNarrowly(t *testing.T) {
	provider, err := modelstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	decide := func(request string) map[string]any {
		prompt, _ := json.Marshal(map[string]any{"thread": map[string]any{"request": request}, "targets": []map[string]any{{"id": "pens", "title": "만년필", "intent": "fountain pen", "researchable": true}}})
		response, err := provider.Complete(context.Background(), runnerapp.ModelRequest{SchemaName: "vitlane_curation_thread_decision", UserPrompt: string(prompt)})
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err = json.Unmarshal([]byte(response.Content), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := decide("둘 중에 입문자한테 뭐가 나아?"); out["answer"] != true {
		t.Fatalf("a question must route to the reply: %+v", out)
	}
	for _, request := range []string{"더 저렴한 걸로 다시 찾아줄 수 있어?", "잉크도 추가해줄래?", "다시 찾아줘"} {
		if out := decide(request); out["answer"] == true {
			t.Fatalf("%q asks for work and must not be answered: %+v", request, out)
		}
	}
}

func TestStubScoresDifferByProductSoReviewCanTellAPickFromTheRest(t *testing.T) {
	provider, err := modelstub.New("test")
	if err != nil {
		t.Fatal(err)
	}
	rank := func(observation string) map[string]int {
		prompt := "contentLocale=ko-KR\n" +
			"observationId=" + observation + "-1 minPrice=12.50 USD maxPrice=12.50 USD merchant=Aurora Goods name=Aurora camping chair description=x serverIntentPoint= serverFeatures= serverSpecifications=\n" +
			"observationId=" + observation + "-2 minPrice=96.00 USD maxPrice=96.00 USD merchant=Fjord Outfitters name=Fjord camping chair description=x serverIntentPoint= serverFeatures= serverSpecifications=\n"
		response, err := provider.Complete(context.Background(), runnerapp.ModelRequest{SchemaName: "vitlane_candidate_ranking", UserPrompt: prompt})
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Ranked []struct {
				ObservationID string `json:"observationId"`
				AxisScores    []struct {
					ScorePercent int `json:"scorePercent"`
				} `json:"axisScores"`
			} `json:"ranked"`
		}
		if err = json.Unmarshal([]byte(response.Content), &out); err != nil {
			t.Fatal(err)
		}
		scores := map[string]int{}
		for _, candidate := range out.Ranked {
			if len(candidate.AxisScores) != 1 || candidate.AxisScores[0].ScorePercent < 40 || candidate.AxisScores[0].ScorePercent > 89 {
				t.Fatalf("score out of the stub range: %+v", candidate)
			}
			scores[strings.TrimPrefix(candidate.ObservationID, observation)] = candidate.AxisScores[0].ScorePercent
		}
		return scores
	}
	first, second := rank("round-a"), rank("round-b")
	if first["-1"] == first["-2"] {
		t.Fatalf("two products must not tie by construction: %+v", first)
	}
	// The score follows the product, not the run: a second research gives the same product the same score.
	if first["-1"] != second["-1"] || first["-2"] != second["-2"] {
		t.Fatalf("scores must be stable across observations: %+v %+v", first, second)
	}
}
