package app

import (
	"context"
	"encoding/json"
	"testing"

	session "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
)

type recordedLocalePlans struct {
	frozenCriteriaPlans
	current, recorded string
}

func (p *recordedLocalePlans) ContentLocale(context.Context, string) (string, error) {
	return p.current, nil
}
func (p *recordedLocalePlans) PlanContentLocale(context.Context, string, string) (string, error) {
	return p.recorded, nil
}

func TestRoundContentLocaleKeepsTheRecordedLanguageWithoutABrowser(t *testing.T) {
	for _, tc := range []struct {
		name    string
		round   int
		current string
		want    string
	}{
		{"first round keeps the plan language", 1, "en-US", "ko-KR"},
		{"requested later round follows the account or browser", 2, "en-US", "en-US"},
		{"background later round keeps the plan language", 2, "", "ko-KR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &Service{plans: &recordedLocalePlans{current: tc.current, recorded: "ko-KR"}}
			raw, err := json.Marshal(ResearchContext{RoundNumber: tc.round, PlanID: "plan-1", Target: session.TargetSnapshot{ID: "target-1", CurationID: "curation-1"}})
			if err != nil {
				t.Fatal(err)
			}
			enriched, err := service.attachCriteria(context.Background(), "user-1", "plan-1", raw)
			if err != nil {
				t.Fatal(err)
			}
			var got ResearchContext
			if err := json.Unmarshal(enriched, &got); err != nil {
				t.Fatal(err)
			}
			if got.ContentLocale != tc.want {
				t.Fatalf("ContentLocale = %q, want %q", got.ContentLocale, tc.want)
			}
		})
	}
}
