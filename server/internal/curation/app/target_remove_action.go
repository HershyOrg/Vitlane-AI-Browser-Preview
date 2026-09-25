package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

// TargetRemovalRepository is the narrow owner port for TARGET_REMOVE. All
// methods participate in the Service's ambient transaction.
type TargetRemovalRepository interface {
	CurationActionRepository
	CurationStateRepository
	GetCurationTarget(
		context.Context,
		string,
		string,
		string,
		bool,
	) (curationdomain.PlanTarget, error)
	SaveCurationTargetRemoval(
		context.Context,
		int64,
		curationdomain.PlanTarget,
	) error
}

type ExecuteTargetRemoveActionInput struct {
	ActionID                string
	UserID                  string
	CurationID              string
	TargetID                string
	ExpectedCurationVersion int64
}

type ExecuteTargetRemoveActionResult struct {
	Action          curationdomain.CurationAction `json:"action"`
	TargetID        string                        `json:"targetId"`
	CurationVersion int64                         `json:"curationVersion"`
	RemovedAt       time.Time                     `json:"removedAt"`
	Replay          bool                          `json:"replay"`
}

// ExecuteTargetRemoveAction records the immutable owner command, soft-removes
// the Target and its Shopping-owned active Selections, and advances
// Curation.version in one transaction. ShoppingPlan, Research and AgencyOrder
// rows are deliberately outside this mutation.
func (s *Service) ExecuteTargetRemoveAction(
	ctx context.Context,
	input ExecuteTargetRemoveActionInput,
) (ExecuteTargetRemoveActionResult, error) {
	input.ActionID = strings.TrimSpace(input.ActionID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.CurationID = strings.TrimSpace(input.CurationID)
	input.TargetID = strings.TrimSpace(input.TargetID)
	if !uuidPattern.MatchString(input.ActionID) ||
		input.UserID == "" ||
		input.CurationID == "" ||
		input.TargetID == "" {
		return ExecuteTargetRemoveActionResult{},
			curationdomain.ErrCurationActionInvalid
	}

	subjectID := input.TargetID
	recordInput := RecordCurationActionInput{
		ActionID:                input.ActionID,
		UserID:                  input.UserID,
		CurationID:              input.CurationID,
		Type:                    curationdomain.CurationActionTargetRemove,
		SubjectType:             curationdomain.CurationActionSubjectTarget,
		SubjectID:               &subjectID,
		ExpectedCurationVersion: input.ExpectedCurationVersion,
		SourceRefType:           curationdomain.CurationActionSourcePlanTarget,
		SourceRefID:             input.TargetID,
	}
	requestHash, err := hashCurationActionRecord(recordInput)
	if err != nil {
		return ExecuteTargetRemoveActionResult{}, err
	}
	repository, err := s.targetRemovalRepository()
	if err != nil {
		return ExecuteTargetRemoveActionResult{}, err
	}
	if s.targetSelections == nil {
		return ExecuteTargetRemoveActionResult{},
			fmt.Errorf("target selection remover is unavailable")
	}

	var result ExecuteTargetRemoveActionResult
	err = s.transactor.WithinTransaction(
		ctx,
		func(txContext context.Context) error {
			existing, getErr := repository.GetCurationAction(
				txContext,
				input.UserID,
				input.ActionID,
				true,
			)
			switch {
			case getErr == nil:
				replayed, replayErr := replayTargetRemoveAction(
					existing,
					requestHash,
				)
				if replayErr != nil {
					return replayErr
				}
				result = replayed
				return nil
			case !errors.Is(getErr, ErrCurationActionNotFound):
				return getErr
			}

			curation, getErr := repository.GetCuration(
				txContext,
				input.UserID,
				input.CurationID,
				true,
			)
			if getErr != nil {
				return getErr
			}
			if curation.ArchivedAt != nil {
				return curationdomain.ErrCurationArchived
			}
			if gateErr := ensureNoActiveCurationWork(
				txContext,
				s.foregroundWork,
				input.UserID,
				input.CurationID,
			); gateErr != nil {
				return gateErr
			}

			// The Curation row serializes resource-version mutations. Recheck
			// the action after acquiring it so a concurrent identical request
			// becomes a replay rather than a stale-version failure.
			existing, getErr = repository.GetCurationAction(
				txContext,
				input.UserID,
				input.ActionID,
				true,
			)
			switch {
			case getErr == nil:
				replayed, replayErr := replayTargetRemoveAction(
					existing,
					requestHash,
				)
				if replayErr != nil {
					return replayErr
				}
				result = replayed
				return nil
			case !errors.Is(getErr, ErrCurationActionNotFound):
				return getErr
			}

			target, getErr := repository.GetCurationTarget(
				txContext,
				input.UserID,
				input.CurationID,
				input.TargetID,
				true,
			)
			if getErr != nil {
				return getErr
			}
			phase, phaseErr := persistedCurationActionPhase(curation.Phase)
			if phaseErr != nil {
				return phaseErr
			}
			// PostgreSQL timestamptz stores microseconds. Canonicalizing before
			// the first response keeps RemovedAt byte-stable on a later replay.
			now := s.clock.Now().UTC().Truncate(time.Microsecond)
			action, actionErr :=
				curationdomain.NewTargetRemoveCurationAction(
					curationdomain.NewCurationActionInput{
						ID: curationdomain.CurationActionID(
							input.ActionID,
						),
						CurationID:             curation.ID,
						ActorUserID:            curation.UserID,
						PhaseAtRequest:         phase,
						CurrentCurationVersion: curation.Version,
						Request: curationdomain.CurationActionRequest{
							Type:                    curationdomain.CurationActionTargetRemove,
							SubjectType:             curationdomain.CurationActionSubjectTarget,
							SubjectID:               &subjectID,
							ExpectedCurationVersion: input.ExpectedCurationVersion,
						},
						SourceRefType: curationdomain.CurationActionSourcePlanTarget,
						SourceRefID:   input.TargetID,
						RequestHash:   requestHash[:],
						CreatedAt:     now,
					},
				)
			if actionErr != nil {
				return actionErr
			}

			previousCurationVersion := curation.Version
			previousTargetVersion := target.Version
			if removeErr := curation.RemoveTarget(
				&target,
				curation.UserID,
				input.ExpectedCurationVersion,
				now,
			); removeErr != nil {
				return removeErr
			}

			inserted, insertErr := repository.InsertCurationAction(
				txContext,
				action,
			)
			if insertErr != nil {
				return insertErr
			}
			if !inserted {
				existing, getErr = repository.GetCurationAction(
					txContext,
					input.UserID,
					input.ActionID,
					true,
				)
				if errors.Is(getErr, ErrCurationActionNotFound) {
					return curationdomain.ErrIdempotencyKeyReused
				}
				if getErr != nil {
					return getErr
				}
				replayed, replayErr := replayTargetRemoveAction(
					existing,
					requestHash,
				)
				if replayErr != nil {
					return replayErr
				}
				result = replayed
				return nil
			}
			if saveErr := repository.SaveCurationTargetRemoval(
				txContext,
				previousTargetVersion,
				target,
			); saveErr != nil {
				return saveErr
			}
			if removeErr := s.targetSelections.RemoveActiveSelectionsForTarget(
				txContext,
				input.UserID,
				input.CurationID,
				input.TargetID,
				now,
			); removeErr != nil {
				return removeErr
			}
			if saveErr := repository.SaveCuration(
				txContext,
				previousCurationVersion,
				curation,
			); saveErr != nil {
				return saveErr
			}
			result = targetRemoveActionResult(action, false)
			return nil
		},
	)
	return result, err
}

func (s *Service) targetRemovalRepository() (
	TargetRemovalRepository,
	error,
) {
	repository, ok := s.repository.(TargetRemovalRepository)
	if !ok {
		return nil, fmt.Errorf("target removal repository is unavailable")
	}
	return repository, nil
}

func replayTargetRemoveAction(
	action curationdomain.CurationAction,
	requestHash curationdomain.CurationActionRequestHash,
) (ExecuteTargetRemoveActionResult, error) {
	if action.RequestHash != requestHash ||
		action.Type != curationdomain.CurationActionTargetRemove ||
		action.SubjectType != curationdomain.CurationActionSubjectTarget ||
		action.SubjectID == nil ||
		action.SourceRefType != curationdomain.CurationActionSourcePlanTarget ||
		action.SourceRefID != *action.SubjectID {
		return ExecuteTargetRemoveActionResult{},
			curationdomain.ErrIdempotencyKeyReused
	}
	return targetRemoveActionResult(action, true), nil
}

func targetRemoveActionResult(
	action curationdomain.CurationAction,
	replay bool,
) ExecuteTargetRemoveActionResult {
	targetID := ""
	if action.SubjectID != nil {
		targetID = *action.SubjectID
	}
	return ExecuteTargetRemoveActionResult{
		Action:          action,
		TargetID:        targetID,
		CurationVersion: action.ExpectedCurationVersion + 1,
		RemovedAt:       action.CreatedAt,
		Replay:          replay,
	}
}
