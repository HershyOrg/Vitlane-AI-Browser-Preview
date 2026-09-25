package app

import (
	"context"
	"strings"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

// TargetSelectionRemovalRepository is the Curation-owned persistence boundary
// for a TARGET_REMOVE command. Implementations update every active
// Selection in the caller's ambient transaction without opening a nested
// transaction or creating a standalone Selection command.
type TargetSelectionRemovalRepository interface {
	SoftRemoveActiveSelectionsForTarget(
		context.Context,
		string,
		string,
		string,
		time.Time,
	) error
}

// RemoveActiveSelectionsForTarget terminally soft-removes the active
// Selections owned by one Target. The TARGET_REMOVE CurationAction is the
// command audit source; curation_selection_commands remains reserved for
// direct Selection create/update/remove commands.
func (s *SelectionService) RemoveActiveSelectionsForTarget(
	ctx context.Context,
	userID, curationID, targetID string,
	removedAt time.Time,
) error {
	repository, ok := s.repository.(TargetSelectionRemovalRepository)
	if !ok {
		return curationdomain.ErrSelectionInvalid
	}
	userID = strings.TrimSpace(userID)
	curationID = strings.TrimSpace(curationID)
	targetID = strings.TrimSpace(targetID)
	if userID == "" || curationID == "" || targetID == "" ||
		removedAt.IsZero() {
		return curationdomain.ErrSelectionInvalid
	}
	return repository.SoftRemoveActiveSelectionsForTarget(
		ctx,
		userID,
		curationID,
		targetID,
		removedAt,
	)
}
