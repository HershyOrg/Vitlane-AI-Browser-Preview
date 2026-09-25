package domain

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestFinalizeCandidateBatchV2AppendsStableCandidatesAndMovesPointer(
	t *testing.T,
) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	pool, err := NewCandidatePoolV2("target-1")
	if err != nil {
		t.Fatal(err)
	}
	initial := CandidatePoolSnapshotV2{Pool: pool}
	firstInput := candidateBatchInputFixtureV2(
		"batch-1", 1, CandidateBatchResearchRoundV2,
		CandidateAssessmentResearchRankingV1, now,
		candidateAdmissionFixtureV2(
			1, CandidateAssessmentResearchRankingV1, now,
		),
		candidateAdmissionFixtureV2(
			2, CandidateAssessmentResearchRankingV1, now,
		),
	)
	first, err := FinalizeCandidateBatchV2(initial, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.Pool.Version != 2 || first.Pool.CanonicalIdentityCount != 2 ||
		first.Pool.CurrentVisibleBatchID != "batch-1" ||
		first.Batch.Status != CandidateBatchCompletedNonemptyV2 ||
		first.Batch.AdmittedCount != 2 || len(first.Candidates) != 2 ||
		len(first.Discoveries) != 2 || len(first.Assessments) != 2 {
		t.Fatalf("first commit=%#v", first)
	}
	if first.Discoveries[0].SourceKind !=
		CandidateDiscoveryResearchRoundV2 ||
		first.Discoveries[0].SourceID != firstInput.ProcessSourceID {
		t.Fatalf("first discovery=%#v", first.Discoveries[0])
	}
	for _, candidate := range first.Candidates {
		if err := candidate.Validate(); err != nil {
			t.Fatalf("candidate validation: %v", err)
		}
	}
	for _, discovery := range first.Discoveries {
		if err := discovery.Validate(); err != nil {
			t.Fatalf("discovery validation: %v", err)
		}
	}
	for _, assessment := range first.Assessments {
		if err := assessment.Validate(); err != nil {
			t.Fatalf("assessment validation: %v", err)
		}
	}
	if err := first.Batch.Validate(); err != nil {
		t.Fatalf("batch validation: %v", err)
	}

	snapshot := candidatePoolSnapshotAfterV2(initial, first)
	reobserved := candidateAdmissionFixtureV2(
		1, CandidateAssessmentExpansionV1, now.Add(time.Minute),
	)
	reobserved.CandidateID = first.Candidates[0].ID
	reobserved.IdentityKey = first.Candidates[0].IdentityKey
	reobserved.DiscoveryID = "discovery-refresh-1"
	reobserved.Assessment.ID = "assessment-refresh-1"
	newCandidate := candidateAdmissionFixtureV2(
		3, CandidateAssessmentExpansionV1, now.Add(time.Minute),
	)
	newCandidate.RankPosition = 2
	secondInput := candidateBatchInputFixtureV2(
		"batch-2", 2, CandidateBatchExpansionV2,
		CandidateAssessmentExpansionV1, now.Add(time.Minute),
		reobserved, newCandidate,
	)
	second, err := FinalizeCandidateBatchV2(snapshot, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if second.Pool.Version != 3 || second.Pool.CanonicalIdentityCount != 3 ||
		second.Pool.CurrentVisibleBatchID != "batch-2" ||
		len(second.Candidates) != 1 ||
		second.Candidates[0].ID != "candidate-3" ||
		len(second.Discoveries) != 2 ||
		second.Discoveries[0].CandidateID != first.Candidates[0].ID ||
		second.Discoveries[0].SourceKind != CandidateDiscoveryExpansionV2 {
		t.Fatalf("second commit=%#v", second)
	}
	if first.Candidates[0].ContentHash != snapshot.Candidates[0].ContentHash {
		t.Fatal("rediscovery mutated the stable Candidate")
	}
}

func TestFinalizeCandidateBatchV2PreservesPointerForEmptyAndFailure(
	t *testing.T,
) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	snapshot, _, _ := candidatePoolSnapshotWithCandidatesV2(t, 1, now)
	originalPointer := snapshot.Pool.CurrentVisibleBatchID

	emptyInput := candidateBatchInputFixtureV2(
		"batch-empty", snapshot.Pool.Version,
		CandidateBatchExpansionV2, CandidateAssessmentExpansionV1,
		now.Add(time.Minute),
	)
	empty, err := FinalizeCandidateBatchV2(snapshot, emptyInput)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Batch.Status != CandidateBatchCompletedEmptyV2 ||
		empty.Batch.ClosedReason != CandidateBatchNoNewResultsV2 ||
		empty.Pool.CurrentVisibleBatchID != originalPointer ||
		empty.Pool.Version != snapshot.Pool.Version+1 ||
		len(empty.Discoveries) != 0 {
		t.Fatalf("empty commit=%#v", empty)
	}

	afterEmpty := candidatePoolSnapshotAfterV2(snapshot, empty)
	failureInput := CandidateBatchFinalizeInputV2{
		BatchID: "batch-failed", ExpectedPoolVersion: afterEmpty.Pool.Version,
		ProcessSourceKind: CandidateBatchExpansionV2,
		ProcessSourceID:   "expansion-batch-failed",
		Outcome:           CandidateBatchFinalizeFailureV2,
		FailureReasonCode: "PROVIDER_SCHEMA_MISMATCH",
		CompletedAt:       now.Add(2 * time.Minute),
	}
	failed, err := FinalizeCandidateBatchV2(afterEmpty, failureInput)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Batch.Status != CandidateBatchFailedV2 ||
		failed.Batch.FailureReasonCode != "PROVIDER_SCHEMA_MISMATCH" ||
		failed.Pool.CurrentVisibleBatchID != originalPointer ||
		failed.Pool.CanonicalIdentityCount != 1 ||
		len(failed.Candidates) != 0 || len(failed.Discoveries) != 0 {
		t.Fatalf("failed commit=%#v", failed)
	}
}

func TestCandidateExpansionRequiresDeterministicAssessmentPolicy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	pool, err := NewCandidatePoolV2("target-1")
	if err != nil {
		t.Fatal(err)
	}
	admission := candidateAdmissionFixtureV2(
		1, CandidateAssessmentResearchRankingV1, now,
	)
	input := candidateBatchInputFixtureV2(
		"batch-model-expand", pool.Version, CandidateBatchExpansionV2,
		CandidateAssessmentResearchRankingV1, now, admission,
	)
	if _, err := FinalizeCandidateBatchV2(
		CandidatePoolSnapshotV2{Pool: pool}, input,
	); !errors.Is(err, ErrCandidatePoolV2Invalid) {
		t.Fatalf("model-based Expand assessment err=%v", err)
	}

	admission.Assessment.SourcePolicy = CandidateAssessmentExpansionV1
	input.Admissions = []CandidateAdmissionInputV2{admission}
	commit, err := FinalizeCandidateBatchV2(
		CandidatePoolSnapshotV2{Pool: pool}, input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Assessments) != 1 ||
		commit.Assessments[0].SourcePolicy != CandidateAssessmentExpansionV1 {
		t.Fatalf("deterministic assessment commit=%#v", commit)
	}
}

func TestCandidatePoolSnapshotRejectsTamperedCandidateOrCanonicalCount(
	t *testing.T,
) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 13, 30, 0, 0, time.UTC)
	snapshot, _, _ := candidatePoolSnapshotWithCandidatesV2(t, 1, now)
	empty := candidateBatchInputFixtureV2(
		"batch-after-tamper", snapshot.Pool.Version,
		CandidateBatchExpansionV2, CandidateAssessmentExpansionV1,
		now.Add(time.Minute),
	)

	tamperedCandidate := snapshot
	tamperedCandidate.Candidates = append(
		[]CandidateV2(nil), snapshot.Candidates...,
	)
	tamperedCandidate.Candidates[0].IdentityKey = "tampered-identity"
	if _, err := FinalizeCandidateBatchV2(tamperedCandidate, empty); !errors.Is(err, ErrCandidatePoolV2Invalid) {
		t.Fatalf("tampered Candidate err=%v", err)
	}

	tamperedCount := snapshot
	tamperedCount.Pool.CanonicalIdentityCount = 0
	if _, err := FinalizeCandidateBatchV2(tamperedCount, empty); !errors.Is(err, ErrCandidatePoolV2Invalid) {
		t.Fatalf("tampered canonical count err=%v", err)
	}
}

func TestFinalizeCandidateBatchV2EnforcesCapacityAndPoolVersionCAS(
	t *testing.T,
) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 11, 0, 0, 0, time.UTC)
	snapshot, _, _ := candidatePoolSnapshotWithCandidatesV2(t, 49, now)
	if err := snapshot.Pool.RequireExpansionCapacity(); err != nil {
		t.Fatalf("49 candidate expansion guard: %v", err)
	}

	admissions := []CandidateAdmissionInputV2{
		candidateAdmissionFixtureV2(
			50, CandidateAssessmentExpansionV1, now.Add(time.Minute),
		),
		candidateAdmissionFixtureV2(
			51, CandidateAssessmentExpansionV1, now.Add(time.Minute),
		),
		candidateAdmissionFixtureV2(
			52, CandidateAssessmentExpansionV1, now.Add(time.Minute),
		),
	}
	for index := range admissions {
		admissions[index].RankPosition = index + 1
	}
	input := candidateBatchInputFixtureV2(
		"batch-cap", snapshot.Pool.Version, CandidateBatchExpansionV2,
		CandidateAssessmentExpansionV1, now.Add(time.Minute), admissions...,
	)
	commit, err := FinalizeCandidateBatchV2(snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	if commit.Pool.CanonicalIdentityCount != 50 ||
		len(commit.Candidates) != 1 || len(commit.Discoveries) != 1 ||
		len(commit.RejectedIdentityKeys) != 2 ||
		commit.Candidates[0].ID != "candidate-50" {
		t.Fatalf("capacity commit=%#v", commit)
	}
	if err := commit.Pool.RequireExpansionCapacity(); !errors.Is(err, ErrCandidatePoolV2Limit) {
		t.Fatalf("50 candidate expansion guard err=%v", err)
	}

	afterCap := candidatePoolSnapshotAfterV2(snapshot, commit)
	stale := input
	stale.BatchID = "batch-stale"
	if _, err := FinalizeCandidateBatchV2(afterCap, stale); !errors.Is(err, ErrCandidatePoolV2Version) {
		t.Fatalf("stale pool version err=%v", err)
	}

	allNew := candidateAdmissionFixtureV2(
		53, CandidateAssessmentResearchRankingV1, now.Add(2*time.Minute),
	)
	capacityInput := candidateBatchInputFixtureV2(
		"batch-capacity-empty", afterCap.Pool.Version,
		CandidateBatchResearchRoundV2,
		CandidateAssessmentResearchRankingV1,
		now.Add(2*time.Minute), allNew,
	)
	capacity, err := FinalizeCandidateBatchV2(afterCap, capacityInput)
	if err != nil {
		t.Fatal(err)
	}
	if capacity.Batch.Status != CandidateBatchCompletedEmptyV2 ||
		capacity.Batch.ClosedReason !=
			CandidateBatchCapacityReachedNoAdmissionV2 ||
		capacity.Pool.CurrentVisibleBatchID !=
			commit.Pool.CurrentVisibleBatchID ||
		len(capacity.Candidates) != 0 || len(capacity.Discoveries) != 0 {
		t.Fatalf("capacity empty=%#v", capacity)
	}

	atCapacity := candidatePoolSnapshotAfterV2(afterCap, capacity)
	refresh := candidateAdmissionFixtureV2(
		1, CandidateAssessmentResearchRankingV1, now.Add(3*time.Minute),
	)
	refresh.CandidateID = atCapacity.Candidates[0].ID
	refresh.IdentityKey = atCapacity.Candidates[0].IdentityKey
	refresh.DiscoveryID = "discovery-at-cap-refresh"
	refresh.Assessment.ID = "assessment-at-cap-refresh"
	refresh.RankPosition = 1
	refreshInput := candidateBatchInputFixtureV2(
		"batch-at-cap-refresh", atCapacity.Pool.Version,
		CandidateBatchResearchRoundV2,
		CandidateAssessmentResearchRankingV1,
		now.Add(3*time.Minute), refresh,
	)
	refreshed, err := FinalizeCandidateBatchV2(atCapacity, refreshInput)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Batch.Status != CandidateBatchCompletedNonemptyV2 ||
		refreshed.Pool.CanonicalIdentityCount != 50 ||
		len(refreshed.Candidates) != 0 || len(refreshed.Discoveries) != 1 {
		t.Fatalf("existing identity refresh at capacity=%#v", refreshed)
	}
}

func TestFinalizeCandidateBatchV2AliasesCanonicalRootsWithoutMovingHistory(
	t *testing.T,
) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	snapshot, first, _ := candidatePoolSnapshotWithCandidatesV2(t, 2, now)
	aliasInput := CandidateIdentityAliasInputV2{
		ID:                     "alias-2-to-1",
		ProvisionalIdentityKey: first.Candidates[1].IdentityKey,
		CanonicalCandidateID:   first.Candidates[0].ID,
		CorrelationEvidenceRefs: []string{
			"exact-correlation-evidence-1",
		},
		CreatedAt: now.Add(time.Minute),
	}
	rediscovery := candidateAdmissionFixtureV2(
		2, CandidateAssessmentExpansionV1, now.Add(time.Minute),
	)
	rediscovery.CandidateID = first.Candidates[0].ID
	rediscovery.DiscoveryID = "discovery-alias-2"
	rediscovery.Assessment.ID = "assessment-alias-2"
	input := candidateBatchInputFixtureV2(
		"batch-alias", snapshot.Pool.Version, CandidateBatchExpansionV2,
		CandidateAssessmentExpansionV1, now.Add(time.Minute), rediscovery,
	)
	input.Aliases = []CandidateIdentityAliasInputV2{aliasInput}
	commit, err := FinalizeCandidateBatchV2(snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	if commit.Pool.CanonicalIdentityCount != 1 || len(commit.Aliases) != 1 ||
		len(commit.Candidates) != 0 || len(commit.Discoveries) != 1 ||
		commit.Discoveries[0].CandidateID != first.Candidates[0].ID ||
		len(snapshot.Candidates) != 2 ||
		snapshot.Candidates[1].ID != first.Candidates[1].ID {
		t.Fatalf("alias commit=%#v snapshot=%#v", commit, snapshot)
	}
	if err := commit.Aliases[0].Validate(); err != nil {
		t.Fatalf("alias validation: %v", err)
	}

	cycleSnapshot := candidatePoolSnapshotAfterV2(snapshot, commit)
	cycleInput := candidateBatchInputFixtureV2(
		"batch-cycle", cycleSnapshot.Pool.Version,
		CandidateBatchExpansionV2, CandidateAssessmentExpansionV1,
		now.Add(2*time.Minute),
	)
	cycleInput.Aliases = []CandidateIdentityAliasInputV2{{
		ID:                     "alias-1-to-2",
		ProvisionalIdentityKey: first.Candidates[0].IdentityKey,
		CanonicalCandidateID:   first.Candidates[1].ID,
		CorrelationEvidenceRefs: []string{
			"exact-correlation-evidence-2",
		},
		CreatedAt: now.Add(2 * time.Minute),
	}}
	if _, err := FinalizeCandidateBatchV2(cycleSnapshot, cycleInput); !errors.Is(err, ErrCandidateAliasV2Cycle) {
		t.Fatalf("alias cycle err=%v", err)
	}
}

func candidatePoolSnapshotWithCandidatesV2(
	t *testing.T,
	count int,
	now time.Time,
) (CandidatePoolSnapshotV2, CandidateBatchCommitV2, []CandidateDiscoveryV2) {
	t.Helper()
	pool, err := NewCandidatePoolV2("target-1")
	if err != nil {
		t.Fatal(err)
	}
	admissions := make([]CandidateAdmissionInputV2, 0, count)
	for index := 1; index <= count; index++ {
		admission := candidateAdmissionFixtureV2(
			index, CandidateAssessmentResearchRankingV1, now,
		)
		admission.RankPosition = index
		admissions = append(admissions, admission)
	}
	input := candidateBatchInputFixtureV2(
		"batch-initial", pool.Version, CandidateBatchResearchRoundV2,
		CandidateAssessmentResearchRankingV1, now, admissions...,
	)
	initial := CandidatePoolSnapshotV2{Pool: pool}
	commit, err := FinalizeCandidateBatchV2(initial, input)
	if err != nil {
		t.Fatal(err)
	}
	return candidatePoolSnapshotAfterV2(initial, commit), commit,
		append([]CandidateDiscoveryV2(nil), commit.Discoveries...)
}

func candidatePoolSnapshotAfterV2(
	previous CandidatePoolSnapshotV2,
	commit CandidateBatchCommitV2,
) CandidatePoolSnapshotV2 {
	return CandidatePoolSnapshotV2{
		Pool: commit.Pool,
		Candidates: append(
			append([]CandidateV2(nil), previous.Candidates...),
			commit.Candidates...,
		),
		Aliases: append(
			append([]CandidateIdentityAliasV2(nil), previous.Aliases...),
			commit.Aliases...,
		),
	}
}

func candidateBatchInputFixtureV2(
	batchID string,
	expectedVersion int64,
	source CandidateBatchProcessSourceKindV2,
	policy CandidateAssessmentSourcePolicyV2,
	completedAt time.Time,
	admissions ...CandidateAdmissionInputV2,
) CandidateBatchFinalizeInputV2 {
	for index := range admissions {
		admissions[index].Assessment.SourcePolicy = policy
	}
	input := CandidateBatchFinalizeInputV2{
		BatchID: batchID, ExpectedPoolVersion: expectedVersion,
		ProcessSourceKind: source,
		ProcessSourceID:   "process-" + batchID,
		Outcome:           CandidateBatchFinalizeSuccessV2,
		Admissions:        admissions,
		CompletedAt:       completedAt,
	}
	return input
}

func candidateAdmissionFixtureV2(
	ordinal int,
	policy CandidateAssessmentSourcePolicyV2,
	discoveredAt time.Time,
) CandidateAdmissionInputV2 {
	suffix := fmt.Sprintf("%d", ordinal)
	return CandidateAdmissionInputV2{
		CandidateID:       "candidate-" + suffix,
		IdentityKey:       "identity-" + suffix,
		InitialLocatorRef: "initial-locator-" + suffix,
		DiscoveryID:       "discovery-" + suffix,
		LocatorRef:        "locator-" + suffix,
		PreviewVariantRef: "preview-variant-" + suffix,
		ObservationEvidenceRefs: []string{
			"observation-evidence-" + suffix,
		},
		RankPosition:         ordinal,
		RankingPolicyVersion: "ranking-policy-1",
		DiscoveredAt:         discoveredAt,
		Assessment: CandidateAssessmentInputV2{
			ID:           "assessment-" + suffix,
			SourcePolicy: policy,
			IntentPoint: CandidateAssessmentClaimV2{
				Text:         "Matches the explicit target requirement.",
				EvidenceRefs: []string{"intent-evidence-" + suffix},
			},
			Features: []CandidateAssessmentLineV2{{
				Text:   "Observed feature " + suffix,
				Origin: CandidateClaimProviderExplicitV2,
				EvidenceRefs: []string{
					"feature-evidence-" + suffix,
				},
			}},
			Specifications: []CandidateAssessmentLineV2{{
				Text:   "Observed specification " + suffix,
				Origin: CandidateClaimProviderExplicitV2,
				EvidenceRefs: []string{
					"spec-evidence-" + suffix,
				},
			}},
			ScoreComponents: []CandidateScoreComponentV2{{
				Name: "explicit-fit", Score: 90,
			}},
			EvidenceRefs:     []string{"assessment-evidence-" + suffix},
			SourceObservedAt: discoveredAt.Add(-time.Second),
		},
	}
}
