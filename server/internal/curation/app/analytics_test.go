package app

import (
	"context"
	"errors"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"testing"
	"time"
)

type analyticsCapture struct{ events []sharedapp.AnalyticsEvent }

func (c *analyticsCapture) Record(e sharedapp.AnalyticsEvent) { c.events = append(c.events, e) }

type failedAnalyticsCommit struct{}

func (failedAnalyticsCommit) WithinTransaction(ctx context.Context, f func(context.Context) error) error {
	if err := f(ctx); err != nil {
		return err
	}
	return errors.New("commit failed")
}
func TestCurationAnalyticsExcludesReplayAndFailedCommit(t *testing.T) {
	input := CreatePlanInput{UserID: "user-1", OriginalIntent: "camping chair", PlanningMode: "SINGLE", ExecutionMode: "EXPERIMENT", TotalBudget: MoneyInput{Amount: "100", Currency: "USD"}, Country: "KR", City: "Seoul", Category: "camping-chair", URLMode: "NONE", AgentMode: "MANAGED", ModelKey: "gpt-5-nano", IdempotencyKey: "11111111-1111-4111-8111-111111111111"}
	s := newTestService(&memoryPlanningRepository{}, &memorySessions{})
	sink := &analyticsCapture{}
	ctx := sharedapp.WithAnalyticsSink(context.Background(), sink)
	if _, err := s.CreatePlan(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePlan(ctx, input); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].Name != "curation_created" {
		t.Fatal(sink.events)
	}
	failed := newTestService(&memoryPlanningRepository{}, &memorySessions{})
	failed.transactor = failedAnalyticsCommit{}
	if _, err := failed.CreatePlan(ctx, input); err == nil {
		t.Fatal("expected commit failure")
	}
	if len(sink.events) != 1 {
		t.Fatal("rollback emitted optional analytics")
	}
	refused := newTestService(&memoryPlanningRepository{}, &memorySessions{})
	if _, err := refused.CreatePlan(context.Background(), input); err != nil {
		t.Fatal("consent affected required business write", err)
	}
}
func TestCartAnalyticsOnlyForSuccessfulVersion(t *testing.T) {
	repo := &catalogCartRepositoryV2{state: CatalogCartStateV2{Version: 0}}
	s, err := NewCatalogCartServiceV2(repo, fixedClock{now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	sink := &analyticsCapture{}
	ctx := sharedapp.WithAnalyticsSink(context.Background(), sink)
	if _, err := s.Replace(ctx, "user", "curation", 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Replace(ctx, "user", "curation", 0, nil); err == nil {
		t.Fatal("stale version accepted")
	}
	if len(sink.events) != 1 || sink.events[0].Name != "cart_updated" {
		t.Fatal(sink.events)
	}
}
