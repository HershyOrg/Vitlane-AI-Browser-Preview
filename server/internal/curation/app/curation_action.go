package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

var ErrCurationActionNotFound = errors.New("CURATION_ACTION_NOT_FOUND")

const maxCurationActionListLimit = 200

// CurationActionRepository is deliberately separate from the legacy Planning
// repository contract so the action read/write boundary can be introduced
// without broadening every adapter at once.
type CurationActionRepository interface {
	GetCuration(
		context.Context,
		string,
		string,
		bool,
	) (curationdomain.Curation, error)
	GetCurationAction(
		context.Context,
		string,
		string,
		bool,
	) (curationdomain.CurationAction, error)
	InsertCurationAction(
		context.Context,
		curationdomain.CurationAction,
	) (bool, error)
	ListCurationActions(
		context.Context,
		string,
		string,
		int,
	) ([]curationdomain.CurationAction, error)
}

type CurationForegroundWorkRepository interface {
	HasActiveCurationWork(
		context.Context,
		string,
		string,
	) (bool, error)
}

type AvailableCurationActionsResult struct {
	CurationID string                                    `json:"curationId"`
	Phase      curationdomain.CurationPhase              `json:"phase"`
	Version    int64                                     `json:"version"`
	Actions    []curationdomain.CurationActionDescriptor `json:"availableActions"`
}

type RecordCurationActionInput struct {
	ActionID                string
	UserID                  string
	CurationID              string
	Type                    curationdomain.CurationActionType
	SubjectType             curationdomain.CurationActionSubjectType
	SubjectID               *string
	Body                    string
	ExpectedCurationVersion int64
	SourceRefType           curationdomain.CurationActionSourceRefType
	SourceRefID             string
}

type RecordCurationActionResult struct {
	Action curationdomain.CurationAction `json:"action"`
	Replay bool                          `json:"replay"`
}

func (s *Service) GetAvailableCurationActions(
	ctx context.Context,
	userID, curationID string,
) (AvailableCurationActionsResult, error) {
	repository, err := s.curationActionRepository()
	if err != nil {
		return AvailableCurationActionsResult{}, err
	}
	curation, err := repository.GetCuration(
		ctx,
		userID,
		curationID,
		false,
	)
	if err != nil {
		return AvailableCurationActionsResult{}, err
	}
	record, err := s.repository.Get(
		ctx,
		userID,
		string(curation.ShoppingPlanID),
		false,
	)
	if err != nil {
		return AvailableCurationActionsResult{}, err
	}
	if record.Curation.ID != curation.ID {
		return AvailableCurationActionsResult{},
			curationdomain.ErrCurationNotFound
	}
	if record.Curation.ArchivedAt != nil {
		return AvailableCurationActionsResult{}, curationdomain.ErrCurationArchived
	}
	actions := availableCurationActions(record.Curation, len(record.Targets))
	return AvailableCurationActionsResult{
		CurationID: string(record.Curation.ID),
		Phase:      record.Curation.Phase,
		Version:    record.Curation.Version,
		Actions:    actions,
	}, nil
}

// RecordCurationAction is the immutable PUT primitive only. It does not create
// Intelligence work or Planning/Research state changes.
func (s *Service) RecordCurationAction(
	ctx context.Context,
	input RecordCurationActionInput,
) (RecordCurationActionResult, error) {
	return s.recordCurationAction(ctx, input, false, false)
}

// RecordOwnedPatchCurationAction may only be called by the product that owns
// the Selection PATCH_ONLY mutation. The caller must already be inside the
// transaction that persists its own command, so an orphan audit row cannot
// survive a failed Selection mutation.
func (s *Service) RecordOwnedPatchCurationAction(
	ctx context.Context,
	input RecordCurationActionInput,
) (RecordCurationActionResult, error) {
	switch input.Type {
	case curationdomain.CurationActionSelectionMutation:
	default:
		return RecordCurationActionResult{},
			curationdomain.ErrCurationActionTypeInvalid
	}
	return s.recordCurationAction(ctx, input, true, false)
}

// RecordManagedContinuationCurationAction records the Server-issued
// PLANNING -> CURATING continuation while the Planning Job that authorized it
// is still RUNNING. That Job is the current pipeline, not a competing user
// command, so only this exact action/source pair may bypass the foreground
// work gate.
func (s *Service) RecordManagedContinuationCurationAction(
	ctx context.Context,
	input RecordCurationActionInput,
) (RecordCurationActionResult, error) {
	if input.Type != curationdomain.CurationActionPlanningStartCurating ||
		input.SourceRefType !=
			curationdomain.CurationActionSourceResearchStartRequest {
		return RecordCurationActionResult{},
			curationdomain.ErrCurationActionTypeInvalid
	}
	return s.recordCurationAction(ctx, input, false, true)
}

// recordInitialIntentCurationAction is the only action write allowed after
// CreateIdempotent has installed the INITIAL PlanningTask. That task is the
// effect of this same Intent action, not a competing foreground command.
func (s *Service) recordInitialIntentCurationAction(
	ctx context.Context,
	input RecordCurationActionInput,
) (RecordCurationActionResult, error) {
	if input.Type != curationdomain.CurationActionIntentNextStep ||
		input.SourceRefType != curationdomain.CurationActionSourceShoppingPlan {
		return RecordCurationActionResult{},
			curationdomain.ErrCurationActionTypeInvalid
	}
	return s.recordCurationAction(ctx, input, false, true)
}

func (s *Service) recordCurationAction(
	ctx context.Context,
	input RecordCurationActionInput,
	ownedPatch bool,
	allowCurrentPipelineWork bool,
) (RecordCurationActionResult, error) {
	if !uuidPattern.MatchString(input.ActionID) ||
		strings.TrimSpace(input.UserID) == "" ||
		strings.TrimSpace(input.CurationID) == "" {
		return RecordCurationActionResult{}, curationdomain.ErrCurationActionInvalid
	}
	requestHash, err := hashCurationActionRecord(input)
	if err != nil {
		return RecordCurationActionResult{}, err
	}
	repository, err := s.curationActionRepository()
	if err != nil {
		return RecordCurationActionResult{}, err
	}

	var result RecordCurationActionResult
	err = s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		existing, getErr := repository.GetCurationAction(
			txContext,
			input.UserID,
			input.ActionID,
			true,
		)
		switch {
		case getErr == nil:

			scope := ThreadExecutionFrom(txContext)
			if scope.ThreadID != "" && scope.ActionID == input.ActionID && existing.ThreadID == scope.ThreadID {
				if existing.CurationID != curationdomain.CurationID(input.CurationID) || existing.Terminal() || existing.Type != input.Type && existing.Type != curationdomain.CurationActionStartResearch {
					return curationdomain.ErrCurationActionInvalid
				}
				if existing.TargetID != "" && (input.SubjectID == nil || *input.SubjectID != existing.TargetID) {
					return curationdomain.ErrCurationActionInvalid
				}
				if existing.Instruction != "" && existing.Type != curationdomain.CurationActionStartResearch && existing.Instruction != input.Body {
					return curationdomain.ErrCurationActionInvalid
				}
				if dispatch, ok := repository.(interface {
					PrepareConversationAction(context.Context, RecordCurationActionInput) error
				}); ok {
					if err := dispatch.PrepareConversationAction(txContext, input); err != nil {
						return err
					}
				}
				result = RecordCurationActionResult{Action: existing}
				return nil
			}
			replayed, replayErr := replayCurationAction(existing, requestHash)
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

		// The first lookup can race with another transaction that owns the same
		// action ID. Recheck after acquiring the Curation row lock so an
		// identical retry replays the committed action before the foreground
		// work gate observes the work created by that action.
		existing, getErr = repository.GetCurationAction(
			txContext,
			input.UserID,
			input.ActionID,
			true,
		)
		switch {
		case getErr == nil:

			scope := ThreadExecutionFrom(txContext)
			if scope.ThreadID != "" && scope.ActionID == input.ActionID && existing.ThreadID == scope.ThreadID {
				if existing.CurationID != curationdomain.CurationID(input.CurationID) || existing.Terminal() || existing.Type != input.Type && existing.Type != curationdomain.CurationActionStartResearch {
					return curationdomain.ErrCurationActionInvalid
				}
				if existing.TargetID != "" && (input.SubjectID == nil || *input.SubjectID != existing.TargetID) {
					return curationdomain.ErrCurationActionInvalid
				}
				if existing.Instruction != "" && existing.Type != curationdomain.CurationActionStartResearch && existing.Instruction != input.Body {
					return curationdomain.ErrCurationActionInvalid
				}
				if dispatch, ok := repository.(interface {
					PrepareConversationAction(context.Context, RecordCurationActionInput) error
				}); ok {
					if err := dispatch.PrepareConversationAction(txContext, input); err != nil {
						return err
					}
				}
				result = RecordCurationActionResult{Action: existing}
				return nil
			}
			replayed, replayErr := replayCurationAction(existing, requestHash)
			if replayErr != nil {
				return replayErr
			}
			result = replayed
			return nil
		case !errors.Is(getErr, ErrCurationActionNotFound):
			return getErr
		}

		if dispatch, ok := repository.(interface {
			PrepareConversationAction(context.Context, RecordCurationActionInput) error
		}); ok {
			if err := dispatch.PrepareConversationAction(txContext, input); err != nil {
				return err
			}
		}

		if !allowCurrentPipelineWork {
			if err := ensureNoActiveCurationWork(
				txContext,
				s.foregroundWork,
				input.UserID,
				input.CurationID,
			); err != nil {
				return err
			}
		}
		phase, phaseErr := actionPhaseAtRequest(curation, input.Type)
		if phaseErr != nil {
			return phaseErr
		}
		newInput := curationdomain.NewCurationActionInput{
			ID:         curationdomain.CurationActionID(input.ActionID),
			CurationID: curation.ID, ActorUserID: curation.UserID,
			PhaseAtRequest: phase, CurrentCurationVersion: curation.Version,
			Request: curationdomain.CurationActionRequest{
				Type: input.Type, SubjectType: input.SubjectType,
				SubjectID: input.SubjectID, Body: input.Body,
				ExpectedCurationVersion: input.ExpectedCurationVersion,
			},
			SourceRefType: input.SourceRefType,
			SourceRefID:   input.SourceRefID,
			RequestHash:   requestHash[:],
			CreatedAt:     s.clock.Now(),
		}
		var action curationdomain.CurationAction
		var actionErr error
		if ownedPatch {
			action, actionErr = curationdomain.NewOwnedPatchCurationAction(
				newInput,
			)
		} else {
			action, actionErr = curationdomain.NewCurationAction(newInput)
		}
		if actionErr != nil {
			return actionErr
		}
		inserted, insertErr := repository.InsertCurationAction(
			txContext,
			action,
		)
		if insertErr != nil {
			return insertErr
		}
		if inserted {
			result = RecordCurationActionResult{Action: action}
			return nil
		}

		// ON CONFLICT handles the race where another request inserted the same
		// action ID after the first lookup.
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
		replayed, replayErr := replayCurationAction(existing, requestHash)
		if replayErr != nil {
			return replayErr
		}
		result = replayed
		return nil
	})
	return result, err
}

func ensureNoActiveCurationWork(
	ctx context.Context,
	workRepository CurationForegroundWorkRepository,
	userID, curationID string,
) error {
	if workRepository == nil {
		return nil
	}
	active, err := workRepository.HasActiveCurationWork(
		ctx,
		userID,
		curationID,
	)
	if err != nil {
		return err
	}
	if active {
		return curationdomain.ErrCurationActionInProgress
	}
	return nil
}

func (s *Service) ListCurationActions(
	ctx context.Context,
	userID, curationID string,
	limit int,
) ([]curationdomain.CurationAction, error) {
	repository, err := s.curationActionRepository()
	if err != nil {
		return nil, err
	}
	if _, err := repository.GetCuration(
		ctx,
		userID,
		curationID,
		false,
	); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > maxCurationActionListLimit {
		limit = maxCurationActionListLimit
	}
	return repository.ListCurationActions(
		ctx,
		userID,
		curationID,
		limit,
	)
}

func (s *Service) curationActionRepository() (
	CurationActionRepository,
	error,
) {
	repository, ok := s.repository.(CurationActionRepository)
	if !ok {
		return nil, fmt.Errorf("curation action repository is unavailable")
	}
	return repository, nil
}

func actionPhaseAtRequest(
	curation curationdomain.Curation,
	actionType curationdomain.CurationActionType,
) (curationdomain.CurationActionPhase, error) {
	if actionType == curationdomain.CurationActionIntentNextStep {
		if curation.Phase != curationdomain.CurationPhasePlanning ||
			curation.Version != 1 {
			return "", fmt.Errorf(
				"%w: %s at %s v%d",
				curationdomain.ErrCurationActionUnavailable,
				actionType,
				curation.Phase,
				curation.Version,
			)
		}
		return curationdomain.CurationActionPhaseHavingIntent, nil
	}
	return persistedCurationActionPhase(curation.Phase)
}

func persistedCurationActionPhase(
	phase curationdomain.CurationPhase,
) (curationdomain.CurationActionPhase, error) {
	switch phase {
	case curationdomain.CurationPhasePlanning:
		return curationdomain.CurationActionPhasePlanning, nil
	case curationdomain.CurationPhaseCurating:
		return curationdomain.CurationActionPhaseCurating, nil
	default:
		return "", fmt.Errorf(
			"%w: %q",
			curationdomain.ErrCurationActionPhaseInvalid,
			phase,
		)
	}
}

func replayCurationAction(
	action curationdomain.CurationAction,
	requestHash curationdomain.CurationActionRequestHash,
) (RecordCurationActionResult, error) {
	if action.RequestHash != requestHash {
		return RecordCurationActionResult{}, curationdomain.ErrIdempotencyKeyReused
	}
	return RecordCurationActionResult{Action: action, Replay: true}, nil
}

func hashCurationActionRecord(
	input RecordCurationActionInput,
) (curationdomain.CurationActionRequestHash, error) {
	canonical := struct {
		CurationID              string
		Type                    curationdomain.CurationActionType
		SubjectType             curationdomain.CurationActionSubjectType
		SubjectID               *string
		Body                    string
		ExpectedCurationVersion int64
		SourceRefType           curationdomain.CurationActionSourceRefType
		SourceRefID             string
	}{
		CurationID: input.CurationID, Type: input.Type,
		SubjectType: input.SubjectType, SubjectID: input.SubjectID,
		Body:                    input.Body,
		ExpectedCurationVersion: input.ExpectedCurationVersion,
		SourceRefType:           input.SourceRefType, SourceRefID: input.SourceRefID,
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return curationdomain.CurationActionRequestHash{},
			fmt.Errorf("hash curation action request: %w", err)
	}
	sum := sha256.Sum256(payload)
	return curationdomain.CurationActionRequestHash(sum), nil
}
