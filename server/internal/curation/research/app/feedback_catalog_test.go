package app

import (
	"context"
	"testing"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

func TestInteractionSnapshotUsesOnlyCatalogVariantPreferences(t *testing.T) {
	repository := &liveReviewWorkspaceRepositoryV2{
		state: CatalogWorkspaceStoredStateV2{
			Interactions: []CatalogVariantInteractionV2{
				{TargetID: "target-1", CandidateID: "candidate-b", VariantID: "variant-2", Sentiment: "LIKE"},
				{TargetID: "target-2", CandidateID: "candidate-other", VariantID: "variant-9", Pinned: true, Sentiment: "NONE"},
				{TargetID: "target-1", CandidateID: "candidate-a", VariantID: "variant-1", Pinned: true, Sentiment: "NONE"},
				{TargetID: "target-1", CandidateID: "candidate-neutral", VariantID: "variant-3", Sentiment: "NONE"},
			},
		},
	}
	service := &Service{liveCatalog: &LiveCatalogReviewServiceV2{workspace: repository}}

	snapshot, err := service.interactionSnapshot(
		context.Background(), "user-1", "curation-1", "target-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Variants) != 2 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if snapshot.Variants[0].CandidateID != "candidate-a" ||
		!snapshot.Variants[0].Pinned ||
		snapshot.Variants[0].Sentiment != researchdomain.CatalogVariantSentimentNone ||
		snapshot.Variants[1].CandidateID != "candidate-b" ||
		snapshot.Variants[1].Sentiment != researchdomain.CatalogVariantSentimentLike {
		t.Fatalf("variants=%#v", snapshot.Variants)
	}
}
