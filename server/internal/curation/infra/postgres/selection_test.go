package postgres

import (
	"testing"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

func TestSelectionSnapshotHashMatchesGoldenVector(t *testing.T) {
	t.Parallel()
	hash, err := selectionSnapshotHash(curationdomain.CurationSelection{
		ID:                         "44444444-4444-4444-8444-444444444444",
		CurationID:                 "22222222-2222-4222-8222-222222222222",
		PlanTargetID:               "77777777-7777-4777-8777-777777777777",
		ShoppingSessionID:          "88888888-8888-4888-8888-888888888888",
		CandidateID:                "99999999-9999-4999-8999-999999999999",
		CandidateConfigurationID:   "66666666-6666-4666-8666-666666666666",
		CandidateConfigurationHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Quantity:                   2,
		Version:                    3,
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = "sha256:e2654daccb61c0e3379167dd8104bce4b2ff6134be8e2e3a675b21c98d0fea2b"
	if hash != want {
		t.Fatalf("hash=%q want=%q", hash, want)
	}
}
