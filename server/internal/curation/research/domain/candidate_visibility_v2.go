package domain

import (
	"slices"
	"sort"
	"strings"
)

type CandidateVisibilityReasonV2 string

const (
	CandidateVisibilityLatestBatchV2 CandidateVisibilityReasonV2 = "LATEST_BATCH"
	CandidateVisibilityPinnedV2      CandidateVisibilityReasonV2 = "PINNED"
	CandidateVisibilityHistoricalV2  CandidateVisibilityReasonV2 = "HISTORICAL"
)

type CandidateVisibilityProjectionInputV2 struct {
	Snapshot                    CandidatePoolSnapshotV2
	Discoveries                 []CandidateDiscoveryV2
	PinnedCandidateIDs          []string
	IncludeHistoricalCandidates bool
}

// CandidateVisibilityV2 is canonical-group deduped. MemberCandidateIDs retain
// stable roots whose interactions/configurations/selections must not move when
// exact correlation joins them.
type CandidateVisibilityV2 struct {
	CanonicalCandidateID string                      `json:"candidateId"`
	MemberCandidateIDs   []string                    `json:"memberCandidateIds"`
	DisplayDiscoveryID   string                      `json:"displayDiscoveryId"`
	VisibilityReason     CandidateVisibilityReasonV2 `json:"visibilityReason"`
}

// ProjectCandidateVisibilityV2 derives visibility only. Eye-toggle changes
// IncludeHistoricalCandidates and never mutates Candidate or CandidatePool.
func ProjectCandidateVisibilityV2(
	input CandidateVisibilityProjectionInputV2,
) ([]CandidateVisibilityV2, error) {
	resolver, err := validateCandidatePoolSnapshotV2(input.Snapshot)
	if err != nil {
		return nil, err
	}

	discoveriesByCanonical := make(map[string][]CandidateDiscoveryV2)
	discoveryIDs := make(map[string]struct{}, len(input.Discoveries))
	currentBatchDiscoveries := make([]CandidateDiscoveryV2, 0)
	for _, discovery := range input.Discoveries {
		if err := discovery.Validate(); err != nil {
			return nil, ErrCandidatePoolV2Invalid
		}
		if _, duplicate := discoveryIDs[discovery.ID]; duplicate {
			return nil, ErrCandidatePoolV2Invalid
		}
		discoveryIDs[discovery.ID] = struct{}{}
		if _, exists := resolver.candidatesByID[discovery.CandidateID]; !exists {
			return nil, ErrCandidatePoolV2Invalid
		}
		canonicalID, err := resolver.resolveCandidateID(
			discovery.CandidateID, make(map[string]struct{}),
		)
		if err != nil {
			return nil, err
		}
		discoveriesByCanonical[canonicalID] = append(
			discoveriesByCanonical[canonicalID], discovery,
		)
		if discovery.CandidateBatchID ==
			input.Snapshot.Pool.CurrentVisibleBatchID {
			currentBatchDiscoveries = append(
				currentBatchDiscoveries, discovery,
			)
		}
	}
	for candidateID := range resolver.candidatesByID {
		canonicalID, err := resolver.resolveCandidateID(
			candidateID, make(map[string]struct{}),
		)
		if err != nil || len(discoveriesByCanonical[canonicalID]) == 0 {
			return nil, ErrCandidatePoolV2Invalid
		}
	}
	if input.Snapshot.Pool.CurrentVisibleBatchID != "" &&
		len(currentBatchDiscoveries) == 0 {
		return nil, ErrCandidatePoolV2Invalid
	}

	pinnedCanonical := make(map[string]struct{}, len(input.PinnedCandidateIDs))
	pinnedIDs := make(map[string]struct{}, len(input.PinnedCandidateIDs))
	for _, candidateID := range input.PinnedCandidateIDs {
		candidateID = strings.TrimSpace(candidateID)
		if candidateID == "" {
			return nil, ErrCandidatePoolV2Invalid
		}
		if _, duplicate := pinnedIDs[candidateID]; duplicate {
			return nil, ErrCandidatePoolV2Invalid
		}
		pinnedIDs[candidateID] = struct{}{}
		if _, exists := resolver.candidatesByID[candidateID]; !exists {
			return nil, ErrCandidatePoolV2Invalid
		}
		canonicalID, err := resolver.resolveCandidateID(
			candidateID, make(map[string]struct{}),
		)
		if err != nil {
			return nil, err
		}
		pinnedCanonical[canonicalID] = struct{}{}
	}

	memberIDs := make(map[string][]string)
	for candidateID := range resolver.candidatesByID {
		canonicalID, err := resolver.resolveCandidateID(
			candidateID, make(map[string]struct{}),
		)
		if err != nil {
			return nil, err
		}
		memberIDs[canonicalID] = append(memberIDs[canonicalID], candidateID)
	}
	for canonicalID := range memberIDs {
		sort.Strings(memberIDs[canonicalID])
		sort.Slice(discoveriesByCanonical[canonicalID], func(left, right int) bool {
			leftValue := discoveriesByCanonical[canonicalID][left]
			rightValue := discoveriesByCanonical[canonicalID][right]
			if !leftValue.DiscoveredAt.Equal(rightValue.DiscoveredAt) {
				return leftValue.DiscoveredAt.Before(rightValue.DiscoveredAt)
			}
			return leftValue.ID < rightValue.ID
		})
	}

	sort.Slice(currentBatchDiscoveries, func(left, right int) bool {
		if currentBatchDiscoveries[left].RankPosition !=
			currentBatchDiscoveries[right].RankPosition {
			return currentBatchDiscoveries[left].RankPosition <
				currentBatchDiscoveries[right].RankPosition
		}
		return currentBatchDiscoveries[left].ID <
			currentBatchDiscoveries[right].ID
	})
	currentDisplay := make(map[string]string)
	currentOrder := make([]string, 0, len(currentBatchDiscoveries))
	for _, discovery := range currentBatchDiscoveries {
		canonicalID, err := resolver.resolveCandidateID(
			discovery.CandidateID, make(map[string]struct{}),
		)
		if err != nil {
			return nil, err
		}
		if _, exists := currentDisplay[canonicalID]; exists {
			continue
		}
		currentDisplay[canonicalID] = discovery.ID
		currentOrder = append(currentOrder, canonicalID)
	}

	result := make([]CandidateVisibilityV2, 0, len(memberIDs))
	added := make(map[string]struct{}, len(memberIDs))
	appendVisibility := func(
		canonicalID string,
		reason CandidateVisibilityReasonV2,
		discoveryID string,
	) {
		result = append(result, CandidateVisibilityV2{
			CanonicalCandidateID: canonicalID,
			MemberCandidateIDs:   slices.Clone(memberIDs[canonicalID]),
			DisplayDiscoveryID:   discoveryID,
			VisibilityReason:     reason,
		})
		added[canonicalID] = struct{}{}
	}
	for _, canonicalID := range currentOrder {
		appendVisibility(
			canonicalID, CandidateVisibilityLatestBatchV2,
			currentDisplay[canonicalID],
		)
	}

	canonicalOrder := make([]string, 0, len(memberIDs))
	for canonicalID := range memberIDs {
		canonicalOrder = append(canonicalOrder, canonicalID)
	}
	sort.Slice(canonicalOrder, func(left, right int) bool {
		leftCandidate := resolver.candidatesByID[canonicalOrder[left]]
		rightCandidate := resolver.candidatesByID[canonicalOrder[right]]
		if !leftCandidate.CreatedAt.Equal(rightCandidate.CreatedAt) {
			return leftCandidate.CreatedAt.Before(rightCandidate.CreatedAt)
		}
		return canonicalOrder[left] < canonicalOrder[right]
	})
	for _, canonicalID := range canonicalOrder {
		if _, exists := added[canonicalID]; exists {
			continue
		}
		discoveries := discoveriesByCanonical[canonicalID]
		latestDiscoveryID := discoveries[len(discoveries)-1].ID
		if _, pinned := pinnedCanonical[canonicalID]; pinned {
			appendVisibility(
				canonicalID, CandidateVisibilityPinnedV2,
				latestDiscoveryID,
			)
			continue
		}
		if input.IncludeHistoricalCandidates {
			appendVisibility(
				canonicalID, CandidateVisibilityHistoricalV2,
				latestDiscoveryID,
			)
		}
	}
	return result, nil
}
