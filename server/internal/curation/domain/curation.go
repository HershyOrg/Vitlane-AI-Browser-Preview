package domain

import (
	"fmt"
	"time"
)

// Curation owns the user's durable Planning -> Curating phase. It deliberately
// does not model Intelligence execution, authentication, AgencyOrder, or coverage state.
// Those lifecycles are projected from their owning products.
type Curation struct {
	ID             CurationID     `json:"id"`
	ShoppingPlanID ShoppingPlanID `json:"shoppingPlanId"`
	UserID         UserID         `json:"userId"`
	Phase          CurationPhase  `json:"phase"`
	Version        int64          `json:"version"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	ArchivedAt     *time.Time     `json:"archivedAt,omitempty"`
	ArchivedBy     *UserID        `json:"archivedByUserId,omitempty"`
}

type NewCurationInput struct {
	ID             CurationID
	ShoppingPlanID ShoppingPlanID
	UserID         UserID
	Now            time.Time
}

func NewCuration(input NewCurationInput) (Curation, error) {
	if input.ID == "" || input.ShoppingPlanID == "" || input.UserID == "" {
		return Curation{}, fmt.Errorf("%w: identity is required", ErrCurationNotFound)
	}
	if input.Now.IsZero() {
		return Curation{}, fmt.Errorf("%w: createdAt is required", ErrCurationPhaseInvalid)
	}
	return Curation{
		ID:             input.ID,
		ShoppingPlanID: input.ShoppingPlanID,
		UserID:         input.UserID,
		Phase:          CurationPhasePlanning,
		Version:        1,
		CreatedAt:      input.Now,
		UpdatedAt:      input.Now,
	}, nil
}

// StartCurating commits only the product phase transition. The application
// layer must place it in the same transaction as the first ResearchRound and
// exact AgentControl work batch.
func (c *Curation) StartCurating(
	activeTargetCount int,
	expectedVersion int64,
	now time.Time,
) error {
	if c.ArchivedAt != nil {
		return ErrCurationArchived
	}
	if c.Phase == CurationPhaseCurating {
		return nil
	}
	if c.Phase != CurationPhasePlanning {
		return fmt.Errorf("%w: %q", ErrCurationPhaseInvalid, c.Phase)
	}
	if c.Version != expectedVersion {
		return ErrVersionConflict
	}
	if activeTargetCount < 1 {
		return ErrCurationTargetsRequired
	}
	c.Phase = CurationPhaseCurating
	c.Version++
	c.UpdatedAt = now
	return nil
}

// AddTargets advances the membership version after a PlanningTask atomically
// materializes one or more new active Targets. ShoppingPlan never participates
// in this optimistic-concurrency boundary.
func (c *Curation) AddTargets(
	count int,
	expectedVersion int64,
	now time.Time,
) error {
	if c.ArchivedAt != nil {
		return ErrCurationArchived
	}
	if c.Phase != CurationPhasePlanning &&
		c.Phase != CurationPhaseCurating {
		return fmt.Errorf("%w: %q", ErrCurationPhaseInvalid, c.Phase)
	}
	if c.Version != expectedVersion {
		return ErrVersionConflict
	}
	if count < 1 || now.IsZero() || now.Before(c.UpdatedAt) {
		return ErrCurationActionInvalid
	}
	c.Version++
	c.UpdatedAt = now
	return nil
}

// RemoveTarget is the Curation-owned, non-Agent mutation. It leaves
// ShoppingPlan and all downstream Research/AgencyOrder history untouched.
func (c *Curation) RemoveTarget(
	target *PlanTarget,
	actor UserID,
	expectedVersion int64,
	now time.Time,
) error {
	if c.ArchivedAt != nil {
		return ErrCurationArchived
	}
	if c.Phase != CurationPhasePlanning &&
		c.Phase != CurationPhaseCurating {
		return fmt.Errorf("%w: %q", ErrCurationPhaseInvalid, c.Phase)
	}
	if c.Version != expectedVersion {
		return ErrVersionConflict
	}
	if target == nil ||
		actor == "" ||
		actor != c.UserID ||
		target.UserID != c.UserID ||
		target.CurationID != c.ID ||
		target.PlanID != c.ShoppingPlanID ||
		target.RemovedAt != nil {
		return ErrTargetNotFound
	}
	if target.Version < 1 ||
		now.IsZero() ||
		now.Before(c.UpdatedAt) ||
		now.Before(target.UpdatedAt) {
		return ErrCurationActionInvalid
	}

	removedAt := now
	removedBy := actor
	target.RemovedAt = &removedAt
	target.RemovedByUserID = &removedBy
	target.Version++
	target.UpdatedAt = now
	c.Version++
	c.UpdatedAt = now
	return nil
}

// Archive is independent from Phase. It hides a workspace without changing
// the meaning of Planning or Curating and is safe to replay.
func (c *Curation) Archive(
	actor UserID,
	expectedVersion int64,
	now time.Time,
) error {
	if c.ArchivedAt != nil {
		return nil
	}
	if c.Version != expectedVersion {
		return ErrVersionConflict
	}
	if actor == "" || actor != c.UserID {
		return ErrCurationNotFound
	}
	c.ArchivedAt = &now
	c.ArchivedBy = &actor
	c.Version++
	c.UpdatedAt = now
	return nil
}

func (c Curation) Validate() error {
	if c.ID == "" || c.ShoppingPlanID == "" || c.UserID == "" {
		return ErrCurationNotFound
	}
	switch c.Phase {
	case CurationPhasePlanning, CurationPhaseCurating:
	default:
		return fmt.Errorf("%w: %q", ErrCurationPhaseInvalid, c.Phase)
	}
	if c.Version < 1 || c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		return ErrCurationPhaseInvalid
	}
	if (c.ArchivedAt == nil) != (c.ArchivedBy == nil) {
		return ErrCurationArchived
	}
	return nil
}
