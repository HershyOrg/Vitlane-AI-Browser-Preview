package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	planningapp "github.com/vitlane/vitlane/server/internal/curation/planning/app"
	shoppingsessionapp "github.com/vitlane/vitlane/server/internal/curation/research/session/app"
	shoppingsessiondomain "github.com/vitlane/vitlane/server/internal/curation/research/session/domain"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
)

type Repository interface {
	CreateIdempotent(
		context.Context,
		curationdomain.PlanSnapshot,
		[]curationdomain.PlanTarget,
		*curationdomain.PlanningTask,
		string,
		[]byte,
	) (PlanRecord, bool, error)
	Get(context.Context, string, string, bool) (PlanRecord, error)
	InsertTargets(context.Context, []curationdomain.PlanTarget) error
	GetPlanningTask(context.Context, string, string, bool) (curationdomain.PlanningTask, error)
	ListPlanningTasks(context.Context, string, string, int) ([]curationdomain.PlanningTask, error)
	UpdatePlanningTask(context.Context, curationdomain.PlanningTask) error
	InsertProposal(context.Context, curationdomain.PlanningProposal) error
}

type CurationRepository interface {
	CreateExpansion(
		context.Context,
		curationdomain.CurationRun,
		curationdomain.PlanningTask,
	) (curationdomain.CurationRun, curationdomain.PlanningTask, bool, error)
	GetCurationRun(
		context.Context, string, string, string, bool,
	) (curationdomain.CurationRun, error)
	GetCurationRunByTask(
		context.Context, string, string, string, bool,
	) (curationdomain.CurationRun, error)
	FindOpenExpansion(
		context.Context, string, string,
	) (curationdomain.CurationRun, bool, error)
	GetExpansionByIdempotency(
		context.Context, string, string, bool,
	) (curationdomain.CurationRun, curationdomain.PlanningTask, error)
	UpdateCurationRun(context.Context, curationdomain.CurationRun) error
}

type CurationStateRepository interface {
	GetCuration(
		context.Context, string, string, bool,
	) (curationdomain.Curation, error)
	SaveCuration(
		context.Context, int64, curationdomain.Curation,
	) error
}

type ShoppingSessions interface {
	CreateReady(context.Context, shoppingsessionapp.CreateReadyInput) (shoppingsessiondomain.ShoppingSession, error)
	FindByTarget(context.Context, string, string) (shoppingsessiondomain.ShoppingSession, error)
	Get(context.Context, string, string) (shoppingsessiondomain.ShoppingSession, error)
}

// TargetSelectionRemover isolates the Selection aggregate from Target
// membership changes inside Curation. The implementation joins the caller's
// ambient transaction and terminally soft-removes every active Selection for
// the exact user/Curation/Target lineage.
type TargetSelectionRemover interface {
	RemoveActiveSelectionsForTarget(
		context.Context,
		string,
		string,
		string,
		time.Time,
	) error
}

type PlanRecord struct {
	Plan     curationdomain.PlanSnapshot `json:"plan"`
	Curation curationdomain.Curation     `json:"curation"`
	Targets  []curationdomain.PlanTarget `json:"targets"`
}

type Service struct {
	budgetEstimator    BudgetEstimator
	researchSelections ResearchSelectionRecorder
	repository         Repository
	sessions           ShoppingSessions
	transactor         sharedapp.Transactor
	clock              sharedapp.Clock
	ids                sharedapp.IDGenerator
	logger             *slog.Logger
	intelligenceWork   IntelligenceWorkCreator
	foregroundWork     CurationForegroundWorkRepository
	targetSelections   TargetSelectionRemover
}

func (s *Service) EnableCurationForegroundWork(
	work CurationForegroundWorkRepository,
) {
	s.foregroundWork = work
}

func NewService(
	repository Repository,
	sessions ShoppingSessions,
	transactor sharedapp.Transactor,
	clock sharedapp.Clock,
	ids sharedapp.IDGenerator,
	logger *slog.Logger,
) *Service {
	return &Service{
		repository: repository, sessions: sessions, transactor: transactor,
		clock: clock, ids: ids, logger: logger,
	}
}

func (s *Service) EnableTargetSelectionRemover(
	remover TargetSelectionRemover,
) {
	s.targetSelections = remover
}

type MoneyInput struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type CreatePlanInput struct {
	ControlMode    string
	BudgetRequest  *curationdomain.InitialBudgetRequest
	UserID         string
	AuthSessionID  string
	OriginalIntent string
	PlanningMode   string
	ExecutionMode  string
	TotalBudget    MoneyInput
	Country        string
	City           string
	Category       string
	AllowedItems   []string
	BlockedItems   []string
	MinPrice       *MoneyInput
	MaxPrice       *MoneyInput
	ReferenceURL   string
	URLMode        string
	AgentMode      string
	ModelKey       string
	IdempotencyKey string
}

type PlanResult struct {
	Plan             curationdomain.PlanSnapshot               `json:"plan"`
	Curation         curationdomain.Curation                   `json:"curation"`
	Targets          []curationdomain.PlanTarget               `json:"targets"`
	Sessions         []shoppingsessiondomain.ShoppingSession   `json:"sessions"`
	PlanningTask     *curationdomain.PlanningTask              `json:"planningTask,omitempty"`
	IntelligenceJob  *IntelligenceJobRef                       `json:"intelligenceJob,omitempty"`
	AvailableActions []curationdomain.CurationActionDescriptor `json:"availableActions"`
	Journey          PlanJourney                               `json:"journey"`
}

type CreateExpansionInput struct {
	UserID           string
	AuthSessionID    string
	PlanID           string
	CurationID       string
	CurationActionID string
	Instruction      string
	IdempotencyKey   string
}

type ExpansionResult struct {
	Run             curationdomain.CurationRun  `json:"run"`
	PlanningTask    curationdomain.PlanningTask `json:"planningTask"`
	IntelligenceJob *IntelligenceJobRef         `json:"intelligenceJob,omitempty"`
	Replay          bool                        `json:"replay"`
}

func (s *Service) CreateExpansion(
	ctx context.Context,
	input CreateExpansionInput,
) (ExpansionResult, error) {
	if !uuidPattern.MatchString(input.IdempotencyKey) {
		return ExpansionResult{}, curationdomain.ErrIdempotencyKeyRequired
	}
	if !uuidPattern.MatchString(input.CurationActionID) {
		return ExpansionResult{}, curationdomain.ErrCurationActionInvalid
	}
	instruction := strings.TrimSpace(input.Instruction)
	if instruction == "" {
		return ExpansionResult{}, curationdomain.ErrExpansionInstruction
	}
	repository, ok := s.repository.(CurationRepository)
	if !ok {
		return ExpansionResult{}, curationdomain.ErrExpansionUnavailable
	}
	var result ExpansionResult
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		existingRun, existingTask, existingErr := repository.GetExpansionByIdempotency(
			txContext, input.UserID, input.IdempotencyKey, true,
		)
		switch {
		case existingErr == nil:
			if string(existingRun.PlanID) != input.PlanID ||
				(existingRun.Kind != curationdomain.CurationRunInitial &&
					existingRun.Kind != curationdomain.CurationRunExpansion) ||
				existingRun.Instruction != instruction {
				return curationdomain.ErrIdempotencyKeyReused
			}
			result = ExpansionResult{
				Run: existingRun, PlanningTask: existingTask, Replay: true,
			}
			// The plan owns the agent mode, so a replay has to read it back
			// rather than assume the default and hand MANAGED work to an
			// external agent.
			existingPlan, planErr := s.repository.Get(
				txContext, input.UserID, input.PlanID, false,
			)
			if planErr != nil {
				return planErr
			}
			job, err := s.attachPlanningJob(
				txContext, input.UserID, string(existingPlan.Curation.ID),
				input.CurationActionID, input.PlanID,
				string(existingPlan.Plan.AgentMode), existingPlan.Plan.ModelKey,
				existingTask,
			)
			result.IntelligenceJob = job
			return err
		case !errors.Is(existingErr, curationdomain.ErrCurationRunNotFound):
			return existingErr
		}
		record, err := s.repository.Get(txContext, input.UserID, input.PlanID, true)
		if err != nil {
			return err
		}
		if input.CurationID != "" &&
			string(record.Curation.ID) != strings.TrimSpace(input.CurationID) {
			return curationdomain.ErrCurationNotFound
		}
		// ADR-0038: no new work enters the retired external Agent path. The
		// replay branch above still serves expansions stored before the
		// cutover.
		if record.Plan.AgentMode == curationdomain.AgentModeExternal {
			return curationdomain.ErrExternalAgentRetired
		}
		if err := s.guardActionConcurrency(
			txContext, input.UserID, input.PlanID,
		); err != nil {
			return err
		}
		latestTask, latestTaskErr := s.repository.GetPlanningTask(
			txContext, input.UserID, input.PlanID, true,
		)
		noPlanningHistory := errors.Is(
			latestTaskErr,
			curationdomain.ErrPlanningTaskNotFound,
		)
		if latestTaskErr != nil && !noPlanningHistory {
			return latestTaskErr
		}
		if latestTaskErr == nil &&
			latestTask.Status == curationdomain.PlanningTaskStatusRequested {
			return curationdomain.ErrExpansionInProgress
		}
		initial := record.Curation.Phase == curationdomain.CurationPhasePlanning &&
			record.Plan.PlanningMode == curationdomain.PlanningModeAuto &&
			len(record.Targets) == 0 &&
			noPlanningHistory
		expansion := !initial &&
			(record.Curation.Phase == curationdomain.CurationPhasePlanning ||
				record.Curation.Phase == curationdomain.CurationPhaseCurating)
		if (!initial && !expansion) || len(record.Targets) >= curationdomain.MaxPlanTargets {
			return curationdomain.ErrExpansionUnavailable
		}
		if initial {
			// A fresh AUTO Curation intentionally has no PlanningTask. The
			// first typed add action creates its INITIAL Task and Run.
		} else {
			_, found, err := repository.FindOpenExpansion(
				txContext, input.UserID, input.PlanID,
			)
			if err != nil {
				return err
			}
			if found {
				return curationdomain.ErrExpansionInProgress
			}
		}
		contextHash, requestHash, err := hashExpansionContext(
			record.Plan, record.Curation.Version, record.Targets, instruction,
		)
		if err != nil {
			return err
		}
		runKind := curationdomain.CurationRunExpansion
		if initial {
			runKind = curationdomain.CurationRunInitial
			contextHash, err = record.Plan.ContextHash()
			if err != nil {
				return err
			}
		}
		now := s.clock.Now()
		task := curationdomain.PlanningTask{
			ID:             curationdomain.PlanningTaskID(s.ids.NewID()),
			PlanID:         curationdomain.ShoppingPlanID(record.Plan.ID),
			UserID:         curationdomain.UserID(record.Plan.UserID),
			ContextVersion: record.Curation.Version,
			ContextHash:    contextHash,
			Status:         curationdomain.PlanningTaskStatusRequested,
			ExpiresAt:      now.Add(24 * time.Hour), CreatedAt: now, UpdatedAt: now,
		}
		run := curationdomain.CurationRun{
			ID:         curationdomain.CurationRunID(s.ids.NewID()),
			CurationID: record.Curation.ID,
			PlanID:     curationdomain.ShoppingPlanID(record.Plan.ID),
			UserID:     curationdomain.UserID(record.Plan.UserID),
			Kind:       runKind, Instruction: instruction,
			PlanningTaskID: task.ID, Status: curationdomain.CurationRunRequested,
			IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash,
			CreatedAt: now, UpdatedAt: now,
		}
		storedRun, storedTask, replay, err := repository.CreateExpansion(
			txContext, run, task,
		)
		if err != nil {
			return err
		}
		if !replay {
			if repo, ok := s.repository.(planLocaleRepository); ok {
				locale, err := s.ContentLocale(txContext, input.UserID)
				if err != nil {
					return err
				}
				// Without an account or browser language the Task keeps the
				// language its Plan recorded.
				if locale != "" {
					if err = repo.SavePlanContentLocale(txContext, input.UserID, string(storedTask.ID), locale); err != nil {
						return err
					}
				}
			}
		}
		result = ExpansionResult{
			Run: storedRun, PlanningTask: storedTask, Replay: replay,
		}
		job, err := s.attachPlanningJob(
			txContext, input.UserID, string(record.Curation.ID),
			input.CurationActionID, input.PlanID,
			string(record.Plan.AgentMode), record.Plan.ModelKey, storedTask,
		)
		result.IntelligenceJob = job
		return err
	})
	if err != nil {
		return ExpansionResult{}, err
	}
	s.logger.InfoContext(ctx, "curation expansion requested",
		"event", "curation.expansion.requested", "result", "success",
		"request_id", sharedapp.RequestID(ctx), "user_id", input.UserID,
		"plan_id", input.PlanID, "curation_run_id", result.Run.ID,
		"replay", result.Replay)
	return result, nil
}

func (s *Service) GetExpansion(
	ctx context.Context,
	userID, planID, runID string,
) (curationdomain.CurationRun, error) {
	repository, ok := s.repository.(CurationRepository)
	if !ok {
		return curationdomain.CurationRun{}, curationdomain.ErrExpansionUnavailable
	}
	var result curationdomain.CurationRun
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		if _, _, err := repository.FindOpenExpansion(
			txContext, userID, planID,
		); err != nil {
			return err
		}
		var err error
		result, err = repository.GetCurationRun(
			txContext, userID, planID, runID, false,
		)
		return err
	})
	return result, err
}

func (s *Service) GetCurrentExpansion(
	ctx context.Context,
	userID, planID string,
) (ExpansionResult, error) {
	repository, ok := s.repository.(CurationRepository)
	if !ok {
		return ExpansionResult{}, curationdomain.ErrExpansionUnavailable
	}
	var result ExpansionResult
	var foundCurrent bool
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		run, found, err := repository.FindOpenExpansion(
			txContext, userID, planID,
		)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		storedRun, task, err := repository.GetExpansionByIdempotency(
			txContext, userID, run.IdempotencyKey, false,
		)
		if err != nil {
			return err
		}
		if storedRun.ID != run.ID || storedRun.PlanningTaskID != task.ID {
			return curationdomain.ErrCurationRunNotFound
		}
		result = ExpansionResult{
			Run: storedRun, PlanningTask: task, Replay: true,
		}
		foundCurrent = true
		return nil
	})
	if err != nil {
		return ExpansionResult{}, err
	}
	if !foundCurrent {
		return ExpansionResult{}, curationdomain.ErrCurationRunNotFound
	}
	return result, nil
}

func (s *Service) CreatePlan(ctx context.Context, input CreatePlanInput) (PlanResult, error) {
	if !uuidPattern.MatchString(input.IdempotencyKey) {
		return PlanResult{}, curationdomain.ErrIdempotencyKeyRequired
	}
	if input.ControlMode != "" && input.ControlMode != "AUTO" && input.ControlMode != "MANUAL" {
		return PlanResult{}, curationdomain.ErrCurationActionInvalid
	}
	if input.ControlMode == "MANUAL" {
		input.PlanningMode = "SINGLE"
	}
	if strings.TrimSpace(input.PlanningMode) == "" {
		input.PlanningMode = string(curationdomain.PlanningModeAuto)
	}
	if input.BudgetRequest != nil {
		copy := *input.BudgetRequest
		if input.ControlMode == "AUTO" {
			copy.InputMode = "AUTO"
			copy.AllocationMode = "AUTO"
		}
		if input.ControlMode == "MANUAL" {
			copy.InputMode = "EXPLICIT"
			copy.AllocationMode = "EQUAL"
		}
		input.BudgetRequest = &copy
		if err := input.BudgetRequest.Validate(); err != nil {
			return PlanResult{}, err
		}
		amount := "0"
		if input.BudgetRequest.TotalAmount != nil {
			amount = *input.BudgetRequest.TotalAmount
		}
		input.TotalBudget = MoneyInput{Amount: amount, Currency: input.BudgetRequest.Currency}
		input.MinPrice, input.MaxPrice = nil, nil
	}
	budget, location, scope, err := parsePlanInputs(input)
	if err != nil {
		return PlanResult{}, err
	}
	now := s.clock.Now()
	planID := s.ids.NewID()
	plan, err := planningapp.NewShoppingPlan(planningapp.NewShoppingPlanInput{
		BudgetOptional: input.BudgetRequest != nil,
		PlanID:         planningapp.ShoppingPlanID(planID),
		UserID:         planningapp.UserID(input.UserID), OriginalIntent: input.OriginalIntent,
		PlanningMode:  planningapp.PlanningMode(input.PlanningMode),
		ExecutionMode: planningapp.ExecutionMode(input.ExecutionMode),
		TotalBudget:   budget, LocationContext: location,
		ResearchScope: toPlanningResearchScope(scope),
		AgentMode:     planningapp.AgentMode(input.AgentMode),
		ModelKey:      input.ModelKey,
		Now:           now,
	})
	if err != nil {
		return PlanResult{}, toCurationPlanError(err)
	}
	planView, targets, task, err := curationdomain.NewPlanSnapshot(curationdomain.NewPlanSnapshotInput{
		BudgetRequest: input.BudgetRequest,
		PlanID:        curationdomain.ShoppingPlanID(planID), TargetID: curationdomain.PlanTargetID(s.ids.NewID()),
		TaskID: curationdomain.PlanningTaskID(s.ids.NewID()),
		UserID: curationdomain.UserID(input.UserID), OriginalIntent: plan.OriginalIntent,
		PlanningMode:  curationdomain.PlanningMode(plan.PlanningMode),
		ExecutionMode: curationdomain.ExecutionMode(plan.ExecutionMode),
		TotalBudget:   plan.TotalBudget, LocationContext: plan.LocationContext,
		ResearchScope: toCurationResearchScope(plan.ResearchScope),
		AgentMode:     curationdomain.AgentMode(plan.AgentMode),
		ModelKey:      plan.ModelKey,
		Now:           now, TaskExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		return PlanResult{}, err
	}
	requestHash, err := hashCreateInput(input)
	if err != nil {
		return PlanResult{}, err
	}
	var record PlanRecord
	var duplicate bool
	var sessions []shoppingsessiondomain.ShoppingSession
	var storedPlanningTask *curationdomain.PlanningTask
	var intelligenceJob *IntelligenceJobRef
	if err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		var createErr error
		record, duplicate, createErr = s.repository.CreateIdempotent(
			txContext, planView, targets, task, input.IdempotencyKey, requestHash,
		)
		if createErr != nil {
			return createErr
		}
		// ADR-0038 closed the external Agent beta to new work. A replay of a
		// plan stored before the cutover still reads back; only a new
		// EXTERNAL plan is rejected, and the rollback leaves no row behind.
		if !duplicate &&
			record.Plan.AgentMode == curationdomain.AgentModeExternal {
			return curationdomain.ErrExternalAgentRetired
		}
		// A replay converges even while its own job is active. A new plan,
		// however, is an action and participates in the same per-user ceiling
		// as expansion and research commands.
		if !duplicate {
			if repo, ok := s.repository.(interface {
				InitializeControlMode(context.Context, string, string) error
			}); ok {
				mode := input.ControlMode
				if mode == "" {
					mode = "AUTO"
				}
				if err := repo.InitializeControlMode(txContext, string(record.Curation.ID), mode); err != nil {
					return err
				}
			}
			if repo, ok := s.repository.(planLocaleRepository); ok {
				locale, err := s.ContentLocale(txContext, input.UserID)
				if err != nil {
					return err
				}
				if locale == "" {
					locale = "en-US"
				}
				if err = repo.SavePlanContentLocale(txContext, input.UserID, string(record.Plan.ID), locale); err != nil {
					return err
				}
			}
			// Account country/display currency are saved by explicit preference
			// changes. A plan snapshot (including its budget denomination) must
			// not overwrite the account's latest selections.
			if admissionErr := s.guardActionConcurrency(
				txContext, input.UserID, string(record.Plan.ID),
			); admissionErr != nil {
				return admissionErr
			}
		}
		if _, actionErr := s.recordInitialIntentCurationAction(
			txContext,
			RecordCurationActionInput{
				ActionID:                input.IdempotencyKey,
				UserID:                  input.UserID,
				CurationID:              string(record.Curation.ID),
				Type:                    curationdomain.CurationActionIntentNextStep,
				SubjectType:             curationdomain.CurationActionSubjectIntent,
				Body:                    record.Plan.OriginalIntent,
				ExpectedCurationVersion: 1,
				SourceRefType:           curationdomain.CurationActionSourceShoppingPlan,
				SourceRefID:             string(record.Plan.ID),
			},
		); actionErr != nil {
			return actionErr
		}
		if task != nil {
			storedTask, taskErr := s.repository.GetPlanningTask(
				txContext, input.UserID, string(record.Plan.ID), false,
			)
			if taskErr != nil {
				return taskErr
			}
			storedPlanningTask = &storedTask
			intelligenceJob, taskErr = s.attachPlanningJob(
				txContext, input.UserID, string(record.Curation.ID),
				input.IdempotencyKey, string(record.Plan.ID),
				string(record.Plan.AgentMode), record.Plan.ModelKey, storedTask,
			)
			return taskErr
		}
		return nil
	}); err != nil {
		return PlanResult{}, err
	}
	result := PlanResult{
		Plan: record.Plan, Curation: record.Curation, Targets: record.Targets,
		Sessions: sessions, PlanningTask: storedPlanningTask,
		IntelligenceJob: intelligenceJob,
	}
	if result.Sessions == nil {
		result.Sessions = []shoppingsessiondomain.ShoppingSession{}
	}
	if duplicate {
		s.logger.InfoContext(ctx, "duplicate plan creation blocked",
			"event", "idempotency.duplicate_blocked", "result", "existing_returned",
			"request_id", sharedapp.RequestID(ctx), "user_id", input.UserID,
			"plan_id", record.Plan.ID)
	}
	s.logger.InfoContext(ctx, "plan created",
		"event", "plan.created", "result", "success",
		"request_id", sharedapp.RequestID(ctx), "user_id", input.UserID,
		"plan_id", record.Plan.ID, "planning_mode", record.Plan.PlanningMode,
		"target_count", len(record.Targets))
	if !duplicate {
		sharedapp.RecordAnalytics(ctx, sharedapp.AnalyticsEvent{Name: "curation_created", UserID: input.UserID, Key: string(record.Curation.ID), CurationID: string(record.Curation.ID)})
	}
	result.Journey = BuildPlanJourney(planView, result.Curation, result.Sessions)
	result.AvailableActions = availableCurationActions(
		result.Curation, len(result.Targets),
	)
	return result, nil
}

func (s *Service) Get(ctx context.Context, userID, planID string) (PlanResult, error) {
	record, err := s.repository.Get(ctx, userID, planID, false)
	if err != nil {
		return PlanResult{}, err
	}
	result := PlanResult{
		Plan: record.Plan, Curation: record.Curation, Targets: record.Targets,
		Sessions: []shoppingsessiondomain.ShoppingSession{},
	}
	for _, target := range record.Targets {
		session, sessionErr := s.sessions.FindByTarget(ctx, userID, string(target.ID))
		if sessionErr == nil {
			result.Sessions = append(result.Sessions, session)
		} else if !errors.Is(sessionErr, shoppingsessiondomain.ErrSessionNotFound) {
			return PlanResult{}, sessionErr
		}
	}
	task, taskErr := s.repository.GetPlanningTask(ctx, userID, planID, false)
	if errors.Is(taskErr, curationdomain.ErrPlanningTaskNotFound) {
		// A fresh AUTO Curation intentionally has no Task. The first
		// PLANNING_ADD_TARGETS action creates the INITIAL Task/Run.
	} else if taskErr != nil {
		return PlanResult{}, taskErr
	} else if task.Status == curationdomain.PlanningTaskStatusRequested {
		result.PlanningTask = &task
	}
	result.Journey = BuildPlanJourney(result.Plan, result.Curation, result.Sessions)
	result.AvailableActions = availableCurationActions(
		result.Curation, len(result.Targets),
	)
	return result, nil
}

func (s *Service) GetByCuration(
	ctx context.Context,
	userID, curationID string,
) (PlanResult, error) {
	repository, ok := s.repository.(CurationStateRepository)
	if !ok {
		return PlanResult{}, curationdomain.ErrCurationNotFound
	}
	curation, err := repository.GetCuration(
		ctx,
		strings.TrimSpace(userID),
		strings.TrimSpace(curationID),
		false,
	)
	if err != nil {
		return PlanResult{}, err
	}
	if curation.ArchivedAt != nil {
		return PlanResult{}, curationdomain.ErrCurationArchived
	}
	result, err := s.Get(
		ctx,
		userID,
		string(curation.ShoppingPlanID),
	)
	if err != nil {
		return PlanResult{}, err
	}
	if result.Curation.ID != curation.ID {
		return PlanResult{}, curationdomain.ErrCurationNotFound
	}
	return result, nil
}

type StartCuratingResult struct {
	Plan     curationdomain.PlanSnapshot `json:"plan"`
	Curation curationdomain.Curation     `json:"curation"`
}

// StartCurating is called inside the same ambient transaction that creates the
// first ResearchRounds and AgentControl batch. Every caller must provide the
// exact Curation version.
func (s *Service) StartCurating(
	ctx context.Context,
	userID, planID string,
	expectedVersion int64,
) (StartCuratingResult, error) {
	repository, ok := s.repository.(CurationStateRepository)
	if !ok {
		return StartCuratingResult{}, curationdomain.ErrCurationNotFound
	}
	var activated curationdomain.PlanSnapshot
	var curation curationdomain.Curation
	err := s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		record, err := s.repository.Get(txContext, userID, planID, true)
		if err != nil {
			return err
		}
		previousCurationVersion := record.Curation.Version
		now := s.clock.Now()
		if err := record.Curation.StartCurating(
			len(record.Targets), expectedVersion, now,
		); err != nil {
			return err
		}
		if record.Curation.Version != previousCurationVersion {
			if err := repository.SaveCuration(
				txContext, previousCurationVersion, record.Curation,
			); err != nil {
				return err
			}
		}
		activated = record.Plan
		curation = record.Curation
		return nil
	})
	return StartCuratingResult{Plan: activated, Curation: curation}, err
}

type TargetInput struct {
	ProductVertical  string                              `json:"productVertical,omitempty"`
	ContentLocale    string                              `json:"contentLocale,omitempty"`
	Criteria         *curationdomain.TargetCriteriaSetV1 `json:"criteria,omitempty"`
	Quantity         int                                 `json:"quantity,omitempty"`
	Title            string                              `json:"title"`
	NormalizedIntent string                              `json:"normalizedIntent"`
	Category         string                              `json:"category"`
	AllocatedBudget  MoneyInput                          `json:"allocatedBudget"`
	AllowedItems     []string                            `json:"allowedItems"`
	BlockedItems     []string                            `json:"blockedItems"`
	MinPrice         *MoneyInput                         `json:"minPrice,omitempty"`
	MaxPrice         *MoneyInput                         `json:"maxPrice,omitempty"`
	ReferenceURL     string                              `json:"referenceUrl,omitempty" jsonschema:"required only when urlMode is REFERENCE or EXACT_PRODUCT"`
	URLMode          string                              `json:"urlMode" jsonschema:"one of NONE, REFERENCE, or EXACT_PRODUCT; use NONE when no reference URL is supplied"`
}

func availableCurationActions(
	curation curationdomain.Curation,
	activeTargetCount int,
) []curationdomain.CurationActionDescriptor {
	if curation.ID == "" || curation.Version < 1 {
		return []curationdomain.CurationActionDescriptor{}
	}
	phase := curationdomain.CurationActionPhase(curation.Phase)
	actions, err := curationdomain.AvailableCurationActions(
		phase, curation.Version,
	)
	if err != nil {
		return []curationdomain.CurationActionDescriptor{}
	}
	for index := range actions {
		if actions[index].ID ==
			curationdomain.CurationActionPlanningStartCurating &&
			activeTargetCount == 0 {
			actions[index].Enabled = false
			actions[index].UnavailableReason = "CURATION_TARGETS_REQUIRED"
		}
	}
	return actions
}

type PlanningContext struct {
	ControlMode        string                               `json:"controlMode"`
	ContentLocale      string                               `json:"contentLocale"`
	BudgetVersion      int64                                `json:"budgetVersion"`
	BudgetRequest      *curationdomain.InitialBudgetRequest `json:"budgetRequest,omitempty"`
	BudgetEnabled      bool                                 `json:"budgetEnabled"`
	BudgetCurrency     string                               `json:"budgetCurrency,omitempty"`
	TaskID             string                               `json:"taskId"`
	PlanID             string                               `json:"planId"`
	RunID              string                               `json:"runId,omitempty"`
	RunKind            curationdomain.CurationRunKind       `json:"runKind,omitempty"`
	Instruction        string                               `json:"instruction,omitempty"`
	OriginalIntent     string                               `json:"originalIntent"`
	PlanningMode       curationdomain.PlanningMode          `json:"planningMode"`
	ExecutionMode      curationdomain.ExecutionMode         `json:"executionMode"`
	TotalBudget        shareddomain.Money                   `json:"totalBudget"`
	Location           shareddomain.LocationContext         `json:"location"`
	ResearchScope      curationdomain.ResearchScope         `json:"researchScope"`
	ContextVersion     int64                                `json:"contextVersion"`
	ContextHash        string                               `json:"contextHash"`
	ProposalSchema     string                               `json:"proposalSchema"`
	MinimumTargetCount int                                  `json:"minimumTargetCount"`
	MaximumTargetCount int                                  `json:"maximumTargetCount"`
	ExistingTargets    []PlanningContextTarget              `json:"existingTargets"`
}

type PlanningContextTarget struct {
	ID               string             `json:"id"`
	Title            string             `json:"title"`
	NormalizedIntent string             `json:"normalizedIntent"`
	Category         string             `json:"category"`
	AllocatedBudget  shareddomain.Money `json:"allocatedBudget"`
}

func (s *Service) EnsurePlanningAvailable(
	ctx context.Context, userID, planID string,
) (curationdomain.PlanningTask, error) {
	record, err := s.repository.Get(ctx, userID, planID, false)
	if err != nil {
		return curationdomain.PlanningTask{}, err
	}
	task, err := s.repository.GetPlanningTask(ctx, userID, planID, false)
	if err != nil {
		return curationdomain.PlanningTask{}, err
	}
	if task.Status != curationdomain.PlanningTaskStatusRequested || !s.clock.Now().Before(task.ExpiresAt) {
		return curationdomain.PlanningTask{}, curationdomain.ErrPlanningTaskClosed
	}
	repository, ok := s.repository.(CurationRepository)
	if !ok {
		return curationdomain.PlanningTask{}, curationdomain.ErrPlanningRequired
	}
	run, runErr := repository.GetCurationRunByTask(
		ctx, userID, planID, string(task.ID), false,
	)
	if runErr != nil {
		return curationdomain.PlanningTask{}, curationdomain.ErrPlanningRequired
	}
	switch run.Kind {
	case curationdomain.CurationRunInitial:
		if record.Curation.Phase != curationdomain.CurationPhasePlanning ||
			(record.Plan.PlanningMode != curationdomain.PlanningModeAuto &&
				record.Plan.PlanningMode != curationdomain.PlanningModeSingle) ||
			len(record.Targets) != 0 {
			return curationdomain.PlanningTask{}, curationdomain.ErrPlanningRequired
		}
	case curationdomain.CurationRunExpansion:
		if record.Curation.Phase != curationdomain.CurationPhasePlanning &&
			record.Curation.Phase != curationdomain.CurationPhaseCurating {
			return curationdomain.PlanningTask{}, curationdomain.ErrPlanningRequired
		}
	default:
		return curationdomain.PlanningTask{}, curationdomain.ErrPlanningRequired
	}
	if task.ContextVersion != record.Curation.Version {
		return curationdomain.PlanningTask{}, curationdomain.ErrPlanningContextStale
	}
	return task, nil
}

func (s *Service) ListPlanningTasks(
	ctx context.Context, userID, planID string, limit int,
) ([]curationdomain.PlanningTask, error) {
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	return s.repository.ListPlanningTasks(ctx, userID, planID, limit)
}

func (s *Service) GetPlanningContext(
	ctx context.Context, userID, planID, taskID string,
) (PlanningContext, error) {
	record, err := s.repository.Get(ctx, userID, planID, false)
	if err != nil {
		return PlanningContext{}, err
	}
	task, err := s.repository.GetPlanningTask(ctx, userID, planID, false)
	if err != nil {
		return PlanningContext{}, err
	}
	if string(task.ID) != taskID {
		return PlanningContext{}, curationdomain.ErrPlanningTaskNotFound
	}
	return s.buildPlanningContext(ctx, record, task)
}

func (s *Service) buildPlanningContext(
	ctx context.Context,
	record PlanRecord,
	task curationdomain.PlanningTask,
) (PlanningContext, error) {
	if task.Status != curationdomain.PlanningTaskStatusRequested || !s.clock.Now().Before(task.ExpiresAt) {
		return PlanningContext{}, curationdomain.ErrPlanningTaskClosed
	}
	context := PlanningContext{
		BudgetRequest: record.Plan.BudgetRequest,
		TaskID:        string(task.ID), PlanID: string(record.Plan.ID),
		OriginalIntent: record.Plan.OriginalIntent,
		PlanningMode:   record.Plan.PlanningMode,
		ExecutionMode:  record.Plan.ExecutionMode,
		TotalBudget:    record.Plan.TotalBudget, Location: record.Plan.LocationContext,
		ResearchScope:  record.Plan.ResearchScope,
		ContextVersion: task.ContextVersion, ContextHash: task.ContextHash,
		ProposalSchema:     curationdomain.PlanningProposalSchemaV1,
		MinimumTargetCount: 1,
		MaximumTargetCount: curationdomain.MaxPlanTargets,
		ExistingTargets:    planningContextTargets(record.Targets),
	}
	if repository, ok := s.repository.(CurationRepository); ok {
		run, runErr := repository.GetCurationRunByTask(
			ctx, string(task.UserID), string(task.PlanID), string(task.ID), false,
		)
		if runErr == nil {
			context.RunID = string(run.ID)
			context.RunKind = run.Kind
			context.Instruction = run.Instruction
			if run.Kind == curationdomain.CurationRunExpansion {
				// An expansion is a new shopping request, not a request to
				// reinterpret the plan's original Intent. Feed the immutable
				// action body stored on the Run into the existing Intelligence
				// PlanningContext field so the V1 prompt plans exactly what the
				// user just asked to add. INITIAL deliberately keeps the plan
				// Intent because its action only starts the original plan.
				context.OriginalIntent = run.Instruction
				context.MaximumTargetCount = curationdomain.MaxPlanTargets - len(record.Targets)
			}
		} else if !errors.Is(runErr, curationdomain.ErrCurationRunNotFound) {
			return PlanningContext{}, runErr
		}
	}
	if record.Plan.PlanningMode == curationdomain.PlanningModeSingle &&
		context.RunKind == curationdomain.CurationRunInitial {
		context.MaximumTargetCount = 1
	}
	if repo, ok := s.repository.(BudgetRepository); ok {
		ledger, err := repo.ReadBudget(ctx, string(record.Plan.UserID), string(record.Curation.ID), false)
		if err != nil {
			return PlanningContext{}, err
		}
		if modes, ok := s.repository.(controlModeReader); ok {
			mode, e := modes.ReadControlMode(ctx, string(record.Plan.UserID), string(record.Curation.ID))
			if e != nil {
				return PlanningContext{}, e
			}
			context.ControlMode = mode.Mode
		}
		context.BudgetEnabled, context.BudgetCurrency = ledger.Enabled, ledger.Currency
		context.BudgetVersion = ledger.ResearchVersion
		if context.RunKind == curationdomain.CurationRunInitial && record.Plan.BudgetRequest != nil {
			context.BudgetEnabled = record.Plan.BudgetRequest.TotalAmount != nil
		}
	}
	locale, localeErr := s.PlanContentLocale(ctx, string(record.Plan.UserID), string(task.ID))
	if localeErr != nil {
		return PlanningContext{}, localeErr
	}
	context.ContentLocale = locale

	return context, nil
}

type SubmitPlanningProposalInput struct {
	AutoBudgetDecision *curationdomain.ActionDecision       `json:"-"`
	ResolvedBudget     *curationdomain.InitialBudgetRequest `json:"resolvedBudget,omitempty"`
	BudgetVersion      *int64                               `json:"budgetVersion,omitempty"`
	UserID             string                               `json:"-"`
	PlanID             string                               `json:"-"`
	// IntelligenceJobID is the ADR-0038 provenance: the job that produced this
	// submission, and the only provenance a new proposal can carry.
	IntelligenceJobID string        `json:"-"`
	TaskID            string        `json:"taskId"`
	ClientProposalID  string        `json:"clientProposalId"`
	ContextVersion    int64         `json:"contextVersion"`
	ContextHash       string        `json:"contextHash"`
	SchemaVersion     string        `json:"schemaVersion"`
	Targets           []TargetInput `json:"targets"`
}

type SubmitPlanningProposalResult struct {
	ProposalID        string                                  `json:"proposalId"`
	PlanID            string                                  `json:"planId"`
	ValidationStatus  curationdomain.ProposalValidationStatus `json:"validationStatus"`
	ReasonCodes       []string                                `json:"reasonCodes"`
	TargetCount       int                                     `json:"targetCount"`
	CreatedTargetIDs  []string                                `json:"createdTargetIds"`
	CreatedSessionIDs []string                                `json:"createdSessionIds"`
	NextAction        string                                  `json:"nextAction"`
}

func (s *Service) submitPlanningProposal(
	ctx context.Context,
	input SubmitPlanningProposalInput,
) (SubmitPlanningProposalResult, error) {
	// ADR-0038 leaves one provenance: the job the Server dispatched. Historical
	// rows keep their grant or work-order ids, but nothing writes them again.
	if strings.TrimSpace(input.IntelligenceJobID) == "" {
		return SubmitPlanningProposalResult{}, curationdomain.ErrProposalInvalid
	}
	if !uuidPattern.MatchString(input.ClientProposalID) {
		return SubmitPlanningProposalResult{}, curationdomain.ErrProposalIDInvalid
	}
	if input.SchemaVersion != curationdomain.PlanningProposalSchemaV1 {
		return SubmitPlanningProposalResult{}, curationdomain.ErrProposalSchemaInvalid
	}
	proposalHash, payload, err := curationdomain.HashProposal(struct {
		ResolvedBudget   *curationdomain.InitialBudgetRequest `json:"resolvedBudget,omitempty"`
		BudgetVersion    *int64                               `json:"budgetVersion,omitempty"`
		TaskID           string                               `json:"taskId"`
		ClientProposalID string                               `json:"clientProposalId"`
		ContextVersion   int64                                `json:"contextVersion"`
		ContextHash      string                               `json:"contextHash"`
		SchemaVersion    string                               `json:"schemaVersion"`
		Targets          []TargetInput                        `json:"targets"`
	}{
		ResolvedBudget: input.ResolvedBudget, BudgetVersion: input.BudgetVersion, TaskID: input.TaskID, ClientProposalID: input.ClientProposalID,
		ContextVersion: input.ContextVersion, ContextHash: input.ContextHash,
		SchemaVersion: input.SchemaVersion, Targets: input.Targets,
	})
	if err != nil {
		return SubmitPlanningProposalResult{}, err
	}

	var result SubmitPlanningProposalResult
	var semanticErr error
	err = s.transactor.WithinTransaction(ctx, func(txContext context.Context) error {
		existing, found, findErr := s.findProposal(txContext, input)
		if findErr != nil {
			return findErr
		}
		if found {
			if existing.ProposalHash != proposalHash {
				return curationdomain.ErrIdempotencyKeyReused
			}
			result = proposalResult(existing, len(input.Targets))
			if existing.ValidationStatus == curationdomain.ProposalValidationRejected {
				semanticErr = curationdomain.ErrProposalInvalid
				return nil
			}
			return s.hydrateProposalContinuation(txContext, input, &result)
		}

		record, getErr := s.repository.Get(txContext, input.UserID, input.PlanID, true)
		if getErr != nil {
			return getErr
		}
		var task curationdomain.PlanningTask
		var taskErr error
		task, taskErr = s.repository.GetPlanningTask(
			txContext, input.UserID, input.PlanID, true,
		)
		if taskErr != nil {
			return taskErr
		}
		if string(task.ID) != input.TaskID {
			return curationdomain.ErrPlanningTaskNotFound
		}
		if task.ContextVersion != input.ContextVersion || task.ContextHash != input.ContextHash {
			return curationdomain.ErrPlanningContextStale
		}
		var run *curationdomain.CurationRun
		var curationRepository CurationRepository
		var curationStateRepository CurationStateRepository
		if repository, ok := s.repository.(CurationRepository); ok {
			curationRepository = repository
			value, runErr := repository.GetCurationRunByTask(
				txContext, input.UserID, input.PlanID, input.TaskID, true,
			)
			if runErr == nil {
				run = &value
			} else if !errors.Is(runErr, curationdomain.ErrCurationRunNotFound) {
				return runErr
			}
		}
		if repository, ok := s.repository.(CurationStateRepository); ok {
			curationStateRepository = repository
		}
		if run == nil {
			return curationdomain.ErrCurationRunNotFound
		}
		if curationStateRepository == nil {
			return curationdomain.ErrCurationNotFound
		}
		isExpansion := run.Kind == curationdomain.CurationRunExpansion
		if run.Kind != curationdomain.CurationRunInitial && !isExpansion {
			return curationdomain.ErrPlanningRequired
		}
		now := s.clock.Now()
		if task.Status != curationdomain.PlanningTaskStatusRequested || !now.Before(task.ExpiresAt) {
			return curationdomain.ErrPlanningTaskClosed
		}
		if task.ContextVersion != record.Curation.Version {
			return curationdomain.ErrPlanningContextStale
		}
		proposal := curationdomain.PlanningProposal{
			ID:                curationdomain.PlanningProposalID(s.ids.NewID()),
			TaskID:            task.ID,
			PlanID:            curationdomain.ShoppingPlanID(record.Plan.ID),
			UserID:            curationdomain.UserID(record.Plan.UserID),
			IntelligenceJobID: input.IntelligenceJobID,
			ClientProposalID:  input.ClientProposalID,
			ContextVersion:    input.ContextVersion, ContextHash: input.ContextHash,
			SchemaVersion: input.SchemaVersion, ProposalHash: proposalHash, Payload: payload,
			ValidationStatus:      curationdomain.ProposalValidationAccepted,
			ValidationReasonCodes: []string{}, SubmittedAt: now,
		}
		if isExpansion && input.ResolvedBudget != nil {
			return curationdomain.ErrProposalInvalid
		}
		var threadBudgetBefore curationdomain.BudgetLedger
		validationPlan, resolveErr := record.Plan.ResolveInitialBudget(input.ResolvedBudget)
		if resolveErr != nil {
			return resolveErr
		}
		if repo, ok := s.repository.(BudgetRepository); ok {
			ledger, e := repo.ReadBudget(txContext, input.UserID, string(record.Curation.ID), false)
			if e != nil {
				return e
			}
			threadBudgetBefore = ledger
			if input.BudgetVersion != nil && *input.BudgetVersion != ledger.ResearchVersion {
				return curationdomain.ErrPlanningContextStale
			}
			if isExpansion {
				validationPlan.TotalBudget.Currency = shareddomain.CurrencyCode(ledger.Currency)
				validationPlan.BudgetRequest = &curationdomain.InitialBudgetRequest{SchemaVersion: curationdomain.BudgetSchema, Currency: ledger.Currency, AllocationMode: "AUTO"}
			}
		}
		var nextTargets []curationdomain.PlanTarget
		var createdTargets []curationdomain.PlanTarget
		var validationErr error
		if isExpansion {
			proposed, targetErr := s.targetsFromExpansionProposal(
				validationPlan, record.Targets, input.Targets, now,
			)
			if targetErr == nil {
				nextTargets, createdTargets, validationErr =
					curationdomain.AppendConfirmedTargets(
						validationPlan,
						record.Targets,
						proposed,
						run.ID,
						now,
					)
			} else {
				validationErr = targetErr
			}
		} else {
			proposed, targetErr := s.targetsFromProposal(
				validationPlan, input.Targets, now,
			)
			if targetErr == nil {
				createdTargets, validationErr =
					curationdomain.MaterializeInitialTargets(
						record.Plan,
						&task,
						proposed,
						run.ID,
						now, input.ResolvedBudget,
					)
				nextTargets = createdTargets
			} else {
				validationErr = targetErr
			}
		}
		if validationErr != nil {
			proposal.ValidationStatus = curationdomain.ProposalValidationRejected
			proposal.ValidationReasonCodes = []string{ReasonCode(validationErr)}
			if insertErr := s.repository.InsertProposal(txContext, proposal); insertErr != nil {
				return insertErr
			}
			result = proposalResult(proposal, len(input.Targets))
			semanticErr = validationErr
			return nil
		}
		if insertErr := s.repository.InsertProposal(txContext, proposal); insertErr != nil {
			return insertErr
		}
		if isExpansion {
			task.Status = curationdomain.PlanningTaskStatusCompleted
			task.CompletedAt = &now
			task.UpdatedAt = now
		}
		previousCurationVersion := record.Curation.Version
		if addErr := record.Curation.AddTargets(
			len(createdTargets),
			task.ContextVersion,
			now,
		); addErr != nil {
			return addErr
		}
		for index := range createdTargets {
			createdTargets[index].CurationID = record.Curation.ID
			createdTargets[index].UserID = record.Curation.UserID
		}
		if saveErr := s.insertPlannedTargets(txContext, createdTargets, input.ResolvedBudget, input.Targets); saveErr != nil {
			return saveErr
		}
		if recorder, ok := s.repository.(interface {
			RecordPlanningThreadResult(context.Context, string, string, string, curationdomain.BudgetLedger, []curationdomain.PlanTarget, *curationdomain.ActionDecision) error
		}); ok && input.IntelligenceJobID != "" {
			if e := recorder.RecordPlanningThreadResult(txContext, input.UserID, string(record.Curation.ID), input.IntelligenceJobID, threadBudgetBefore, createdTargets, input.AutoBudgetDecision); e != nil {
				return e
			}
		}
		record.Targets = nextTargets
		if saveErr := curationStateRepository.SaveCuration(
			txContext,
			previousCurationVersion,
			record.Curation,
		); saveErr != nil {
			return saveErr
		}
		if taskErr := s.repository.UpdatePlanningTask(txContext, task); taskErr != nil {
			return taskErr
		}
		sessions := make([]shoppingsessiondomain.ShoppingSession, 0, len(createdTargets))
		for _, target := range createdTargets {
			session, sessionErr := s.sessions.CreateReady(
				txContext, sessionInput(record.Plan, target),
			)
			if sessionErr != nil {
				return sessionErr
			}
			sessions = append(sessions, session)
		}
		result = proposalResult(proposal, len(createdTargets))
		result.CreatedTargetIDs = targetIDs(createdTargets)
		result.CreatedSessionIDs = sessionIDs(sessions)
		result.NextAction = "START_CURATING"
		if curationRepository != nil {
			run.Status = curationdomain.CurationRunCompleted
			run.UpdatedAt = now
			run.CompletedAt = &now
			if updateErr := curationRepository.UpdateCurationRun(
				txContext, *run,
			); updateErr != nil {
				return updateErr
			}
		}
		return nil
	})
	if err != nil {
		return SubmitPlanningProposalResult{}, err
	}
	if semanticErr != nil {
		return result, &RejectedProposalError{Cause: semanticErr}
	}
	provenance := "intelligence-job:" + input.IntelligenceJobID
	s.logger.InfoContext(ctx, "planning proposal accepted",
		"event", "planning.proposal.accepted", "result", "success",
		"plan_id", input.PlanID, "task_id", input.TaskID,
		"agent_provenance", provenance,
		"target_count", result.TargetCount)
	return result, nil
}

func (s *Service) findProposal(
	ctx context.Context,
	input SubmitPlanningProposalInput,
) (curationdomain.PlanningProposal, bool, error) {
	repository, ok := s.repository.(IntelligencePlanningRepository)
	if !ok {
		return curationdomain.PlanningProposal{}, false,
			curationdomain.ErrPlanningTaskNotFound
	}
	return repository.FindIntelligenceProposal(
		ctx, input.TaskID, input.IntelligenceJobID, input.ClientProposalID,
	)
}

func proposalResult(
	proposal curationdomain.PlanningProposal,
	targetCount int,
) SubmitPlanningProposalResult {
	return SubmitPlanningProposalResult{
		ProposalID: string(proposal.ID), PlanID: string(proposal.PlanID),
		ValidationStatus: proposal.ValidationStatus,
		ReasonCodes:      proposal.ValidationReasonCodes, TargetCount: targetCount,
	}
}

func (s *Service) hydrateProposalContinuation(
	ctx context.Context,
	input SubmitPlanningProposalInput,
	result *SubmitPlanningProposalResult,
) error {
	record, err := s.repository.Get(ctx, input.UserID, input.PlanID, false)
	if err != nil {
		return err
	}
	created := record.Targets
	if repository, ok := s.repository.(CurationRepository); ok {
		run, runErr := repository.GetCurationRunByTask(
			ctx, input.UserID, input.PlanID, input.TaskID, false,
		)
		if runErr == nil {
			created = created[:0]
			for _, target := range record.Targets {
				if target.CreatedByCurationRunID != nil &&
					*target.CreatedByCurationRunID == run.ID {
					created = append(created, target)
				}
			}
		} else if !errors.Is(runErr, curationdomain.ErrCurationRunNotFound) {
			return runErr
		}
	}
	sessions := make([]shoppingsessiondomain.ShoppingSession, 0, len(created))
	for _, target := range created {
		session, sessionErr := s.sessions.FindByTarget(
			ctx, input.UserID, string(target.ID),
		)
		if sessionErr != nil {
			return sessionErr
		}
		sessions = append(sessions, session)
	}
	result.CreatedTargetIDs = targetIDs(created)
	result.CreatedSessionIDs = sessionIDs(sessions)
	result.NextAction = "START_CURATING"
	return nil
}

func targetIDs(targets []curationdomain.PlanTarget) []string {
	result := make([]string, len(targets))
	for index := range targets {
		result[index] = string(targets[index].ID)
	}
	return result
}

func sessionIDs(sessions []shoppingsessiondomain.ShoppingSession) []string {
	result := make([]string, len(sessions))
	for index := range sessions {
		result[index] = string(sessions[index].ID)
	}
	return result
}

func planningContextTargets(
	targets []curationdomain.PlanTarget,
) []PlanningContextTarget {
	result := make([]PlanningContextTarget, len(targets))
	for index, target := range targets {
		result[index] = PlanningContextTarget{
			ID: string(target.ID), Title: target.Title,
			NormalizedIntent: target.NormalizedIntent,
			Category:         target.Category, AllocatedBudget: target.AllocatedBudget,
		}
	}
	return result
}

func hashExpansionContext(
	plan curationdomain.PlanSnapshot,
	curationVersion int64,
	targets []curationdomain.PlanTarget,
	instruction string,
) (string, []byte, error) {
	payload, err := json.Marshal(struct {
		Schema          string                  `json:"schema"`
		PlanID          string                  `json:"planId"`
		CurationVersion int64                   `json:"curationVersion"`
		OriginalIntent  string                  `json:"originalIntent"`
		Instruction     string                  `json:"instruction"`
		ExistingTargets []PlanningContextTarget `json:"existingTargets"`
	}{
		Schema: "vitlane.curation-expansion-context.v1",
		PlanID: string(plan.ID), CurationVersion: curationVersion,
		OriginalIntent: plan.OriginalIntent, Instruction: instruction,
		ExistingTargets: planningContextTargets(targets),
	})
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(payload)
	requestHash := make([]byte, len(sum))
	copy(requestHash, sum[:])
	return fmt.Sprintf("%x", sum), requestHash, nil
}

func (s *Service) targetsFromExpansionProposal(
	plan curationdomain.PlanSnapshot,
	current []curationdomain.PlanTarget,
	inputs []TargetInput,
	now time.Time,
) ([]curationdomain.PlanTarget, error) {
	if len(inputs) == 0 || len(current)+len(inputs) > curationdomain.MaxPlanTargets {
		return nil, curationdomain.ErrTargetCountInvalid
	}
	targets := make([]curationdomain.PlanTarget, 0, len(inputs))
	for index, input := range inputs {
		if strings.TrimSpace(input.Category) == "" {
			return nil, curationdomain.ErrTargetIncomplete
		}
		target, err := s.targetFromInput(plan, input, len(current)+index, now)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func (s *Service) targetsFromProposal(
	plan curationdomain.PlanSnapshot,
	inputs []TargetInput,
	now time.Time,
) ([]curationdomain.PlanTarget, error) {
	if len(inputs) == 0 || len(inputs) > curationdomain.MaxPlanTargets {
		return nil, curationdomain.ErrTargetCountInvalid
	}
	if plan.BudgetRequest != nil && plan.BudgetRequest.TotalAmount != nil {
		weights := make([]int64, len(inputs))
		for i, t := range inputs {
			n, e := curationdomain.BudgetMinor(t.AllocatedBudget.Amount, plan.BudgetRequest.Currency)
			if e != nil {
				return nil, e
			}
			weights[i] = n
			if plan.BudgetRequest.AllocationMode == "EQUAL" {
				weights[i] = 1
			}
		}
		total, e := curationdomain.BudgetMinor(*plan.BudgetRequest.TotalAmount, plan.BudgetRequest.Currency)
		if e != nil {
			return nil, e
		}
		values, e := curationdomain.AllocateBudget(total, weights)
		if e != nil {
			return nil, e
		}
		inputs = append([]TargetInput(nil), inputs...)
		for i, n := range values {
			inputs[i].AllocatedBudget = MoneyInput{Amount: curationdomain.BudgetAmount(n, plan.BudgetRequest.Currency), Currency: plan.BudgetRequest.Currency}
		}
	}
	targets := make([]curationdomain.PlanTarget, 0, len(inputs))
	for index, input := range inputs {
		if strings.TrimSpace(input.Category) == "" {
			return nil, curationdomain.ErrTargetIncomplete
		}
		target, err := s.targetFromInput(plan, input, index, now)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	if err := plan.ValidateTargetSet(targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *Service) targetFromInput(
	plan curationdomain.PlanSnapshot,
	input TargetInput,
	orderIndex int,
	now time.Time,
) (curationdomain.PlanTarget, error) {
	if input.Quantity < 0 || input.Quantity > 99 {
		return curationdomain.PlanTarget{}, curationdomain.ErrProposalInvalid
	}
	budget, err := moneyFromInput(input.AllocatedBudget)
	if err != nil {
		return curationdomain.PlanTarget{}, err
	}
	minPrice, err := optionalMoney(input.MinPrice)
	if err != nil {
		return curationdomain.PlanTarget{}, err
	}
	maxPrice, err := optionalMoney(input.MaxPrice)
	if err != nil {
		return curationdomain.PlanTarget{}, err
	}
	scope, err := curationdomain.NewResearchScope(
		input.Category, plan.LocationContext, input.AllowedItems, input.BlockedItems,
		minPrice, maxPrice, input.ReferenceURL, curationdomain.URLMode(input.URLMode),
	)
	if err != nil {
		return curationdomain.PlanTarget{}, err
	}
	return curationdomain.PlanTarget{
		ID: curationdomain.PlanTargetID(s.ids.NewID()), PlanID: plan.ID,
		Title:            strings.TrimSpace(input.Title),
		ProductVertical:  curationdomain.NormalizeProductVertical(input.ProductVertical),
		NormalizedIntent: strings.TrimSpace(input.NormalizedIntent),
		Category:         strings.TrimSpace(input.Category), AllocatedBudget: budget,
		ResearchScope: scope, OrderIndex: orderIndex,
		TargetHashSchema: curationdomain.PlanTargetHashSchemaV1,
		Version:          1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func parsePlanInputs(
	input CreatePlanInput,
) (shareddomain.Money, shareddomain.LocationContext, curationdomain.ResearchScope, error) {
	budget, err := moneyFromInput(input.TotalBudget)
	if err != nil {
		return shareddomain.Money{}, shareddomain.LocationContext{}, curationdomain.ResearchScope{}, err
	}
	location, err := shareddomain.NewLocationContext(input.Country, input.City)
	if err != nil {
		return shareddomain.Money{}, shareddomain.LocationContext{}, curationdomain.ResearchScope{}, err
	}
	minPrice, err := optionalMoney(input.MinPrice)
	if err != nil {
		return shareddomain.Money{}, shareddomain.LocationContext{}, curationdomain.ResearchScope{}, err
	}
	maxPrice, err := optionalMoney(input.MaxPrice)
	if err != nil {
		return shareddomain.Money{}, shareddomain.LocationContext{}, curationdomain.ResearchScope{}, err
	}
	scope, err := curationdomain.NewResearchScope(
		input.Category, location, input.AllowedItems, input.BlockedItems,
		minPrice, maxPrice, input.ReferenceURL, curationdomain.URLMode(input.URLMode),
	)
	return budget, location, scope, err
}

func toPlanningResearchScope(scope curationdomain.ResearchScope) planningapp.ResearchScope {
	return planningapp.ResearchScope{
		Category: scope.Category, Country: scope.Country, City: scope.City,
		AllowedItems: scope.AllowedItems, BlockedItems: scope.BlockedItems,
		MinPrice: scope.MinPrice, MaxPrice: scope.MaxPrice,
		ReferenceURL: scope.ReferenceURL,
		URLMode:      planningapp.URLMode(scope.URLMode),
	}
}

func toCurationResearchScope(scope planningapp.ResearchScope) curationdomain.ResearchScope {
	return curationdomain.ResearchScope{
		Category: scope.Category, Country: scope.Country, City: scope.City,
		AllowedItems: scope.AllowedItems, BlockedItems: scope.BlockedItems,
		MinPrice: scope.MinPrice, MaxPrice: scope.MaxPrice,
		ReferenceURL: scope.ReferenceURL,
		URLMode:      curationdomain.URLMode(scope.URLMode),
	}
}

func toCurationPlanSnapshot(plan planningapp.ShoppingPlan) curationdomain.PlanSnapshot {
	return curationdomain.PlanSnapshot{
		ID:             curationdomain.ShoppingPlanID(plan.ID),
		UserID:         curationdomain.UserID(plan.UserID),
		OriginalIntent: plan.OriginalIntent,
		PlanningMode:   curationdomain.PlanningMode(plan.PlanningMode),
		ExecutionMode:  curationdomain.ExecutionMode(plan.ExecutionMode),
		TotalBudget:    plan.TotalBudget, LocationContext: plan.LocationContext,
		ResearchScope: toCurationResearchScope(plan.ResearchScope),
		AgentMode:     curationdomain.AgentMode(plan.AgentMode), ModelKey: plan.ModelKey,
		CreatedAt: plan.CreatedAt,
	}
}

func toCurationPlanError(err error) error {
	switch {
	case errors.Is(err, planningapp.ErrIntentRequired):
		return curationdomain.ErrIntentRequired
	case errors.Is(err, planningapp.ErrBudgetNonPositive):
		return curationdomain.ErrBudgetNonPositive
	case errors.Is(err, planningapp.ErrPlanningModeInvalid):
		return curationdomain.ErrPlanningModeInvalid
	case errors.Is(err, planningapp.ErrExecutionModeInvalid):
		return curationdomain.ErrExecutionModeInvalid
	case errors.Is(err, planningapp.ErrLiveCountryUnsupported):
		return curationdomain.ErrLiveCountryUnsupported
	case errors.Is(err, planningapp.ErrPriceRangeInvalid):
		return curationdomain.ErrPriceRangeInvalid
	case errors.Is(err, planningapp.ErrURLModeInvalid):
		return curationdomain.ErrURLModeInvalid
	case errors.Is(err, planningapp.ErrAgentModeInvalid):
		return curationdomain.ErrAgentModeInvalid
	default:
		return err
	}
}

func sessionInput(plan curationdomain.PlanSnapshot, target curationdomain.PlanTarget) shoppingsessionapp.CreateReadyInput {
	confirmedAt := ""
	if target.ConfirmedAt != nil {
		confirmedAt = target.ConfirmedAt.Format(time.RFC3339Nano)
	}
	return shoppingsessionapp.CreateReadyInput{
		UserID: string(plan.UserID), TargetID: string(target.ID),
		TargetSnapshot: shoppingsessionapp.TargetSnapshot{
			ID: string(target.ID), CurationID: string(target.CurationID),
			PlanID: string(plan.ID), Title: target.Title,
			NormalizedIntent: target.NormalizedIntent, Category: target.Category, ProductVertical: target.ProductVertical,
			AllocatedBudget: target.AllocatedBudget, TargetHash: string(target.TargetHash),
			TargetHashSchema: target.TargetHashSchema, ConfirmedAt: confirmedAt,
		},
		ScopeSnapshot: shoppingsessionapp.ResearchScopeSnapshot{
			Category: target.ResearchScope.Category, Country: string(target.ResearchScope.Country),
			City: target.ResearchScope.City, AllowedItems: target.ResearchScope.AllowedItems,
			BlockedItems: target.ResearchScope.BlockedItems, MinPrice: target.ResearchScope.MinPrice,
			MaxPrice: target.ResearchScope.MaxPrice, ReferenceURL: target.ResearchScope.ReferenceURL,
			URLMode: string(target.ResearchScope.URLMode),
		},
	}
}

func moneyFromInput(input MoneyInput) (shareddomain.Money, error) {
	money, err := shareddomain.NewMoney(input.Amount, input.Currency)
	if err != nil {
		return shareddomain.Money{}, fmt.Errorf("money: %w", err)
	}
	return money, nil
}

func optionalMoney(input *MoneyInput) (*shareddomain.Money, error) {
	if input == nil {
		return nil, nil
	}
	money, err := moneyFromInput(*input)
	if err != nil {
		return nil, err
	}
	return &money, nil
}

func ReasonCode(err error) string {
	codes := []error{
		curationdomain.ErrIntentRequired,
		curationdomain.ErrExecutionModeInvalid,
		curationdomain.ErrBudgetNonPositive,
		curationdomain.ErrPriceRangeInvalid,
		curationdomain.ErrLiveCountryUnsupported,
		curationdomain.ErrURLModeInvalid,
		curationdomain.ErrTargetIncomplete,
		curationdomain.ErrPlanNotEditable,
		curationdomain.ErrVersionConflict,
		curationdomain.ErrPlanNotFound,
		curationdomain.ErrTargetNotFound,
		curationdomain.ErrIdempotencyKeyRequired,
		curationdomain.ErrIdempotencyKeyReused,
		curationdomain.ErrPlanningModeInvalid,
		curationdomain.ErrExternalAgentRetired,
		curationdomain.ErrPlanningRequired,
		curationdomain.ErrPlanningTaskNotFound,
		curationdomain.ErrPlanningTaskClosed,
		curationdomain.ErrPlanningContextStale,
		curationdomain.ErrProposalInvalid,
		curationdomain.ErrProposalIDInvalid,
		curationdomain.ErrProposalSchemaInvalid,
		curationdomain.ErrTargetCountInvalid,
		curationdomain.ErrTargetBudgetInvalid,
		curationdomain.ErrTargetSetMismatch,
		curationdomain.ErrSingleTargetRequired,
		curationdomain.ErrCurationRunNotFound,
		curationdomain.ErrCurationRunClosed,
		curationdomain.ErrCurationActionInProgress,
		curationdomain.ErrExpansionUnavailable,
		curationdomain.ErrExpansionInstruction,
		curationdomain.ErrTargetRemovalBlocked,
		curationdomain.ErrExpansionInProgress,
		curationdomain.ErrTooManyActiveActions,
		shareddomain.ErrInvalidMoney,
		shareddomain.ErrInvalidLocation,
	}
	for _, code := range codes {
		if errors.Is(err, code) {
			return code.Error()
		}
	}
	return "INTERNAL_ERROR"
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func hashCreateInput(input CreatePlanInput) ([]byte, error) {
	canonical := struct {
		ControlMode    string                               `json:"controlMode,omitempty"`
		BudgetRequest  *curationdomain.InitialBudgetRequest `json:"BudgetRequest,omitempty"`
		OriginalIntent string
		PlanningMode   string
		ExecutionMode  string
		TotalBudget    MoneyInput
		Country        string
		City           string
		Category       string
		AllowedItems   []string
		BlockedItems   []string
		MinPrice       *MoneyInput
		MaxPrice       *MoneyInput
		ReferenceURL   string
		URLMode        string
	}{
		ControlMode: input.ControlMode, BudgetRequest: input.BudgetRequest, OriginalIntent: input.OriginalIntent, PlanningMode: input.PlanningMode,
		ExecutionMode: input.ExecutionMode, TotalBudget: input.TotalBudget,
		Country: input.Country, City: input.City, Category: input.Category,
		AllowedItems: input.AllowedItems, BlockedItems: input.BlockedItems,
		MinPrice: input.MinPrice, MaxPrice: input.MaxPrice,
		ReferenceURL: input.ReferenceURL, URLMode: input.URLMode,
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("hash plan creation request: %w", err)
	}
	sum := sha256.Sum256(payload)
	return sum[:], nil
}
