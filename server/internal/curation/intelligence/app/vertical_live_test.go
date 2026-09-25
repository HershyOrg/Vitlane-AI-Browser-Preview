package app

import (
	"context"
	"encoding/json"
	"fmt"
	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	"github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/infra/openaiapi"
	"net/http"
	"os"
	"testing"
	"time"
)

// Opt-in uses the actual Planning and Query schemas/prompts with one request
// per representative item; no catalog access or production state is changed.
func TestLiveRepresentativeRoutingClassification(t *testing.T) {
	if os.Getenv("VITLANE_ROUTE_CLASSIFICATION_LIVE") != "1" {
		t.Skip("explicit live model opt-in required")
	}
	secret := os.Getenv("MANAGED_OPENAI_API_SECRET")
	if secret == "" {
		t.Fatal("model credential required")
	}
	client := openaiapi.NewClient("", secret, &http.Client{Timeout: 90 * time.Second})
	model := runnerdomain.DefaultModels()[1]
	entries := []map[string]any{}
	for i, tc := range []struct{ title, want string }{{"운동화", "FASHION"}, {"가디건", "FASHION"}, {"선크림", "BEAUTY"}, {"우유", "FOOD"}, {"생수", "FOOD"}, {"수납함", "LIVING"}, {"주방용품", "LIVING"}, {"청소기", "ELECTRONICS"}, {"노트북", "ELECTRONICS"}, {"적당한 선물", "GENERAL"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		c := PlanningContext{OriginalIntent: tc.title, Country: "KR", ContentLocale: "ko-KR", PlanningMode: "SINGLE"}
		system, prompt, schema, name := planningSystemPromptForContext(c), planningPrompt(c, 1), planningSchema(c, 1), SchemaPlanningTargets
		if i%2 == 1 {
			q := ResearchContext{TargetTitle: tc.title, TargetIntent: tc.title, Country: "KR", ContentLocale: "ko-KR"}
			system = researchSystemPrompt(providerCatalogQueryPrompt("KR"), q)
			prompt = catalogQueryPrompt(q)
			schema = CatalogQuerySchema()
			name = SchemaCatalogQuery
		}
		started := time.Now()
		response, err := client.Complete(ctx, runnerapp.ModelRequest{Model: model, SystemPrompt: system, UserPrompt: prompt, Schema: schema, SchemaName: name, RequestKey: fmt.Sprintf("route-category-live-%d-%d", time.Now().Unix(), i)})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if name == SchemaPlanningTargets {
			var p PlanningTargetsPayload
			if err := json.Unmarshal([]byte(response.Content), &p); err != nil {
				t.Fatal(err)
			}
			if len(p.Targets) == 1 {
				got = p.Targets[0].ProductVertical
			}
		} else {
			var q CatalogQueryPayload
			if err := json.Unmarshal([]byte(response.Content), &q); err != nil {
				t.Fatal(err)
			}
			got = q.ProductVertical
		}
		entries = append(entries, map[string]any{"request": tc.title, "expected": tc.want, "actual": got, "schema": name, "durationMs": time.Since(started).Milliseconds(), "inputTokens": response.Usage.InputTokens, "outputTokens": response.Usage.OutputTokens})
		if got != tc.want {
			t.Errorf("%s: got %s want %s", tc.title, got, tc.want)
		}
		t.Logf("item=%s family=%s schema=%s", tc.title, got, name)
	}
	if path := os.Getenv("VITLANE_ROUTE_CLASSIFICATION_EVIDENCE"); path != "" {
		raw, _ := json.MarshalIndent(map[string]any{"scope": "actual Planning/Query prompts, no production writes", "model": model.ProviderModelID, "observedAt": time.Now().UTC(), "entries": entries}, "", "  ")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLiveFiftyCandidateEightAxisAssessment(t *testing.T) {
	if os.Getenv("VITLANE_ROUTE_ASSESSMENT_LIVE") != "1" {
		t.Skip("explicit paid model opt-in required")
	}
	secret := os.Getenv("MANAGED_OPENAI_API_SECRET")
	if secret == "" {
		t.Fatal("model credential required")
	}
	client := openaiapi.NewClient("", secret, &http.Client{Timeout: 180 * time.Second})
	model := runnerdomain.DefaultModels()[1].AssessmentModel(50, 8)
	criteria := &ResearchCriteria{Subject: ResearchSubject{Label: "검증용 펜", ProductType: "pen"}, Exclusions: []string{}}
	for i, label := range []string{"휴대성", "그립", "내구성", "필기감", "디자인", "관리 편의", "다용도", "선물 적합성"} {
		criteria.Axes = append(criteria.Axes, ResearchAxis{AxisID: fmt.Sprint("axis-", i), Label: label, Definition: label, Importance: 3, Origin: "REQUEST"})
	}
	c := ResearchContext{Criteria: criteria, ContentLocale: "ko-KR", Country: "KR"}
	observations := make([]ResearchCandidateObservation, 50)
	for i := range observations {
		observations[i] = ResearchCandidateObservation{ObservationID: fmt.Sprint("synthetic-", i), Name: fmt.Sprintf("Synthetic blue pen %d", i), Description: "Synthetic fixture. Blue plastic pen, 14 cm. No other verified product information.", FactIDs: []string{}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	started := time.Now()
	response, err := client.Complete(ctx, runnerapp.ModelRequest{Model: model, SystemPrompt: researchSystemPrompt(rankingSystemPrompt, c), UserPrompt: rankingPrompt(c, observations, len(observations)), Schema: CandidateRankingSchema(len(observations), 8), SchemaName: SchemaCandidateRanking, RequestKey: fmt.Sprint("route-assessment-live-", time.Now().Unix())})
	if err != nil {
		t.Fatal(err)
	}
	var payload CandidateRankingPayload
	if err := json.Unmarshal([]byte(response.Content), &payload); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range payload.Ranked {
		if seen[row.ObservationID] || len(row.AxisScores) != 8 {
			t.Error("duplicate or incomplete assessment")
		}
		seen[row.ObservationID] = true
	}
	for _, o := range observations {
		if !seen[o.ObservationID] {
			t.Errorf("missing %s", o.ObservationID)
		}
	}
	evidence := map[string]any{"scope": "synthetic 50 observations, actual model, no recommendation quality claim", "model": model.ProviderModelID, "candidates": len(payload.Ranked), "axes": 8, "maxOutputTokens": model.MaxOutputTokens, "durationMs": time.Since(started).Milliseconds(), "inputTokens": response.Usage.InputTokens, "outputTokens": response.Usage.OutputTokens}
	if path := os.Getenv("VITLANE_ROUTE_ASSESSMENT_EVIDENCE"); path != "" {
		raw, _ := json.MarshalIndent(evidence, "", "  ")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("50-candidate assessment: items=%d input=%d output=%d durationMs=%d", len(payload.Ranked), response.Usage.InputTokens, response.Usage.OutputTokens, time.Since(started).Milliseconds())
}
