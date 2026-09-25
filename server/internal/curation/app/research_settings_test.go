package app

import (
	"context"
	"testing"
)

type selectionRecorder struct{ calls [][3]string }

func (r *selectionRecorder) RememberResearchSelection(_ context.Context, user, country, currency string) error {
	r.calls = append(r.calls, [3]string{user, country, currency})
	return nil
}
func TestPlanCreationAndReplayDoNotOverwriteAccountResearchPreferences(t *testing.T) {
	repository := &memoryPlanningRepository{}
	service := newTestService(repository, &memorySessions{})
	recorder := &selectionRecorder{}
	service.EnableResearchSelectionRecorder(recorder)
	input := CreatePlanInput{UserID: "user-1", OriginalIntent: "한국 문구", PlanningMode: "SINGLE", ExecutionMode: "LIVE", TotalBudget: MoneyInput{Amount: "100000", Currency: "KRW"}, Country: "KR", Category: "stationery", URLMode: "NONE", AgentMode: "MANAGED", ModelKey: "gpt-5.6-luna", IdempotencyKey: "32222222-2222-4222-8222-222222222223"}
	if _, err := service.CreatePlan(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreatePlan(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if len(recorder.calls) != 0 {
		t.Fatal("plan submission or replay overwrote account settings", recorder.calls)
	}
	input.Country = "INVALID"
	if _, err := service.CreatePlan(context.Background(), input); err == nil {
		t.Fatal("invalid country accepted")
	}
	if len(recorder.calls) != 0 {
		t.Fatal("invalid input wrote Last Select")
	}
}
