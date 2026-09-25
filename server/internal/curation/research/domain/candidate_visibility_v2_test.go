package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestProjectCandidateVisibilityV2UsesLatestBatchPinAndEyeToggle(
	t *testing.T,
) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	snapshot, initial, discoveries := candidatePoolSnapshotWithCandidatesV2(
		t, 2, now,
	)

	third := candidateAdmissionFixtureV2(
		3, CandidateAssessmentExpansionV1, now.Add(time.Minute),
	)
	third.RankPosition = 1
	secondInput := candidateBatchInputFixtureV2(
		"batch-current", snapshot.Pool.Version, CandidateBatchExpansionV2,
		CandidateAssessmentExpansionV1, now.Add(time.Minute), third,
	)
	second, err := FinalizeCandidateBatchV2(snapshot, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	snapshot = candidatePoolSnapshotAfterV2(snapshot, second)
	discoveries = append(discoveries, second.Discoveries...)

	// Correlation joins Candidate 2 into Candidate 1 without moving either
	// Candidate row. The empty alias batch must preserve batch-current.
	aliasInput := candidateBatchInputFixtureV2(
		"batch-alias-empty", snapshot.Pool.Version,
		CandidateBatchExpansionV2, CandidateAssessmentExpansionV1,
		now.Add(2*time.Minute),
	)
	aliasInput.Aliases = []CandidateIdentityAliasInputV2{{
		ID:                     "alias-2-to-1",
		ProvisionalIdentityKey: initial.Candidates[1].IdentityKey,
		CanonicalCandidateID:   initial.Candidates[0].ID,
		CorrelationEvidenceRefs: []string{
			"exact-correlation-evidence",
		},
		CreatedAt: now.Add(2 * time.Minute),
	}}
	aliasCommit, err := FinalizeCandidateBatchV2(snapshot, aliasInput)
	if err != nil {
		t.Fatal(err)
	}
	if aliasCommit.Batch.Status != CandidateBatchCompletedEmptyV2 ||
		aliasCommit.Pool.CurrentVisibleBatchID != "batch-current" ||
		aliasCommit.Pool.CanonicalIdentityCount != 2 {
		t.Fatalf("alias empty commit=%#v", aliasCommit)
	}
	snapshot = candidatePoolSnapshotAfterV2(snapshot, aliasCommit)
	beforeProjection := snapshot

	defaultVisible, err := ProjectCandidateVisibilityV2(
		CandidateVisibilityProjectionInputV2{
			Snapshot: snapshot, Discoveries: discoveries,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultVisible) != 1 ||
		defaultVisible[0].CanonicalCandidateID != "candidate-3" ||
		defaultVisible[0].VisibilityReason !=
			CandidateVisibilityLatestBatchV2 {
		t.Fatalf("default visibility=%#v", defaultVisible)
	}

	pinnedVisible, err := ProjectCandidateVisibilityV2(
		CandidateVisibilityProjectionInputV2{
			Snapshot: snapshot, Discoveries: discoveries,
			PinnedCandidateIDs: []string{"candidate-2"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinnedVisible) != 2 ||
		pinnedVisible[0].CanonicalCandidateID != "candidate-3" ||
		pinnedVisible[1].CanonicalCandidateID != "candidate-1" ||
		pinnedVisible[1].VisibilityReason != CandidateVisibilityPinnedV2 ||
		!reflect.DeepEqual(
			pinnedVisible[1].MemberCandidateIDs,
			[]string{"candidate-1", "candidate-2"},
		) {
		t.Fatalf("pinned visibility=%#v", pinnedVisible)
	}

	historicalVisible, err := ProjectCandidateVisibilityV2(
		CandidateVisibilityProjectionInputV2{
			Snapshot: snapshot, Discoveries: discoveries,
			IncludeHistoricalCandidates: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(historicalVisible) != 2 ||
		historicalVisible[0].VisibilityReason !=
			CandidateVisibilityLatestBatchV2 ||
		historicalVisible[1].CanonicalCandidateID != "candidate-1" ||
		historicalVisible[1].VisibilityReason !=
			CandidateVisibilityHistoricalV2 {
		t.Fatalf("historical visibility=%#v", historicalVisible)
	}
	if !reflect.DeepEqual(beforeProjection, snapshot) {
		t.Fatal("visibility projection mutated CandidatePool state")
	}
}
