package shopifystorefront

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"sync"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

// MemoryVariantPageCursorAuthorityV2 keeps provider cursors server-side and
// gives the browser only a random, short-lived lookup token. Cursor state is a
// disposable browse projection: restart/expiry intentionally produces
// STALE_VARIANT_PAGE and requires reopening the first page.
type MemoryVariantPageCursorAuthorityV2 struct {
	clock sharedapp.Clock
	mu    sync.Mutex
	items map[string]researchapp.VariantChoiceContinuationV2
}

var _ researchapp.VariantPageCursorAuthorityV2 = (*MemoryVariantPageCursorAuthorityV2)(nil)

func NewMemoryVariantPageCursorAuthorityV2(
	clock sharedapp.Clock,
) (*MemoryVariantPageCursorAuthorityV2, error) {
	if clock == nil {
		return nil, fault.New(
			fault.InvalidInput, researchapp.VariantChoiceFailureInvalidV2, false,
		)
	}
	return &MemoryVariantPageCursorAuthorityV2{
		clock: clock, items: make(map[string]researchapp.VariantChoiceContinuationV2),
	}, nil
}

func (authority *MemoryVariantPageCursorAuthorityV2) IssueContinuation(
	_ context.Context,
	continuation researchapp.VariantChoiceContinuationV2,
) (string, error) {
	if authority == nil || authority.clock == nil ||
		strings.TrimSpace(continuation.OwnerID) == "" ||
		strings.TrimSpace(continuation.ProviderCursor) == "" ||
		!continuation.ExpiresAt.After(authority.clock.Now().UTC()) {
		return "", fault.New(
			fault.InternalFailure, researchapp.VariantChoiceFailureArtifactV2, false,
		)
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fault.Wrap(
			err, fault.InternalFailure, researchapp.VariantChoiceFailureArtifactV2, false,
		)
	}
	token := "vpc_" + base64.RawURLEncoding.EncodeToString(random)
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.removeExpiredLockedV2()
	authority.items[token] = cloneVariantContinuationV2(continuation)
	return token, nil
}

func (authority *MemoryVariantPageCursorAuthorityV2) VerifyContinuation(
	_ context.Context,
	ownerID, token string,
) (researchapp.VariantChoiceContinuationV2, error) {
	ownerID = strings.TrimSpace(ownerID)
	token = strings.TrimSpace(token)
	if authority == nil || authority.clock == nil || ownerID == "" || token == "" ||
		!strings.HasPrefix(token, "vpc_") {
		return researchapp.VariantChoiceContinuationV2{}, staleVariantCursorFaultV2()
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.removeExpiredLockedV2()
	continuation, exists := authority.items[token]
	if !exists || continuation.OwnerID != ownerID {
		return researchapp.VariantChoiceContinuationV2{}, staleVariantCursorFaultV2()
	}
	return cloneVariantContinuationV2(continuation), nil
}

func (authority *MemoryVariantPageCursorAuthorityV2) removeExpiredLockedV2() {
	now := authority.clock.Now().UTC()
	for token, continuation := range authority.items {
		if !continuation.ExpiresAt.After(now) {
			delete(authority.items, token)
		}
	}
}

func cloneVariantContinuationV2(
	continuation researchapp.VariantChoiceContinuationV2,
) researchapp.VariantChoiceContinuationV2 {
	continuation.SeenVariantIDs = append([]string(nil), continuation.SeenVariantIDs...)
	return continuation
}

func staleVariantCursorFaultV2() error {
	return fault.New(
		fault.Conflict, researchapp.VariantChoiceFailureStalePageV2, false,
	)
}
