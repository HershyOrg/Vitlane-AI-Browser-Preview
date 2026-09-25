package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	browserdomain "github.com/vitlane/vitlane/server/internal/browserrun/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

const SchemaVersion = "vitlane.browser-run.v1"

type CandidateResolver interface {
	ResolveCandidate(
		ctx context.Context,
		userID, curationID, candidateID string,
	) (browserdomain.CandidateReference, error)
}

type Repository interface {
	CandidateResolver
	FindCommand(
		ctx context.Context,
		userID, commandType, idempotencyKey string,
	) (CommandRecord, bool, error)
	InsertRun(ctx context.Context, run browserdomain.Run) error
	GetRun(
		ctx context.Context,
		userID, runID string,
		forUpdate bool,
	) (browserdomain.Run, error)
	UpdateRun(
		ctx context.Context,
		run browserdomain.Run,
		expectedVersion int64,
	) error
	AppendEvent(ctx context.Context, event Event) error
	InsertCommand(ctx context.Context, command CommandRecord) error
}

type CommandRecord struct {
	UserID          string
	RunID           string
	CommandType     string
	IdempotencyKey  string
	RequestHash     string
	ResponseVersion int64
	CreatedAt       time.Time
}

type Event struct {
	ID        string
	UserID    string
	RunID     string
	Sequence  int64
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type Result struct {
	Run    browserdomain.Run
	Replay bool
}

type CreateInput struct {
	UserID         string
	CurationID     string
	CandidateID    string
	IdempotencyKey string
}

type CommandInput struct {
	UserID          string
	RunID           string
	ExpectedVersion int64
	IdempotencyKey  string
}

type PreparationApprovalInput struct {
	CommandInput
	PlanRevision            int64
	QuoteDigest             string
	AllowedPreparationSteps []browserdomain.PreparationStep
	PriceCeilingMinor       int64
	PriceCurrency           string
}

type ObservationInput struct {
	CommandInput
	ObservationRevision   int64
	Kind                  browserdomain.ObservationKind
	Origin                string
	PageIdentityDigest    string
	SessionStateHint      browserdomain.SessionStateHint
	Sanitized             bool
	ContainsSensitiveData bool
}

type HandoffInput struct {
	CommandInput
	Reason browserdomain.HandoffReason
}

type ResultVerificationInput struct {
	CommandInput
	Outcome             browserdomain.ResultOutcome
	EvidenceSource      browserdomain.ResultEvidenceSource
	EvidenceDigest      string
	ObservationRevision int64
}

type Service struct {
	repository Repository
	transactor sharedapp.Transactor
	clock      sharedapp.Clock
	ids        sharedapp.IDGenerator
}

func NewService(
	repository Repository,
	transactor sharedapp.Transactor,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
) *Service {
	return &Service{
		repository: repository, transactor: transactor, clock: clock, ids: ids,
	}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Result, error) {
	if err := validateIdentity(input.UserID, input.CurationID, input.CandidateID); err != nil {
		return Result{}, err
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return Result{}, err
	}
	requestHash, err := requestHash("CREATE", input.UserID, "", 0, struct {
		CurationID  string `json:"curationId"`
		CandidateID string `json:"candidateId"`
	}{input.CurationID, input.CandidateID})
	if err != nil {
		return Result{}, err
	}
	var result Result
	err = s.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		replay, replayErr := s.replay(tx, input.UserID, "CREATE", input.IdempotencyKey, requestHash)
		if replayErr != nil {
			return replayErr
		}
		if replay != nil {
			result = *replay
			return nil
		}
		candidate, resolveErr := s.repository.ResolveCandidate(
			tx, input.UserID, input.CurationID, input.CandidateID,
		)
		if resolveErr != nil {
			return resolveErr
		}
		now := s.clock.Now().UTC()
		run, runErr := browserdomain.NewRun(s.ids.NewID(), candidate, now)
		if runErr != nil {
			return runErr
		}
		if insertErr := s.repository.InsertRun(tx, run); insertErr != nil {
			return insertErr
		}
		if eventErr := s.repository.AppendEvent(tx, Event{
			ID: s.ids.NewID(), UserID: input.UserID, RunID: run.ID,
			Sequence: run.Version, Type: "RUN_CREATED",
			Payload: mustJSON(map[string]string{
				"curationId": input.CurationID, "candidateId": input.CandidateID,
				"merchantOrigin": run.MerchantOrigin,
			}),
			CreatedAt: now,
		}); eventErr != nil {
			return eventErr
		}
		if commandErr := s.repository.InsertCommand(tx, CommandRecord{
			UserID: input.UserID, RunID: run.ID, CommandType: "CREATE",
			IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash,
			ResponseVersion: run.Version, CreatedAt: now,
		}); commandErr != nil {
			return commandErr
		}
		result.Run = run
		return nil
	})
	return result, err
}

func (s *Service) Get(ctx context.Context, userID, runID string) (browserdomain.Run, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(runID) == "" {
		return browserdomain.Run{}, browserdomain.ErrInvalid
	}
	return s.repository.GetRun(ctx, userID, runID, false)
}

func (s *Service) ApproveNavigation(ctx context.Context, input CommandInput) (Result, error) {
	return s.mutate(ctx, "APPROVE_NAVIGATION", "NAVIGATION_APPROVED", input, struct{}{},
		func(run *browserdomain.Run, now time.Time) error { return run.ApproveNavigation(now) })
}

func (s *Service) ApprovePreparation(
	ctx context.Context,
	input PreparationApprovalInput,
) (Result, error) {
	payload := struct {
		PlanRevision            int64                           `json:"planRevision"`
		QuoteDigest             string                          `json:"quoteDigest"`
		AllowedPreparationSteps []browserdomain.PreparationStep `json:"allowedPreparationSteps"`
		PriceCeilingMinor       int64                           `json:"priceCeilingMinor"`
		PriceCurrency           string                          `json:"priceCurrency"`
	}{
		input.PlanRevision, input.QuoteDigest,
		append([]browserdomain.PreparationStep(nil), input.AllowedPreparationSteps...),
		input.PriceCeilingMinor, input.PriceCurrency,
	}
	return s.mutate(ctx, "APPROVE_PREPARATION", "PREPARATION_APPROVED", input.CommandInput, payload,
		func(run *browserdomain.Run, now time.Time) error {
			return run.ApprovePreparation(browserdomain.PreparationApproval{
				PlanRevision: input.PlanRevision, QuoteDigest: input.QuoteDigest,
				AllowedPreparationSteps: append([]browserdomain.PreparationStep(nil), input.AllowedPreparationSteps...),
				PriceCeilingMinor:       input.PriceCeilingMinor, PriceCurrency: input.PriceCurrency,
			}, now)
		})
}

func (s *Service) RecordObservation(ctx context.Context, input ObservationInput) (Result, error) {
	payload := struct {
		Revision              int64                          `json:"revision"`
		Kind                  browserdomain.ObservationKind  `json:"kind"`
		Origin                string                         `json:"origin"`
		PageIdentityDigest    string                         `json:"pageIdentityDigest"`
		SessionStateHint      browserdomain.SessionStateHint `json:"sessionStateHint"`
		Sanitized             bool                           `json:"sanitized"`
		ContainsSensitiveData bool                           `json:"containsSensitiveData"`
	}{input.ObservationRevision, input.Kind, input.Origin, input.PageIdentityDigest,
		input.SessionStateHint,
		input.Sanitized, input.ContainsSensitiveData}
	return s.mutate(ctx, "RECORD_OBSERVATION", "OBSERVATION_RECORDED", input.CommandInput, payload,
		func(run *browserdomain.Run, now time.Time) error {
			return run.RecordObservation(browserdomain.Observation{
				Revision: input.ObservationRevision, Kind: input.Kind,
				Origin: input.Origin, PageIdentityDigest: input.PageIdentityDigest,
				SessionStateHint: input.SessionStateHint,
			}, input.Sanitized, input.ContainsSensitiveData, now)
		})
}

func (s *Service) RequireHandoff(ctx context.Context, input HandoffInput) (Result, error) {
	return s.mutate(ctx, "REQUIRE_HANDOFF", "HANDOFF_REQUIRED", input.CommandInput,
		struct {
			Reason browserdomain.HandoffReason `json:"reason"`
		}{input.Reason},
		func(run *browserdomain.Run, now time.Time) error { return run.RequireHandoff(input.Reason, now) })
}

func (s *Service) TakeOver(ctx context.Context, input CommandInput) (Result, error) {
	return s.mutate(ctx, "TAKE_OVER", "USER_TOOK_CONTROL", input, struct{}{},
		func(run *browserdomain.Run, now time.Time) error { return run.TakeOver(now) })
}

func (s *Service) Pause(ctx context.Context, input CommandInput) (Result, error) {
	return s.mutate(ctx, "PAUSE", "RUN_PAUSED", input, struct{}{},
		func(run *browserdomain.Run, now time.Time) error { return run.Pause(now) })
}

func (s *Service) RequestResume(ctx context.Context, input CommandInput) (Result, error) {
	return s.mutate(ctx, "REQUEST_RESUME", "RESUME_REQUESTED", input, struct{}{},
		func(run *browserdomain.Run, now time.Time) error { return run.RequestResume(now) })
}

func (s *Service) Cancel(ctx context.Context, input CommandInput) (Result, error) {
	return s.mutate(ctx, "CANCEL", "RUN_CANCELLED", input, struct{}{},
		func(run *browserdomain.Run, now time.Time) error { return run.Cancel(now) })
}

func (s *Service) VerifyResult(ctx context.Context, input ResultVerificationInput) (Result, error) {
	payload := struct {
		Outcome             browserdomain.ResultOutcome        `json:"outcome"`
		EvidenceSource      browserdomain.ResultEvidenceSource `json:"evidenceSource"`
		EvidenceDigest      string                             `json:"evidenceDigest,omitempty"`
		ObservationRevision int64                              `json:"observationRevision,omitempty"`
	}{input.Outcome, input.EvidenceSource, input.EvidenceDigest, input.ObservationRevision}
	return s.mutate(ctx, "VERIFY_RESULT", "RESULT_VERIFIED", input.CommandInput, payload,
		func(run *browserdomain.Run, now time.Time) error {
			return run.VerifyResult(browserdomain.ResultVerification{
				Outcome: input.Outcome, EvidenceSource: input.EvidenceSource,
				EvidenceDigest:      input.EvidenceDigest,
				ObservationRevision: input.ObservationRevision,
			}, now)
		})
}

func (s *Service) mutate(
	ctx context.Context,
	commandType, eventType string,
	input CommandInput,
	payload any,
	transition func(*browserdomain.Run, time.Time) error,
) (Result, error) {
	if strings.TrimSpace(input.UserID) == "" || strings.TrimSpace(input.RunID) == "" || input.ExpectedVersion < 1 {
		return Result{}, browserdomain.ErrInvalid
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return Result{}, err
	}
	requestHash, err := requestHash(commandType, input.UserID, input.RunID, input.ExpectedVersion, payload)
	if err != nil {
		return Result{}, err
	}
	var result Result
	err = s.transactor.WithinTransaction(ctx, func(tx context.Context) error {
		replay, replayErr := s.replay(tx, input.UserID, commandType, input.IdempotencyKey, requestHash)
		if replayErr != nil {
			return replayErr
		}
		if replay != nil {
			result = *replay
			return nil
		}
		run, getErr := s.repository.GetRun(tx, input.UserID, input.RunID, true)
		if getErr != nil {
			return getErr
		}
		if versionErr := run.CheckExpectedVersion(input.ExpectedVersion); versionErr != nil {
			return versionErr
		}
		expected := run.Version
		now := s.clock.Now().UTC()
		if transitionErr := transition(&run, now); transitionErr != nil {
			return transitionErr
		}
		if updateErr := s.repository.UpdateRun(tx, run, expected); updateErr != nil {
			return updateErr
		}
		if eventErr := s.repository.AppendEvent(tx, Event{
			ID: s.ids.NewID(), UserID: input.UserID, RunID: run.ID,
			Sequence: run.Version, Type: eventType, Payload: mustJSON(payload), CreatedAt: now,
		}); eventErr != nil {
			return eventErr
		}
		if commandErr := s.repository.InsertCommand(tx, CommandRecord{
			UserID: input.UserID, RunID: run.ID, CommandType: commandType,
			IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash,
			ResponseVersion: run.Version, CreatedAt: now,
		}); commandErr != nil {
			return commandErr
		}
		result.Run = run
		return nil
	})
	return result, err
}

func (s *Service) replay(
	ctx context.Context,
	userID, commandType, key, hash string,
) (*Result, error) {
	command, found, err := s.repository.FindCommand(ctx, userID, commandType, key)
	if err != nil || !found {
		return nil, err
	}
	if command.CommandType != commandType || command.RequestHash != hash {
		return nil, browserdomain.ErrIdempotencyConflict
	}
	run, err := s.repository.GetRun(ctx, userID, command.RunID, false)
	if err != nil {
		return nil, err
	}
	return &Result{Run: run, Replay: true}, nil
}

func requestHash(commandType, userID, runID string, expectedVersion int64, payload any) (string, error) {
	return shareddomain.CanonicalJSONHash(struct {
		SchemaVersion   string `json:"schemaVersion"`
		CommandType     string `json:"commandType"`
		UserID          string `json:"userId"`
		RunID           string `json:"runId,omitempty"`
		ExpectedVersion int64  `json:"expectedVersion,omitempty"`
		Payload         any    `json:"payload"`
	}{SchemaVersion, commandType, userID, runID, expectedVersion, payload})
}

func validateIdentity(values ...string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return browserdomain.ErrInvalid
		}
	}
	return nil
}

func validateIdempotencyKey(value string) error {
	length := len(strings.TrimSpace(value))
	if length < 8 || length > 200 {
		return fmt.Errorf("%w: idempotency key", browserdomain.ErrInvalid)
	}
	return nil
}

func mustJSON(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Errorf("marshal browser run event payload: %w", err))
	}
	return payload
}
