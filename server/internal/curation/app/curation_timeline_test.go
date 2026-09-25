package app

import (
	"context"
	"errors"
	"testing"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

type curationTimelineTestRepository struct {
	Repository
	items []CurationTimelineItem
	limit int
}

func (r *curationTimelineTestRepository) ListCurationTimeline(
	_ context.Context,
	_, _ string,
	limit int,
) ([]CurationTimelineItem, error) {
	r.limit = limit
	return append([]CurationTimelineItem(nil), r.items...), nil
}

func TestListCurationTimelineCapsLimitAndReturnsSafeProjection(
	t *testing.T,
) {
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	repository := &curationTimelineTestRepository{
		items: []CurationTimelineItem{{
			Action: CurationTimelineAction{
				ID:         "11111111-1111-4111-8111-111111111111",
				CurationID: "22222222-2222-4222-8222-222222222222",
				Type: curationdomain.
					CurationActionPlanningAddTargets,
				PhaseAtRequest: curationdomain.
					CurationActionPhasePlanning,
				SubjectType: curationdomain.
					CurationActionSubjectTargetList,
				EffectKind: curationdomain.
					CurationActionEffectIntelligence,
				ExpectedCurationVersion: 1,
				CreatedAt:               now,
			},
			DisplayBody: "업무용 조명도 추가해줘",
			Result: &CurationTimelineResult{
				Kind:       CurationTimelineResultTargetExpansion,
				Summary:    "Target 1개를 추가했습니다.",
				OccurredAt: now,
				Diff: &CurationTimelineDiff{
					Added: []string{"업무용 조명"},
				},
			},
		}},
	}
	service := &Service{repository: repository}
	items, err := service.ListCurationTimeline(
		context.Background(),
		"user-1",
		"22222222-2222-4222-8222-222222222222",
		1000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if repository.limit != maxCurationTimelineListLimit ||
		len(items) != 1 ||
		items[0].DisplayBody != "업무용 조명도 추가해줘" ||
		items[0].Result == nil ||
		len(items[0].Result.Diff.Added) != 1 {
		t.Fatalf(
			"limit=%d items=%#v",
			repository.limit,
			items,
		)
	}
}

func TestListCurationTimelineRejectsPatchOnlyComponentHook(
	t *testing.T,
) {
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	selectionID := "33333333-3333-4333-8333-333333333333"
	repository := &curationTimelineTestRepository{
		items: []CurationTimelineItem{{
			Action: CurationTimelineAction{
				ID:         "11111111-1111-4111-8111-111111111111",
				CurationID: "22222222-2222-4222-8222-222222222222",
				Type: curationdomain.
					CurationActionSelectionMutation,
				PhaseAtRequest: curationdomain.
					CurationActionPhaseCurating,
				SubjectType: curationdomain.
					CurationActionSubjectSelection,
				SubjectID: &selectionID,
				EffectKind: curationdomain.
					CurationActionEffectNone,
				ExpectedCurationVersion: 2,
				CreatedAt:               now,
			},
			DisplayBody: "CREATE",
		}},
	}
	service := &Service{repository: repository}
	_, err := service.ListCurationTimeline(
		context.Background(),
		"user-1",
		"22222222-2222-4222-8222-222222222222",
		10,
	)
	if !errors.Is(err, curationdomain.ErrCurationActionInvalid) {
		t.Fatalf(
			"error=%v want=%v",
			err,
			curationdomain.ErrCurationActionInvalid,
		)
	}
}

func TestListCurationTimelineRejectsRemovedPurchaseAction(t *testing.T) {
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	testCases := []struct {
		name string
		item CurationTimelineItem
	}{
		{
			name: "removed-purchase-action",
			item: CurationTimelineItem{
				Action: CurationTimelineAction{
					ID: "11111111-1111-4111-8111-111111111111",
					CurationID: "22222222-2222-4222-8222-" +
						"222222222222",
					Type: curationdomain.CurationActionType("PURCHASE_DIRECT"),
					PhaseAtRequest: curationdomain.
						CurationActionPhaseCurating,
					ExpectedCurationVersion: 2,
					CreatedAt:               now,
				},
				DisplayBody: "configuration-id-must-not-leak",
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &curationTimelineTestRepository{
				items: []CurationTimelineItem{testCase.item},
			}
			service := &Service{repository: repository}
			_, err := service.ListCurationTimeline(
				context.Background(),
				"user-1",
				"22222222-2222-4222-8222-222222222222",
				10,
			)
			if !errors.Is(
				err,
				curationdomain.ErrCurationActionInvalid,
			) {
				t.Fatalf(
					"error=%v want=%v",
					err,
					curationdomain.ErrCurationActionInvalid,
				)
			}
		})
	}
}
