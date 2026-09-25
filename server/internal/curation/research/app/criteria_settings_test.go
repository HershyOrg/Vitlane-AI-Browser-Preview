package app

import (
	"context"
	"reflect"
	"testing"

	c "github.com/vitlane/vitlane/server/internal/curation/app"
	d "github.com/vitlane/vitlane/server/internal/curation/domain"
	rd "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	session "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
)

type frozenCriteriaPlans struct {
	testPlanningService
	criteria *d.TargetCriteriaSetV1
	writes   int
}

func (p *frozenCriteriaPlans) LockTargetCriteria(context.Context, string, string, string) (*d.TargetCriteriaSetV1, error) {
	return p.criteria, nil
}
func (p *frozenCriteriaPlans) TargetCriteria(context.Context, string, string, string) (*d.TargetCriteriaSetV1, error) {
	return p.criteria, nil
}
func (p *frozenCriteriaPlans) ChangeTargetCriteria(_ context.Context, _, _, _ string, cmd c.CriteriaCommand) (d.TargetCriteriaSetV1, error) {
	p.writes++
	p.criteria = &cmd.Criteria
	return cmd.Criteria, nil
}
func (*frozenCriteriaPlans) ContentLocale(context.Context, string) (string, error) {
	return "ko-KR", nil
}
func (*frozenCriteriaPlans) PlanContentLocale(context.Context, string, string) (string, error) {
	return "ko-KR", nil
}

type frozenCriteriaRepository struct {
	memoryResearchRepository
	saved *CatalogIntelligenceCatalogQuery
}

func (r *frozenCriteriaRepository) ReadCriteriaCheckpoint(context.Context, string, string) (*CatalogIntelligenceCatalogQuery, error) {
	return r.saved, nil
}
func (r *frozenCriteriaRepository) SaveCriteriaCheckpoint(_ context.Context, _, _ string, q CatalogIntelligenceCatalogQuery) error {
	r.saved = &q
	return nil
}

func TestResearchCheckpointCannotWriteExistingSettings(t *testing.T) {
	for _, mode := range []string{"changed", "omitted", "stale", "initial", "manual-feedback"} {
		t.Run(mode, func(t *testing.T) {
			original := &d.TargetCriteriaSetV1{SchemaVersion: d.CriteriaSchema, Version: 3, Subject: d.ResearchSubject{Label: "펜", ProductType: "pen"}, Axes: []d.ResearchAxis{{AxisID: "comfort", Label: "필기감", Definition: "쓰기 편안함", Importance: 5, Origin: "USER_EDIT"}}, Exclusions: []string{"used"}}
			plans := &frozenCriteriaPlans{criteria: original}
			repo := &frozenCriteriaRepository{memoryResearchRepository: memoryResearchRepository{round: rd.ResearchRound{ID: testRoundID, UserID: "user-1", Status: "REQUESTED"}}}
			service := &Service{plans: plans, repository: repo, transactor: testTransactor{}}
			proposed := *original
			proposed.Subject.Label = "변경된 대상"
			proposed.Axes = []d.ResearchAxis{{AxisID: "price", Label: "가성비", Importance: 1, Definition: "가격", UsesPrice: true, Origin: "FEEDBACK"}}
			proposed.Exclusions = []string{}
			q := CatalogIntelligenceCatalogQuery{Criteria: &proposed, Query: "smooth pen", QuerySeeds: []string{"smooth pen"}}
			snapshot := ResearchContext{PlanID: "plan-1", Target: session.TargetSnapshot{ID: "target-1", CurationID: "curation-1"}, Criteria: original, ResearchScope: session.ResearchScopeSnapshot{Country: "US"}}
			if mode == "omitted" {
				q.Criteria = nil
			}
			if mode == "stale" {
				stale := *original
				stale.Version--
				snapshot.Criteria = &stale
			}
			if mode == "initial" {
				plans.criteria = nil
				snapshot.Criteria = nil
			}
			ctx := context.Background()
			if mode == "manual-feedback" {
				ctx = c.WithThreadFeedbackCriteria(c.WithThreadExecution(ctx, "thread", "step"))
			}
			got, err := service.checkpointCriteria(ctx, "user-1", testRoundID, snapshot, q)
			if mode == "stale" {
				if err == nil || repo.saved != nil || plans.writes != 0 {
					t.Fatal("stale settings accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "manual-feedback" {
				if plans.writes != 1 || got.Criteria.Version != 4 {
					t.Fatal("explicit manual feedback was not recorded")
				}
				return
			}
			if mode == "initial" {
				if plans.writes != 1 || got.Criteria.Version != 1 {
					t.Fatal("new target criteria were not initialized")
				}
				return
			}
			if plans.writes != 0 || !reflect.DeepEqual(got.Criteria, original) || !reflect.DeepEqual(repo.saved.Criteria, original) {
				t.Fatalf("research changed settings: %+v", got)
			}
			// A replay uses the captured criteria even if settings are explicitly edited later.
			newer := *original
			newer.Version++
			plans.criteria = &newer
			again, err := service.checkpointCriteria(context.Background(), "user-1", testRoundID, snapshot, q)
			if err != nil || !reflect.DeepEqual(again.Criteria, original) || plans.writes != 0 {
				t.Fatal("checkpoint did not preserve original criteria")
			}
		})
	}
}
