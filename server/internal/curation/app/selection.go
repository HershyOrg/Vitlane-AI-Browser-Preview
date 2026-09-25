package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

var selectionCommandIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// ResolvedSelectionConfiguration is authoritative lineage read from the
// CandidateConfiguration and its owning Curation graph. Clients provide only
// the configuration ID and cannot supply or derive a Target ID.
type ResolvedSelectionConfiguration struct {
	CurationID        string
	PlanTargetID      string
	SessionID         string
	CandidateID       string
	ConfigurationID   string
	ConfigurationHash string
}

type SelectionCommand struct {
	ID               string
	UserID           string
	CurationID       string
	SelectionID      string
	ClientCommandID  string
	CommandKind      string
	RequestHash      string
	ResponseSnapshot json.RawMessage
	CreatedAt        time.Time
}

type SelectionMutation struct {
	Selection       curationdomain.CurationSelection
	ExpectedVersion int64
	Command         SelectionCommand
}

type SelectionReference struct {
	SelectionID     string `json:"selectionId"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

type SelectionMutationActionInput struct {
	ActionID                string
	UserID                  string
	CurationID              string
	SelectionID             string
	Body                    string
	ExpectedCurationVersion int64
}

// SelectionMutationActionRecorder records the PATCH_ONLY CurationAction in the
// same ambient transaction as the Selection owner mutation.
type SelectionMutationActionRecorder interface {
	RecordSelectionMutationAction(
		context.Context,
		SelectionMutationActionInput,
	) error
}

func (s *Service) RecordSelectionMutationAction(
	ctx context.Context,
	input SelectionMutationActionInput,
) error {
	subjectID := input.SelectionID
	_, err := s.RecordOwnedPatchCurationAction(
		ctx,
		RecordCurationActionInput{
			ActionID:                input.ActionID,
			UserID:                  input.UserID,
			CurationID:              input.CurationID,
			Type:                    curationdomain.CurationActionSelectionMutation,
			SubjectType:             curationdomain.CurationActionSubjectSelection,
			SubjectID:               &subjectID,
			Body:                    input.Body,
			ExpectedCurationVersion: input.ExpectedCurationVersion,
			SourceRefType:           curationdomain.CurationActionSourceSelectionCommand,
			SourceRefID:             input.ActionID,
		},
	)
	return err
}

type SelectionSnapshot struct {
	Selection    curationdomain.CurationSelection
	SnapshotHash string
}

type SelectionSnapshotResult struct {
	Reference  SelectionReference
	Snapshot   *SelectionSnapshot
	ReasonCode string
	Retryable  bool
}

type SelectionRepository interface {
	GetCartView(
		context.Context,
		string,
		string,
	) (curationdomain.CartView, error)
	GetSelection(
		context.Context,
		string,
		string,
		string,
		bool,
	) (curationdomain.CurationSelection, error)
	FindSelectionCommand(
		context.Context,
		string,
		string,
	) (SelectionCommand, bool, error)
	ResolveSelectionConfiguration(
		context.Context,
		string,
		string,
		string,
	) (ResolvedSelectionConfiguration, error)
	CreateSelection(context.Context, SelectionMutation) error
	UpdateSelection(context.Context, SelectionMutation) error
	RemoveSelection(context.Context, SelectionMutation) error
	LockSelectionSnapshots(
		context.Context,
		string,
		string,
		[]SelectionReference,
	) ([]SelectionSnapshotResult, error)
}

type CreateSelectionInput struct {
	UserID                  string
	CurationID              string
	ConfigurationID         string
	Quantity                int64
	ClientCommandID         string
	ExpectedCurationVersion int64
}

type UpdateSelectionInput struct {
	UserID                  string
	CurationID              string
	SelectionID             string
	ExpectedVersion         int64
	Quantity                int64
	ConfigurationID         string
	ClientCommandID         string
	ExpectedCurationVersion int64
}

type RemoveSelectionInput struct {
	UserID                  string
	CurationID              string
	SelectionID             string
	ExpectedVersion         int64
	ClientCommandID         string
	ExpectedCurationVersion int64
}

type SelectionCommandResult struct {
	Selection curationdomain.CurationSelection `json:"selection"`
	Replay    bool                             `json:"replay"`
}

type SelectionService struct {
	repository       any
	transactor       sharedapp.Transactor
	clock            sharedapp.Clock
	ids              sharedapp.IDGenerator
	selectionActions SelectionMutationActionRecorder
}

func NewSelectionService(
	repository any,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
) *SelectionService {
	return &SelectionService{repository: repository, clock: clock, ids: ids}
}

func (s *SelectionService) selectionRepository() (SelectionRepository, error) {
	repository, ok := s.repository.(SelectionRepository)
	if !ok {
		return nil, curationdomain.ErrCurationNotFound
	}
	return repository, nil
}

func (s *SelectionService) EnableTransactor(transactor sharedapp.Transactor) {
	s.transactor = transactor
}

func (s *SelectionService) EnableSelectionMutationActions(
	recorder SelectionMutationActionRecorder,
) {
	s.selectionActions = recorder
}

func (s *SelectionService) GetCurationCart(
	ctx context.Context,
	userID, curationID string,
) (curationdomain.CartView, error) {
	repository, err := s.selectionRepository()
	if err != nil {
		return curationdomain.CartView{}, err
	}
	userID = strings.TrimSpace(userID)
	curationID = strings.TrimSpace(curationID)
	if userID == "" || curationID == "" {
		return curationdomain.CartView{}, curationdomain.ErrCurationNotFound
	}
	return repository.GetCartView(ctx, userID, curationID)
}

func (s *SelectionService) CreateSelection(
	ctx context.Context,
	input CreateSelectionInput,
) (SelectionCommandResult, error) {
	repository, err := s.selectionRepository()
	if err != nil || s.transactor == nil || s.selectionActions == nil {
		return SelectionCommandResult{}, curationdomain.ErrSelectionInvalid
	}
	input.UserID = strings.TrimSpace(input.UserID)
	input.CurationID = strings.TrimSpace(input.CurationID)
	input.ConfigurationID = strings.TrimSpace(input.ConfigurationID)
	input.ClientCommandID = strings.TrimSpace(input.ClientCommandID)
	if input.UserID == "" || input.CurationID == "" ||
		input.ConfigurationID == "" ||
		input.Quantity < 1 || input.Quantity > 99 ||
		input.ExpectedCurationVersion < 1 ||
		!selectionCommandIDPattern.MatchString(input.ClientCommandID) {
		return SelectionCommandResult{}, curationdomain.ErrSelectionInvalid
	}
	requestHash, err := selectionRequestHash(struct {
		ClientCommandID          string `json:"clientCommandId"`
		CandidateConfigurationID string `json:"candidateConfigurationId"`
		CurationID               string `json:"curationId"`
		ExpectedCurationVersion  int64  `json:"expectedCurationVersion"`
		Quantity                 int64  `json:"quantity"`
	}{
		ClientCommandID:          input.ClientCommandID,
		CandidateConfigurationID: input.ConfigurationID,
		CurationID:               input.CurationID,
		ExpectedCurationVersion:  input.ExpectedCurationVersion,
		Quantity:                 input.Quantity,
	})
	if err != nil {
		return SelectionCommandResult{}, err
	}
	if replay, found, replayErr := replaySelectionCommand(
		ctx, repository, input.UserID, input.ClientCommandID, requestHash,
	); replayErr != nil || found {
		return SelectionCommandResult{Selection: replay, Replay: found}, replayErr
	}

	var result SelectionCommandResult
	err = s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		if replayed, found, replayErr := replaySelectionCommand(
			txContext,
			repository,
			input.UserID,
			input.ClientCommandID,
			requestHash,
		); replayErr != nil {
			return replayErr
		} else if found {
			result = SelectionCommandResult{
				Selection: replayed,
				Replay:    true,
			}
			return nil
		}
		selectionID := s.ids.NewID()
		actionBody, marshalErr := json.Marshal(struct {
			CandidateConfigurationID string `json:"candidateConfigurationId"`
			Operation                string `json:"operation"`
			Quantity                 int64  `json:"quantity"`
		}{
			CandidateConfigurationID: input.ConfigurationID,
			Operation:                "CREATE",
			Quantity:                 input.Quantity,
		})
		if marshalErr != nil {
			return marshalErr
		}
		if actionErr := s.selectionActions.RecordSelectionMutationAction(
			txContext,
			SelectionMutationActionInput{
				ActionID: input.ClientCommandID, UserID: input.UserID,
				CurationID: input.CurationID, SelectionID: selectionID,
				Body:                    string(actionBody),
				ExpectedCurationVersion: input.ExpectedCurationVersion,
			},
		); actionErr != nil {
			return actionErr
		}
		resolved, resolveErr := repository.ResolveSelectionConfiguration(
			txContext, input.UserID, input.CurationID, input.ConfigurationID,
		)
		if resolveErr != nil {
			return resolveErr
		}
		if !validResolvedConfiguration(
			resolved, input.CurationID, input.ConfigurationID,
		) {
			return curationdomain.ErrSelectionInvalid
		}
		now := s.clock.Now()
		selection, createErr := curationdomain.NewCurationSelection(
			selectionID, input.UserID, resolved.CurationID,
			resolved.PlanTargetID, resolved.SessionID, resolved.CandidateID,
			resolved.ConfigurationID, resolved.ConfigurationHash,
			input.Quantity, now,
		)
		if createErr != nil {
			return createErr
		}
		response, marshalErr := json.Marshal(selection)
		if marshalErr != nil {
			return marshalErr
		}
		if createErr := repository.CreateSelection(
			txContext,
			SelectionMutation{
				Selection: selection,
				Command: SelectionCommand{
					ID:               s.ids.NewID(),
					UserID:           input.UserID,
					CurationID:       input.CurationID,
					SelectionID:      selection.ID,
					ClientCommandID:  input.ClientCommandID,
					CommandKind:      "CREATE",
					RequestHash:      requestHash,
					ResponseSnapshot: response,
					CreatedAt:        now,
				},
			},
		); createErr != nil {
			return createErr
		}
		result = SelectionCommandResult{Selection: selection}
		return nil
	})
	if err != nil {
		if replay, found, replayErr := replaySelectionCommand(
			ctx, repository, input.UserID, input.ClientCommandID, requestHash,
		); replayErr != nil || found {
			return SelectionCommandResult{
				Selection: replay,
				Replay:    found,
			}, replayErr
		}
		return SelectionCommandResult{}, err
	}
	return result, nil
}

func (s *SelectionService) UpdateSelection(
	ctx context.Context,
	input UpdateSelectionInput,
) (SelectionCommandResult, error) {
	repository, err := s.selectionRepository()
	if err != nil || s.transactor == nil || s.selectionActions == nil {
		return SelectionCommandResult{}, curationdomain.ErrSelectionInvalid
	}
	input.UserID = strings.TrimSpace(input.UserID)
	input.CurationID = strings.TrimSpace(input.CurationID)
	input.SelectionID = strings.TrimSpace(input.SelectionID)
	input.ConfigurationID = strings.TrimSpace(input.ConfigurationID)
	input.ClientCommandID = strings.TrimSpace(input.ClientCommandID)
	if input.UserID == "" || input.CurationID == "" || input.SelectionID == "" ||
		input.ConfigurationID == "" || input.Quantity < 1 || input.Quantity > 99 ||
		input.ExpectedVersion < 1 ||
		input.ExpectedCurationVersion < 1 ||
		!selectionCommandIDPattern.MatchString(input.ClientCommandID) {
		return SelectionCommandResult{}, curationdomain.ErrSelectionInvalid
	}
	requestHash, err := selectionRequestHash(struct {
		CurationID              string `json:"curationId"`
		SelectionID             string `json:"selectionId"`
		ExpectedVersion         int64  `json:"expectedVersion"`
		ExpectedCurationVersion int64  `json:"expectedCurationVersion"`
		Quantity                int64  `json:"quantity"`
		ClientCommandID         string `json:"clientCommandId"`
		ConfigurationID         string `json:"candidateConfigurationId"`
	}{
		CurationID:              input.CurationID,
		SelectionID:             input.SelectionID,
		ExpectedVersion:         input.ExpectedVersion,
		ExpectedCurationVersion: input.ExpectedCurationVersion,
		Quantity:                input.Quantity,
		ClientCommandID:         input.ClientCommandID,
		ConfigurationID:         input.ConfigurationID,
	})
	if err != nil {
		return SelectionCommandResult{}, err
	}
	if replay, found, replayErr := replaySelectionCommand(
		ctx, repository, input.UserID, input.ClientCommandID, requestHash,
	); replayErr != nil || found {
		return SelectionCommandResult{Selection: replay, Replay: found}, replayErr
	}

	var result curationdomain.CurationSelection
	replayedWithinTransaction := false
	err = s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		if replayed, found, replayErr := replaySelectionCommand(
			txContext,
			repository,
			input.UserID,
			input.ClientCommandID,
			requestHash,
		); replayErr != nil {
			return replayErr
		} else if found {
			result = replayed
			replayedWithinTransaction = true
			return nil
		}
		actionBody, marshalErr := json.Marshal(struct {
			CandidateConfigurationID string `json:"candidateConfigurationId"`
			ExpectedSelectionVersion int64  `json:"expectedSelectionVersion"`
			Operation                string `json:"operation"`
			Quantity                 int64  `json:"quantity"`
		}{
			CandidateConfigurationID: input.ConfigurationID,
			ExpectedSelectionVersion: input.ExpectedVersion,
			Operation:                "UPDATE",
			Quantity:                 input.Quantity,
		})
		if marshalErr != nil {
			return marshalErr
		}
		if actionErr := s.selectionActions.RecordSelectionMutationAction(
			txContext,
			SelectionMutationActionInput{
				ActionID: input.ClientCommandID, UserID: input.UserID,
				CurationID:              input.CurationID,
				SelectionID:             input.SelectionID,
				Body:                    string(actionBody),
				ExpectedCurationVersion: input.ExpectedCurationVersion,
			},
		); actionErr != nil {
			return actionErr
		}
		current, getErr := repository.GetSelection(
			txContext, input.UserID, input.CurationID, input.SelectionID, true,
		)
		if getErr != nil {
			return getErr
		}
		resolved, resolveErr := repository.ResolveSelectionConfiguration(
			txContext, input.UserID, input.CurationID, input.ConfigurationID,
		)
		if resolveErr != nil {
			return resolveErr
		}
		if !validResolvedConfiguration(
			resolved, input.CurationID, input.ConfigurationID,
		) ||
			resolved.PlanTargetID != current.PlanTargetID ||
			resolved.SessionID != current.ShoppingSessionID ||
			resolved.CandidateID != current.CandidateID {
			return curationdomain.ErrSelectionInvalid
		}
		next := current
		if reviseErr := next.Revise(
			resolved.ConfigurationID,
			resolved.ConfigurationHash,
			input.Quantity,
			input.ExpectedVersion,
			s.clock.Now(),
		); reviseErr != nil {
			return reviseErr
		}
		response, marshalErr := json.Marshal(next)
		if marshalErr != nil {
			return marshalErr
		}
		if updateErr := repository.UpdateSelection(
			txContext,
			SelectionMutation{
				Selection:       next,
				ExpectedVersion: input.ExpectedVersion,
				Command: SelectionCommand{
					ID:               s.ids.NewID(),
					UserID:           input.UserID,
					CurationID:       input.CurationID,
					SelectionID:      input.SelectionID,
					ClientCommandID:  input.ClientCommandID,
					CommandKind:      "UPDATE",
					RequestHash:      requestHash,
					ResponseSnapshot: response,
					CreatedAt:        next.UpdatedAt,
				},
			},
		); updateErr != nil {
			return updateErr
		}
		result = next
		return nil
	})
	if err != nil {
		if replay, found, replayErr := replaySelectionCommand(
			ctx, repository, input.UserID, input.ClientCommandID, requestHash,
		); replayErr != nil || found {
			return SelectionCommandResult{
				Selection: replay,
				Replay:    found,
			}, replayErr
		}
		return SelectionCommandResult{}, err
	}
	return SelectionCommandResult{
		Selection: result,
		Replay:    replayedWithinTransaction,
	}, nil
}

func (s *SelectionService) RemoveSelection(
	ctx context.Context,
	input RemoveSelectionInput,
) (SelectionCommandResult, error) {
	repository, err := s.selectionRepository()
	if err != nil || s.transactor == nil || s.selectionActions == nil {
		return SelectionCommandResult{}, curationdomain.ErrSelectionInvalid
	}
	input.UserID = strings.TrimSpace(input.UserID)
	input.CurationID = strings.TrimSpace(input.CurationID)
	input.SelectionID = strings.TrimSpace(input.SelectionID)
	input.ClientCommandID = strings.TrimSpace(input.ClientCommandID)
	if input.UserID == "" || input.CurationID == "" || input.SelectionID == "" ||
		input.ExpectedVersion < 1 ||
		input.ExpectedCurationVersion < 1 ||
		!selectionCommandIDPattern.MatchString(input.ClientCommandID) {
		return SelectionCommandResult{}, curationdomain.ErrSelectionInvalid
	}
	requestHash, err := selectionRequestHash(struct {
		ClientCommandID         string `json:"clientCommandId"`
		CurationID              string `json:"curationId"`
		ExpectedCurationVersion int64  `json:"expectedCurationVersion"`
		ExpectedVersion         int64  `json:"expectedVersion"`
		SelectionID             string `json:"selectionId"`
	}{
		ClientCommandID:         input.ClientCommandID,
		CurationID:              input.CurationID,
		ExpectedCurationVersion: input.ExpectedCurationVersion,
		ExpectedVersion:         input.ExpectedVersion,
		SelectionID:             input.SelectionID,
	})
	if err != nil {
		return SelectionCommandResult{}, err
	}
	if replay, found, replayErr := replaySelectionCommand(
		ctx, repository, input.UserID, input.ClientCommandID, requestHash,
	); replayErr != nil || found {
		return SelectionCommandResult{Selection: replay, Replay: found}, replayErr
	}

	var result curationdomain.CurationSelection
	replayedWithinTransaction := false
	err = s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		if replayed, found, replayErr := replaySelectionCommand(
			txContext,
			repository,
			input.UserID,
			input.ClientCommandID,
			requestHash,
		); replayErr != nil {
			return replayErr
		} else if found {
			result = replayed
			replayedWithinTransaction = true
			return nil
		}
		actionBody, marshalErr := json.Marshal(struct {
			ExpectedSelectionVersion int64  `json:"expectedSelectionVersion"`
			Operation                string `json:"operation"`
		}{
			ExpectedSelectionVersion: input.ExpectedVersion,
			Operation:                "REMOVE",
		})
		if marshalErr != nil {
			return marshalErr
		}
		if actionErr := s.selectionActions.RecordSelectionMutationAction(
			txContext,
			SelectionMutationActionInput{
				ActionID: input.ClientCommandID, UserID: input.UserID,
				CurationID:              input.CurationID,
				SelectionID:             input.SelectionID,
				Body:                    string(actionBody),
				ExpectedCurationVersion: input.ExpectedCurationVersion,
			},
		); actionErr != nil {
			return actionErr
		}
		current, getErr := repository.GetSelection(
			txContext, input.UserID, input.CurationID, input.SelectionID, true,
		)
		if getErr != nil {
			return getErr
		}
		if removeErr := current.Remove(
			input.ExpectedVersion,
			s.clock.Now(),
		); removeErr != nil {
			return removeErr
		}
		response, marshalErr := json.Marshal(current)
		if marshalErr != nil {
			return marshalErr
		}
		if removeErr := repository.RemoveSelection(
			txContext,
			SelectionMutation{
				Selection:       current,
				ExpectedVersion: input.ExpectedVersion,
				Command: SelectionCommand{
					ID:               s.ids.NewID(),
					UserID:           input.UserID,
					CurationID:       input.CurationID,
					SelectionID:      input.SelectionID,
					ClientCommandID:  input.ClientCommandID,
					CommandKind:      "REMOVE",
					RequestHash:      requestHash,
					ResponseSnapshot: response,
					CreatedAt:        current.UpdatedAt,
				},
			},
		); removeErr != nil {
			return removeErr
		}
		result = current
		return nil
	})
	if err != nil {
		if replay, found, replayErr := replaySelectionCommand(
			ctx, repository, input.UserID, input.ClientCommandID, requestHash,
		); replayErr != nil || found {
			return SelectionCommandResult{
				Selection: replay,
				Replay:    found,
			}, replayErr
		}
		return SelectionCommandResult{}, err
	}
	return SelectionCommandResult{
		Selection: result,
		Replay:    replayedWithinTransaction,
	}, nil
}

func selectionRequestHash(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:]), nil
}

func (s *SelectionService) LockCurationSelectionSnapshots(
	ctx context.Context,
	userID, curationID string,
	references []SelectionReference,
) ([]SelectionSnapshotResult, error) {
	repository, err := s.selectionRepository()
	if err != nil {
		return nil, err
	}
	return repository.LockSelectionSnapshots(
		ctx,
		strings.TrimSpace(userID),
		strings.TrimSpace(curationID),
		references,
	)
}

func replaySelectionCommand(
	ctx context.Context,
	repository SelectionRepository,
	userID, clientCommandID, requestHash string,
) (curationdomain.CurationSelection, bool, error) {
	command, found, err := repository.FindSelectionCommand(
		ctx, userID, clientCommandID,
	)
	if err != nil || !found {
		return curationdomain.CurationSelection{}, false, err
	}
	if command.RequestHash != requestHash {
		return curationdomain.CurationSelection{}, false,
			curationdomain.ErrSelectionCommandConflict
	}
	var selection curationdomain.CurationSelection
	if err := json.Unmarshal(command.ResponseSnapshot, &selection); err != nil {
		return curationdomain.CurationSelection{}, false, err
	}
	return selection, true, nil
}

func validResolvedConfiguration(
	resolved ResolvedSelectionConfiguration,
	curationID, configurationID string,
) bool {
	return resolved.CurationID == curationID &&
		resolved.ConfigurationID == configurationID &&
		strings.TrimSpace(resolved.PlanTargetID) != "" &&
		strings.TrimSpace(resolved.SessionID) != "" &&
		strings.TrimSpace(resolved.CandidateID) != "" &&
		strings.TrimSpace(resolved.ConfigurationHash) != ""
}
