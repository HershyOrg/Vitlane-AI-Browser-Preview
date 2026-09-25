package app

import (
	"context"
	"errors"
	"testing"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestSaveWorkspaceInteractionWritesLikeAndInteractionThroughOneRepositoryCommand(t *testing.T) {
	clock := &catalogPreferenceClockV2{now: time.Date(2026, 8, 13, 3, 4, 5, 0, time.UTC)}
	repository := &catalogPreferenceRepositoryV2{reference: catalogPreferenceCandidateV2()}
	service := &LiveCatalogReviewServiceV2{workspace: repository, clock: clock}
	input := catalogLikedPreferenceInputV2()

	if err := service.SaveWorkspaceInteractionV2(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if repository.calls != 1 || repository.interaction.Sentiment != "LIKE" ||
		!repository.interaction.Pinned || repository.liked == nil {
		t.Fatalf("calls=%d interaction=%+v liked=%+v", repository.calls, repository.interaction, repository.liked)
	}
	if repository.liked.UserID != input.UserID ||
		repository.liked.CurationID != input.CurationID ||
		repository.liked.CandidateID != input.CandidateID ||
		repository.liked.VariantID != input.VariantID ||
		repository.liked.UpdatedAt != clock.now {
		t.Fatalf("liked identity/time=%+v", repository.liked)
	}
}

func TestSaveWorkspaceInteractionDeletesLikeForNoneAndDislike(t *testing.T) {
	for _, sentiment := range []string{"NONE", "DISLIKE"} {
		t.Run(sentiment, func(t *testing.T) {
			repository := &catalogPreferenceRepositoryV2{reference: catalogPreferenceCandidateV2()}
			service := &LiveCatalogReviewServiceV2{
				workspace: repository,
				clock:     &catalogPreferenceClockV2{now: time.Date(2026, 8, 13, 3, 4, 5, 0, time.UTC)},
			}
			input := catalogLikedPreferenceInputV2()
			input.Sentiment = sentiment
			input.LikedSnapshot = nil
			if err := service.SaveWorkspaceInteractionV2(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if repository.calls != 1 || repository.liked != nil ||
				repository.interaction.Sentiment != sentiment {
				t.Fatalf("calls=%d interaction=%+v liked=%+v", repository.calls, repository.interaction, repository.liked)
			}
		})
	}
}

func TestSaveWorkspaceInteractionRejectsLikeWithoutDisplaySnapshotBeforeWrite(t *testing.T) {
	repository := &catalogPreferenceRepositoryV2{reference: catalogPreferenceCandidateV2()}
	service := &LiveCatalogReviewServiceV2{
		workspace: repository,
		clock:     &catalogPreferenceClockV2{now: time.Date(2026, 8, 13, 3, 4, 5, 0, time.UTC)},
	}
	input := catalogLikedPreferenceInputV2()
	input.LikedSnapshot = nil

	err := service.SaveWorkspaceInteractionV2(context.Background(), input)
	failure, ok := fault.As(err)
	if !ok || failure.Reason != "PHASE8_LIKED_VARIANT_SNAPSHOT_REQUIRED" || repository.calls != 0 {
		t.Fatalf("error=%v failure=%+v writes=%d", err, failure, repository.calls)
	}
}

func TestSaveWorkspaceInteractionRejectsInvalidLikeSnapshotBeforeWrite(t *testing.T) {
	repository := &catalogPreferenceRepositoryV2{reference: catalogPreferenceCandidateV2()}
	service := &LiveCatalogReviewServiceV2{
		workspace: repository,
		clock:     &catalogPreferenceClockV2{now: time.Date(2026, 8, 13, 3, 4, 5, 0, time.UTC)},
	}
	input := catalogLikedPreferenceInputV2()
	input.LikedSnapshot.Currency = "US"

	err := service.SaveWorkspaceInteractionV2(context.Background(), input)
	if !errors.Is(err, researchdomain.ErrLikedVariantInvalid) || repository.calls != 0 {
		t.Fatalf("error=%v writes=%d", err, repository.calls)
	}
}

func TestSaveWorkspaceInteractionReplayConvergesOnSameDesiredState(t *testing.T) {
	clock := &catalogPreferenceClockV2{now: time.Date(2026, 8, 13, 3, 4, 5, 0, time.UTC)}
	repository := &catalogPreferenceRepositoryV2{reference: catalogPreferenceCandidateV2()}
	service := &LiveCatalogReviewServiceV2{workspace: repository, clock: clock}
	input := catalogLikedPreferenceInputV2()

	for range 2 {
		if err := service.SaveWorkspaceInteractionV2(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	if repository.calls != 2 || repository.interaction.UpdatedAt != clock.now ||
		repository.liked == nil || repository.liked.UpdatedAt != clock.now {
		t.Fatalf("replay did not converge: calls=%d interaction=%+v liked=%+v", repository.calls, repository.interaction, repository.liked)
	}
}

type catalogPreferenceClockV2 struct{ now time.Time }

func (clock *catalogPreferenceClockV2) Now() time.Time { return clock.now }

type catalogPreferenceRepositoryV2 struct {
	CatalogWorkspaceRepositoryV2
	reference   CatalogCandidateReferenceV2
	calls       int
	interaction CatalogVariantInteractionV2
	liked       *researchdomain.LikedVariantV2
}

func (repository *catalogPreferenceRepositoryV2) ResolveCatalogCandidateV2(
	context.Context, string, string, string,
) (CatalogCandidateReferenceV2, error) {
	return repository.reference, nil
}

func (repository *catalogPreferenceRepositoryV2) SaveCatalogVariantPreferenceV2(
	_ context.Context,
	_, _ string,
	interaction CatalogVariantInteractionV2,
	liked *researchdomain.LikedVariantV2,
) error {
	repository.calls++
	repository.interaction = interaction
	if liked == nil {
		repository.liked = nil
	} else {
		copy := *liked
		repository.liked = &copy
	}
	return nil
}

func catalogPreferenceCandidateV2() CatalogCandidateReferenceV2 {
	return CatalogCandidateReferenceV2{
		UserID:       "00000000-0000-4000-8000-000000000001",
		CurationID:   "00000000-0000-4000-8000-000000000002",
		PlanTargetID: "00000000-0000-4000-8000-000000000003",
		CandidateID:  "candidate-1",
	}
}

func catalogLikedPreferenceInputV2() CatalogSaveVariantInteractionInputV2 {
	return CatalogSaveVariantInteractionInputV2{
		UserID:      "00000000-0000-4000-8000-000000000001",
		CurationID:  "00000000-0000-4000-8000-000000000002",
		CandidateID: "candidate-1",
		VariantID:   "gid://shopify/ProductVariant/1",
		Pinned:      true,
		Sentiment:   "LIKE",
		LikedSnapshot: &CatalogLikedVariantSnapshotV2{
			ProductTitle: "Commuter Pack", VariantTitle: "Black / Small",
			ProductURL: "https://shop.example/products/commuter-pack?variant=1",
			Merchant:   "Shop Example", PriceMinor: 8200, Currency: "USD",
			TargetTitle: "Commuter backpack",
		},
	}
}
