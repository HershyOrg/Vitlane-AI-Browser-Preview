package domain

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
)

const CandidatePoolMaximumCanonicalIdentitiesV2 int64 = 50

var (
	ErrCandidatePoolV2Invalid = errors.New("CANDIDATE_POOL_V2_INVALID")
	ErrCandidatePoolV2Version = errors.New("CANDIDATE_POOL_V2_VERSION_CONFLICT")
	ErrCandidatePoolV2Limit   = errors.New("TARGET_CANDIDATE_LIMIT_REACHED")
	ErrCandidateAliasV2Cycle  = errors.New("CANDIDATE_IDENTITY_ALIAS_CYCLE")
)

type CandidateBatchProcessSourceKindV2 string

const (
	CandidateBatchResearchRoundV2 CandidateBatchProcessSourceKindV2 = "RESEARCH_ROUND"
	CandidateBatchExpansionV2     CandidateBatchProcessSourceKindV2 = "CANDIDATE_EXPANSION"
)

type CandidateDiscoverySourceKindV2 string

const (
	CandidateDiscoveryResearchRoundV2 CandidateDiscoverySourceKindV2 = "RESEARCH_ROUND"
	CandidateDiscoveryExpansionV2     CandidateDiscoverySourceKindV2 = "CANDIDATE_EXPANSION"
)

type CandidateBatchStatusV2 string

const (
	CandidateBatchCompletedNonemptyV2 CandidateBatchStatusV2 = "COMPLETED_NONEMPTY"
	CandidateBatchCompletedEmptyV2    CandidateBatchStatusV2 = "COMPLETED_EMPTY"
	CandidateBatchFailedV2            CandidateBatchStatusV2 = "FAILED"
)

type CandidateBatchClosedReasonV2 string

const (
	CandidateBatchNoResultsV2                  CandidateBatchClosedReasonV2 = "NO_RESULTS"
	CandidateBatchNoNewResultsV2               CandidateBatchClosedReasonV2 = "NO_NEW_RESULTS"
	CandidateBatchCapacityReachedNoAdmissionV2 CandidateBatchClosedReasonV2 = "CAPACITY_REACHED_NO_ADMISSION"
)

type CandidateBatchFinalizeOutcomeV2 string

const (
	CandidateBatchFinalizeSuccessV2 CandidateBatchFinalizeOutcomeV2 = "SUCCESS"
	CandidateBatchFinalizeFailureV2 CandidateBatchFinalizeOutcomeV2 = "FAILURE"
)

type CandidateAssessmentSourcePolicyV2 string

const (
	CandidateAssessmentResearchRankingV1 CandidateAssessmentSourcePolicyV2 = "research-ranking.v1"
	CandidateAssessmentExpansionV1       CandidateAssessmentSourcePolicyV2 = "research-expansion-assessment.v1"
)

type CandidateAssessmentClaimOriginV2 string

const (
	CandidateClaimProviderExplicitV2 CandidateAssessmentClaimOriginV2 = "PROVIDER_EXPLICIT"
	CandidateClaimProviderInferredV2 CandidateAssessmentClaimOriginV2 = "PROVIDER_INFERRED"
)

// CandidateV2 owns only stable identity and the first safe locator reference.
// Provider listing fields belong to response-scoped fresh reads.
type CandidateV2 struct {
	ID                string    `json:"candidateId"`
	TargetID          string    `json:"targetId"`
	IdentityKey       string    `json:"candidateIdentityKey"`
	InitialLocatorRef string    `json:"initialLocatorRef"`
	CreatedAt         time.Time `json:"createdAt"`
	ContentHash       string    `json:"contentHash"`
}

// CandidateIdentityAliasV2 is an append-only exact-correlation fact. The
// provisional key may belong to an already-materialized Candidate or may be
// bound before a duplicate Candidate is materialized.
type CandidateIdentityAliasV2 struct {
	ID                      string    `json:"candidateIdentityAliasId"`
	TargetID                string    `json:"targetId"`
	ProvisionalIdentityKey  string    `json:"provisionalIdentityKey"`
	CanonicalCandidateID    string    `json:"canonicalCandidateId"`
	CorrelationEvidenceRefs []string  `json:"correlationEvidenceRefs"`
	CreatedAt               time.Time `json:"createdAt"`
	ContentHash             string    `json:"contentHash"`
}

type CandidateAssessmentClaimV2 struct {
	Text         string   `json:"text"`
	EvidenceRefs []string `json:"evidenceRefs"`
}

type CandidateAssessmentLineV2 struct {
	Text         string                           `json:"text"`
	Origin       CandidateAssessmentClaimOriginV2 `json:"origin"`
	EvidenceRefs []string                         `json:"evidenceRefs"`
}

type CandidateScoreComponentV2 struct {
	Name  string `json:"name"`
	Score int    `json:"score"`
}

type CandidateAssessmentRevisionV2 struct {
	ID                string                            `json:"assessmentRevisionId"`
	CandidateID       string                            `json:"candidateId"`
	SourceDiscoveryID string                            `json:"sourceDiscoveryId"`
	SourcePolicy      CandidateAssessmentSourcePolicyV2 `json:"sourcePolicy"`
	IntentPoint       CandidateAssessmentClaimV2        `json:"intentPoint"`
	Features          []CandidateAssessmentLineV2       `json:"features"`
	Specifications    []CandidateAssessmentLineV2       `json:"specifications"`
	Tradeoffs         []CandidateAssessmentClaimV2      `json:"tradeoffs"`
	ScoreComponents   []CandidateScoreComponentV2       `json:"scoreComponents"`
	EvidenceRefs      []string                          `json:"evidenceRefs"`
	SourceObservedAt  time.Time                         `json:"sourceObservedAt"`
	ContentHash       string                            `json:"contentHash"`
}

type CandidateDiscoveryV2 struct {
	ID                      string                         `json:"discoveryId"`
	CandidateID             string                         `json:"candidateId"`
	SourceKind              CandidateDiscoverySourceKindV2 `json:"sourceKind"`
	SourceID                string                         `json:"sourceId"`
	CandidateBatchID        string                         `json:"candidateBatchId"`
	LocatorRef              string                         `json:"locatorRef"`
	PreviewVariantRef       string                         `json:"previewVariantRef,omitempty"`
	ObservationEvidenceRefs []string                       `json:"observationEvidenceRefs"`
	AssessmentRevisionID    string                         `json:"assessmentRevisionId"`
	RankPosition            int                            `json:"rankPosition"`
	RankingPolicyVersion    string                         `json:"rankingPolicyVersion"`
	DiscoveredAt            time.Time                      `json:"discoveredAt"`
	ContentHash             string                         `json:"contentHash"`
}

type CandidatePoolV2 struct {
	TargetID               string `json:"targetId"`
	Version                int64  `json:"version"`
	CurrentVisibleBatchID  string `json:"currentVisibleBatchId,omitempty"`
	CanonicalIdentityCount int64  `json:"canonicalIdentityCount"`
}

type CandidateBatchV2 struct {
	ID                  string                            `json:"candidateBatchId"`
	TargetID            string                            `json:"targetId"`
	ProcessSourceKind   CandidateBatchProcessSourceKindV2 `json:"processSourceKind"`
	ProcessSourceID     string                            `json:"processSourceId"`
	Status              CandidateBatchStatusV2            `json:"status"`
	ClosedReason        CandidateBatchClosedReasonV2      `json:"closedReason,omitempty"`
	FailureReasonCode   string                            `json:"failureReasonCode,omitempty"`
	AdmittedCount       int                               `json:"admittedCount"`
	OrderedDiscoveryIDs []string                          `json:"orderedDiscoveryIds"`
	CompletedAt         time.Time                         `json:"completedAt"`
	ContentHash         string                            `json:"contentHash"`
}

// CandidatePoolSnapshotV2 is the repository snapshot required to validate
// canonical identity count before a pool CAS. It deliberately excludes
// mutable listing data and Candidate discovery history.
type CandidatePoolSnapshotV2 struct {
	Pool       CandidatePoolV2
	Candidates []CandidateV2
	Aliases    []CandidateIdentityAliasV2
}

type CandidateIdentityAliasInputV2 struct {
	ID                      string
	ProvisionalIdentityKey  string
	CanonicalCandidateID    string
	CorrelationEvidenceRefs []string
	CreatedAt               time.Time
}

type CandidateAssessmentInputV2 struct {
	ID               string
	SourcePolicy     CandidateAssessmentSourcePolicyV2
	IntentPoint      CandidateAssessmentClaimV2
	Features         []CandidateAssessmentLineV2
	Specifications   []CandidateAssessmentLineV2
	Tradeoffs        []CandidateAssessmentClaimV2
	ScoreComponents  []CandidateScoreComponentV2
	EvidenceRefs     []string
	SourceObservedAt time.Time
}

// CandidateAdmissionInputV2 is already product-level, ranked and filtered.
// CandidateID must name either the existing canonical root for IdentityKey or
// the new stable Candidate to create.
type CandidateAdmissionInputV2 struct {
	CandidateID             string
	IdentityKey             string
	InitialLocatorRef       string
	DiscoveryID             string
	LocatorRef              string
	PreviewVariantRef       string
	ObservationEvidenceRefs []string
	RankPosition            int
	RankingPolicyVersion    string
	DiscoveredAt            time.Time
	Assessment              CandidateAssessmentInputV2
}

type CandidateBatchFinalizeInputV2 struct {
	BatchID             string
	ExpectedPoolVersion int64
	ProcessSourceKind   CandidateBatchProcessSourceKindV2
	ProcessSourceID     string
	Outcome             CandidateBatchFinalizeOutcomeV2
	FailureReasonCode   string
	Aliases             []CandidateIdentityAliasInputV2
	Admissions          []CandidateAdmissionInputV2
	CompletedAt         time.Time
}

// CandidateBatchCommitV2 is one local transaction outcome: append-only facts
// plus the updated pool root. No result may be persisted partially.
type CandidateBatchCommitV2 struct {
	Pool                 CandidatePoolV2
	Batch                CandidateBatchV2
	Candidates           []CandidateV2
	Aliases              []CandidateIdentityAliasV2
	Discoveries          []CandidateDiscoveryV2
	Assessments          []CandidateAssessmentRevisionV2
	RejectedIdentityKeys []string
}

func NewCandidatePoolV2(targetID string) (CandidatePoolV2, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return CandidatePoolV2{}, ErrCandidatePoolV2Invalid
	}
	return CandidatePoolV2{TargetID: targetID, Version: 1}, nil
}

// RequireExpansionCapacity is the pre-provider guard for deterministic
// Expand. Research Again may still refresh an existing identity at the cap and
// therefore uses batch admission rather than this guard.
func (p CandidatePoolV2) RequireExpansionCapacity() error {
	if p.TargetID == "" || p.TargetID != strings.TrimSpace(p.TargetID) ||
		p.Version < 1 || p.CanonicalIdentityCount < 0 ||
		p.CanonicalIdentityCount >
			CandidatePoolMaximumCanonicalIdentitiesV2 ||
		p.CurrentVisibleBatchID != strings.TrimSpace(
			p.CurrentVisibleBatchID,
		) {
		return ErrCandidatePoolV2Invalid
	}
	if p.CanonicalIdentityCount ==
		CandidatePoolMaximumCanonicalIdentitiesV2 {
		return ErrCandidatePoolV2Limit
	}
	return nil
}

func FinalizeCandidateBatchV2(
	snapshot CandidatePoolSnapshotV2,
	input CandidateBatchFinalizeInputV2,
) (CandidateBatchCommitV2, error) {
	resolver, err := validateCandidatePoolSnapshotV2(snapshot)
	if err != nil {
		return CandidateBatchCommitV2{}, err
	}
	if input.ExpectedPoolVersion != snapshot.Pool.Version {
		return CandidateBatchCommitV2{}, ErrCandidatePoolV2Version
	}
	input = canonicalCandidateBatchFinalizeInputV2(input)
	if !validCandidateBatchProcessV2(
		input.ProcessSourceKind,
		input.ProcessSourceID,
		input.Outcome,
	) || input.BatchID == "" || input.CompletedAt.IsZero() {
		return CandidateBatchCommitV2{}, ErrCandidatePoolV2Invalid
	}

	if input.Outcome == CandidateBatchFinalizeFailureV2 {
		if input.FailureReasonCode == "" || len(input.Aliases) != 0 ||
			len(input.Admissions) != 0 {
			return CandidateBatchCommitV2{}, ErrCandidatePoolV2Invalid
		}
		batch, err := newCandidateBatchV2(
			snapshot.Pool.TargetID, input, CandidateBatchFailedV2,
			"", nil,
		)
		if err != nil {
			return CandidateBatchCommitV2{}, err
		}
		pool := snapshot.Pool
		pool.Version++
		return CandidateBatchCommitV2{Pool: pool, Batch: batch}, nil
	}
	if input.FailureReasonCode != "" {
		return CandidateBatchCommitV2{}, ErrCandidatePoolV2Invalid
	}

	newAliases, resolver, err := appendCandidateAliasesV2(
		snapshot, resolver, input.Aliases, input.CompletedAt,
	)
	if err != nil {
		return CandidateBatchCommitV2{}, err
	}
	canonicalCount, err := resolver.canonicalCount()
	if err != nil {
		return CandidateBatchCommitV2{}, err
	}

	commit := CandidateBatchCommitV2{Aliases: newAliases}
	seenAdmissionKeys := make(map[string]struct{}, len(input.Admissions))
	seenCandidateIDs := make(map[string]struct{}, len(input.Admissions))
	seenDiscoveryIDs := make(map[string]struct{}, len(input.Admissions))
	seenAssessmentIDs := make(map[string]struct{}, len(input.Admissions))
	previousRank := 0
	for _, admission := range input.Admissions {
		admission = canonicalCandidateAdmissionInputV2(admission)
		if admission.IdentityKey == "" || admission.CandidateID == "" ||
			admission.DiscoveryID == "" || admission.Assessment.ID == "" ||
			admission.RankPosition <= previousRank ||
			admission.DiscoveredAt.IsZero() ||
			admission.DiscoveredAt.After(input.CompletedAt) {
			return CandidateBatchCommitV2{}, ErrCandidatePoolV2Invalid
		}
		previousRank = admission.RankPosition
		if _, duplicate := seenAdmissionKeys[admission.IdentityKey]; duplicate {
			return CandidateBatchCommitV2{}, ErrCandidatePoolV2Invalid
		}
		seenAdmissionKeys[admission.IdentityKey] = struct{}{}
		if _, duplicate := seenDiscoveryIDs[admission.DiscoveryID]; duplicate {
			return CandidateBatchCommitV2{}, ErrCandidatePoolV2Invalid
		}
		seenDiscoveryIDs[admission.DiscoveryID] = struct{}{}
		if _, duplicate := seenAssessmentIDs[admission.Assessment.ID]; duplicate {
			return CandidateBatchCommitV2{}, ErrCandidatePoolV2Invalid
		}
		seenAssessmentIDs[admission.Assessment.ID] = struct{}{}

		candidateID, exists, err := resolver.resolveIdentityKey(
			admission.IdentityKey,
		)
		if err != nil {
			return CandidateBatchCommitV2{}, err
		}
		if exists {
			if admission.CandidateID != candidateID {
				return CandidateBatchCommitV2{}, ErrCandidatePoolV2Invalid
			}
		} else {
			if canonicalCount >= CandidatePoolMaximumCanonicalIdentitiesV2 {
				commit.RejectedIdentityKeys = append(
					commit.RejectedIdentityKeys, admission.IdentityKey,
				)
				continue
			}
			if _, duplicate := resolver.candidatesByID[admission.CandidateID]; duplicate {
				return CandidateBatchCommitV2{}, ErrCandidatePoolV2Invalid
			}
			if _, duplicate := seenCandidateIDs[admission.CandidateID]; duplicate {
				return CandidateBatchCommitV2{}, ErrCandidatePoolV2Invalid
			}
			candidate, err := newCandidateV2(
				admission.CandidateID, snapshot.Pool.TargetID,
				admission.IdentityKey, admission.InitialLocatorRef,
				admission.DiscoveredAt,
			)
			if err != nil {
				return CandidateBatchCommitV2{}, err
			}
			commit.Candidates = append(commit.Candidates, candidate)
			seenCandidateIDs[candidate.ID] = struct{}{}
			if err := resolver.addCandidate(candidate); err != nil {
				return CandidateBatchCommitV2{}, err
			}
			candidateID = candidate.ID
			canonicalCount++
		}

		discovery, assessment, err := newCandidateDiscoveryAndAssessmentV2(
			snapshot.Pool.TargetID, input, candidateID, admission,
		)
		if err != nil {
			return CandidateBatchCommitV2{}, err
		}
		commit.Discoveries = append(commit.Discoveries, discovery)
		commit.Assessments = append(commit.Assessments, assessment)
	}

	pool := snapshot.Pool
	pool.Version++
	pool.CanonicalIdentityCount = canonicalCount
	status := CandidateBatchCompletedNonemptyV2
	closedReason := CandidateBatchClosedReasonV2("")
	if len(commit.Discoveries) == 0 {
		status = CandidateBatchCompletedEmptyV2
		switch {
		case len(commit.RejectedIdentityKeys) > 0:
			closedReason = CandidateBatchCapacityReachedNoAdmissionV2
		case input.ProcessSourceKind == CandidateBatchExpansionV2:
			closedReason = CandidateBatchNoNewResultsV2
		default:
			closedReason = CandidateBatchNoResultsV2
		}
	}
	orderedDiscoveryIDs := make([]string, 0, len(commit.Discoveries))
	for _, discovery := range commit.Discoveries {
		orderedDiscoveryIDs = append(orderedDiscoveryIDs, discovery.ID)
	}
	batch, err := newCandidateBatchV2(
		snapshot.Pool.TargetID, input, status, closedReason,
		orderedDiscoveryIDs,
	)
	if err != nil {
		return CandidateBatchCommitV2{}, err
	}
	if status == CandidateBatchCompletedNonemptyV2 {
		pool.CurrentVisibleBatchID = batch.ID
	}
	commit.Pool = pool
	commit.Batch = batch
	return commit, nil
}

func validateCandidatePoolSnapshotV2(
	snapshot CandidatePoolSnapshotV2,
) (*candidateCanonicalResolverV2, error) {
	if snapshot.Pool.TargetID == "" ||
		snapshot.Pool.TargetID != strings.TrimSpace(snapshot.Pool.TargetID) ||
		snapshot.Pool.Version < 1 || snapshot.Pool.CanonicalIdentityCount < 0 ||
		snapshot.Pool.CanonicalIdentityCount >
			CandidatePoolMaximumCanonicalIdentitiesV2 ||
		snapshot.Pool.CurrentVisibleBatchID != strings.TrimSpace(
			snapshot.Pool.CurrentVisibleBatchID,
		) {
		return nil, ErrCandidatePoolV2Invalid
	}
	resolver := newCandidateCanonicalResolverV2(snapshot.Pool.TargetID)
	for _, candidate := range snapshot.Candidates {
		if err := candidate.Validate(); err != nil ||
			candidate.TargetID != snapshot.Pool.TargetID {
			return nil, ErrCandidatePoolV2Invalid
		}
		if err := resolver.addCandidate(candidate); err != nil {
			return nil, err
		}
	}
	aliasIDs := make(map[string]struct{}, len(snapshot.Aliases))
	for _, alias := range snapshot.Aliases {
		if err := alias.Validate(); err != nil ||
			alias.TargetID != snapshot.Pool.TargetID {
			return nil, ErrCandidatePoolV2Invalid
		}
		if _, duplicate := aliasIDs[alias.ID]; duplicate {
			return nil, ErrCandidatePoolV2Invalid
		}
		aliasIDs[alias.ID] = struct{}{}
		if err := resolver.addAlias(alias); err != nil {
			return nil, err
		}
	}
	count, err := resolver.canonicalCount()
	if err != nil {
		return nil, err
	}
	if count != snapshot.Pool.CanonicalIdentityCount {
		return nil, ErrCandidatePoolV2Invalid
	}
	return resolver, nil
}

func appendCandidateAliasesV2(
	snapshot CandidatePoolSnapshotV2,
	resolver *candidateCanonicalResolverV2,
	inputs []CandidateIdentityAliasInputV2,
	completedAt time.Time,
) ([]CandidateIdentityAliasV2, *candidateCanonicalResolverV2, error) {
	result := make([]CandidateIdentityAliasV2, 0, len(inputs))
	seenIDs := make(map[string]struct{}, len(snapshot.Aliases)+len(inputs))
	for _, alias := range snapshot.Aliases {
		seenIDs[alias.ID] = struct{}{}
	}
	for _, input := range inputs {
		input.ID = strings.TrimSpace(input.ID)
		input.ProvisionalIdentityKey = strings.TrimSpace(
			input.ProvisionalIdentityKey,
		)
		input.CanonicalCandidateID = strings.TrimSpace(
			input.CanonicalCandidateID,
		)
		input.CorrelationEvidenceRefs = canonicalStringSetV2(
			input.CorrelationEvidenceRefs,
		)
		if input.ID == "" || input.ProvisionalIdentityKey == "" ||
			input.CanonicalCandidateID == "" || input.CreatedAt.IsZero() ||
			input.CreatedAt.After(completedAt) ||
			len(input.CorrelationEvidenceRefs) == 0 {
			return nil, nil, ErrCandidatePoolV2Invalid
		}
		if _, duplicate := seenIDs[input.ID]; duplicate {
			return nil, nil, ErrCandidatePoolV2Invalid
		}
		seenIDs[input.ID] = struct{}{}
		alias := CandidateIdentityAliasV2{
			ID: input.ID, TargetID: snapshot.Pool.TargetID,
			ProvisionalIdentityKey:  input.ProvisionalIdentityKey,
			CanonicalCandidateID:    input.CanonicalCandidateID,
			CorrelationEvidenceRefs: input.CorrelationEvidenceRefs,
			CreatedAt:               input.CreatedAt,
		}
		hash, err := candidatePoolContentHashV2(
			"vitlane.candidate-identity-alias.v2", alias,
		)
		if err != nil {
			return nil, nil, err
		}
		alias.ContentHash = hash
		if err := resolver.addAlias(alias); err != nil {
			return nil, nil, err
		}
		result = append(result, alias)
	}
	if _, err := resolver.canonicalCount(); err != nil {
		return nil, nil, err
	}
	return result, resolver, nil
}

func newCandidateV2(
	id, targetID, identityKey, initialLocatorRef string,
	createdAt time.Time,
) (CandidateV2, error) {
	value := CandidateV2{
		ID: strings.TrimSpace(id), TargetID: strings.TrimSpace(targetID),
		IdentityKey:       strings.TrimSpace(identityKey),
		InitialLocatorRef: strings.TrimSpace(initialLocatorRef),
		CreatedAt:         createdAt,
	}
	if value.ID == "" || value.TargetID == "" || value.IdentityKey == "" ||
		value.InitialLocatorRef == "" || value.CreatedAt.IsZero() {
		return CandidateV2{}, ErrCandidatePoolV2Invalid
	}
	hash, err := candidatePoolContentHashV2("vitlane.candidate-hash.v3", value)
	if err != nil {
		return CandidateV2{}, err
	}
	value.ContentHash = hash
	return value, nil
}

func (c CandidateV2) Validate() error {
	if c.ID == "" || c.ID != strings.TrimSpace(c.ID) ||
		c.TargetID == "" || c.TargetID != strings.TrimSpace(c.TargetID) ||
		c.IdentityKey == "" || c.IdentityKey != strings.TrimSpace(c.IdentityKey) ||
		c.InitialLocatorRef == "" ||
		c.InitialLocatorRef != strings.TrimSpace(c.InitialLocatorRef) ||
		c.CreatedAt.IsZero() || c.ContentHash == "" ||
		c.ContentHash != strings.TrimSpace(c.ContentHash) {
		return ErrCandidatePoolV2Invalid
	}
	expected, err := candidatePoolContentHashV2("vitlane.candidate-hash.v3", c)
	if err != nil {
		return err
	}
	if expected != c.ContentHash {
		return ErrCandidatePoolV2Invalid
	}
	return nil
}

func (a CandidateIdentityAliasV2) Validate() error {
	refs := canonicalStringSetV2(a.CorrelationEvidenceRefs)
	if a.ID == "" || a.ID != strings.TrimSpace(a.ID) ||
		a.TargetID == "" || a.TargetID != strings.TrimSpace(a.TargetID) ||
		a.ProvisionalIdentityKey == "" ||
		a.ProvisionalIdentityKey != strings.TrimSpace(a.ProvisionalIdentityKey) ||
		a.CanonicalCandidateID == "" ||
		a.CanonicalCandidateID != strings.TrimSpace(a.CanonicalCandidateID) ||
		len(refs) == 0 || !slices.Equal(refs, a.CorrelationEvidenceRefs) ||
		a.CreatedAt.IsZero() || a.ContentHash == "" ||
		a.ContentHash != strings.TrimSpace(a.ContentHash) {
		return ErrCandidatePoolV2Invalid
	}
	expected, err := candidatePoolContentHashV2(
		"vitlane.candidate-identity-alias.v2", a,
	)
	if err != nil {
		return err
	}
	if expected != a.ContentHash {
		return ErrCandidatePoolV2Invalid
	}
	return nil
}

func newCandidateDiscoveryAndAssessmentV2(
	targetID string,
	batch CandidateBatchFinalizeInputV2,
	candidateID string,
	input CandidateAdmissionInputV2,
) (CandidateDiscoveryV2, CandidateAssessmentRevisionV2, error) {
	sourceKind := CandidateDiscoveryResearchRoundV2
	sourceID := batch.ProcessSourceID
	requiredPolicy := CandidateAssessmentResearchRankingV1
	if batch.ProcessSourceKind == CandidateBatchExpansionV2 {
		sourceKind = CandidateDiscoveryExpansionV2
		sourceID = batch.ProcessSourceID
		requiredPolicy = CandidateAssessmentExpansionV1
	}
	if input.Assessment.SourcePolicy != requiredPolicy {
		return CandidateDiscoveryV2{}, CandidateAssessmentRevisionV2{},
			ErrCandidatePoolV2Invalid
	}
	assessment, err := newCandidateAssessmentRevisionV2(
		candidateID, input.DiscoveryID, input.Assessment,
		input.DiscoveredAt,
	)
	if err != nil {
		return CandidateDiscoveryV2{}, CandidateAssessmentRevisionV2{}, err
	}
	discovery := CandidateDiscoveryV2{
		ID: input.DiscoveryID, CandidateID: candidateID,
		SourceKind: sourceKind, SourceID: sourceID,
		CandidateBatchID:  batch.BatchID,
		LocatorRef:        input.LocatorRef,
		PreviewVariantRef: input.PreviewVariantRef,
		ObservationEvidenceRefs: canonicalStringSetV2(
			input.ObservationEvidenceRefs,
		),
		AssessmentRevisionID: assessment.ID,
		RankPosition:         input.RankPosition,
		RankingPolicyVersion: input.RankingPolicyVersion,
		DiscoveredAt:         input.DiscoveredAt,
	}
	if discovery.ID == "" || discovery.CandidateID == "" ||
		discovery.SourceID == "" || discovery.CandidateBatchID == "" ||
		discovery.LocatorRef == "" ||
		len(discovery.ObservationEvidenceRefs) == 0 ||
		discovery.AssessmentRevisionID == "" || discovery.RankPosition < 1 ||
		discovery.RankingPolicyVersion == "" || discovery.DiscoveredAt.IsZero() ||
		strings.TrimSpace(targetID) == "" {
		return CandidateDiscoveryV2{}, CandidateAssessmentRevisionV2{},
			ErrCandidatePoolV2Invalid
	}
	hash, err := candidatePoolContentHashV2(
		"vitlane.candidate-discovery.v2", discovery,
	)
	if err != nil {
		return CandidateDiscoveryV2{}, CandidateAssessmentRevisionV2{}, err
	}
	discovery.ContentHash = hash
	return discovery, assessment, nil
}

func newCandidateAssessmentRevisionV2(
	candidateID, discoveryID string,
	input CandidateAssessmentInputV2,
	discoveredAt time.Time,
) (CandidateAssessmentRevisionV2, error) {
	value := CandidateAssessmentRevisionV2{
		ID:                strings.TrimSpace(input.ID),
		CandidateID:       strings.TrimSpace(candidateID),
		SourceDiscoveryID: strings.TrimSpace(discoveryID),
		SourcePolicy:      input.SourcePolicy,
		IntentPoint:       canonicalCandidateAssessmentClaimV2(input.IntentPoint),
		Features:          canonicalCandidateAssessmentLinesV2(input.Features),
		Specifications: canonicalCandidateAssessmentLinesV2(
			input.Specifications,
		),
		Tradeoffs:        canonicalCandidateAssessmentClaimsV2(input.Tradeoffs),
		ScoreComponents:  canonicalCandidateScoreComponentsV2(input.ScoreComponents),
		EvidenceRefs:     canonicalStringSetV2(input.EvidenceRefs),
		SourceObservedAt: input.SourceObservedAt,
	}
	if value.ID == "" || value.CandidateID == "" ||
		value.SourceDiscoveryID == "" ||
		!validCandidateAssessmentPolicyV2(value.SourcePolicy) ||
		!validCandidateAssessmentClaimV2(value.IntentPoint) ||
		(len(value.Features) == 0 && len(value.Specifications) == 0) ||
		!validCandidateAssessmentLinesV2(value.Features) ||
		!validCandidateAssessmentLinesV2(value.Specifications) ||
		!validCandidateAssessmentClaimsV2(value.Tradeoffs) ||
		len(value.ScoreComponents) == 0 ||
		!validCandidateScoreComponentsV2(value.ScoreComponents) ||
		len(value.EvidenceRefs) == 0 || value.SourceObservedAt.IsZero() ||
		value.SourceObservedAt.After(discoveredAt) {
		return CandidateAssessmentRevisionV2{}, ErrCandidatePoolV2Invalid
	}
	hash, err := candidatePoolContentHashV2(
		"vitlane.candidate-assessment-revision.v2", value,
	)
	if err != nil {
		return CandidateAssessmentRevisionV2{}, err
	}
	value.ContentHash = hash
	return value, nil
}

func (d CandidateDiscoveryV2) Validate() error {
	refs := canonicalStringSetV2(d.ObservationEvidenceRefs)
	if d.ID == "" || d.ID != strings.TrimSpace(d.ID) ||
		d.CandidateID == "" || d.CandidateID != strings.TrimSpace(d.CandidateID) ||
		!validCandidateDiscoverySourceV2(d.SourceKind) ||
		d.SourceID == "" || d.SourceID != strings.TrimSpace(d.SourceID) ||
		d.CandidateBatchID == "" ||
		d.CandidateBatchID != strings.TrimSpace(d.CandidateBatchID) ||
		d.LocatorRef == "" || d.LocatorRef != strings.TrimSpace(d.LocatorRef) ||
		d.PreviewVariantRef != strings.TrimSpace(d.PreviewVariantRef) ||
		len(refs) == 0 || !slices.Equal(refs, d.ObservationEvidenceRefs) ||
		d.AssessmentRevisionID == "" ||
		d.AssessmentRevisionID != strings.TrimSpace(d.AssessmentRevisionID) ||
		d.RankPosition < 1 || d.RankingPolicyVersion == "" ||
		d.RankingPolicyVersion != strings.TrimSpace(d.RankingPolicyVersion) ||
		d.DiscoveredAt.IsZero() || d.ContentHash == "" {
		return ErrCandidatePoolV2Invalid
	}
	expected, err := candidatePoolContentHashV2(
		"vitlane.candidate-discovery.v2", d,
	)
	if err != nil {
		return err
	}
	if expected != d.ContentHash {
		return ErrCandidatePoolV2Invalid
	}
	return nil
}

func (a CandidateAssessmentRevisionV2) Validate() error {
	canonical := CandidateAssessmentRevisionV2{
		ID: strings.TrimSpace(a.ID), CandidateID: strings.TrimSpace(a.CandidateID),
		SourceDiscoveryID: strings.TrimSpace(a.SourceDiscoveryID),
		SourcePolicy:      a.SourcePolicy,
		IntentPoint:       canonicalCandidateAssessmentClaimV2(a.IntentPoint),
		Features:          canonicalCandidateAssessmentLinesV2(a.Features),
		Specifications: canonicalCandidateAssessmentLinesV2(
			a.Specifications,
		),
		Tradeoffs:        canonicalCandidateAssessmentClaimsV2(a.Tradeoffs),
		ScoreComponents:  canonicalCandidateScoreComponentsV2(a.ScoreComponents),
		EvidenceRefs:     canonicalStringSetV2(a.EvidenceRefs),
		SourceObservedAt: a.SourceObservedAt,
		ContentHash:      a.ContentHash,
	}
	if canonical.ID == "" || canonical.CandidateID == "" ||
		canonical.SourceDiscoveryID == "" || !reflect.DeepEqual(canonical, a) {
		return ErrCandidatePoolV2Invalid
	}
	copyValue := a
	copyValue.ContentHash = ""
	if !validCandidateAssessmentPolicyV2(a.SourcePolicy) ||
		!validCandidateAssessmentClaimV2(a.IntentPoint) ||
		(len(a.Features) == 0 && len(a.Specifications) == 0) ||
		!validCandidateAssessmentLinesV2(a.Features) ||
		!validCandidateAssessmentLinesV2(a.Specifications) ||
		!validCandidateAssessmentClaimsV2(a.Tradeoffs) ||
		len(a.ScoreComponents) == 0 ||
		!validCandidateScoreComponentsV2(a.ScoreComponents) ||
		len(a.EvidenceRefs) == 0 || a.SourceObservedAt.IsZero() ||
		a.ContentHash == "" {
		return ErrCandidatePoolV2Invalid
	}
	expected, err := candidatePoolContentHashV2(
		"vitlane.candidate-assessment-revision.v2", copyValue,
	)
	if err != nil {
		return err
	}
	if expected != a.ContentHash {
		return ErrCandidatePoolV2Invalid
	}
	return nil
}

func newCandidateBatchV2(
	targetID string,
	input CandidateBatchFinalizeInputV2,
	status CandidateBatchStatusV2,
	closedReason CandidateBatchClosedReasonV2,
	orderedDiscoveryIDs []string,
) (CandidateBatchV2, error) {
	value := CandidateBatchV2{
		ID: input.BatchID, TargetID: targetID,
		ProcessSourceKind:   input.ProcessSourceKind,
		ProcessSourceID:     input.ProcessSourceID,
		Status:              status,
		ClosedReason:        closedReason,
		FailureReasonCode:   input.FailureReasonCode,
		AdmittedCount:       len(orderedDiscoveryIDs),
		OrderedDiscoveryIDs: slices.Clone(orderedDiscoveryIDs),
		CompletedAt:         input.CompletedAt,
	}
	if err := value.validateShapeV2(); err != nil {
		return CandidateBatchV2{}, err
	}
	hash, err := candidatePoolContentHashV2(
		"vitlane.candidate-batch.v2", value,
	)
	if err != nil {
		return CandidateBatchV2{}, err
	}
	value.ContentHash = hash
	return value, nil
}

func (b CandidateBatchV2) Validate() error {
	if err := b.validateShapeV2(); err != nil || b.ContentHash == "" {
		return ErrCandidatePoolV2Invalid
	}
	expected, err := candidatePoolContentHashV2(
		"vitlane.candidate-batch.v2", b,
	)
	if err != nil {
		return err
	}
	if expected != b.ContentHash {
		return ErrCandidatePoolV2Invalid
	}
	return nil
}

func (b CandidateBatchV2) validateShapeV2() error {
	if b.ID == "" || b.ID != strings.TrimSpace(b.ID) ||
		b.TargetID == "" || b.TargetID != strings.TrimSpace(b.TargetID) ||
		b.ProcessSourceID == "" ||
		b.ProcessSourceID != strings.TrimSpace(b.ProcessSourceID) ||
		b.FailureReasonCode != strings.TrimSpace(b.FailureReasonCode) ||
		b.CompletedAt.IsZero() || b.AdmittedCount < 0 ||
		b.AdmittedCount != len(b.OrderedDiscoveryIDs) ||
		!validCandidateBatchProcessV2(
			b.ProcessSourceKind, b.ProcessSourceID,
			candidateBatchOutcomeFromStatusV2(b.Status),
		) || hasBlankOrDuplicateStringsV2(b.OrderedDiscoveryIDs) {
		return ErrCandidatePoolV2Invalid
	}
	switch b.Status {
	case CandidateBatchCompletedNonemptyV2:
		if b.AdmittedCount == 0 || b.ClosedReason != "" ||
			b.FailureReasonCode != "" {
			return ErrCandidatePoolV2Invalid
		}
	case CandidateBatchCompletedEmptyV2:
		if b.AdmittedCount != 0 ||
			!validCandidateEmptyReasonV2(b.ClosedReason) ||
			b.FailureReasonCode != "" {
			return ErrCandidatePoolV2Invalid
		}
		if (b.ClosedReason == CandidateBatchNoResultsV2 &&
			b.ProcessSourceKind != CandidateBatchResearchRoundV2) ||
			(b.ClosedReason == CandidateBatchNoNewResultsV2 &&
				b.ProcessSourceKind != CandidateBatchExpansionV2) {
			return ErrCandidatePoolV2Invalid
		}
	case CandidateBatchFailedV2:
		if b.AdmittedCount != 0 || b.ClosedReason != "" ||
			b.FailureReasonCode == "" {
			return ErrCandidatePoolV2Invalid
		}
	default:
		return ErrCandidatePoolV2Invalid
	}
	return nil
}

func candidateBatchOutcomeFromStatusV2(
	status CandidateBatchStatusV2,
) CandidateBatchFinalizeOutcomeV2 {
	if status == CandidateBatchFailedV2 {
		return CandidateBatchFinalizeFailureV2
	}
	return CandidateBatchFinalizeSuccessV2
}

func validCandidateBatchProcessV2(
	kind CandidateBatchProcessSourceKindV2,
	processID string,
	outcome CandidateBatchFinalizeOutcomeV2,
) bool {
	if strings.TrimSpace(processID) == "" ||
		processID != strings.TrimSpace(processID) {
		return false
	}
	switch kind {
	case CandidateBatchResearchRoundV2:
		return outcome == CandidateBatchFinalizeSuccessV2 ||
			outcome == CandidateBatchFinalizeFailureV2
	case CandidateBatchExpansionV2:
		return outcome == CandidateBatchFinalizeSuccessV2 ||
			outcome == CandidateBatchFinalizeFailureV2
	default:
		return false
	}
}

func validCandidateEmptyReasonV2(value CandidateBatchClosedReasonV2) bool {
	switch value {
	case CandidateBatchNoResultsV2, CandidateBatchNoNewResultsV2,
		CandidateBatchCapacityReachedNoAdmissionV2:
		return true
	default:
		return false
	}
}

func canonicalCandidateBatchFinalizeInputV2(
	value CandidateBatchFinalizeInputV2,
) CandidateBatchFinalizeInputV2 {
	value.BatchID = strings.TrimSpace(value.BatchID)
	value.ProcessSourceID = strings.TrimSpace(value.ProcessSourceID)
	value.FailureReasonCode = strings.TrimSpace(value.FailureReasonCode)
	value.Aliases = slices.Clone(value.Aliases)
	value.Admissions = slices.Clone(value.Admissions)
	return value
}

func canonicalCandidateAdmissionInputV2(
	value CandidateAdmissionInputV2,
) CandidateAdmissionInputV2 {
	value.CandidateID = strings.TrimSpace(value.CandidateID)
	value.IdentityKey = strings.TrimSpace(value.IdentityKey)
	value.InitialLocatorRef = strings.TrimSpace(value.InitialLocatorRef)
	value.DiscoveryID = strings.TrimSpace(value.DiscoveryID)
	value.LocatorRef = strings.TrimSpace(value.LocatorRef)
	value.PreviewVariantRef = strings.TrimSpace(value.PreviewVariantRef)
	value.ObservationEvidenceRefs = canonicalStringSetV2(
		value.ObservationEvidenceRefs,
	)
	value.RankingPolicyVersion = strings.TrimSpace(value.RankingPolicyVersion)
	value.Assessment.ID = strings.TrimSpace(value.Assessment.ID)
	return value
}

func validCandidateAssessmentPolicyV2(
	value CandidateAssessmentSourcePolicyV2,
) bool {
	return value == CandidateAssessmentResearchRankingV1 ||
		value == CandidateAssessmentExpansionV1
}

func validCandidateDiscoverySourceV2(
	value CandidateDiscoverySourceKindV2,
) bool {
	return value == CandidateDiscoveryResearchRoundV2 ||
		value == CandidateDiscoveryExpansionV2
}

func canonicalCandidateAssessmentClaimV2(
	value CandidateAssessmentClaimV2,
) CandidateAssessmentClaimV2 {
	value.Text = strings.TrimSpace(value.Text)
	value.EvidenceRefs = canonicalStringSetV2(value.EvidenceRefs)
	return value
}

func canonicalCandidateAssessmentClaimsV2(
	values []CandidateAssessmentClaimV2,
) []CandidateAssessmentClaimV2 {
	result := slices.Clone(values)
	for index := range result {
		result[index] = canonicalCandidateAssessmentClaimV2(result[index])
	}
	if result == nil {
		result = []CandidateAssessmentClaimV2{}
	}
	return result
}

func canonicalCandidateAssessmentLinesV2(
	values []CandidateAssessmentLineV2,
) []CandidateAssessmentLineV2 {
	result := slices.Clone(values)
	for index := range result {
		result[index].Text = strings.TrimSpace(result[index].Text)
		result[index].EvidenceRefs = canonicalStringSetV2(
			result[index].EvidenceRefs,
		)
	}
	if result == nil {
		result = []CandidateAssessmentLineV2{}
	}
	return result
}

func canonicalCandidateScoreComponentsV2(
	values []CandidateScoreComponentV2,
) []CandidateScoreComponentV2 {
	result := slices.Clone(values)
	for index := range result {
		result[index].Name = strings.TrimSpace(result[index].Name)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	if result == nil {
		result = []CandidateScoreComponentV2{}
	}
	return result
}

func validCandidateAssessmentClaimV2(value CandidateAssessmentClaimV2) bool {
	return value.Text != "" && value.Text == strings.TrimSpace(value.Text) &&
		len(value.EvidenceRefs) > 0 &&
		slices.Equal(value.EvidenceRefs, canonicalStringSetV2(value.EvidenceRefs))
}

func validCandidateAssessmentClaimsV2(
	values []CandidateAssessmentClaimV2,
) bool {
	for _, value := range values {
		if !validCandidateAssessmentClaimV2(value) {
			return false
		}
	}
	return true
}

func validCandidateAssessmentLinesV2(
	values []CandidateAssessmentLineV2,
) bool {
	for _, value := range values {
		if value.Text == "" || value.Text != strings.TrimSpace(value.Text) ||
			(value.Origin != CandidateClaimProviderExplicitV2 &&
				value.Origin != CandidateClaimProviderInferredV2) ||
			len(value.EvidenceRefs) == 0 ||
			!slices.Equal(
				value.EvidenceRefs, canonicalStringSetV2(value.EvidenceRefs),
			) {
			return false
		}
	}
	return true
}

func validCandidateScoreComponentsV2(
	values []CandidateScoreComponentV2,
) bool {
	previous := ""
	for _, value := range values {
		if value.Name == "" || value.Name != strings.TrimSpace(value.Name) ||
			value.Name <= previous || value.Score < 0 || value.Score > 100 {
			return false
		}
		previous = value.Name
	}
	return true
}

func hasBlankOrDuplicateStringsV2(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) {
			return true
		}
		if _, duplicate := seen[value]; duplicate {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func candidatePoolContentHashV2(schema string, value any) (string, error) {
	// Every V2 content value has ContentHash as its last JSON field. Marshal a
	// copy with that field cleared through the concrete type switch.
	switch typed := value.(type) {
	case CandidateV2:
		typed.ContentHash = ""
		value = typed
	case CandidateIdentityAliasV2:
		typed.ContentHash = ""
		value = typed
	case CandidateDiscoveryV2:
		typed.ContentHash = ""
		value = typed
	case CandidateAssessmentRevisionV2:
		typed.ContentHash = ""
		value = typed
	case CandidateBatchV2:
		typed.ContentHash = ""
		value = typed
	default:
		return "", fmt.Errorf("%w: unsupported hash value", ErrCandidatePoolV2Invalid)
	}
	hash, _, err := HashJSON(struct {
		Schema string `json:"schema"`
		Value  any    `json:"value"`
	}{Schema: schema, Value: value})
	if err != nil {
		return "", err
	}
	return "sha256:" + hash, nil
}

type candidateCanonicalResolverV2 struct {
	targetID         string
	candidatesByID   map[string]CandidateV2
	candidateIDByKey map[string]string
	aliasTargetByKey map[string]string
}

func newCandidateCanonicalResolverV2(
	targetID string,
) *candidateCanonicalResolverV2 {
	return &candidateCanonicalResolverV2{
		targetID:         targetID,
		candidatesByID:   make(map[string]CandidateV2),
		candidateIDByKey: make(map[string]string),
		aliasTargetByKey: make(map[string]string),
	}
}

func (r *candidateCanonicalResolverV2) addCandidate(value CandidateV2) error {
	if value.TargetID != r.targetID {
		return ErrCandidatePoolV2Invalid
	}
	if _, duplicate := r.candidatesByID[value.ID]; duplicate {
		return ErrCandidatePoolV2Invalid
	}
	if _, duplicate := r.candidateIDByKey[value.IdentityKey]; duplicate {
		return ErrCandidatePoolV2Invalid
	}
	r.candidatesByID[value.ID] = value
	r.candidateIDByKey[value.IdentityKey] = value.ID
	return nil
}

func (r *candidateCanonicalResolverV2) addAlias(
	value CandidateIdentityAliasV2,
) error {
	if value.TargetID != r.targetID {
		return ErrCandidatePoolV2Invalid
	}
	if _, exists := r.candidatesByID[value.CanonicalCandidateID]; !exists {
		return ErrCandidatePoolV2Invalid
	}
	if value.CreatedAt.Before(
		r.candidatesByID[value.CanonicalCandidateID].CreatedAt,
	) {
		return ErrCandidatePoolV2Invalid
	}
	if _, duplicate := r.aliasTargetByKey[value.ProvisionalIdentityKey]; duplicate {
		return ErrCandidatePoolV2Invalid
	}
	r.aliasTargetByKey[value.ProvisionalIdentityKey] = value.CanonicalCandidateID
	if _, err := r.canonicalCount(); err != nil {
		delete(r.aliasTargetByKey, value.ProvisionalIdentityKey)
		return err
	}
	return nil
}

func (r *candidateCanonicalResolverV2) resolveIdentityKey(
	identityKey string,
) (string, bool, error) {
	identityKey = strings.TrimSpace(identityKey)
	if targetID, exists := r.aliasTargetByKey[identityKey]; exists {
		resolved, err := r.resolveCandidateID(targetID, make(map[string]struct{}))
		return resolved, true, err
	}
	candidateID, exists := r.candidateIDByKey[identityKey]
	if !exists {
		return "", false, nil
	}
	resolved, err := r.resolveCandidateID(candidateID, make(map[string]struct{}))
	return resolved, true, err
}

func (r *candidateCanonicalResolverV2) resolveCandidateID(
	candidateID string,
	seen map[string]struct{},
) (string, error) {
	if _, cycle := seen[candidateID]; cycle {
		return "", ErrCandidateAliasV2Cycle
	}
	candidate, exists := r.candidatesByID[candidateID]
	if !exists {
		return "", ErrCandidatePoolV2Invalid
	}
	seen[candidateID] = struct{}{}
	targetID, aliased := r.aliasTargetByKey[candidate.IdentityKey]
	if !aliased {
		return candidateID, nil
	}
	return r.resolveCandidateID(targetID, seen)
}

func (r *candidateCanonicalResolverV2) canonicalCount() (int64, error) {
	canonical := make(map[string]struct{}, len(r.candidatesByID))
	for candidateID := range r.candidatesByID {
		resolved, err := r.resolveCandidateID(
			candidateID, make(map[string]struct{}),
		)
		if err != nil {
			return 0, err
		}
		canonical[resolved] = struct{}{}
	}
	return int64(len(canonical)), nil
}
