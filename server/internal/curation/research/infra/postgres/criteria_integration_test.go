package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	curationpostgres "github.com/vitlane/vitlane/server/internal/curation/infra/postgres"
	planningpostgres "github.com/vitlane/vitlane/server/internal/curation/planning/infra/postgres"
	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestCriteriaCASLocaleAndCandidateAssessmentRemainIndependent(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := openCatalogPoolIntegrationDatabaseV2(t, ctx, url)
	seedCatalogPoolIntegrationTargetV2(t, ctx, db)
	const user = "98000000-0000-4000-8000-000000000001"
	const cur = "98000000-0000-4000-8000-000000000003"
	const target = "98000000-0000-4000-8000-000000000004"
	repo := curationpostgres.NewRepository(db, planningpostgres.NewRepository(db))
	criteria := curationdomain.TargetCriteriaSetV1{SchemaVersion: curationdomain.CriteriaSchema, Version: 1, Subject: curationdomain.ResearchSubject{Label: "만년필", ProductType: "fountain pen"}, Axes: []curationdomain.ResearchAxis{{AxisID: "writing", Label: "필기감", Definition: "쓰기의 편안함", Importance: 5, Origin: "REQUEST"}}, Exclusions: []string{}}
	command := curationapp.CriteriaCommand{SchemaVersion: "vitlane.criteria-command.v1", ExpectedCurationVersion: 1, ExpectedCriteriaVersion: 0, IdempotencyKey: "initial", Criteria: criteria}
	save := func(c curationapp.CriteriaCommand) (curationdomain.TargetCriteriaSetV1, error) {
		var v curationdomain.TargetCriteriaSetV1
		err := db.WithinTransaction(ctx, func(tx context.Context) error {
			var e error
			v, e = repo.SaveCriteria(tx, user, cur, target, c)
			return e
		})
		return v, err
	}
	if _, err := save(command); err != nil {
		t.Fatal(err)
	}
	if _, err := save(command); err != nil {
		t.Fatalf("replay: %v", err)
	}
	conflicting := command
	conflicting.IdempotencyKey = "stale"
	if _, err := save(conflicting); err == nil {
		t.Fatal("stale criteria version accepted")
	} else if f, ok := fault.As(err); !ok || f.Reason != "RESEARCH_CRITERIA_CHANGED" {
		t.Fatal(err)
	}
	conflicting = command
	conflicting.Criteria.Subject.Label = "changed"
	if _, err := save(conflicting); err == nil {
		t.Fatal("same key accepted different body")
	}
	if _, err := repo.ReadCriteria(ctx, "98000000-0000-4000-8000-000000000099", cur, target); err == nil {
		t.Fatal("foreign user read criteria")
	}
	if err := repo.SavePlanContentLocale(ctx, user, target, "ko-KR"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SavePlanContentLocale(ctx, user, target, "en-US"); err != nil {
		t.Fatal(err)
	}
	if locale, err := repo.ReadPlanContentLocale(ctx, user, target); err != nil || locale != "ko-KR" {
		t.Fatalf("locale overwritten %s %v", locale, err)
	}
	research := NewRepository(db)
	now := time.Now().UTC()
	assessment, err := researchdomain.NewAxisAssessment(criteria, []researchdomain.AxisScoreV1{{AxisID: "writing", ScorePercent: 97, Basis: "UNKNOWN", Explanation: "필기감은 직접 확인하지 못했습니다."}}, "ko-KR", "round-original", "model-original", now)
	if err != nil {
		t.Fatal(err)
	}
	pool := catalogPoolIntegrationCommandV2(user, cur, target, "discover", 0, researchapp.CatalogResearchAppendV2, 'a')
	pre, err := research.PreflightCatalogSearchV2(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	pool = catalogPoolCommandWithReservationV2(pool, pre)
	candidate := catalogCandidateReferenceForPoolTestV2(user, cur, target, "candidate-original", "product-original", "shopify-product:product-original", "https://shop.example/products/original", 0, now)
	candidate.Assessment.AxisAssessment = assessment
	candidate.Assessment.IntentPoint = "처음 설명"
	if _, err = research.CompleteCatalogSearchV2(ctx, pool, []researchapp.CatalogCandidateReferenceV2{candidate}, researchapp.LiveCatalogReviewMetricsV2{CompletedAt: now}); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = db.DB.QueryRowContext(ctx, `SELECT axis_assessment::text FROM phase8_research_candidates WHERE candidate_id='candidate-original'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	next := command
	next.ExpectedCriteriaVersion = 1
	next.IdempotencyKey = "add-axis"
	next.Criteria.Version = 2
	next.Criteria.Axes = append(next.Criteria.Axes, curationdomain.ResearchAxis{AxisID: "portable", Label: "휴대성", Definition: "휴대하기 쉬움", Importance: 3, Origin: "USER_EDIT"})
	if _, err = save(next); err != nil {
		t.Fatal(err)
	}

	retired := next
	retired.ExpectedCriteriaVersion = 2
	retired.Criteria.Version = 3
	retired.IdempotencyKey = "retire-writing"
	retired.Criteria.Axes = append([]curationdomain.ResearchAxis{}, next.Criteria.Axes[1:]...)
	if _, err = save(retired); err != nil {
		t.Fatal(err)
	}
	reused := retired
	reused.ExpectedCriteriaVersion = 3
	reused.Criteria.Version = 4
	reused.IdempotencyKey = "reuse-writing-meaning"
	reused.Criteria.Axes = append(append([]curationdomain.ResearchAxis{}, retired.Criteria.Axes...), curationdomain.ResearchAxis{AxisID: "writing", Label: "unrelated", Definition: "a different meaning", Importance: 2, Origin: "USER_EDIT"})
	if _, err = save(reused); err == nil {
		t.Fatal("retired axis identity reused with different meaning")
	}
	restored := reused
	restored.IdempotencyKey = "restore-writing"
	restored.Criteria.Axes = append(append([]curationdomain.ResearchAxis{}, retired.Criteria.Axes...), criteria.Axes[0])
	if _, err = save(restored); err != nil {
		t.Fatalf("same meaning restoration: %v", err)
	}
	if _, err = db.DB.ExecContext(ctx, `UPDATE phase8_research_candidates SET visible=false WHERE candidate_id='candidate-original'`); err != nil {
		t.Fatal(err)
	}
	pool = catalogPoolIntegrationCommandV2(user, cur, target, "rediscover", 1, researchapp.CatalogResearchAppendV2, 'b')
	pre, err = research.PreflightCatalogSearchV2(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	pool = catalogPoolCommandWithReservationV2(pool, pre)
	candidate.Assessment.IntentPoint = "should not replace"
	candidate.Assessment.AxisAssessment = nil
	if _, err = research.CompleteCatalogSearchV2(ctx, pool, []researchapp.CatalogCandidateReferenceV2{candidate}, researchapp.LiveCatalogReviewMetricsV2{CompletedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	var after, intent string
	var visible bool
	if err = db.DB.QueryRowContext(ctx, `SELECT axis_assessment::text,intent_point_snapshot,visible FROM phase8_research_candidates WHERE candidate_id='candidate-original'`).Scan(&after, &intent, &visible); err != nil {
		t.Fatal(err)
	}
	if after != before || intent != "처음 설명" || visible {
		t.Fatalf("existing content changed: %s %s visible=%v", before, after, visible)
	}
}
