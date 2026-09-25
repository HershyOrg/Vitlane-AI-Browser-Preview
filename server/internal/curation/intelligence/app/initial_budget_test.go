package app

import (
	"context"
	intelligencedomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/domain"
	"reflect"
	"strings"
	"testing"
	"time"
)

type initialBudgetProducts struct {
	pipelineResearchProductsV2
	input, submitted PlanningContext
}

func (p *initialBudgetProducts) ReadPlanningContext(context.Context, string, string) (PlanningContext, error) {
	return p.input, nil
}
func (p *initialBudgetProducts) SubmitPlanning(_ context.Context, _, _ string, c PlanningContext, targets []ProposedTarget) (SubmissionOutcome, error) {
	p.submitted = c
	p.plannedTargets = targets
	return SubmissionOutcome{Accepted: true}, nil
}

type initialBudgetProvider struct {
	pipelineResearchProviderV2
	requests []CompletionRequest
}

func (p *initialBudgetProvider) Complete(_ context.Context, r CompletionRequest) (CompletionResult, error) {
	p.requests = append(p.requests, r)
	if r.SchemaName == BudgetEstimatesSchemaName {
		return CompletionResult{Content: `{"estimates":[{"amount":"100000"}]}`}, nil
	}
	return CompletionResult{Content: `{"budget":{"amount":"100000","currency":"KRW"},"targets":[{"title":"만년필","category":"Stationery","searchQuery":"fountain pen","quantity":2,"rationale":"Requested pen."}]}`}, nil
}
func TestInitialInferenceNeverOverridesExplicitSettingsOrExpansion(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		initial, infer, enabled bool
	}{
		{"untouched init", true, true, false}, {"explicit none", true, false, false}, {"explicit amount", true, false, true}, {"curation", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			job := intelligencedomain.Job{ID: "job-1", UserID: "user-1", Provider: intelligencedomain.ProviderManaged, ModelKey: "model", Target: intelligencedomain.JobTarget{Kind: intelligencedomain.TargetPlanningTask, ID: "task-1"}}
			attempt := intelligencedomain.Attempt{ID: "attempt-1", DeadlineAt: now.Add(time.Minute)}
			provider := &initialBudgetProvider{}
			registry, err := NewProviderRegistry(RegisteredProvider{Provider: provider, Concurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			products := &initialBudgetProducts{input: PlanningContext{OriginalIntent: "개당 5만원 만년필 2개", TaskID: "task-1", PlanID: "plan-1", Country: "US", PlanningMode: "AUTO", InitialRun: tc.initial, BudgetPolicy: true, BudgetInferenceAllowed: tc.infer, BudgetEnabled: tc.enabled, BudgetCurrency: "USD", BudgetAllocationMode: "AUTO", TotalBudget: Money{Amount: "50", Currency: "USD"}}}
			service := &Service{repository: &pipelineResearchRepositoryV2{}, products: products, providers: registry, clock: pipelineResearchClockV2{now: now}, ids: &pipelineResearchIDsV2{}, logger: cancelTestLogger()}
			err = service.runPlanning(context.Background(), ClaimedJob{Job: job, Attempt: attempt}, &stepTracker{service: service, job: job, attempt: attempt})
			if err != nil {
				t.Fatal(err)
			}
			allowed := tc.initial && tc.infer
			if (products.submitted.ResolvedBudget != nil) != allowed {
				t.Fatalf("resolved=%+v", products.submitted.ResolvedBudget)
			}
			if !allowed && products.submitted.BudgetCurrency != "USD" {
				t.Fatal("model overrode explicit currency")
			}
			if products.plannedTargets[0].Quantity != 2 {
				t.Fatal("goal quantity lost")
			}
			_, schemaAllows := provider.requests[0].Schema["properties"].(map[string]any)["budget"]
			if schemaAllows != allowed {
				t.Fatal("schema allows budget outside init")
			}
			if !allowed && !strings.Contains(provider.requests[0].SystemPrompt, "Ignore natural-language") {
				t.Fatal("missing text budget policy")
			}
		})
	}
}

func TestInferredCurrencyUsesRequestUnitOrConfiguredCurrency(t *testing.T) {
	for _, tc := range []struct {
		text, currency string
		want           []string
	}{
		{"펜 10000 이내", "KRW", []string{"KRW"}},
		{"pen under 10", "USD", []string{"USD"}},
		{"원하는 펜, 20 이내", "USD", []string{"USD"}},
		{"pen under $10", "KRW", []string{"USD"}},
		{"만원 내의 펜", "USD", []string{"KRW"}},
	} {
		if got := inferredBudgetCurrencies(PlanningContext{OriginalIntent: tc.text, BudgetCurrency: tc.currency, Country: "US"}); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: %v", tc.text, got)
		}
	}
}
